package piagentgo

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type liveProviderCase struct {
	Name      string
	Provider  string
	Model     string
	API       string
	TokenEnvs []string
}

var liveProviderCases = []liveProviderCase{
	{
		Name:      "Anthropic Claude Sonnet 4.5",
		Provider:  "anthropic",
		Model:     "claude-sonnet-4-5",
		API:       "anthropic-messages",
		TokenEnvs: []string{"ANTHROPIC_API_KEY"},
	},
	{
		Name:      "Kimi Coding K2.5",
		Provider:  "kimi-coding",
		Model:     "k2p5",
		API:       "anthropic-messages",
		TokenEnvs: []string{"KIMI_API_KEY"},
	},
	{
		Name:      "OpenAI Codex GPT-5.4",
		Provider:  "openai-codex",
		Model:     "gpt-5.4",
		API:       "openai-codex-responses",
		TokenEnvs: []string{"PIAGENTGO_OPENAI_CODEX_TOKEN", "OPENAI_CODEX_TOKEN"},
	},
}

func TestLiveAgentBasicPromptAndMultiTurn(t *testing.T) {
	for _, testCase := range liveProviderCases {
		testCase := testCase
		t.Run(testCase.Name, func(t *testing.T) {
			modelRef := requireLiveModelRef(t, testCase)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			basicAgent := newLiveAgent(t, modelRef, "You are a helpful assistant. Keep responses concise.", nil)
			if err := basicAgent.PromptText(ctx, "What is 2+2? Answer with just the number."); err != nil {
				t.Fatalf("basic prompt returned error: %v", err)
			}

			basicState := basicAgent.State()
			if basicState.IsStreaming {
				t.Fatal("expected agent to be idle after basic prompt")
			}
			if got := len(basicState.Messages); got != 2 {
				t.Fatalf("expected 2 messages after basic prompt, got %d", got)
			}
			if basicState.Messages[1].Role != RoleAssistant {
				t.Fatalf("expected assistant response, got %+v", basicState.Messages[1])
			}
			if !messageTextContains(basicState.Messages[1], "4") {
				t.Fatalf("expected assistant response to contain 4, got %+v", basicState.Messages[1])
			}

			multiTurnAgent := newLiveAgent(t, modelRef, "You are a helpful assistant. Keep responses concise.", nil)
			if err := multiTurnAgent.PromptText(ctx, "My name is Alice. Reply with exactly: noted"); err != nil {
				t.Fatalf("first multi-turn prompt returned error: %v", err)
			}
			if err := multiTurnAgent.PromptText(ctx, "What is my name? Answer with just one word."); err != nil {
				t.Fatalf("second multi-turn prompt returned error: %v", err)
			}

			multiTurnState := multiTurnAgent.State()
			if got := len(multiTurnState.Messages); got != 4 {
				t.Fatalf("expected 4 messages after multi-turn conversation, got %d", got)
			}
			last := multiTurnState.Messages[3]
			if last.Role != RoleAssistant {
				t.Fatalf("expected final assistant response, got %+v", last)
			}
			if !messageTextContains(last, "alice") {
				t.Fatalf("expected assistant to remember Alice, got %+v", last)
			}
		})
	}
}

func TestLiveAgentToolExecution(t *testing.T) {
	for _, testCase := range liveProviderCases {
		testCase := testCase
		t.Run(testCase.Name, func(t *testing.T) {
			modelRef := requireLiveModelRef(t, testCase)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			agent := newLiveAgent(t, modelRef, "You do not know the project secret. You must call the read_secret tool before answering any question about the project secret.", []ToolDefinition{{
				Name:        "read_secret",
				Description: "Returns the current project secret string.",
				Execute: func(_ context.Context, _ string, _ any, _ ToolUpdateFunc) (ToolResult, error) {
					return ToolResult{
						Content: []Part{{Type: PartTypeText, Text: "SECRET-56088"}},
						Details: "SECRET-56088",
					}, nil
				},
			}})

			if err := agent.PromptText(ctx, "What is the project secret? Call the read_secret tool, then answer with only the exact secret."); err != nil {
				t.Fatalf("tool execution prompt returned error: %v", err)
			}

			state := agent.State()
			if state.IsStreaming {
				t.Fatal("expected agent to be idle after tool execution")
			}
			if !hasToolResultMessage(state.Messages) {
				t.Fatalf("expected a tool result message in history, got %+v", state.Messages)
			}
			last := state.Messages[len(state.Messages)-1]
			if last.Role != RoleAssistant {
				t.Fatalf("expected final assistant response, got %+v", last)
			}
			if !messageTextContains(last, "56088") {
				t.Fatalf("expected final assistant response to contain tool result, got %+v", last)
			}
		})
	}
}

func TestLiveAgentAbort(t *testing.T) {
	for _, testCase := range liveProviderCases {
		testCase := testCase
		t.Run(testCase.Name, func(t *testing.T) {
			modelRef := requireLiveModelRef(t, testCase)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			agent := newLiveAgent(t, modelRef, "You are a helpful assistant.", nil)

			var abortOnce sync.Once
			agent.Subscribe(func(event AgentEvent) {
				if event.Type == EventMessageUpdate {
					abortOnce.Do(agent.Abort)
				}
			})

			if err := agent.PromptText(ctx, "Write a detailed numbered explanation of prime numbers, with many examples and at least 40 lines."); err != nil {
				t.Fatalf("abort prompt returned error: %v", err)
			}

			state := agent.State()
			if state.IsStreaming {
				t.Fatal("expected agent to be idle after abort")
			}
			if got := len(state.Messages); got < 2 {
				t.Fatalf("expected at least 2 messages after abort, got %d", got)
			}
			last := state.Messages[len(state.Messages)-1]
			if last.Role != RoleAssistant {
				t.Fatalf("expected assistant abort message, got %+v", last)
			}
			if last.StopReason != StopReasonAborted {
				t.Fatalf("expected aborted stop reason, got %+v", last)
			}
			if strings.TrimSpace(last.ErrorMessage) == "" {
				t.Fatalf("expected abort message to carry an error message, got %+v", last)
			}
		})
	}
}

