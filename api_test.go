package piagentgo

import (
	"context"
	"testing"
	"time"
)

func TestNewAgentWithOptionsUsesInitialState(t *testing.T) {
	agent, err := NewAgentWithOptions(AgentOptions{
		Model: staticModel{
			streamFn: func(_ context.Context, _ ModelRequest) (AssistantStream, error) {
				return newStaticAssistantStream(Message{
					Role:       RoleAssistant,
					Parts:      []Part{{Type: PartTypeText, Text: "ok"}},
					Timestamp:  time.Now().UTC(),
					StopReason: StopReasonStop,
				}, nil), nil
			},
		},
		InitialState: AgentInitialState{
			SystemPrompt:  "You are helpful.",
			ModelRef:      ModelRef{Provider: "openai", Model: "gpt-4o-mini"},
			ThinkingLevel: ThinkingLow,
			Tools:         []ToolDefinition{{Name: "test"}},
			Messages:      []Message{NewTextMessage(RoleUser, "hello")},
			SessionID:     "session-1",
			Metadata:      map[string]any{"trace": "1"},
		},
		Transport:       TransportSSE,
		MaxRetryDelayMs: 1234,
	})
	if err != nil {
		t.Fatalf("NewAgentWithOptions returned error: %v", err)
	}

	state := agent.State()
	if state.SystemPrompt != "You are helpful." {
		t.Fatalf("expected system prompt to be restored, got %q", state.SystemPrompt)
	}
	if state.Model.Provider != "openai" || state.Model.Model != "gpt-4o-mini" {
		t.Fatalf("unexpected model ref %+v", state.Model)
	}
	if state.ThinkingLevel != ThinkingLow {
		t.Fatalf("expected thinking level %q, got %q", ThinkingLow, state.ThinkingLevel)
	}
	if len(state.Tools) != 1 || state.Tools[0].Name != "test" {
		t.Fatalf("unexpected tools %+v", state.Tools)
	}
	if len(state.Messages) != 1 || state.Messages[0].Parts[0].Text != "hello" {
		t.Fatalf("unexpected initial messages %+v", state.Messages)
	}
	if state.SessionID != "session-1" {
		t.Fatalf("expected session id %q, got %q", "session-1", state.SessionID)
	}
	if state.MaxRetryDelayMs != 1234 {
		t.Fatalf("expected max retry delay 1234, got %d", state.MaxRetryDelayMs)
	}
	if state.Metadata["trace"] != "1" {
		t.Fatalf("unexpected metadata %+v", state.Metadata)
	}
}

func TestAgentPromptHelpers(t *testing.T) {
	var requests []ModelRequest

	agent, err := NewAgentWithOptions(AgentOptions{
		Model: StreamFunc(func(_ context.Context, request ModelRequest) (AssistantStream, error) {
			requests = append(requests, request)
			return newStaticAssistantStream(Message{
				Role:       RoleAssistant,
				Parts:      []Part{{Type: PartTypeText, Text: "ok"}},
				Timestamp:  time.Now().UTC(),
				StopReason: StopReasonStop,
			}, nil), nil
		}),
	})
	if err != nil {
		t.Fatalf("NewAgentWithOptions returned error: %v", err)
	}

	if err := agent.PromptText(context.Background(), "hello"); err != nil {
		t.Fatalf("PromptText returned error: %v", err)
	}
	if err := agent.PromptTextWithImages(context.Background(), "look", NewImagePart("https://example.com/a.png", "image/png")); err != nil {
		t.Fatalf("PromptTextWithImages returned error: %v", err)
	}
	if err := agent.PromptMessage(context.Background(), NewCustomMessage("artifact", map[string]any{"id": "1"}, NewTextPart("artifact"))); err != nil {
		t.Fatalf("PromptMessage returned error: %v", err)
	}

	if got := len(requests); got != 3 {
		t.Fatalf("expected 3 model requests, got %d", got)
	}
	if got := requests[0].Messages[len(requests[0].Messages)-1].Parts[0].Text; got != "hello" {
		t.Fatalf("unexpected PromptText message %q", got)
	}
	lastPrompt := requests[1].Messages[len(requests[1].Messages)-1]
	if got := len(lastPrompt.Parts); got != 2 {
		t.Fatalf("expected text+image parts, got %d", got)
	}
	if lastPrompt.Parts[1].Type != PartTypeImage {
		t.Fatalf("expected image part, got %+v", lastPrompt.Parts[1])
	}
}

func TestPackageLevelLoopExports(t *testing.T) {
	snapshot := &AgentSnapshot{}
	definition := AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, _ ModelRequest) (AssistantStream, error) {
				return newStaticAssistantStream(Message{
					Role:       RoleAssistant,
					Parts:      []Part{{Type: PartTypeText, Text: "done"}},
					Timestamp:  time.Now().UTC(),
					StopReason: StopReasonStop,
				}, nil), nil
			},
		},
	}

	next, err := RunAgentLoop(context.Background(), definition, snapshot, []Message{NewTextMessage(RoleUser, "hi")}, nil)
	if err != nil {
		t.Fatalf("RunAgentLoop returned error: %v", err)
	}
	if got := len(next.Messages); got != 2 {
		t.Fatalf("expected 2 messages after RunAgentLoop, got %d", got)
	}

	resumed, err := RunAgentLoopContinue(context.Background(), definition, next, nil)
	if err != ErrCannotContinueFromAssistant {
		t.Fatalf("expected ErrCannotContinueFromAssistant from assistant tail, got %v", err)
	}
	if resumed != nil {
		t.Fatalf("expected nil resumed snapshot on assistant-tail continue, got %+v", resumed)
	}
}

func TestCustomMessageHelper(t *testing.T) {
	message := NewCustomMessage("notification", map[string]any{"level": "info"}, NewTextPart("hello"))

	if message.Role != RoleCustom {
		t.Fatalf("expected custom role, got %q", message.Role)
	}
	if message.Kind != "notification" {
		t.Fatalf("expected kind %q, got %q", "notification", message.Kind)
	}
	if message.Payload["level"] != "info" {
		t.Fatalf("unexpected payload %+v", message.Payload)
	}
	if len(message.Parts) != 1 || message.Parts[0].Text != "hello" {
		t.Fatalf("unexpected parts %+v", message.Parts)
	}
}
