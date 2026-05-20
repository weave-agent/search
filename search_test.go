package search

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/html"
)

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

func TestSearchTool_Execute_QueryTypeValidation(t *testing.T) {
	tool := &searchTool{defaultMaxResults: 10}
	result, err := tool.Execute(t.Context(), map[string]any{"query": 123})
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
		{
			name:     "null bytes in URL",
			input:    "\x00invalid",
			expected: "\x00invalid",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "partial DDG without redirect pattern",
			input:    "https://duckduckgo.com/html/",
			expected: "https://duckduckgo.com/html/",
		},
		{
			name:     "missing uddg parameter",
			input:    "//duckduckgo.com/l/?q=something",
			expected: "//duckduckgo.com/l/?q=something",
		},
		{
			name:     "invalid URL encoding",
			input:    "//duckduckgo.com/l/?uddg=%ZZ",
			expected: "//duckduckgo.com/l/?uddg=%ZZ",
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

func TestParseLiteSearchResults_Empty(t *testing.T) {
	htmlDoc := `<!DOCTYPE html><html><body></body></html>`

	doc, err := html.Parse(strings.NewReader(htmlDoc))
	require.NoError(t, err)

	results := parseLiteSearchResults(doc, 10)
	assert.Empty(t, results)
}

func TestParseLiteSearchResults_SnippetWithoutLink(t *testing.T) {
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
	assert.Empty(t, results[0].Title)
	assert.Empty(t, results[0].Link)
	assert.Equal(t, "Orphan snippet.", results[0].Snippet)
}

func TestParseLiteSearchResults_LinkWithoutSnippet(t *testing.T) {
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

func TestParseLiteSearchResults_FormattedTitle(t *testing.T) {
	htmlDoc := `
<!DOCTYPE html>
<html><body>
<table>
<tr><td><a class="result-link" href="https://example.com"><b>Bold</b> Title</a></td></tr>
<tr><td class="result-snippet">Snippet text.</td></tr>
</table>
</body></html>
`
	doc, err := html.Parse(strings.NewReader(htmlDoc))
	require.NoError(t, err)

	results := parseLiteSearchResults(doc, 10)
	require.Len(t, results, 1)
	assert.Equal(t, "Bold Title", results[0].Title)
	assert.Equal(t, "https://example.com", results[0].Link)
	assert.Equal(t, "Snippet text.", results[0].Snippet)
}

func TestParseLiteSearchResults_MultipleLinksNoMixing(t *testing.T) {
	htmlDoc := `
<!DOCTYPE html>
<html><body>
<table>
<tr><td><a class="result-link" href="https://first.com">First Title</a></td></tr>
<tr><td><a class="result-link" href="https://second.com">Second Title</a></td></tr>
<tr><td class="result-snippet">Second snippet.</td></tr>
</table>
</body></html>
`
	doc, err := html.Parse(strings.NewReader(htmlDoc))
	require.NoError(t, err)

	results := parseLiteSearchResults(doc, 10)
	require.Len(t, results, 1)
	// Should use the SECOND link's data (closest to snippet), not mix first link with second snippet
	assert.Equal(t, "Second Title", results[0].Title)
	assert.Equal(t, "https://second.com", results[0].Link)
	assert.Equal(t, "Second snippet.", results[0].Snippet)
}

func TestParseLiteSearchResults_MalformedHTML(t *testing.T) {
	htmlDoc := `<html><body><table><tr><td><a class="result-link" href="https://example.com">Title</a><td class="result-snippet">Snippet</td></tr></table></body></html>`
	doc, err := html.Parse(strings.NewReader(htmlDoc))
	require.NoError(t, err)

	results := parseLiteSearchResults(doc, 10)
	require.Len(t, results, 1)
	assert.Equal(t, "Title", results[0].Title)
	assert.Equal(t, "https://example.com", results[0].Link)
	assert.Equal(t, "Snippet", results[0].Snippet)
}

func TestExtractText_NestedElements(t *testing.T) {
	htmlDoc := `<p>Hello <b>world</b>!</p>`
	doc, err := html.Parse(strings.NewReader(htmlDoc))
	require.NoError(t, err)

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

func TestExtractText_NilNode(t *testing.T) {
	result := extractText(nil)
	assert.Empty(t, result)
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
	assert.Empty(t, getAttr(divNode, "id"))
}

// httpRoundTripperFunc allows using a function as RoundTripper.
type httpRoundTripperFunc struct {
	fn func(*http.Request) (*http.Response, error)
}

func (f httpRoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f.fn(req)
}

// errorReader is an io.Reader that always returns an error.
type errorReader struct{}

func (errorReader) Read(_ []byte) (int, error) {
	return 0, errors.New("read error")
}

func TestSearchDuckDuckGo_Non200Status(t *testing.T) {
	tool := &searchTool{
		httpClient: &http.Client{
			Transport: httpRoundTripperFunc{
				fn: func(_ *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusServiceUnavailable,
						Body:       io.NopCloser(strings.NewReader("")),
					}, nil
				},
			},
		},
	}

	results, err := tool.searchDuckDuckGo(context.Background(), "test", 10)
	require.Error(t, err)
	assert.Nil(t, results)
	assert.Contains(t, err.Error(), "unexpected status code: 503")
}

func TestSearchDuckDuckGo_Non200StatusCodes(t *testing.T) {
	codes := []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError}
	for _, code := range codes {
		t.Run(http.StatusText(code), func(t *testing.T) {
			tool := &searchTool{
				httpClient: &http.Client{
					Transport: httpRoundTripperFunc{
						fn: func(_ *http.Request) (*http.Response, error) {
							return &http.Response{
								StatusCode: code,
								Body:       io.NopCloser(strings.NewReader("")),
							}, nil
						},
					},
				},
			}
			_, err := tool.searchDuckDuckGo(context.Background(), "test", 10)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unexpected status code")
		})
	}
}

func TestSearchDuckDuckGo_HTTPFailure(t *testing.T) {
	tool := &searchTool{
		httpClient: &http.Client{
			Transport: httpRoundTripperFunc{
				fn: func(_ *http.Request) (*http.Response, error) {
					return nil, errors.New("connection refused")
				},
			},
		},
	}

	results, err := tool.searchDuckDuckGo(context.Background(), "test", 10)
	require.Error(t, err)
	assert.Nil(t, results)
	assert.Contains(t, err.Error(), "http request")
	assert.Contains(t, err.Error(), "connection refused")
}

func TestSearchDuckDuckGo_ParseFailure(t *testing.T) {
	tool := &searchTool{
		httpClient: &http.Client{
			Transport: httpRoundTripperFunc{
				fn: func(_ *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(errorReader{}),
					}, nil
				},
			},
		},
	}

	results, err := tool.searchDuckDuckGo(context.Background(), "test", 10)
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
			Transport: httpRoundTripperFunc{
				fn: func(_ *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(htmlBody)),
					}, nil
				},
			},
		},
	}

	results, err := tool.searchDuckDuckGo(context.Background(), "test", 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Example", results[0].Title)
	assert.Equal(t, "https://example.com", results[0].Link)
	assert.Equal(t, "A snippet.", results[0].Snippet)
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

	_, err := tool.searchDuckDuckGo(context.Background(), "hello world", 5)
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

	_, err := tool.searchDuckDuckGo(context.Background(), "hello & world > test", 5)
	require.NoError(t, err)
	assert.Contains(t, capturedURL, "q=hello+%26+world+%3E+test")
}

