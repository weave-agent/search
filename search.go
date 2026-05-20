package search

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/weave-agent/weave/sdk"
	"golang.org/x/net/html"
)

const (
	paramQuery      = "query"
	paramMaxResults = "max_results"
	maxResultsCap   = 20
	maxBodySize     = 10 * 1024 * 1024 // 10 MB
)

// SearchConfig holds per-tool settings for the search tool.
type SearchConfig struct {
	MaxResults int `json:"max_results" default:"10" env:"MAX_RESULTS"`
	Timeout    int `json:"timeout"    default:"30" env:"TIMEOUT"`
}

// SearchResult represents a single search result.
type SearchResult struct {
	Title    string
	Link     string
	Snippet  string
	Position int
}

type searchTool struct {
	defaultMaxResults int
	httpClient        *http.Client
	lastSearchMu      sync.Mutex
	lastSearchTime    time.Time
}

//nolint:gochecknoinits // SDK pattern requires init() for tool registration.
func init() {
	sdk.RegisterTool("search", func(_ sdk.Config, _ sdk.PreferenceReader, cfg SearchConfig) (sdk.Tool, error) {
		maxResults := cfg.MaxResults
		if maxResults <= 0 {
			maxResults = 10
		}

		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = 30
		}

		return &searchTool{
			defaultMaxResults: maxResults,
			httpClient:        &http.Client{Timeout: time.Duration(timeout) * time.Second},
		}, nil
	})
}

func (t *searchTool) Name() string { return "search" }

func (t *searchTool) Definition() sdk.ToolDef {
	return sdk.ToolDef{
		Name:        "search",
		Description: "Search the web using DuckDuckGo. Returns numbered search results with title, URL, and snippet. No API key required.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				paramQuery: map[string]any{
					"type":        "string",
					"description": "Search query string.",
				},
				paramMaxResults: map[string]any{
					"type":        "number",
					"description": "Maximum number of results to return. Capped at 20.",
				},
			},
			"required":             []string{paramQuery},
			"additionalProperties": false,
		},
	}
}

func (t *searchTool) Execute(ctx context.Context, args map[string]any) (sdk.ToolResult, error) {
	query, ok := args[paramQuery].(string)
	if !ok || strings.TrimSpace(query) == "" {
		return sdk.ToolResult{Content: "error: query is required and must be non-empty", IsError: true}, nil
	}

	maxResults := t.defaultMaxResults
	if v, ok := args[paramMaxResults]; ok {
		switch n := v.(type) {
		case float64:
			if n > 0 {
				maxResults = int(n)
			}
		case int:
			if n > 0 {
				maxResults = n
			}
		case int64:
			if n > 0 && n <= int64(maxResultsCap) {
				maxResults = int(n)
			}
		case uint:
			if n > 0 && n <= uint(maxResultsCap) {
				maxResults = int(n)
			}
		case uint64:
			if n > 0 && n <= uint64(maxResultsCap) {
				maxResults = int(n)
			}
		case string:
			if parsed, err := strconv.Atoi(n); err == nil && parsed > 0 {
				maxResults = parsed
			}
		}
	}
	if maxResults > maxResultsCap {
		maxResults = maxResultsCap
	}

	t.maybeDelaySearch(ctx)

	results, err := t.searchDuckDuckGo(ctx, query, maxResults)
	if err != nil {
		return sdk.ToolResult{Content: fmt.Sprintf("error: %s", err), IsError: true}, nil
	}

	if len(results) == 0 {
		return sdk.ToolResult{Content: "No results found.", IsError: false}, nil
	}

	var lines []string
	for _, r := range results {
		lines = append(lines, fmt.Sprintf("%d. %s\n   %s\n   %s", r.Position, r.Title, r.Link, r.Snippet))
	}

	return sdk.ToolResult{Content: strings.Join(lines, "\n\n"), IsError: false}, nil
}

func (t *searchTool) maybeDelaySearch(ctx context.Context) {
	t.lastSearchMu.Lock()
	defer t.lastSearchMu.Unlock()

	minGap := time.Duration(500+rand.IntN(1500)) * time.Millisecond
	elapsed := time.Since(t.lastSearchTime)
	if elapsed < minGap {
		select {
		case <-time.After(minGap - elapsed):
		case <-ctx.Done():
			return
		}
	}
	t.lastSearchTime = time.Now()
}

func (t *searchTool) searchDuckDuckGo(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	searchURL := "https://lite.duckduckgo.com/lite/?q=" + url.QueryEscape(query)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("User-Agent", randomUserAgent())
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept", "text/html")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	doc, err := html.Parse(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return nil, fmt.Errorf("parse html: %w", err)
	}

	results := parseLiteSearchResults(doc, maxResults)
	return results, nil
}

func randomUserAgent() string {
	agents := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36",
	}
	return agents[rand.IntN(len(agents))]
}

func parseLiteSearchResults(doc *html.Node, maxResults int) []SearchResult {
	var results []SearchResult
	var current *SearchResult

	var f func(*html.Node)
	f = func(n *html.Node) {
		if len(results) >= maxResults {
			return
		}

		if n.Type == html.ElementNode {
			class := getAttr(n, "class")

			switch {
			case strings.Contains(class, "result-link"):
				if n.FirstChild != nil && n.FirstChild.Type == html.TextNode {
					if current == nil {
						current = &SearchResult{Position: len(results) + 1}
					}
					current.Title = strings.TrimSpace(n.FirstChild.Data)
				}
				href := getAttr(n, "href")
				if href != "" {
					if current == nil {
						current = &SearchResult{Position: len(results) + 1}
					}
					current.Link = cleanDuckDuckGoURL(href)
				}

			case strings.Contains(class, "result-snippet"):
				snippet := extractText(n)
				if snippet != "" {
					if current == nil {
						current = &SearchResult{Position: len(results) + 1}
					}
					current.Snippet = snippet
					results = append(results, *current)
					current = nil
				}
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
			if len(results) >= maxResults {
				return
			}
		}
	}

	f(doc)
	return results
}

func getAttr(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func extractText(n *html.Node) string {
	if n == nil {
		return ""
	}

	var b strings.Builder
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(n)
	return strings.TrimSpace(b.String())
}

func cleanDuckDuckGoURL(rawURL string) string {
	if !strings.Contains(rawURL, "duckduckgo.com/l/?uddg=") {
		return rawURL
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}

	uddg := parsed.Query().Get("uddg")
	if uddg == "" {
		return rawURL
	}

	decoded, err := url.QueryUnescape(uddg)
	if err != nil {
		return rawURL
	}

	return decoded
}
