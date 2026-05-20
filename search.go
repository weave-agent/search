package search

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
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
	timeout           time.Duration
	httpClient        *http.Client
}

var (
	lastSearchMu   sync.Mutex
	lastSearchTime time.Time
)

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
			timeout:           time.Duration(timeout) * time.Second,
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

func (t *searchTool) Execute(_ context.Context, args map[string]any) (sdk.ToolResult, error) {
	query, ok := args[paramQuery].(string)
	if !ok || strings.TrimSpace(query) == "" {
		return sdk.ToolResult{Content: "error: query is required and must be non-empty", IsError: true}, nil
	}

	maxResults := t.defaultMaxResults
	if v, ok := args[paramMaxResults]; ok {
		if f, ok := v.(float64); ok && f > 0 {
			maxResults = int(f)
		} else if i, ok := v.(int); ok && i > 0 {
			maxResults = i
		}
	}
	if maxResults > maxResultsCap {
		maxResults = maxResultsCap
	}

	maybeDelaySearch()

	results, err := t.searchDuckDuckGo(query, maxResults)
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

func maybeDelaySearch() {
	lastSearchMu.Lock()
	defer lastSearchMu.Unlock()

	minGap := time.Duration(500+rand.Intn(1500)) * time.Millisecond
	elapsed := time.Since(lastSearchTime)
	if elapsed < minGap {
		time.Sleep(minGap - elapsed)
	}
	lastSearchTime = time.Now()
}

func (t *searchTool) searchDuckDuckGo(query string, maxResults int) ([]SearchResult, error) {
	searchURL := fmt.Sprintf("https://lite.duckduckgo.com/lite/?q=%s", url.QueryEscape(query))

	req, err := http.NewRequest(http.MethodGet, searchURL, nil)
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

	doc, err := html.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parse html: %w", err)
	}

	results := parseLiteSearchResults(doc, maxResults)
	return results, nil
}

func randomUserAgent() string {
	agents := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.0",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.0",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.0",
	}
	return agents[rand.Intn(len(agents))]
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