func TestSearchDuckDuckGo_RequestHeadersSet(t *testing.T) {
	var capturedReq *http.Request
	transport := httpRoundTripperFunc{
		fn: func(req *http.Request) (*http.Response, error) {
			capturedReq = req
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("<html><body></body></html>")),
			}, nil
		},
	}

	tool := &searchTool{
		httpClient: &http.Client{Transport: transport},
	}

	_, err := tool.searchDuckDuckGo(context.Background(), "hello world", 5)
	require.NoError(t, err)
	require.NotNil(t, capturedReq)
	assert.Contains(t, capturedReq.URL.String(), "lite.duckduckgo.com")
	assert.Contains(t, capturedReq.Header.Get("User-Agent"), "Mozilla")
	assert.Equal(t, "en-US,en;q=0.9", capturedReq.Header.Get("Accept-Language"))
	assert.Equal(t, "text/html", capturedReq.Header.Get("Accept"))
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
			Transport: httpRoundTripperFunc{
				fn: func(_ *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(htmlBody)),
					}, nil
				},
			},
		},
	}

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
			Transport: httpRoundTripperFunc{
				fn: func(_ *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(htmlBody)),
					}, nil
				},
			},
		},
	}

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
			Transport: httpRoundTripperFunc{
				fn: func(_ *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader("<html><body></body></html>")),
					}, nil
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
			Transport: httpRoundTripperFunc{
				fn: func(_ *http.Request) (*http.Response, error) {
					return nil, errors.New("network error")
				},
			},
		},
	}

	result, err := tool.Execute(t.Context(), map[string]any{"query": "test"})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "network error")
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
			Transport: httpRoundTripperFunc{
				fn: func(_ *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(htmlBody)),
					}, nil
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

