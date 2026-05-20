package search

import (
	"errors"
	"fmt"
	"io"
	"net/http"
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

// mockTransport implements http.RoundTripper for testing.
type mockTransport struct {
	response *http.Response
	err      error
}

func (m *mockTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return m.response, m.err
}

func TestSearchDuckDuckGo_Non200Status(t *testing.T) {
	tool := &searchTool{
		httpClient: &http.Client{
			Transport: &mockTransport{
				response: &http.Response{
					StatusCode: http.StatusServiceUnavailable,
					Body:       io.NopCloser(strings.NewReader("")),
				},
			},
		},
	}

	results, err := tool.searchDuckDuckGo("test", 10)
	require.Error(t, err)
	assert.Nil(t, results)
	assert.Contains(t, err.Error(), "unexpected status code: 503")
}

func TestSearchDuckDuckGo_HTTPFailure(t *testing.T) {
	tool := &searchTool{
		httpClient: &http.Client{
			Transport: &mockTransport{
				err: errors.New("connection refused"),
			},
		},
	}

	results, err := tool.searchDuckDuckGo("test", 10)
	require.Error(t, err)
	assert.Nil(t, results)
	assert.Contains(t, err.Error(), "http request")
	assert.Contains(t, err.Error(), "connection refused")
}

// errorReader is an io.Reader that always returns an error.
type errorReader struct{}

func (errorReader) Read(_ []byte) (int, error) {
	return 0, errors.New("read error")
}

func TestSearchDuckDuckGo_ParseFailure(t *testing.T) {
	tool := &searchTool{
		httpClient: &http.Client{
			Transport: &mockTransport{
				response: &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(errorReader{}),
				},
			},
		},
	}

	results, err := tool.searchDuckDuckGo("test", 10)
	require.Error(t, err)
	assert.Nil(t, results)
	assert.Contains(t, err.Error(), "parse html")
}

func TestSearchDuckDuckGo_Success(t *testing.T) {
	htmlBody := `
<!DOCTYPE html>
<html>
<body>
<table>
<tr><td><a class="result-link" href="https://example.com">Example</a></td></tr>
<tr><td class="result-snippet">A snippet.</td></tr>
</table>
</body>
</html>
`
	tool := &searchTool{
		httpClient: &http.Client{
			Transport: &mockTransport{
				response: &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(htmlBody)),
				},
			},
		},
	}

	results, err := tool.searchDuckDuckGo("test", 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Example", results[0].Title)
	assert.Equal(t, "https://example.com", results[0].Link)
	assert.Equal(t, "A snippet.", results[0].Snippet)
}

func TestSearchTool_Execute_MaxResultsCap(t *testing.T) {
	htmlBody := `
<!DOCTYPE html>
<html>
<body>
<table>
<tr><td><a class="result-link" href="https://1.com">1</a></td></tr>
<tr><td class="result-snippet">s1</td></tr>
<tr><td><a class="result-link" href="https://2.com">2</a></td></tr>
<tr><td class="result-snippet">s2</td></tr>
<tr><td><a class="result-link" href="https://3.com">3</a></td></tr>
<tr><td class="result-snippet">s3</td></tr>
</table>
</body>
</html>
`
	tool := &searchTool{
		defaultMaxResults: 2,
		httpClient: &http.Client{
			Transport: &mockTransport{
				response: &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(htmlBody)),
				},
			},
		},
	}

	// Request more than cap - max_results from args overrides defaultMaxResults
	// 50 is capped to 20, but only 3 results exist in HTML so all 3 returned
	result, err := tool.Execute(t.Context(), map[string]any{"query": "test", "max_results": 50})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "1. 1")
	assert.Contains(t, result.Content, "2. 2")
	assert.Contains(t, result.Content, "3. 3")
}

