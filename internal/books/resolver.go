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

func NewResolver(cache *storage.BookMetadataStore, client *http.Client) *Resolver {
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	return &Resolver{cache: cache, client: client}
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

	metadata, found, err := r.lookup(ctx, title, isbn)
	if err != nil {
		return models.BookMetadata{Title: strings.TrimSpace(title), Source: "unresolved"}, nil
	}
	if !found {
		return models.BookMetadata{Title: strings.TrimSpace(title), Source: "unresolved"}, nil
	}
	if err := r.cache.Upsert(key, metadata); err != nil {
		return models.BookMetadata{}, err
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

func (r *Resolver) lookup(ctx context.Context, title, isbn string) (models.BookMetadata, bool, error) {
	q := url.Values{"title": {title}, "limit": {"10"}}
	if strings.TrimSpace(isbn) != "" {
		q.Set("isbn", isbn)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, openLibrarySearchURL+"?"+q.Encode(), nil)
	if err != nil {
		return models.BookMetadata{}, false, fmt.Errorf("create catalog request: %w", err)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return models.BookMetadata{}, false, fmt.Errorf("catalog lookup: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return models.BookMetadata{}, false, fmt.Errorf("catalog lookup returned %s", resp.Status)
	}
	var result searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return models.BookMetadata{}, false, fmt.Errorf("decode catalog response: %w", err)
	}
	key := NormalizeTitle(title)
	for _, doc := range result.Docs {
		if NormalizeTitle(doc.Title) != key {
			continue
		} // avoid "similar title" misinformation
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
		return metadata, true, nil
	}
	return models.BookMetadata{}, false, nil
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