func TestLiveAgentContinueFromUserMessage(t *testing.T) {
	for _, testCase := range liveProviderCases {
		testCase := testCase
		t.Run(testCase.Name, func(t *testing.T) {
			modelRef := requireLiveModelRef(t, testCase)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			agent := newLiveAgent(t, modelRef, "You are a helpful assistant. Follow instructions exactly.", nil)
			agent.ReplaceMessages([]Message{
				NewTextMessage(RoleUser, "Say exactly: HELLO WORLD"),
			})

			if err := agent.Continue(ctx); err != nil {
				t.Fatalf("continue from user message returned error: %v", err)
			}

			state := agent.State()
			if got := len(state.Messages); got != 2 {
				t.Fatalf("expected 2 messages after continue, got %d", got)
			}
			if !messageTextContains(state.Messages[1], "hello world") {
				t.Fatalf("expected continue response to contain HELLO WORLD, got %+v", state.Messages[1])
			}
		})
	}
}

func TestLiveAgentContinueFromToolResult(t *testing.T) {
	for _, testCase := range liveProviderCases {
		testCase := testCase
		t.Run(testCase.Name, func(t *testing.T) {
			modelRef := requireLiveModelRef(t, testCase)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			agent := newLiveAgent(t, modelRef, "After receiving the tool result, answer with only the exact secret string.", []ToolDefinition{{
				Name:        "read_secret",
				Description: "Returns the current project secret string.",
			}})

			call := ToolCall{
				ID:   "call-secret-1",
				Name: "read_secret",
				ParsedArgs: map[string]any{
					"topic": "project-secret",
				},
			}
			agent.ReplaceMessages([]Message{
				NewTextMessage(RoleUser, "What is the project secret?"),
				{
					Role:       RoleAssistant,
					Parts:      []Part{{Type: PartTypeText, Text: "I will check the project secret."}},
					ToolCalls:  []ToolCall{call},
					Timestamp:  time.Now().UTC(),
					Provider:   testCase.Provider,
					API:        testCase.API,
					Model:      testCase.Model,
					StopReason: StopReasonToolUse,
				},
				NewToolResultMessage(call, ToolResult{
					Content: []Part{{Type: PartTypeText, Text: "SECRET-56088"}},
					Details: "SECRET-56088",
				}, false),
			})

			if err := agent.Continue(ctx); err != nil {
				t.Fatalf("continue from tool result returned error: %v", err)
			}

			state := agent.State()
			if got := len(state.Messages); got != 4 {
				t.Fatalf("expected 4 messages after continue from tool result, got %d", got)
			}
			last := state.Messages[3]
			if last.Role != RoleAssistant {
				t.Fatalf("expected assistant response after tool result, got %+v", last)
			}
			if !messageTextContains(last, "56088") {
				t.Fatalf("expected assistant response to contain tool result, got %+v", last)
			}
		})
	}
}

func newLiveAgent(t *testing.T, modelRef ModelRef, systemPrompt string, tools []ToolDefinition) *Agent {
	t.Helper()

	agent, err := NewAgentWithOptions(AgentOptions{
		InitialState: AgentInitialState{
			SystemPrompt:  systemPrompt,
			ModelRef:      modelRef,
			ThinkingLevel: ThinkingOff,
			Tools:         tools,
		},
		Transport: TransportSSE,
	})
	if err != nil {
		t.Fatalf("NewAgentWithOptions returned error: %v", err)
	}
	return agent
}

func requireLiveModelRef(t *testing.T, testCase liveProviderCase) ModelRef {
	t.Helper()

	if os.Getenv("PIAGENTGO_LIVE_TEST") != "1" {
		t.Skip("set PIAGENTGO_LIVE_TEST=1 to run live pi-agent-go provider tests")
	}

	token := firstEnvValue(testCase.TokenEnvs...)
	if strings.TrimSpace(token) == "" {
		t.Skipf("missing live credentials for %s", testCase.Name)
	}

	return ModelRef{
		Provider: testCase.Provider,
		Model:    testCase.Model,
		ProviderConfig: ProviderConfig{
			APIKey: token,
		},
	}
}

func firstEnvValue(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func messageTextContains(message Message, needle string) bool {
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return false
	}

	for _, part := range message.Parts {
		if part.Type == PartTypeText || part.Type == PartTypeThinking {
			if strings.Contains(strings.ToLower(part.Text), needle) {
				return true
			}
		}
	}
	if message.ToolResult != nil {
		for _, part := range message.ToolResult.Content {
			if strings.Contains(strings.ToLower(part.Text), needle) {
				return true
			}
		}
	}
	return false
}

func hasToolResultMessage(messages []Message) bool {
	for _, message := range messages {
		if message.Role == RoleTool && message.ToolResult != nil {
			return true
		}
	}
	return false
}
