package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	core "github.com/Icatme/pi-agent-go"
)

type fakeHTTPClient struct {
	callCount int
	responder func(*http.Request) (*http.Response, error)
}

func (f *fakeHTTPClient) Do(request *http.Request) (*http.Response, error) {
	f.callCount++
	return f.responder(request)
}

func TestNewChatAgentDefinitionIncludesDefaultTools(t *testing.T) {
	definition := newChatAgentDefinition(AppConfig{
		SystemPrompt: "system",
	}, core.ModelRef{}, toolRuntimeConfig{
		Client: &fakeHTTPClient{responder: func(request *http.Request) (*http.Response, error) {
			return newHTTPResponse(request, http.StatusOK, "text/plain", "ok"), nil
		}},
		SearchBaseURL: "https://search.example.test/html",
		Now:           func() time.Time { return time.Unix(0, 0).UTC() },
		LocalLocation: time.UTC,
	})

	got := make([]string, 0, len(definition.Tools))
	for _, tool := range definition.Tools {
		got = append(got, tool.Name)
	}

	want := []string{"web_search", "web_fetch", "web_page_meta", "web_extract_links", "get_time", "math_eval"}
	if len(got) != len(want) {
		t.Fatalf("unexpected tool count: got %d want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("unexpected tool order: got %v want %v", got, want)
		}
	}
}

func TestWebSearchParsesDuckDuckGoHTMLAndClampsLimit(t *testing.T) {
	client := &fakeHTTPClient{
		responder: func(request *http.Request) (*http.Response, error) {
			body := `
<html><body>
  <a class="result__a" href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fone">Result One</a>
  <div class="result__snippet">First snippet</div>
  <a class="result__a" href="https://example.com/two">Result Two</a>
  <div class="result__snippet">Second snippet</div>
</body></html>`
			return newHTTPResponse(request, http.StatusOK, "text/html; charset=utf-8", body), nil
		},
	}
	runtime := newToolRuntime(toolRuntimeConfig{
		Client:        client,
		SearchBaseURL: "https://search.example.test/html",
		Now:           time.Now,
		LocalLocation: time.UTC,
	})
	tool := mustFindTool(buildDefaultTools(runtime), "web_search")

	parsed, err := tool.ParseArguments(core.ToolCall{
		Arguments: []byte(`{"query":"golang","limit":50}`),
	})
	if err != nil {
		t.Fatalf("ParseArguments returned error: %v", err)
	}
	args := parsed.(webSearchArgs)
	if args.Limit != maxSearchLimit {
		t.Fatalf("expected limit clamp to %d, got %d", maxSearchLimit, args.Limit)
	}

	result, err := tool.Execute(context.Background(), "call-1", webSearchArgs{Query: "golang", Limit: 2}, nil)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "Result One") || !strings.Contains(text, "https://example.com/one") {
		t.Fatalf("unexpected search text %q", text)
	}
	if !strings.Contains(text, "Second snippet") {
		t.Fatalf("expected snippet in search output, got %q", text)
	}
}

func TestWebFetchBlocksLocalhostBeforeHTTPCall(t *testing.T) {
	client := &fakeHTTPClient{
		responder: func(request *http.Request) (*http.Response, error) {
			t.Fatal("HTTP client should not be called for localhost")
			return nil, nil
		},
	}
	runtime := newToolRuntime(toolRuntimeConfig{
		Client:        client,
		SearchBaseURL: "https://search.example.test/html",
		Now:           time.Now,
		LocalLocation: time.UTC,
	})

	_, err := runtime.executeWebFetch(context.Background(), webFetchArgs{
		URL:      "http://localhost:8065/secret",
		MaxChars: 100,
	})
	if err == nil || !strings.Contains(err.Error(), "local or private addresses are not allowed") {
		t.Fatalf("expected localhost block error, got %v", err)
	}
	if client.callCount != 0 {
		t.Fatalf("expected zero HTTP calls, got %d", client.callCount)
	}
}

func TestWebFetchExtractsHTMLAndTruncates(t *testing.T) {
	client := &fakeHTTPClient{
		responder: func(request *http.Request) (*http.Response, error) {
			body := `
<html>
  <head>
    <title>Example Title</title>
    <meta name="description" content="Example description">
  </head>
  <body>
    <main>
      <h1>Heading</h1>
      <p>This is a long page body used for truncation checks.</p>
    </main>
  </body>
</html>`
			return newHTTPResponse(request, http.StatusOK, "text/html; charset=utf-8", body), nil
		},
	}
	runtime := newToolRuntime(toolRuntimeConfig{
		Client:        client,
		SearchBaseURL: "https://search.example.test/html",
		Now:           time.Now,
		LocalLocation: time.UTC,
	})

	result, err := runtime.executeWebFetch(context.Background(), webFetchArgs{
		URL:      "https://example.com/article",
		MaxChars: 25,
	})
	if err != nil {
		t.Fatalf("executeWebFetch returned error: %v", err)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "Example Title") {
		t.Fatalf("expected title in fetch output, got %q", text)
	}
	details := result.Details.(map[string]any)
	if details["truncated"] != true {
		t.Fatalf("expected truncated detail, got %+v", details)
	}
}

