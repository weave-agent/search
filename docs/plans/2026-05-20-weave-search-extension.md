# weave-search Extension

## Overview

Add a `search` tool to weave that performs web searches via DuckDuckGo Lite scraping. No API key required. Returns numbered search results with title, URL, and snippet for each result.

- **Problem it solves**: Agent cannot access current web information (docs, APIs, recent changes)
- **Key benefits**: Zero setup, no API keys, works immediately after install
- **Integration**: Auto-discovered via `sdk.RegisterTool`, executes concurrently with other read-only tools

## Context (from discovery)

- **Files/components involved**: New extension repo `github.com/weave-agent/weave-search`
- **Related patterns**: `read`, `grep`, `find`, `ls` — all read-only tools that execute concurrently
- **Reference implementation**: crush's `web_search` tool at `internal/agent/tools/web_search.go` and `search.go`
- **Dependencies**: `golang.org/x/net/html` (HTML parsing), standard `net/http`

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- Run tests after each change
- Maintain backward compatibility

## Testing Strategy

- **Unit tests**: Required for every task
  - Mock HTTP responses for DDG scraping
  - Test HTML parsing with sample DDG Lite pages
  - Test URL cleaning, rate limiting, error cases
- **No E2E tests** — this is a backend tool extension

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix

## Implementation Steps

### Task 1: Bootstrap extension module

- [x] Create `go.mod` with module `github.com/weave-agent/weave-search`
- [x] Create `README.md` with tool description and install instructions
- [x] Create `CLAUDE.md` with extension-specific guidance
- [x] Verify module builds: `go build ./...`

### Task 2: Implement search tool core

- [x] Create `search.go` with config struct:
  ```go
  type SearchConfig struct {
      MaxResults int `json:"max_results" default:"10" env:"MAX_RESULTS"`
      Timeout    int `json:"timeout"    default:"30" env:"TIMEOUT"`
  }
  ```
- [x] Implement `searchTool` struct with `sdk.Tool` interface
- [x] Implement `init()` with `sdk.RegisterTool[SearchConfig]`
- [x] Implement DDG Lite scraping:
  - Build URL: `https://lite.duckduckgo.com/lite/?q=<query>`
  - HTTP GET with randomized User-Agent and Accept-Language headers
  - Parse HTML with `golang.org/x/net/html`
  - Extract `.result-link` (title, href) and `.result-snippet` (description)
  - Clean DDG redirect URLs (`//duckduckgo.com/l/?uddg=...` → real URL)
- [x] Implement rate limiting: random 500ms–2s delay between searches (mutex-protected)
- [x] Format results as numbered list

### Task 3: Write tests for search tool

- [x] Write tests for `parseLiteSearchResults` with sample HTML
- [x] Write tests for `cleanDuckDuckGoURL` with various redirect formats
- [x] Write tests for `searchDuckDuckGo` with mocked HTTP client
- [x] Write tests for rate limiting behavior
- [x] Write tests for error cases: empty query, HTTP failure, non-200 status, parse failure
- [x] Run tests: `go test ./...` — must pass

### Task 4: Polish and validate

- [x] Add tool description markdown file (`search.md`)
- [x] Verify tool definition JSON schema is correct
- [x] Run linter: `make lint` or `golangci-lint run`
- [x] Run formatter: `make fmt`
- [x] Verify test coverage is 80%+

### Task 5: Integration test with weave core

- [ ] Install extension locally: `weave install /path/to/search`
- [ ] Verify `weave list` shows the extension
- [ ] Run weave and verify `search` appears in tool list
- [ ] Test actual search query end-to-end
- [ ] Verify results format in conversation

## Technical Details

### Data structures

```go
type SearchResult struct {
    Title    string
    Link     string
    Snippet  string
    Position int
}
```

### Tool definition

- Name: `search`
- Parameters:
  - `query` (string, required): search query
  - `max_results` (int, optional): max results to return, capped at 20

### Processing flow

1. Validate `query` via `sdk.Validate`
2. Cap `max_results` to 20
3. Apply rate-limit delay
4. HTTP GET to DDG Lite
5. Parse HTML → extract results
6. Clean redirect URLs
7. Format and return `sdk.ToolResult`

### Rate limiting

```go
var (
    lastSearchMu   sync.Mutex
    lastSearchTime time.Time
)

func maybeDelaySearch() {
    lastSearchMu.Lock()
    defer lastSearchMu.Unlock()
    minGap := time.Duration(500+rand.IntN(1500)) * time.Millisecond
    elapsed := time.Since(lastSearchTime)
    if elapsed < minGap {
        time.Sleep(minGap - elapsed)
    }
    lastSearchTime = time.Now()
}
```

## Post-Completion

**Manual verification:**
- Test search with various queries (code, docs, news)
- Verify rate limiting works under rapid successive calls
- Check behavior when DDG returns no results or blocks

**External system updates:**
- Add `search` to first-run bootstrap list in `weave/internal/extmanage/bootstrap.go`
- Create GitHub repo `weave-agent/weave-search`
