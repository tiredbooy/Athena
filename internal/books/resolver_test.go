package books

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/tiredbooy/internal/storage"
)

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestResolveCachesExactCatalogMatch(t *testing.T) {
	db, err := storage.Open(t.TempDir() + "/athena.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	requests := 0
	client := &http.Client{Transport: roundTripper(func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"docs":[{"title":"Foundation","author_name":["Isaac Asimov"],"first_publish_year":1951,"subject":["Science fiction"],"isbn":["978-0-553-29335-7"]}]}`)), Header: make(http.Header)}, nil
	})}
	resolver := NewResolver(storage.NewBookMetadataStore(db), client)
	metadata, err := resolver.Resolve(context.Background(), "Foundation", "")
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Authors[0] != "Isaac Asimov" || metadata.PublishedYear != 1951 || metadata.ISBN != "9780553293357" {
		t.Fatalf("unexpected metadata: %+v", metadata)
	}
	_, err = resolver.Resolve(context.Background(), "Foundation", "")
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want one due to cache", requests)
	}
}

func TestISBNValidation(t *testing.T) {
	for _, value := range []string{"978-0-553-29335-7", "0-306-40615-2", "0-306-40615-3"} {
		want := value != "0-306-40615-3"
		if got := IsISBN(value); got != want {
			t.Fatalf("IsISBN(%q) = %t, want %t", value, got, want)
		}
	}
}

func TestResolveUsesUserMetadataOnlyWhenCatalogIsUnavailable(t *testing.T) {
	db, err := storage.Open(t.TempDir() + "/athena.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	client := &http.Client{Transport: roundTripper(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}
	resolver := NewResolver(storage.NewBookMetadataStore(db), client)
	metadata, err := resolver.ResolveWithFallback(t.Context(), "Offline Book", "", []string{"Known Author"}, []string{"Science Fiction"})
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Source != "user" || len(metadata.Authors) != 1 || metadata.Authors[0] != "Known Author" || metadata.Genres[0] != "Science Fiction" {
		t.Fatalf("metadata = %+v", metadata)
	}
}

func TestInspectSuggestsLikelyMisspelledTitleWithoutAcceptingIt(t *testing.T) {
	db, err := storage.Open(t.TempDir() + "/athena.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	client := &http.Client{Transport: roundTripper(func(*http.Request) (*http.Response, error) {
		body := `{"docs":[{"title":"Project Hail Mary","author_name":["Andy Weir"],"subject":["Science fiction"]}]}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	resolver := NewResolver(storage.NewBookMetadataStore(db), client)
	lookup, err := resolver.Inspect(t.Context(), "Project Mary Hill", "")
	if err != nil {
		t.Fatal(err)
	}
	if lookup.Exact != nil || lookup.SuggestedTitle != "Project Hail Mary" || lookup.SuggestedAuthors[0] != "Andy Weir" {
		t.Fatalf("lookup = %+v", lookup)
	}
	_, err = resolver.Resolve(t.Context(), "Project Mary Hill", "")
	var suggestion *TitleSuggestionError
	if !errors.As(err, &suggestion) || suggestion.Suggested != "Project Hail Mary" {
		t.Fatalf("resolve error = %v", err)
	}
}