func TestSearchTool_Execute_DefaultMaxResultsUsed(t *testing.T) {
	htmlBody := `
<!DOCTYPE html>
<html>
<body>
<table>
<tr><td><a class="result-link" href="https://1.com">1</a></td></tr>
<tr><td class="result-snippet">s1</td></tr>
<tr><td><a class="result-link" href="https://2.com">2</a></td></tr>
<tr><td class="result-snippet">s2</td></tr>
<tr><td><a class="result-link" href="https://3.com">3</a></td></tr>
<tr><td class="result-snippet">s3</td></tr>
</table>
</body>
</html>
`
	tool := &searchTool{
		defaultMaxResults: 2,
		httpClient: &http.Client{
			Transport: &mockTransport{
				response: &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(htmlBody)),
				},
			},
		},
	}

	// No max_results in args - should use defaultMaxResults=2
	result, err := tool.Execute(t.Context(), map[string]any{"query": "test"})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "1. 1")
	assert.Contains(t, result.Content, "2. 2")
	assert.NotContains(t, result.Content, "3. 3")
}

func TestSearchTool_Execute_NoResults(t *testing.T) {
	tool := &searchTool{
		defaultMaxResults: 10,
		httpClient: &http.Client{
			Transport: &mockTransport{
				response: &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("<html><body></body></html>")),
				},
			},
		},
	}

	result, err := tool.Execute(t.Context(), map[string]any{"query": "test"})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "No results found.", result.Content)
}

func TestSearchTool_Execute_HTTPError(t *testing.T) {
	tool := &searchTool{
		defaultMaxResults: 10,
		httpClient: &http.Client{
			Transport: &mockTransport{
				err: errors.New("network error"),
			},
		},
	}

	result, err := tool.Execute(t.Context(), map[string]any{"query": "test"})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "network error")
}

func TestMaybeDelaySearch_RateLimits(t *testing.T) {
	// Reset state and set lastSearchTime to now so first call also delays
	lastSearchMu.Lock()
	lastSearchTime = time.Now()
	lastSearchMu.Unlock()

	// First call should delay since lastSearchTime is now
	start := time.Now()
	maybeDelaySearch()
	firstDelay := time.Since(start)
	assert.True(t, firstDelay >= 400*time.Millisecond, "expected delay of at least 400ms, got %v", firstDelay)

	// Second call immediately after should also wait
	start = time.Now()
	maybeDelaySearch()
	secondDelay := time.Since(start)
	assert.True(t, secondDelay >= 400*time.Millisecond, "expected delay of at least 400ms, got %v", secondDelay)
}

func TestCleanDuckDuckGoURL_MissingUddg(t *testing.T) {
	// DDG URL without uddg parameter should return raw
	input := "//duckduckgo.com/l/?q=something"
	got := cleanDuckDuckGoURL(input)
	assert.Equal(t, input, got)
}

func TestCleanDuckDuckGoURL_InvalidEncoding(t *testing.T) {
	// Invalid URL encoding should return raw
	input := "//duckduckgo.com/l/?uddg=%ZZ"
	got := cleanDuckDuckGoURL(input)
	assert.Equal(t, input, got)
}

func TestParseLiteSearchResults_SnippetWithoutLink(t *testing.T) {
	// Snippet without a preceding link: code creates a new result with empty title/link
	htmlDoc := `
<!DOCTYPE html>
<html>
<body>
<table>
<tr><td class="result-snippet">Orphan snippet.</td></tr>
</table>
</body>
</html>
`
	doc, err := html.Parse(strings.NewReader(htmlDoc))
	require.NoError(t, err)

	results := parseLiteSearchResults(doc, 10)
	require.Len(t, results, 1)
	assert.Equal(t, 1, results[0].Position)
	assert.Equal(t, "", results[0].Title)
	assert.Equal(t, "", results[0].Link)
	assert.Equal(t, "Orphan snippet.", results[0].Snippet)
}

func TestParseLiteSearchResults_LinkWithoutSnippet(t *testing.T) {
	// Link without a following snippet should not be added to results
	htmlDoc := `
<!DOCTYPE html>
<html>
<body>
<table>
<tr><td><a class="result-link" href="https://example.com">Title</a></td></tr>
</table>
</body>
</html>
`
	doc, err := html.Parse(strings.NewReader(htmlDoc))
	require.NoError(t, err)

	results := parseLiteSearchResults(doc, 10)
	assert.Empty(t, results)
}