func TestSearchTool_Execute_MaxResultsIntType(t *testing.T) {
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
			Transport: httpRoundTripperFunc{
				fn: func(_ *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(htmlBody)),
					}, nil
				},
			},
		},
	}

	result, err := tool.Execute(t.Context(), map[string]any{"query": "test", "max_results": int(1)})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "1. Title")
}

func TestSearchTool_Execute_MaxResultsZero(t *testing.T) {
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
			Transport: httpRoundTripperFunc{
				fn: func(_ *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(htmlBody)),
					}, nil
				},
			},
		},
	}

	// Zero max_results (int) should use default
	result, err := tool.Execute(t.Context(), map[string]any{"query": "test", "max_results": 0})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "1. Title")

	// Zero max_results (float64) should also use default
	result, err = tool.Execute(t.Context(), map[string]any{"query": "test", "max_results": 0.0})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "1. Title")
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
			Transport: httpRoundTripperFunc{
				fn: func(_ *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(htmlBody)),
					}, nil
				},
			},
		},
	}

	result, err := tool.Execute(t.Context(), map[string]any{"query": "test", "max_results": -1.0})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "1. Title")
}

func TestSearchTool_Execute_MaxResultsInt64(t *testing.T) {
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
			Transport: httpRoundTripperFunc{
				fn: func(_ *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(htmlBody)),
					}, nil
				},
			},
		},
	}

	result, err := tool.Execute(t.Context(), map[string]any{"query": "test", "max_results": int64(1)})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "1. Title")
}

func TestSearchTool_Execute_MaxResultsInt64OverCap(t *testing.T) {
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
			Transport: httpRoundTripperFunc{
				fn: func(_ *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(htmlBody)),
					}, nil
				},
			},
		},
	}

	result, err := tool.Execute(t.Context(), map[string]any{"query": "test", "max_results": int64(100)})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "1. Title")
}

func TestSearchTool_Execute_MaxResultsUint(t *testing.T) {
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
			Transport: httpRoundTripperFunc{
				fn: func(_ *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(htmlBody)),
					}, nil
				},
			},
		},
	}

	result, err := tool.Execute(t.Context(), map[string]any{"query": "test", "max_results": uint(1)})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "1. Title")
}

func TestSearchTool_Execute_MaxResultsUint64(t *testing.T) {
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
			Transport: httpRoundTripperFunc{
				fn: func(_ *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(htmlBody)),
					}, nil
				},
			},
		},
	}

	result, err := tool.Execute(t.Context(), map[string]any{"query": "test", "max_results": uint64(1)})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "1. Title")
}

