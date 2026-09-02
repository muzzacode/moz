package tools

import (
	"strings"
	"testing"
)

func TestRenderWebSearch(t *testing.T) {
	if got := renderWebSearch(nil); got != "no results" {
		t.Fatalf("empty results: got %q", got)
	}

	results := []SearchResult{
		{Title: "Foo", URL: "https://example.com/foo", Snip: "the foo snippet"},
		{Title: "", URL: "https://example.com/bar", Snip: "the bar snippet"},
	}
	got := renderWebSearch(results)

	if !strings.Contains(got, "2 result(s)") {
		t.Errorf("want result count, got %q", got)
	}
	if !strings.Contains(got, "Foo") || !strings.Contains(got, "the foo snippet") || !strings.Contains(got, "https://example.com/foo") {
		t.Errorf("missing first result details, got %q", got)
	}
	if !strings.Contains(got, "https://example.com/bar") {
		t.Errorf("missing second result fallback, got %q", got)
	}
}
