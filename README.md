# weave-search

Search tool extension for [weave](https://github.com/weave-agent/weave) — an event-driven coding agent framework.

Performs web searches via DuckDuckGo Lite scraping. No API key required. Returns numbered search results with title, URL, and snippet for each result.

## Fork & Customize

1. Fork this repo
2. Edit the extension implementation
3. Install your fork: `weave install github.com/<you>/weave-search --name search`

The `--name search` ensures your fork shadows the official extension.

## Install

```bash
weave install github.com/weave-agent/weave-search --name search
```

## Tool: `search`

Parameters:

- `query` (string, required): search query
- `max_results` (int, optional): max results to return, capped at 20

## Configuration

- `max_results` (int, default: 10, env: `MAX_RESULTS`): default maximum results per search
- `timeout` (int, default: 30, env: `TIMEOUT`): HTTP request timeout in seconds

Returns numbered search results with title, URL, and snippet.

## Guardian Enforcement

Before making a DuckDuckGo request, `search` submits the query as a `GuardianActionNetwork` action. If guardian blocks the request, the tool returns a `guardian: blocked` error and does not make the HTTP request. If no guardian is registered, searches run normally.

## Development

```bash
git clone git@github.com:weave-agent/weave-search.git
cd weave-search

# Add temporary replace for local SDK (don't commit this)
echo 'replace github.com/weave-agent/weave => /path/to/local/weave' >> go.mod

# Run tests
go test ./...

# Run linter
golangci-lint run
```

## License

Same as the main weave project.
