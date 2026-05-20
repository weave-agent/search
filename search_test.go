package search

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/weave-agent/weave/sdk"
	"golang.org/x/net/html"
)

func TestSearchTool_Name(t *testing.T) {
	tool := &searchTool{}
	assert.Equal(t, "search", tool.Name())
}

func TestSearchTool_Definition(t *testing.T) {
	tool := &searchTool{}
	def := tool.Definition()

	assert.Equal(t, "search", def.Name)
	assert.NotEmpty(t, def.Description)
	assert.NotNil(t, def.Parameters)
}

func TestSearchTool_Execute_EmptyQuery(t *testing.T) {
	tool := &searchTool{defaultMaxResults: 10}
	result, err := tool.Execute(t.Context(), map[string]any{})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "query is required")
}

func TestSearchTool_Execute_BlankQuery(t *testing.T) {
	tool := &searchTool{defaultMaxResults: 10}
	result, err := tool.Execute(t.Context(), map[string]any{"query": "   "})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "query is required")
}

func TestCleanDuckDuckGoURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain URL",
			input:    "https://example.com",
			expected: "https://example.com",
		},
		{
			name:     "DDG redirect",
			input:    "//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com",
			expected: "https://example.com",
		},
		{
			name:     "DDG redirect with path",
			input:    "//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fpath%3Fq%3D1",
			expected: "https://example.com/path?q=1",
		},
		{
			name:     "invalid URL returns raw",
			input:    "://invalid-url",
			expected: "://invalid-url",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanDuckDuckGoURL(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestParseLiteSearchResults(t *testing.T) {
	htmlDoc := `
<!DOCTYPE html>
<html>
<body>
<table>
<tr>
<td>
<a class="result-link" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com">Example Title</a>
</td>
</tr>
<tr>
<td class="result-snippet">This is a sample snippet.</td>
</tr>
<tr>
<td>
<a class="result-link" href="https://another.com">Another Title</a>
</td>
</tr>
<tr>
<td class="result-snippet">Another snippet text.</td>
</tr>
</table>
</body>
</html>
`

	doc, err := html.Parse(strings.NewReader(htmlDoc))
	require.NoError(t, err)

	results := parseLiteSearchResults(doc, 10)
	require.Len(t, results, 2)

	assert.Equal(t, 1, results[0].Position)
	assert.Equal(t, "Example Title", results[0].Title)
	assert.Equal(t, "https://example.com", results[0].Link)
	assert.Equal(t, "This is a sample snippet.", results[0].Snippet)

	assert.Equal(t, 2, results[1].Position)
	assert.Equal(t, "Another Title", results[1].Title)
	assert.Equal(t, "https://another.com", results[1].Link)
	assert.Equal(t, "Another snippet text.", results[1].Snippet)
}

func TestParseLiteSearchResults_MaxResults(t *testing.T) {
	htmlDoc := `
<!DOCTYPE html>
<html>
<body>
<table>
<tr><td><a class="result-link" href="https://1.com">Title 1</a></td></tr>
<tr><td class="result-snippet">Snippet 1</td></tr>
<tr><td><a class="result-link" href="https://2.com">Title 2</a></td></tr>
<tr><td class="result-snippet">Snippet 2</td></tr>
<tr><td><a class="result-link" href="https://3.com">Title 3</a></td></tr>
<tr><td class="result-snippet">Snippet 3</td></tr>
</table>
</body>
</html>
`

	doc, err := html.Parse(strings.NewReader(htmlDoc))
	require.NoError(t, err)

	results := parseLiteSearchResults(doc, 2)
	require.Len(t, results, 2)
	assert.Equal(t, "Title 1", results[0].Title)
	assert.Equal(t, "Title 2", results[1].Title)
}

func TestParseLiteSearchResults_Empty(t *testing.T) {
	htmlDoc := `<!DOCTYPE html><html><body></body></html>`

	doc, err := html.Parse(strings.NewReader(htmlDoc))
	require.NoError(t, err)

	results := parseLiteSearchResults(doc, 10)
	assert.Empty(t, results)
}

func TestMaybeDelaySearch(t *testing.T) {
	// Just verify it doesn't panic and updates lastSearchTime
	lastSearchTime = time.Time{}
	maybeDelaySearch()
	assert.False(t, lastSearchTime.IsZero())
}

func TestInit_RegistersTool(t *testing.T) {
	assert.True(t, sdk.ToolRegistered("search"))
}