func TestWebFetchRejectsBinaryContent(t *testing.T) {
	client := &fakeHTTPClient{
		responder: func(request *http.Request) (*http.Response, error) {
			return newHTTPResponse(request, http.StatusOK, "image/png", "pngdata"), nil
		},
	}
	runtime := newToolRuntime(toolRuntimeConfig{
		Client:        client,
		SearchBaseURL: "https://search.example.test/html",
		Now:           time.Now,
		LocalLocation: time.UTC,
	})

	_, err := runtime.executeWebFetch(context.Background(), webFetchArgs{
		URL:      "https://example.com/image.png",
		MaxChars: 100,
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported content type") {
		t.Fatalf("expected binary content error, got %v", err)
	}
}

func TestWebPageMetaAndExtractLinks(t *testing.T) {
	client := &fakeHTTPClient{
		responder: func(request *http.Request) (*http.Response, error) {
			body := `
<html>
  <head>
    <title>Meta Title</title>
    <meta name="description" content="Meta description">
  </head>
  <body>
    <a href="/docs">Docs</a>
    <a href="https://example.com/docs">Docs Again</a>
    <a href="https://other.example.com/page">Other</a>
  </body>
</html>`
			return newHTTPResponse(request, http.StatusOK, "text/html", body), nil
		},
	}
	runtime := newToolRuntime(toolRuntimeConfig{
		Client:        client,
		SearchBaseURL: "https://search.example.test/html",
		Now:           time.Now,
		LocalLocation: time.UTC,
	})

	metaResult, err := runtime.executeWebPageMeta(context.Background(), webPageMetaArgs{
		URL: "https://example.com/root",
	})
	if err != nil {
		t.Fatalf("executeWebPageMeta returned error: %v", err)
	}
	if !strings.Contains(metaResult.Content[0].Text, "Meta Title") {
		t.Fatalf("unexpected meta output %q", metaResult.Content[0].Text)
	}

	linkResult, err := runtime.executeWebExtractLinks(context.Background(), webExtractLinksArgs{
		URL:   "https://example.com/root",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("executeWebExtractLinks returned error: %v", err)
	}
	linkDetails := linkResult.Details.(map[string]any)
	links := linkDetails["links"].([]string)
	if len(links) != 2 {
		t.Fatalf("expected deduplicated links, got %+v", links)
	}
	if links[0] != "https://example.com/docs" {
		t.Fatalf("unexpected normalized first link %q", links[0])
	}
}

func TestGetTimeDefaultAndTimezone(t *testing.T) {
	now := time.Date(2026, time.April, 1, 15, 4, 5, 0, time.UTC)
	runtime := newToolRuntime(toolRuntimeConfig{
		Client: &fakeHTTPClient{responder: func(request *http.Request) (*http.Response, error) {
			return newHTTPResponse(request, http.StatusOK, "text/plain", "ok"), nil
		}},
		SearchBaseURL: "https://search.example.test/html",
		Now:           func() time.Time { return now },
		LocalLocation: time.FixedZone("CST", 8*3600),
	})

	defaultResult, err := runtime.executeGetTime(getTimeArgs{})
	if err != nil {
		t.Fatalf("executeGetTime default returned error: %v", err)
	}
	if !strings.Contains(defaultResult.Content[0].Text, "UTC: 2026-04-01T15:04:05Z") {
		t.Fatalf("unexpected default time output %q", defaultResult.Content[0].Text)
	}

	timezoneResult, err := runtime.executeGetTime(getTimeArgs{Timezone: "UTC"})
	if err != nil {
		t.Fatalf("executeGetTime timezone returned error: %v", err)
	}
	if !strings.Contains(timezoneResult.Content[0].Text, "UTC: 2026-04-01T15:04:05Z") {
		t.Fatalf("unexpected timezone output %q", timezoneResult.Content[0].Text)
	}

	_, err = runtime.executeGetTime(getTimeArgs{Timezone: "Bad/Timezone"})
	if err == nil {
		t.Fatal("expected invalid timezone error")
	}
}

func TestMathEvalValidAndInvalidExpressions(t *testing.T) {
	runtime := newToolRuntime(toolRuntimeConfig{
		Client: &fakeHTTPClient{responder: func(request *http.Request) (*http.Response, error) {
			return newHTTPResponse(request, http.StatusOK, "text/plain", "ok"), nil
		}},
		SearchBaseURL: "https://search.example.test/html",
		Now:           time.Now,
		LocalLocation: time.UTC,
	})

	result, err := runtime.executeMathEval(mathEvalArgs{Expression: "(2 + 3) * 4 / 2"})
	if err != nil {
		t.Fatalf("executeMathEval returned error: %v", err)
	}
	if result.Content[0].Text != "10" {
		t.Fatalf("unexpected math result %q", result.Content[0].Text)
	}

	_, err = runtime.executeMathEval(mathEvalArgs{Expression: "2 + foo"})
	if err == nil {
		t.Fatal("expected malformed expression error")
	}
}

func mustFindTool(tools []core.ToolDefinition, name string) core.ToolDefinition {
	for _, tool := range tools {
		if tool.Name == name {
			return tool
		}
	}
	panic("tool not found: " + name)
}

func newHTTPResponse(request *http.Request, statusCode int, contentType, body string) *http.Response {
	clonedRequest := request.Clone(request.Context())
	return &http.Response{
		StatusCode: statusCode,
		Header: http.Header{
			"Content-Type": []string{contentType},
		},
		Body:    io.NopCloser(strings.NewReader(body)),
		Request: clonedRequest,
	}
}
