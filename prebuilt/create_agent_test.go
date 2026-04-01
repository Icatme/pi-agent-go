package prebuilt

import (
	"context"
	"testing"

	core "github.com/Icatme/pi-agent-go"
)

type captureStreamModel struct {
	requests []core.ModelRequest
	final    core.Message
}

func (m *captureStreamModel) Stream(_ context.Context, request core.ModelRequest) (core.AssistantStream, error) {
	m.requests = append(m.requests, request)

	stream := &chatAssistantStream{
		events: make(chan core.AssistantEvent, 2),
		final:  m.final,
	}
	stream.events <- core.AssistantEvent{
		Type:    core.AssistantEventStart,
		Message: core.Message{Role: core.RoleAssistant},
	}
	stream.events <- core.AssistantEvent{
		Type:    core.AssistantEventDone,
		Message: m.final,
	}
	close(stream.events)
	return stream, nil
}

func TestCreateAgentBuildsNativeAgent(t *testing.T) {
	model := &captureStreamModel{
		final: core.Message{
			Role:       core.RoleAssistant,
			Parts:      []core.Part{{Type: core.PartTypeText, Text: "ok"}},
			StopReason: core.StopReasonStop,
		},
	}

	agent, err := CreateAgent(model, []core.ToolDefinition{{Name: "test_tool"}}, WithSystemMessage("system"), WithMaxIterations(3))
	if err != nil {
		t.Fatalf("CreateAgent returned error: %v", err)
	}

	if err := agent.PromptText(context.Background(), "hello"); err != nil {
		t.Fatalf("PromptText returned error: %v", err)
	}

	state := agent.State()
	if state.SystemPrompt != "system" {
		t.Fatalf("expected system prompt %q, got %q", "system", state.SystemPrompt)
	}
	if len(model.requests) != 1 {
		t.Fatalf("expected one model request, got %d", len(model.requests))
	}
	if len(model.requests[0].Tools) != 1 || model.requests[0].Tools[0].Name != "test_tool" {
		t.Fatalf("expected tool definition to be forwarded, got %+v", model.requests[0].Tools)
	}
}

func TestCreateAgentStateModifierAffectsModelRequest(t *testing.T) {
	model := &captureStreamModel{
		final: core.Message{
			Role:       core.RoleAssistant,
			Parts:      []core.Part{{Type: core.PartTypeText, Text: "ok"}},
			StopReason: core.StopReasonStop,
		},
	}

	agent, err := CreateAgent(model, nil, WithStateModifier(func(messages []core.Message) []core.Message {
		cloned := append([]core.Message{}, messages...)
		cloned = append(cloned, core.NewUserTextMessage("modified"))
		return cloned
	}))
	if err != nil {
		t.Fatalf("CreateAgent returned error: %v", err)
	}

	if err := agent.PromptText(context.Background(), "hello"); err != nil {
		t.Fatalf("PromptText returned error: %v", err)
	}

	if len(model.requests) != 1 {
		t.Fatalf("expected one model request, got %d", len(model.requests))
	}
	request := model.requests[0]
	if len(request.Messages) != 2 {
		t.Fatalf("expected state modifier to append one message, got %d messages", len(request.Messages))
	}
	if got := messageText(request.Messages[1]); got != "modified" {
		t.Fatalf("expected modified message in model request, got %q", got)
	}
}
