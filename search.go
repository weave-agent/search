package search

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	mathrand "math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/weave-agent/weave/sdk"
	"golang.org/x/net/html"
)

const (
	paramQuery          = "query"
	paramMaxResults     = "max_results"
	maxResultsCap       = 20
	maxBodySize         = 10 * 1024 * 1024 // 10 MB
	searchMaxAttempts   = 3
	searchRetryDelayMax = 10 * time.Second
)

var (
	lastSearchMu        sync.Mutex
	lastSearchTime      time.Time
	searchCooldownUntil time.Time
	guardianMu          sync.RWMutex
	guardian            sdk.Guardian
	requestSeq          atomic.Uint64
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
}

func setGuardian(g sdk.Guardian) {
	guardianMu.Lock()
	guardian = g
	guardianMu.Unlock()
}

func getGuardian() sdk.Guardian {
	guardianMu.RLock()

	g := guardian

	guardianMu.RUnlock()

	return g
}

//nolint:gochecknoinits // SDK pattern requires init() for tool registration.
func init() {
	sdk.OnBusReady(func(bus sdk.Bus) {
		bus.On(sdk.GuardianRegisteredTopic, func(ev sdk.Event) error {
			if g, ok := ev.Payload.(sdk.Guardian); ok {
				setGuardian(g)
			}

			return nil
		})
	})

	sdk.RegisterTool("search", func(_ sdk.Config, _ sdk.PreferenceReader, cfg SearchConfig) (sdk.Tool, error) {
		maxResults := cfg.MaxResults
		if maxResults <= 0 {
			maxResults = 10
		}
		if maxResults > maxResultsCap {
			maxResults = maxResultsCap
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
					"type":        "integer",
					"description": "Maximum number of results to return. Capped at 20.",
				},
			},
			"required":             []string{paramQuery},
			"additionalProperties": false,
		},
	}
}

func newRequestID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err == nil {
		return prefix + "-" + hex.EncodeToString(b[:])
	}

	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), requestSeq.Add(1))
}

func guardianRequest(query string) sdk.GuardianRequest {
	return sdk.GuardianRequest{
		ID:          newRequestID("search-guardian"),
		ToolName:    "search",
		Action:      sdk.GuardianActionNetwork,
		Description: "Web search: " + query,
		Metadata: map[string]any{
			"operation": "search",
			"query":     query,
		},
	}
}

func checkGuardian(ctx context.Context, query string) (sdk.GuardianRequest, *sdk.ToolResult) {
	req := guardianRequest(query)

	g := getGuardian()
	if g == nil {
		return req, nil
	}

	decision, err := g.Decide(ctx, req)
	if err != nil {
		return req, &sdk.ToolResult{Content: "guardian: " + err.Error(), IsError: true}
	}

	switch decision.Action {
	case sdk.GuardianDecisionAllow:
		return req, nil
	case sdk.GuardianDecisionBlock:
		return req, &sdk.ToolResult{Content: formatGuardianBlock(req, decision), IsError: true}
	default:
		decision.Action = sdk.GuardianDecisionBlock
		if decision.Reason == "" {
			decision.Reason = "guardian returned unresolved approval decision"
		}

		return req, &sdk.ToolResult{Content: formatGuardianBlock(req, decision), IsError: true}
	}
}

func formatGuardianBlock(req sdk.GuardianRequest, decision sdk.GuardianDecision) string {
	var b strings.Builder

	b.WriteString("guardian: blocked")
	b.WriteString("\naction: ")
	b.WriteString(string(req.Action))

	rule := decision.Profile
	if rule == "" {
		rule = decision.MatchedGrantID
	}
	if rule == "" {
		rule = decision.ID
	}
	if rule != "" {
		b.WriteString("\nrule: ")
		b.WriteString(rule)
	}

	if decision.Reason != "" {
		b.WriteString("\nreason: ")
		b.WriteString(decision.Reason)
	}

	return b.String()
}

