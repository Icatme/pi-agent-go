package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	core "github.com/Icatme/pi-agent-go"
)

const (
	defaultFetchChars        = 4000
	maxFetchChars            = 12000
	defaultSearchLimit       = 5
	maxSearchLimit           = 10
	defaultExtractLinksLimit = 20
	maxExtractLinksLimit     = 50
)

type toolRuntimeConfig struct {
	Client        httpDoer
	SearchBaseURL string
	Now           func() time.Time
	LocalLocation *time.Location
}

type toolRuntime struct {
	client        httpDoer
	searchBaseURL string
	now           func() time.Time
	localLocation *time.Location
}

type webSearchArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

type webFetchArgs struct {
	URL      string `json:"url"`
	MaxChars int    `json:"max_chars"`
}

type webPageMetaArgs struct {
	URL string `json:"url"`
}

type webExtractLinksArgs struct {
	URL   string `json:"url"`
	Limit int    `json:"limit"`
}

type getTimeArgs struct {
	Timezone string `json:"timezone"`
}

type mathEvalArgs struct {
	Expression string `json:"expression"`
}

func newToolRuntime(config toolRuntimeConfig) toolRuntime {
	if config.Client == nil {
		config.Client = newRestrictedHTTPClient()
	}
	if strings.TrimSpace(config.SearchBaseURL) == "" {
		config.SearchBaseURL = defaultDuckDuckGoSearchURL
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.LocalLocation == nil {
		config.LocalLocation = time.Local
	}

	return toolRuntime{
		client:        config.Client,
		searchBaseURL: strings.TrimRight(config.SearchBaseURL, "/"),
		now:           config.Now,
		localLocation: config.LocalLocation,
	}
}

func buildDefaultTools(runtime toolRuntime) []core.ToolDefinition {
	return []core.ToolDefinition{
		{
			Name:        "web_search",
			Description: "Search the public web and return the top relevant results.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "The search query.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum number of search results to return.",
					},
				},
				"required": []string{"query"},
			},
			ParseArguments: func(call core.ToolCall) (any, error) {
				var args webSearchArgs
				if err := parseToolArguments(call, &args); err != nil {
					return nil, err
				}
				if strings.TrimSpace(args.Query) == "" {
					return nil, fmt.Errorf("web_search: query is required")
				}
				args.Limit = clampOrDefault(args.Limit, defaultSearchLimit, maxSearchLimit)
				return args, nil
			},
			Execute: func(ctx context.Context, _ string, args any, _ core.ToolUpdateFunc) (core.ToolResult, error) {
				return runtime.executeWebSearch(ctx, args.(webSearchArgs))
			},
		},
		{
			Name:        "web_fetch",
			Description: "Fetch and summarize readable content from a public web page.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "The public HTTP or HTTPS URL to fetch.",
					},
					"max_chars": map[string]any{
						"type":        "integer",
						"description": "Maximum number of characters to return from the page text.",
					},
				},
				"required": []string{"url"},
			},
			ParseArguments: func(call core.ToolCall) (any, error) {
				var args webFetchArgs
				if err := parseToolArguments(call, &args); err != nil {
					return nil, err
				}
				if strings.TrimSpace(args.URL) == "" {
					return nil, fmt.Errorf("web_fetch: url is required")
				}
				args.MaxChars = clampOrDefault(args.MaxChars, defaultFetchChars, maxFetchChars)
				return args, nil
			},
			Execute: func(ctx context.Context, _ string, args any, _ core.ToolUpdateFunc) (core.ToolResult, error) {
				return runtime.executeWebFetch(ctx, args.(webFetchArgs))
			},
		},
		{
			Name:        "web_page_meta",
			Description: "Fetch metadata for a public web page such as title and description.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "The public HTTP or HTTPS URL to inspect.",
					},
				},
				"required": []string{"url"},
			},
			ParseArguments: func(call core.ToolCall) (any, error) {
				var args webPageMetaArgs
				if err := parseToolArguments(call, &args); err != nil {
					return nil, err
				}
				if strings.TrimSpace(args.URL) == "" {
					return nil, fmt.Errorf("web_page_meta: url is required")
				}
				return args, nil
			},
			Execute: func(ctx context.Context, _ string, args any, _ core.ToolUpdateFunc) (core.ToolResult, error) {
				return runtime.executeWebPageMeta(ctx, args.(webPageMetaArgs))
			},
		},
		{
			Name:        "web_extract_links",
			Description: "Fetch a public web page and extract normalized outbound links.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "The public HTTP or HTTPS URL to inspect.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum number of links to return.",
					},
				},
				"required": []string{"url"},
			},
			ParseArguments: func(call core.ToolCall) (any, error) {
				var args webExtractLinksArgs
				if err := parseToolArguments(call, &args); err != nil {
					return nil, err
				}
				if strings.TrimSpace(args.URL) == "" {
					return nil, fmt.Errorf("web_extract_links: url is required")
				}
				args.Limit = clampOrDefault(args.Limit, defaultExtractLinksLimit, maxExtractLinksLimit)
				return args, nil
			},
			Execute: func(ctx context.Context, _ string, args any, _ core.ToolUpdateFunc) (core.ToolResult, error) {
				return runtime.executeWebExtractLinks(ctx, args.(webExtractLinksArgs))
			},
		},
		{
			Name:        "get_time",
			Description: "Return the current time in UTC and local time, or in a requested timezone.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"timezone": map[string]any{
						"type":        "string",
						"description": "Optional IANA timezone name such as Asia/Shanghai.",
					},
				},
			},
			ParseArguments: func(call core.ToolCall) (any, error) {
				var args getTimeArgs
				if err := parseToolArguments(call, &args); err != nil {
					return nil, err
				}
				return args, nil
			},
			Execute: func(_ context.Context, _ string, args any, _ core.ToolUpdateFunc) (core.ToolResult, error) {
				return runtime.executeGetTime(args.(getTimeArgs))
			},
		},
		{
			Name:        "math_eval",
			Description: "Safely evaluate a simple arithmetic expression using numbers and parentheses.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"expression": map[string]any{
						"type":        "string",
						"description": "Arithmetic expression using +, -, *, / and parentheses.",
					},
				},
				"required": []string{"expression"},
			},
			ParseArguments: func(call core.ToolCall) (any, error) {
				var args mathEvalArgs
				if err := parseToolArguments(call, &args); err != nil {
					return nil, err
				}
				if strings.TrimSpace(args.Expression) == "" {
					return nil, fmt.Errorf("math_eval: expression is required")
				}
				return args, nil
			},
			Execute: func(_ context.Context, _ string, args any, _ core.ToolUpdateFunc) (core.ToolResult, error) {
				return runtime.executeMathEval(args.(mathEvalArgs))
			},
		},
	}
}

func parseToolArguments[T any](call core.ToolCall, out *T) error {
	raw := call.Arguments
	if len(raw) == 0 && len(call.ParsedArgs) > 0 {
		body, err := json.Marshal(call.ParsedArgs)
		if err != nil {
			return err
		}
		raw = body
	}
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	return json.Unmarshal(raw, out)
}

func clampOrDefault(value, defaultValue, maxValue int) int {
	if value <= 0 {
		return defaultValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
