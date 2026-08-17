package books

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/tiredbooy/internal/models"
	"github.com/tiredbooy/internal/storage"
)

const openLibrarySearchURL = "https://openlibrary.org/search.json"

// Resolver is local-first: it reads the on-device cache before making the
// optional Open Library lookup, then persists a successful lookup for offline reuse.
type Resolver struct {
	cache  *storage.BookMetadataStore
	client *http.Client
}

// LookupResult separates an exact catalog fact from a possible spelling
// correction. Athena may use Exact automatically; SuggestedTitle always needs
// the user's confirmation before it becomes a vault title.
type LookupResult struct {
	RequestedTitle   string               `json:"requested_title"`
	Exact            *models.BookMetadata `json:"exact,omitempty"`
	SuggestedTitle   string               `json:"suggested_title,omitempty"`
	SuggestedAuthors []string             `json:"suggested_authors,omitempty"`
}

type TitleSuggestionError struct {
	Requested string
	Suggested string
	Authors   []string
}

func (e *TitleSuggestionError) Error() string {
	detail := ""
	if len(e.Authors) > 0 {
		detail = " by " + strings.Join(e.Authors, ", ")
	}
	return fmt.Sprintf("book title %q was not an exact catalog match; did you mean %q%s? Ask the user before changing the title", e.Requested, e.Suggested, detail)
}

func NewResolver(cache *storage.BookMetadataStore, client *http.Client) *Resolver {
	if client == nil {
		// Timeout covers the whole exchange — dial, TLS handshake, redirects and
		// body — so a metadata lookup needs no separate transport timeouts; any
		// added there would sit above this 8s budget and never fire.
		client = &http.Client{Timeout: 8 * time.Second, CheckRedirect: refuseOffHostRedirect}
	}
	return &Resolver{cache: cache, client: client}
}

// refuseOffHostRedirect keeps the lookup query on openlibrary.org.
//
// The query string carries the user's book title, which is text out of their
// vault, and net/http attaches the original URL as Referer when it follows a
// redirect — so following one off-host hands that title to a third party the
// user never chose. There is no credential on this request; the private data
// is the title itself.
//
// Same rule as refuseCredentialLeakingRedirect in internal/ai/http_client.go,
// restated here because that one is unexported in another package. Six lines
// beat exporting a helper out of a package this code does not own.
func refuseOffHostRedirect(req *http.Request, via []*http.Request) error {
	previous := via[len(via)-1]
	// An http -> https upgrade of the same host is safe; the reverse exposes the
	// title in clear text, and any host change exposes it to someone else.
	if req.URL.Host == previous.URL.Host && (req.URL.Scheme == previous.URL.Scheme || req.URL.Scheme == "https") {
		return nil
	}
	return fmt.Errorf("refusing catalog redirect from %s to %s: the query carries the user's book title", previous.URL.Redacted(), req.URL.Redacted())
}

func (r *Resolver) Resolve(ctx context.Context, title, isbn string) (models.BookMetadata, error) {
	key := NormalizeTitle(title)
	if key == "" {
		return models.BookMetadata{}, fmt.Errorf("book title is required")
	}
	if cached, err := r.cache.Get(key); err != nil {
		return models.BookMetadata{}, err
	} else if cached != nil {
		return *cached, nil
	}

	lookup, err := r.Inspect(ctx, title, isbn)
	if err != nil {
		return models.BookMetadata{Title: strings.TrimSpace(title), Source: "unresolved"}, nil
	}
	if lookup.Exact == nil {
		if lookup.SuggestedTitle != "" {
			return models.BookMetadata{}, &TitleSuggestionError{
				Requested: strings.TrimSpace(title), Suggested: lookup.SuggestedTitle,
				Authors: append([]string(nil), lookup.SuggestedAuthors...),
			}
		}
		return models.BookMetadata{Title: strings.TrimSpace(title), Source: "unresolved"}, nil
	}
	metadata := *lookup.Exact
	if err := r.cache.Upsert(key, metadata); err != nil {
		return models.BookMetadata{}, err
	}
	return metadata, nil
}

// ResolveWithFallback uses user-supplied authors and genres only when the
// catalog is unavailable or leaves that field empty. It never replaces a
// non-empty catalog value and never treats model-invented metadata as factual.
func (r *Resolver) ResolveWithFallback(ctx context.Context, title, isbn string, authors, genres []string) (models.BookMetadata, error) {
	metadata, err := r.Resolve(ctx, title, isbn)
	if err != nil {
		return models.BookMetadata{}, err
	}
	usedUserData := false
	if len(metadata.Authors) == 0 {
		metadata.Authors = unique(authors)
		usedUserData = len(metadata.Authors) > 0
	}
	if len(metadata.Genres) == 0 {
		metadata.Genres = unique(genres)
		usedUserData = usedUserData || len(metadata.Genres) > 0
	}
	if usedUserData {
		if metadata.Source == "" || metadata.Source == "unresolved" {
			metadata.Source = "user"
		} else if !strings.Contains(metadata.Source, "user") {
			metadata.Source += "+user"
		}
	}
	return metadata, nil
}

type searchResponse struct {
	Docs []searchDoc `json:"docs"`
}
type searchDoc struct {
	Title            string   `json:"title"`
	Authors          []string `json:"author_name"`
	FirstPublishYear int      `json:"first_publish_year"`
	Subjects         []string `json:"subject"`
	ISBNs            []string `json:"isbn"`
}