func TestExtractText_NestedElements(t *testing.T) {
	htmlDoc := `<p>Hello <b>world</b>!</p>`
	doc, err := html.Parse(strings.NewReader(htmlDoc))
	require.NoError(t, err)

	// Find the p element
	var pNode *html.Node
	var findP func(*html.Node)
	findP = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "p" {
			pNode = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findP(c)
		}
	}
	findP(doc)
	require.NotNil(t, pNode)

	text := extractText(pNode)
	assert.Equal(t, "Hello world!", text)
}

func TestGetAttr_Missing(t *testing.T) {
	htmlDoc := `<div class="foo"></div>`
	doc, err := html.Parse(strings.NewReader(htmlDoc))
	require.NoError(t, err)

	var divNode *html.Node
	var findDiv func(*html.Node)
	findDiv = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "div" {
			divNode = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findDiv(c)
		}
	}
	findDiv(doc)
	require.NotNil(t, divNode)

	assert.Equal(t, "foo", getAttr(divNode, "class"))
	assert.Equal(t, "", getAttr(divNode, "id"))
}

func TestSearchTool_Execute_QueryTypeValidation(t *testing.T) {
	tool := &searchTool{defaultMaxResults: 10}
	// Non-string query
	result, err := tool.Execute(t.Context(), map[string]any{"query": 123})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "query is required")
}

func TestSearchTool_Execute_MaxResultsFromArgs(t *testing.T) {
	htmlBody := `
<!DOCTYPE html>
<html><body>
<table>
<tr><td><a class="result-link" href="https://1.com">1</a></td></tr>
<tr><td class="result-snippet">s1</td></tr>
<tr><td><a class="result-link" href="https://2.com">2</a></td></tr>
<tr><td class="result-snippet">s2</td></tr>
<tr><td><a class="result-link" href="https://3.com">3</a></td></tr>
<tr><td class="result-snippet">s3</td></tr>
</table>
</body></html>
`
	tool := &searchTool{
		defaultMaxResults: 10,
		httpClient: &http.Client{
			Transport: &mockTransport{
				response: &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(htmlBody)),
				},
			},
		},
	}

	result, err := tool.Execute(t.Context(), map[string]any{"query": "test", "max_results": 2})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "1. 1")
	assert.Contains(t, result.Content, "2. 2")
	assert.NotContains(t, result.Content, "3. 3")
}

func TestRandomUserAgent(t *testing.T) {
	ua1 := randomUserAgent()
	ua2 := randomUserAgent()
	assert.NotEmpty(t, ua1)
	assert.NotEmpty(t, ua2)
	assert.Contains(t, ua1, "Mozilla")
}

func TestSearchDuckDuckGo_Non200StatusCodes(t *testing.T) {
	codes := []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError}
	for _, code := range codes {
		t.Run(http.StatusText(code), func(t *testing.T) {
			tool := &searchTool{
				httpClient: &http.Client{
					Transport: &mockTransport{
						response: &http.Response{
							StatusCode: code,
							Body:       io.NopCloser(strings.NewReader("")),
						},
					},
				},
			}
			_, err := tool.searchDuckDuckGo("test", 10)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unexpected status code")
		})
	}
}

func TestSearchResultStruct(t *testing.T) {
	sr := SearchResult{
		Title:    "Test Title",
		Link:     "https://example.com",
		Snippet:  "Test snippet",
		Position: 1,
	}
	assert.Equal(t, "Test Title", sr.Title)
	assert.Equal(t, "https://example.com", sr.Link)
	assert.Equal(t, "Test snippet", sr.Snippet)
	assert.Equal(t, 1, sr.Position)
}