func (t *searchTool) Execute(ctx context.Context, args map[string]any) (sdk.ToolResult, error) {
	query, ok := args[paramQuery].(string)
	if !ok || strings.TrimSpace(query) == "" {
		return sdk.ToolResult{Content: "error: query is required and must be non-empty", IsError: true}, nil
	}

	if _, guardianResult := checkGuardian(ctx, query); guardianResult != nil {
		return *guardianResult, nil
	}

	maxResults := t.defaultMaxResults
	if v, ok := args[paramMaxResults]; ok {
		switch n := v.(type) {
		case float64:
			if n > 0 && n <= float64(maxResultsCap) {
				maxResults = int(n)
			} else if n > float64(maxResultsCap) {
				maxResults = maxResultsCap
			}
		case int:
			if n > 0 && n <= maxResultsCap {
				maxResults = n
			} else if n > maxResultsCap {
				maxResults = maxResultsCap
			}
		case int64:
			if n > 0 && n <= int64(maxResultsCap) {
				maxResults = int(n)
			} else if n > int64(maxResultsCap) {
				maxResults = maxResultsCap
			}
		case uint:
			if n > 0 && n <= uint(maxResultsCap) {
				maxResults = int(n)
			} else if n > uint(maxResultsCap) {
				maxResults = maxResultsCap
			}
		case uint64:
			if n > 0 && n <= uint64(maxResultsCap) {
				maxResults = int(n)
			} else if n > uint64(maxResultsCap) {
				maxResults = maxResultsCap
			}
		case string:
			if parsed, err := strconv.Atoi(n); err == nil && parsed > 0 && parsed <= maxResultsCap {
				maxResults = parsed
			} else if err == nil && parsed > maxResultsCap {
				maxResults = maxResultsCap
			}
		}
	}
	if maxResults > maxResultsCap {
		maxResults = maxResultsCap
	}

	if err := t.maybeDelaySearch(ctx); err != nil {
		return sdk.ToolResult{Content: fmt.Sprintf("error: %s", err), IsError: true}, nil
	}

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

func (t *searchTool) maybeDelaySearch(ctx context.Context) error {
	for {
		lastSearchMu.Lock()
		minGap := time.Duration(500+mathrand.IntN(1500)) * time.Millisecond
		readyAt := lastSearchTime.Add(minGap)
		if searchCooldownUntil.After(readyAt) {
			readyAt = searchCooldownUntil
		}
		now := time.Now()
		if !now.Before(readyAt) {
			lastSearchTime = now
			lastSearchMu.Unlock()
			return nil
		}
		remaining := readyAt.Sub(now)
		lastSearchMu.Unlock()

		timer := time.NewTimer(remaining)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		}
	}
}

func (t *searchTool) searchDuckDuckGo(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	searchURL := "https://lite.duckduckgo.com/lite/?q=" + url.QueryEscape(query)
	var lastStatus int

	for attempt := 1; attempt <= searchMaxAttempts; attempt++ {
		resp, err := t.requestDuckDuckGo(ctx, searchURL)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode == http.StatusOK {
			doc, err := html.Parse(io.LimitReader(resp.Body, maxBodySize))
			resp.Body.Close()
			if err != nil {
				return nil, fmt.Errorf("parse html: %w", err)
			}

			return parseLiteSearchResults(doc, maxResults), nil
		}

		lastStatus = resp.StatusCode
		delay := searchRetryDelay(resp, attempt)
		resp.Body.Close()

		if !isRetryableSearchStatus(lastStatus) {
			return nil, fmt.Errorf("unexpected status code: %d", lastStatus)
		}
		if attempt == searchMaxAttempts {
			break
		}

		recordSearchCooldown(delay)
		if err := waitForSearchRetry(ctx, delay); err != nil {
			return nil, err
		}
	}

	return nil, fmt.Errorf("search provider is temporarily delaying or rate-limiting results (status %d) after %d attempts", lastStatus, searchMaxAttempts)
}

func (t *searchTool) requestDuckDuckGo(ctx context.Context, searchURL string) (*http.Response, error) {
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
	return resp, nil
}

func isRetryableSearchStatus(status int) bool {
	switch status {
	case http.StatusAccepted,
		http.StatusRequestTimeout,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func searchRetryDelay(resp *http.Response, attempt int) time.Duration {
	if delay, ok := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()); ok {
		return min(delay, searchRetryDelayMax)
	}

	delay := time.Second << (attempt - 1)
	jitter := time.Duration(mathrand.IntN(500)) * time.Millisecond
	return min(delay+jitter, searchRetryDelayMax)
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}

	seconds, err := strconv.Atoi(value)
	if err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second, true
	}

	retryAt, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	if retryAt.Before(now) {
		return 0, true
	}
	return retryAt.Sub(now), true
}

func recordSearchCooldown(delay time.Duration) {
	if delay <= 0 {
		return
	}

	lastSearchMu.Lock()
	defer lastSearchMu.Unlock()

	until := time.Now().Add(delay)
	if until.After(searchCooldownUntil) {
		searchCooldownUntil = until
	}
}

func waitForSearchRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func randomUserAgent() string {
	agents := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	}
	return agents[mathrand.IntN(len(agents))]
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
			switch {
			case hasClass(n, "result-link"):
				current = &SearchResult{Position: len(results) + 1}
				title := extractText(n)
				if title != "" {
					current.Title = title
				}
				href := getAttr(n, "href")
				if href != "" {
					current.Link = cleanDuckDuckGoURL(href)
				}

			case hasClass(n, "result-snippet"):
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

func hasClass(n *html.Node, class string) bool {
	for _, attr := range n.Attr {
		if attr.Key == "class" {
			for _, c := range strings.Fields(attr.Val) {
				if c == class {
					return true
				}
			}
		}
	}
	return false
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
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}

	isDDG := (parsed.Host == "duckduckgo.com" || strings.HasSuffix(parsed.Host, ".duckduckgo.com")) && parsed.Path == "/l/"
	isRelative := parsed.Host == "" && parsed.Path == "/l/"
	if !isDDG && !isRelative {
		return rawURL
	}

	uddg := parsed.Query().Get("uddg")
	if uddg == "" {
		return rawURL
	}

	u, err := url.Parse(uddg)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return rawURL
	}

	return uddg
}