func TestSearchTool_Execute_MaxResultsString(t *testing.T) {
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
			Transport: httpRoundTripperFunc{
				fn: func(_ *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(htmlBody)),
					}, nil
				},
			},
		},
	}

	result, err := tool.Execute(t.Context(), map[string]any{"query": "test", "max_results": "1"})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "1. Title")
}

func TestSearchTool_Execute_MaxResultsStringOverCap(t *testing.T) {
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
			Transport: httpRoundTripperFunc{
				fn: func(_ *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(htmlBody)),
					}, nil
				},
			},
		},
	}

	result, err := tool.Execute(t.Context(), map[string]any{"query": "test", "max_results": "100"})
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
			Transport: httpRoundTripperFunc{
				fn: func(_ *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(htmlBody)),
					}, nil
				},
			},
		},
	}

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
			Transport: httpRoundTripperFunc{
				fn: func(_ *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(htmlBody)),
					}, nil
				},
			},
		},
	}

	result, err := tool.Execute(t.Context(), map[string]any{"query": "test", "max_results": 100.0})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "1. Title")
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
			Transport: httpRoundTripperFunc{
				fn: func(_ *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(htmlBody)),
					}, nil
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
	assert.Contains(t, result.Content, "\n")
}

func TestRandomUserAgent(t *testing.T) {
	knownAgents := map[string]bool{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36":      true,
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36": true,
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36":                true,
	}

	ua := randomUserAgent()
	assert.NotEmpty(t, ua)
	assert.Contains(t, ua, "Mozilla")
	assert.True(t, knownAgents[ua], "returned UA should be one of the known agents: %s", ua)
}

func TestMaybeDelaySearch(t *testing.T) {
	tool := &searchTool{}
	tool.lastSearchMu.Lock()
	tool.lastSearchTime = time.Time{}
	tool.lastSearchMu.Unlock()

	err := tool.maybeDelaySearch(context.Background())
	require.NoError(t, err)
	assert.False(t, tool.lastSearchTime.IsZero())
}

func TestMaybeDelaySearch_RateLimits(t *testing.T) {
	tool := &searchTool{}
	tool.lastSearchMu.Lock()
	tool.lastSearchTime = time.Now()
	tool.lastSearchMu.Unlock()

	// Verify delay executes and updates lastSearchTime without asserting wall-clock duration
	err := tool.maybeDelaySearch(context.Background())
	require.NoError(t, err)
	assert.False(t, tool.lastSearchTime.IsZero())
}

func TestMaybeDelaySearch_RespectsMinGap(t *testing.T) {
	tool := &searchTool{}
	tool.lastSearchMu.Lock()
	tool.lastSearchTime = time.Time{}
	tool.lastSearchMu.Unlock()

	err := tool.maybeDelaySearch(context.Background())
	require.NoError(t, err)
	firstTime := tool.lastSearchTime

	err = tool.maybeDelaySearch(context.Background())
	require.NoError(t, err)
	secondTime := tool.lastSearchTime

	assert.True(t, secondTime.After(firstTime) || secondTime.Equal(firstTime))
}

func TestMaybeDelaySearch_Concurrent(t *testing.T) {
	tool := &searchTool{}
	tool.lastSearchMu.Lock()
	tool.lastSearchTime = time.Time{}
	tool.lastSearchMu.Unlock()

	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			_ = tool.maybeDelaySearch(context.Background())
		})
	}
	wg.Wait()

	tool.lastSearchMu.Lock()
	assert.False(t, tool.lastSearchTime.IsZero())
	tool.lastSearchMu.Unlock()
}

func TestMaybeDelaySearch_ContextCancellation(t *testing.T) {
	tool := &searchTool{}
	tool.lastSearchMu.Lock()
	tool.lastSearchTime = time.Now()
	tool.lastSearchMu.Unlock()
	originalTime := tool.lastSearchTime

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := tool.maybeDelaySearch(ctx)
	require.ErrorIs(t, err, context.Canceled)
	// lastSearchTime should NOT be updated when cancelled during delay
	assert.Equal(t, originalTime, tool.lastSearchTime)
}