func TestParseLiteSearchResults_MaxResultsZero(t *testing.T) {
	htmlDoc := `
<!DOCTYPE html>
<html><body>
<table>
<tr><td><a class="result-link" href="https://1.com">1</a></td></tr>
<tr><td class="result-snippet">s1</td></tr>
</table>
</body></html>
`
	doc, err := html.Parse(strings.NewReader(htmlDoc))
	require.NoError(t, err)

	results := parseLiteSearchResults(doc, 0)
	assert.Empty(t, results)
}

func TestMaybeDelaySearch_RespectsMinGap(t *testing.T) {
	// Reset
	lastSearchMu.Lock()
	lastSearchTime = time.Time{}
	lastSearchMu.Unlock()

	// Record the time after first call
	maybeDelaySearch()
	firstTime := lastSearchTime

	// Call again immediately - should sleep
	maybeDelaySearch()
	secondTime := lastSearchTime

	// The second call should have delayed, so secondTime should be after firstTime
	assert.True(t, secondTime.After(firstTime) || secondTime.Equal(firstTime))
}

func TestSearchTool_Execute_ResultsFormatting(t *testing.T) {
	htmlBody := `
<!DOCTYPE html>
<html><body>
<table>
<tr><td><a class="result-link" href="https://example.com">Example Title</a></td></tr>
<tr><td class="result-snippet">A longer snippet with more text.</td></tr>
</table>
</body></html>
`
	tool := &searchTool{
		defaultMaxResults: 10,
		httpClient: &http.Client{
			Transport: &mockTransport{
				response: &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(htmlBody)),
				},
			},
		},
	}

	result, err := tool.Execute(t.Context(), map[string]any{"query": "test"})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "1. Example Title")
	assert.Contains(t, result.Content, "https://example.com")
	assert.Contains(t, result.Content, "A longer snippet")
	// Check formatting has newlines
	assert.True(t, strings.Contains(result.Content, "\n"))
}

// httpRoundTripperFunc allows using a function as RoundTripper.
type httpRoundTripperFunc struct {
	fn func(*http.Request) (*http.Response, error)
}

func (f httpRoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f.fn(req)
}

func TestSearchDuckDuckGo_RequestHeadersSet(t *testing.T) {
	var reqURL string
	transport := httpRoundTripperFunc{
		fn: func(req *http.Request) (*http.Response, error) {
			reqURL = req.URL.String()
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("<html><body></body></html>")),
			}, nil
		},
	}

	tool := &searchTool{
		httpClient: &http.Client{Transport: transport},
	}

	_, err := tool.searchDuckDuckGo("hello world", 5)
	require.NoError(t, err)
	assert.Contains(t, reqURL, "lite.duckduckgo.com")
	assert.Contains(t, reqURL, "hello+world")
}

func TestSearchTool_Defaults(t *testing.T) {
	tool := &searchTool{
		defaultMaxResults: 10,
		timeout:           30 * time.Second,
	}
	assert.Equal(t, 10, tool.defaultMaxResults)
	assert.Equal(t, 30*time.Second, tool.timeout)
}

func TestSearchTool_CustomConfig(t *testing.T) {
	tool := &searchTool{
		defaultMaxResults: 5,
		timeout:           10 * time.Second,
		httpClient:        &http.Client{Timeout: 10 * time.Second},
	}
	assert.Equal(t, 5, tool.defaultMaxResults)
	assert.Equal(t, 10*time.Second, tool.timeout)
	assert.Equal(t, 10*time.Second, tool.httpClient.Timeout)
}

func TestSearchTool_ZeroConfigUsesDefaults(t *testing.T) {
	tool := &searchTool{
		defaultMaxResults: 10,
		timeout:           30 * time.Second,
		httpClient:        &http.Client{Timeout: 30 * time.Second},
	}
	assert.Equal(t, 10, tool.defaultMaxResults)
	assert.Equal(t, 30*time.Second, tool.timeout)
}

func TestCleanDuckDuckGoURL_EmptyString(t *testing.T) {
	got := cleanDuckDuckGoURL("")
	assert.Equal(t, "", got)
}

