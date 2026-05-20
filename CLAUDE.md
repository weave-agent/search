# weave-search Extension

Extension-specific guidance for Claude Code when working on weave-search.

## Architecture

- Single tool: `search` — performs DuckDuckGo Lite web searches
- Implements `sdk.Tool` interface
- Registered via `sdk.RegisterTool[SearchConfig]` in `init()`

## Key Files

- `search.go` — tool implementation (config, struct, register, execute)
- `search_test.go` — unit tests with mocked HTTP

## Patterns

### HTTP client

Use the default `http.Client` with a timeout. For tests, swap in a custom `http.RoundTripper` via `http.Client{Transport: rt}`.

### HTML parsing

Use `golang.org/x/net/html` for parsing DDG Lite response. The key selectors:
- `.result-link` — title and href
- `.result-snippet` — description text

### URL cleaning

DDG redirect URLs look like `//duckduckgo.com/l/?uddg=<encoded-url>`. Extract and decode the real URL from the `uddg` parameter.

### Rate limiting

A random 500ms–2s delay between searches, protected by `sync.Mutex`. This is in `maybeDelaySearch()`.

### Testing

- Mock HTTP responses for DDG scraping
- Test HTML parsing with sample DDG Lite pages
- Test URL cleaning, rate limiting, error cases
- Target 80%+ coverage

## Tool definition

- Name: `search`
- Parameters: `query` (string, required), `max_results` (int, optional, capped at 20)
- Read-only: executes concurrently with other read-only tools