func (r *Resolver) Inspect(ctx context.Context, title, isbn string) (LookupResult, error) {
	requested := strings.TrimSpace(title)
	key := NormalizeTitle(requested)
	if key == "" {
		return LookupResult{}, fmt.Errorf("book title is required")
	}
	if cached, err := r.cache.Get(key); err != nil {
		return LookupResult{}, err
	} else if cached != nil {
		copy := *cached
		return LookupResult{RequestedTitle: requested, Exact: &copy}, nil
	}

	q := url.Values{"title": {title}, "limit": {"10"}}
	if strings.TrimSpace(isbn) != "" {
		q.Set("isbn", isbn)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, openLibrarySearchURL+"?"+q.Encode(), nil)
	if err != nil {
		return LookupResult{}, fmt.Errorf("create catalog request: %w", err)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return LookupResult{}, fmt.Errorf("catalog lookup: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return LookupResult{}, fmt.Errorf("catalog lookup returned %s", resp.Status)
	}
	var result searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return LookupResult{}, fmt.Errorf("decode catalog response: %w", err)
	}
	lookup := LookupResult{RequestedTitle: requested}
	for _, doc := range result.Docs {
		if NormalizeTitle(doc.Title) != key {
			continue
		} // avoid "similar title" misinformation
		metadata := metadataFromDoc(doc, isbn)
		lookup.Exact = &metadata
		return lookup, nil
	}
	if suggestion, ok := bestTitleSuggestion(requested, result.Docs); ok {
		lookup.SuggestedTitle = suggestion.Title
		lookup.SuggestedAuthors = unique(suggestion.Authors)
	}
	return lookup, nil
}

func metadataFromDoc(doc searchDoc, isbn string) models.BookMetadata {
	metadata := models.BookMetadata{Title: doc.Title, Authors: unique(doc.Authors), Genres: genres(doc.Subjects), PublishedYear: doc.FirstPublishYear, Source: "open_library"}
	if IsISBN(isbn) {
		metadata.ISBN = compactISBN(isbn)
	} else {
		for _, candidate := range doc.ISBNs {
			if IsISBN(candidate) {
				metadata.ISBN = compactISBN(candidate)
				break
			}
		}
	}
	return metadata
}

func bestTitleSuggestion(requested string, docs []searchDoc) (searchDoc, bool) {
	requestedKey := NormalizeTitle(requested)
	bestScore := 0.0
	var best searchDoc
	for _, doc := range docs {
		candidateKey := NormalizeTitle(doc.Title)
		if candidateKey == "" || candidateKey == requestedKey {
			continue
		}
		distance := levenshtein(requestedKey, candidateKey)
		longest := max(len([]rune(requestedKey)), len([]rune(candidateKey)))
		if longest == 0 {
			continue
		}
		editScore := 1 - float64(distance)/float64(longest)
		tokenScore := titleTokenOverlap(requested, doc.Title)
		score := max(editScore, tokenScore)
		if score >= 0.62 && score > bestScore {
			bestScore, best = score, doc
		}
	}
	return best, bestScore > 0
}

func titleTokenOverlap(left, right string) float64 {
	tokens := func(value string) map[string]bool {
		out := make(map[string]bool)
		for _, field := range strings.Fields(strings.ToLower(value)) {
			key := NormalizeTitle(field)
			if key != "" {
				out[key] = true
			}
		}
		return out
	}
	a, b := tokens(left), tokens(right)
	shared := 0
	for token := range a {
		if b[token] {
			shared++
		}
	}
	denominator := max(len(a), len(b))
	if denominator == 0 {
		return 0
	}
	return float64(shared) / float64(denominator)
}

func levenshtein(left, right string) int {
	a, b := []rune(left), []rune(right)
	previous := make([]int, len(b)+1)
	for index := range previous {
		previous[index] = index
	}
	for i, leftRune := range a {
		current := make([]int, len(b)+1)
		current[0] = i + 1
		for j, rightRune := range b {
			cost := 1
			if leftRune == rightRune {
				cost = 0
			}
			current[j+1] = min(current[j]+1, previous[j+1]+1, previous[j]+cost)
		}
		previous = current
	}
	return previous[len(b)]
}

func NormalizeTitle(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
func compactISBN(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsDigit(r) || r == 'X' || r == 'x' {
			return unicode.ToUpper(r)
		}
		return -1
	}, value)
}
func IsISBN(value string) bool {
	value = compactISBN(value)
	switch len(value) {
	case 10:
		sum := 0
		for i, r := range value {
			digit := 10
			if r >= '0' && r <= '9' {
				digit = int(r - '0')
			} else if i != 9 || r != 'X' {
				return false
			}
			sum += (10 - i) * digit
		}
		return sum%11 == 0
	case 13:
		sum := 0
		for i, r := range value {
			if r < '0' || r > '9' {
				return false
			}
			weight := 1
			if i%2 == 1 {
				weight = 3
			}
			sum += int(r-'0') * weight
		}
		return sum%10 == 0
	default:
		return false
	}
}
func unique(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
func genres(values []string) []string {
	values = unique(values)
	sort.Strings(values)
	if len(values) > 4 {
		return values[:4]
	}
	return values
}