func TestCleanDuckDuckGoURL_PartialDDG(t *testing.T) {
	// Contains duckduckgo.com but not the redirect pattern
	input := "https://duckduckgo.com/html/"
	got := cleanDuckDuckGoURL(input)
	assert.Equal(t, input, got)
}

func TestParseLiteSearchResults_MultipleClasses(t *testing.T) {
	htmlDoc := `
<!DOCTYPE html>
<html><body>
<table>
<tr><td><a class="result-link extra-class" href="https://example.com">Title</a></td></tr>
<tr><td class="result-snippet other-class">Snippet text.</td></tr>
</table>
</body></html>
`
	doc, err := html.Parse(strings.NewReader(htmlDoc))
	require.NoError(t, err)

	results := parseLiteSearchResults(doc, 10)
	require.Len(t, results, 1)
	assert.Equal(t, "Title", results[0].Title)
	assert.Equal(t, "https://example.com", results[0].Link)
	assert.Equal(t, "Snippet text.", results[0].Snippet)
}

func TestParseLiteSearchResults_RealisticDDG(t *testing.T) {
	// More realistic DDG-like HTML structure
	htmlDoc := `
<!DOCTYPE html>
<html>
<head><title>Search Results</title></head>
<body>
<table>
<tbody>
<tr><td><a class="result-link" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgolang.org%2Fdoc">The Go Programming Language</a></td></tr>
<tr><td class="result-snippet">Go is an open source programming language that makes it easy to build simple, reliable, and efficient software.</td></tr>
<tr><td><a class="result-link" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Ftour.golang.org">A Tour of Go</a></td></tr>
<tr><td class="result-snippet">Welcome to a tour of the Go programming language.</td></tr>
<tr><td><a class="result-link" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgithub.com%2Fgolang">GitHub - golang</a></td></tr>
<tr><td class="result-snippet">The Go programming language.</td></tr>
</tbody>
</table>
</body>
</html>
`
	doc, err := html.Parse(strings.NewReader(htmlDoc))
	require.NoError(t, err)

	results := parseLiteSearchResults(doc, 10)
	require.Len(t, results, 3)

	assert.Equal(t, 1, results[0].Position)
	assert.Equal(t, "The Go Programming Language", results[0].Title)
	assert.Equal(t, "https://golang.org/doc", results[0].Link)

	assert.Equal(t, 2, results[1].Position)
	assert.Equal(t, "A Tour of Go", results[1].Title)
	assert.Equal(t, "https://tour.golang.org", results[1].Link)

	assert.Equal(t, 3, results[2].Position)
	assert.Equal(t, "GitHub - golang", results[2].Title)
	assert.Equal(t, "https://github.com/golang", results[2].Link)
}

func TestParseLiteSearchResults_MalformedHTML(t *testing.T) {
	// Malformed HTML that still parses
	htmlDoc := `<html><body><table><tr><td><a class="result-link" href="https://example.com">Title</a><td class="result-snippet">Snippet</td></tr></table></body></html>`
	doc, err := html.Parse(strings.NewReader(htmlDoc))
	require.NoError(t, err)

	results := parseLiteSearchResults(doc, 10)
	require.Len(t, results, 1)
	assert.Equal(t, "Title", results[0].Title)
	assert.Equal(t, "https://example.com", results[0].Link)
	assert.Equal(t, "Snippet", results[0].Snippet)
}

func TestSearchTool_Execute_NonIntMaxResults(t *testing.T) {
	htmlBody := `
<!DOCTYPE html>
<html><body>
<table>
<tr><td><a class="result-link" href="https://example.com">Title</a></td></tr>
<tr><td class="result-snippet">Snippet</td></tr>
</table>
</body></html>
`
	tool := &searchTool{
		defaultMaxResults: 10,
		httpClient: &http.Client{
			Transport: &mockTransport{
				response: &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(htmlBody)),
				},
			},
		},
	}

	// Negative max_results should use default
	result, err := tool.Execute(t.Context(), map[string]any{"query": "test", "max_results": -1.0})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "1. Title")
}

