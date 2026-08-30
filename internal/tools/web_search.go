package tools

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	readability "codeberg.org/readeck/go-readability/v2"
	"golang.org/x/net/html"
)

type SearchResult struct {
	Title string `json:"title"`
	URL   string `json:"url"`
	Snip  string `json:"snippet"`
}

func (tk *Toolkit) WebSearch(query string) ([]SearchResult, error) {
	if query == "" {
		return nil, fmt.Errorf("empty query")
	}

	u := fmt.Sprintf("https://lite.duckduckgo.com/lite/?q=%s", url.QueryEscape(query))
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}

	return parseDuckDuckGoLite(string(body))
}

func parseDuckDuckGoLite(htmlStr string) ([]SearchResult, error) {
	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return nil, err
	}

	var results []SearchResult

	var current *SearchResult
	var inLink, inSnippet bool
	var textBuf strings.Builder

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.ElementNode:
			switch n.Data {
			case "a":
				for _, a := range n.Attr {
					if a.Key != "href" {
						continue
					}
					if href := resolveDuckDuckGoHref(a.Val); href != "" {
						current = &SearchResult{URL: href}
						inLink = true
						textBuf.Reset()
					}
				}
			case "td":
				if hasClass(n, "result-snippet") {
					inSnippet = true
					textBuf.Reset()
				}
			}
		case html.TextNode:
			if inLink || inSnippet {
				textBuf.WriteString(n.Data)
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}

		if n.Type == html.ElementNode {
			switch n.Data {
			case "a":
				if inLink && current != nil {
					current.Title = strings.TrimSpace(textBuf.String())
					textBuf.Reset()
					inLink = false
				}
			case "td":
				if inSnippet && current != nil {
					current.Snip = strings.TrimSpace(textBuf.String())
					results = append(results, *current)
					current = nil
					textBuf.Reset()
					inSnippet = false
				}
			}
		}
	}

	walk(doc)

	// Filter ads and duplicates.
	var filtered []SearchResult
	seen := make(map[string]bool)
	for _, r := range results {
		if r.URL == "" {
			continue
		}
		if r.Title == "" && r.Snip == "" {
			continue
		}
		if seen[r.URL] {
			continue
		}
		seen[r.URL] = true
		filtered = append(filtered, r)
	}

	return filtered, nil
}

func resolveDuckDuckGoHref(raw string) string {
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	if !strings.Contains(raw, "duckduckgo.com/l/") {
		return ""
	}
	u, err := url.Parse("https:" + strings.TrimPrefix(raw, "//"))
	if err != nil {
		return ""
	}
	uddg := u.Query().Get("uddg")
	if uddg == "" {
		return ""
	}
	decoded, err := url.QueryUnescape(uddg)
	if err != nil {
		return uddg
	}
	return decoded
}

func hasClass(n *html.Node, class string) bool {
	for _, a := range n.Attr {
		if a.Key == "class" && strings.Contains(a.Val, class) {
			return true
		}
	}
	return false
}

// WebFetch fetches a URL and returns the readable article text.
func (tk *Toolkit) WebFetch(u string) (string, error) {
	if u == "" {
		return "", fmt.Errorf("empty URL")
	}

	article, err := readability.FromURL(u, 10*time.Second, func(r *http.Request) {
		r.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	})
	if err != nil {
		// Fallback to a simple text extraction.
		return simpleFetch(u)
	}

	var buf bytes.Buffer
	if err := article.RenderText(&buf); err != nil {
		return "", err
	}

	text := strings.Join(strings.Fields(buf.String()), " ")
	return truncateText(text, 20000), nil
}

func simpleFetch(u string) (string, error) {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}

	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}

	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			text := strings.TrimSpace(n.Data)
			if text != "" {
				b.WriteString(text)
				b.WriteString(" ")
			}
		}
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "noscript") {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode && (n.Data == "p" || n.Data == "div" || n.Data == "br" || n.Data == "h1" || n.Data == "h2" || n.Data == "h3" || n.Data == "h4" || n.Data == "li") {
			if b.Len() > 0 && b.String()[b.Len()-1] != '\n' {
				b.WriteString("\n")
			}
		}
	}
	walk(doc)

	text := strings.Join(strings.Fields(b.String()), " ")
	return truncateText(text, 20000), nil
}

func truncateText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n... (truncated)"
}
