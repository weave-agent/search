# search

Search the web using DuckDuckGo Lite. Returns numbered search results with title, URL, and snippet for each result. No API key required.

## Parameters

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `query` | string | yes | Search query string |
| `max_results` | number | no | Maximum number of results to return (capped at 20, default: 10) |

## Configuration

| Name | Type | Default | Env Var | Description |
|------|------|---------|---------|-------------|
| `max_results` | int | 10 | `MAX_RESULTS` | Default maximum results per search |
| `timeout` | int | 30 | `TIMEOUT` | HTTP request timeout in seconds |

## Returns

Numbered list of search results, each containing:
- **Title** — the page title
- **URL** — the cleaned destination URL
- **Snippet** — a short description of the page

## Example

```json
{
  "query": "golang error handling",
  "max_results": 5
}
```

## Rate Limiting

A random delay of 500ms–2s is applied between consecutive searches to avoid overwhelming DuckDuckGo.