func TestSearchTool_Execute_FloatMaxResults(t *testing.T) {
	htmlBody := `
<!DOCTYPE html>
<html><body>
<table>
<tr><td><a class="result-link" href="https://example.com">Title</a></td></tr>
<tr><td class="result-snippet">Snippet</td></tr>
</table>
</body></html>
`
	tool := &searchTool{
		defaultMaxResults: 10,
		httpClient: &http.Client{
			Transport: &mockTransport{
				response: &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(htmlBody)),
				},
			},
		},
	}

	// Float max_results should work
	result, err := tool.Execute(t.Context(), map[string]any{"query": "test", "max_results": 1.0})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "1. Title")
}

func TestSearchTool_Execute_LargeMaxResults(t *testing.T) {
	htmlBody := `
<!DOCTYPE html>
<html><body>
<table>
<tr><td><a class="result-link" href="https://example.com">Title</a></td></tr>
<tr><td class="result-snippet">Snippet</td></tr>
</table>
</body></html>
`
	tool := &searchTool{
		defaultMaxResults: 10,
		httpClient: &http.Client{
			Transport: &mockTransport{
				response: &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(htmlBody)),
				},
			},
		},
	}

	// Request way over cap
	result, err := tool.Execute(t.Context(), map[string]any{"query": "test", "max_results": 100.0})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "1. Title")
}

func TestSearchTool_DefinitionStructure(t *testing.T) {
	tool := &searchTool{}
	def := tool.Definition()

	assert.Equal(t, "search", def.Name)
	assert.NotEmpty(t, def.Description)
	assert.Contains(t, def.Description, "DuckDuckGo")

	paramsMap, ok := def.Parameters.(map[string]any)
	require.True(t, ok)

	properties, ok := paramsMap["properties"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, properties, "query")
	assert.Contains(t, properties, "max_results")

	required, ok := paramsMap["required"].([]string)
	require.True(t, ok)
	assert.Contains(t, required, "query")

	assert.Equal(t, false, paramsMap["additionalProperties"])
}

func TestSearchDuckDuckGo_URLConstruction(t *testing.T) {
	var capturedURL string
	transport := httpRoundTripperFunc{
		fn: func(req *http.Request) (*http.Response, error) {
			capturedURL = req.URL.String()
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("<html><body></body></html>")),
			}, nil
		},
	}

	tool := &searchTool{
		httpClient: &http.Client{Transport: transport},
	}

	_, err := tool.searchDuckDuckGo("hello world", 5)
	require.NoError(t, err)
	assert.Contains(t, capturedURL, "lite.duckduckgo.com/lite/")
	assert.Contains(t, capturedURL, "q=hello+world")
}

func TestSearchDuckDuckGo_SpecialCharactersInQuery(t *testing.T) {
	var capturedURL string
	transport := httpRoundTripperFunc{
		fn: func(req *http.Request) (*http.Response, error) {
			capturedURL = req.URL.String()
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("<html><body></body></html>")),
			}, nil
		},
	}

	tool := &searchTool{
		httpClient: &http.Client{Transport: transport},
	}

	_, err := tool.searchDuckDuckGo("hello & world > test", 5)
	require.NoError(t, err)
	// The query should be URL-encoded
	assert.Contains(t, capturedURL, "q=")
	assert.NotContains(t, capturedURL, " ")
}

func TestSearchResult_OutputFormatting(t *testing.T) {
	results := []SearchResult{
		{Title: "First", Link: "https://first.com", Snippet: "First snippet", Position: 1},
		{Title: "Second", Link: "https://second.com", Snippet: "Second snippet", Position: 2},
	}

	var lines []string
	for _, r := range results {
		lines = append(lines, fmt.Sprintf("%d. %s\n   %s\n   %s", r.Position, r.Title, r.Link, r.Snippet))
	}

	output := strings.Join(lines, "\n\n")
	assert.Contains(t, output, "1. First")
	assert.Contains(t, output, "https://first.com")
	assert.Contains(t, output, "First snippet")
	assert.Contains(t, output, "2. Second")
	assert.Contains(t, output, "\n\n")
}
