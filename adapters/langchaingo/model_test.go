package langchaingo

import (
	"context"
	"testing"

	"github.com/Icatme/pi-agent-go"
	"github.com/tmc/langchaingo/llms"
)

func TestModelStreamEmitsToolCallEvents(t *testing.T) {
	model := NewModel(&mockModel{
		generateContent: func(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
			return &llms.ContentResponse{
				Choices: []*llms.ContentChoice{{
					ToolCalls: []llms.ToolCall{{
						ID: "tool-1",
						FunctionCall: &llms.FunctionCall{
							Name:      "calculator",
							Arguments: `{"expression":"2+2"}`,
						},
					}},
					StopReason: "tool_calls",
				}},
			}, nil
		},
	})

	stream, err := model.Stream(context.Background(), piagentgo.ModelRequest{
		Tools: []piagentgo.ToolDefinition{{
			Name:        "calculator",
			Description: "Calculates",
		}},
	})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	var toolCallEvents int
	var toolCallEndEvents int
	for event := range stream.Events() {
		if event.Type == piagentgo.AssistantEventToolCallStart {
			toolCallEvents++
			if event.ToolCall == nil || event.ToolCall.Name != "calculator" {
				t.Fatalf("unexpected tool call event: %+v", event)
			}
		}
		if event.Type == piagentgo.AssistantEventToolCallEnd {
			toolCallEndEvents++
		}
	}

	final, err := stream.Wait()
	if err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	if toolCallEvents != 1 {
		t.Fatalf("expected 1 tool-call start event, got %d", toolCallEvents)
	}
	if toolCallEndEvents != 1 {
		t.Fatalf("expected 1 tool-call end event, got %d", toolCallEndEvents)
	}
	if final.StopReason != piagentgo.StopReasonToolUse {
		t.Fatalf("expected tool-use stop reason, got %s", final.StopReason)
	}
}

func TestModelStreamMapsCancellationToAborted(t *testing.T) {
	model := NewModel(&mockModel{
		generateContent: func(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
			return nil, context.Canceled
		},
	})

	stream, err := model.Stream(context.Background(), piagentgo.ModelRequest{})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	for range stream.Events() {
	}

	final, waitErr := stream.Wait()
	if waitErr != nil {
		t.Fatalf("expected nil wait error, got %v", waitErr)
	}
	if final.StopReason != piagentgo.StopReasonAborted {
		t.Fatalf("expected aborted stop reason, got %s", final.StopReason)
	}
	if final.ErrorMessage == "" {
		t.Fatal("expected aborted message to include error text")
	}
}

func TestModelStreamEmitsTextLifecycleEvents(t *testing.T) {
	model := NewModel(&mockModel{
		generateContent: func(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
			callOptions := applyCallOptions(options...)
			if callOptions.StreamingFunc == nil {
				t.Fatal("expected streaming func to be configured")
			}
			if err := callOptions.StreamingFunc(ctx, []byte("hel")); err != nil {
				return nil, err
			}
			if err := callOptions.StreamingFunc(ctx, []byte("lo")); err != nil {
				return nil, err
			}
			return &llms.ContentResponse{
				Choices: []*llms.ContentChoice{{
					Content:    "hello",
					StopReason: "stop",
				}},
			}, nil
		},
	})

	stream, err := model.Stream(context.Background(), piagentgo.ModelRequest{})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	var eventTypes []piagentgo.AssistantEventType
	for event := range stream.Events() {
		eventTypes = append(eventTypes, event.Type)
	}

	final, err := stream.Wait()
	if err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	if final.StopReason != piagentgo.StopReasonStop {
		t.Fatalf("expected stop reason %q, got %q", piagentgo.StopReasonStop, final.StopReason)
	}

	want := []piagentgo.AssistantEventType{
		piagentgo.AssistantEventStart,
		piagentgo.AssistantEventTextStart,
		piagentgo.AssistantEventTextDelta,
		piagentgo.AssistantEventTextDelta,
		piagentgo.AssistantEventTextEnd,
		piagentgo.AssistantEventDone,
	}
	if len(eventTypes) != len(want) {
		t.Fatalf("expected %d events, got %d: %v", len(want), len(eventTypes), eventTypes)
	}
	for i := range want {
		if eventTypes[i] != want[i] {
			t.Fatalf("expected event %d to be %q, got %q", i, want[i], eventTypes[i])
		}
	}
}

func TestToLangChainMessagesIncludesImageParts(t *testing.T) {
	messages := toLangChainMessages(piagentgo.ModelRequest{
		Messages: []piagentgo.Message{{
			Role: piagentgo.RoleUser,
			Parts: []piagentgo.Part{
				{Type: piagentgo.PartTypeText, Text: "look"},
				{Type: piagentgo.PartTypeImage, ImageURL: "https://example.com/image.png"},
			},
		}},
	})

	if got := len(messages); got != 1 {
		t.Fatalf("expected 1 message, got %d", got)
	}
	if got := len(messages[0].Parts); got != 2 {
		t.Fatalf("expected 2 parts, got %d", got)
	}
	if _, ok := messages[0].Parts[1].(llms.ImageURLContent); !ok {
		t.Fatalf("expected second part to be image url content, got %T", messages[0].Parts[1])
	}
}

type mockModel struct {
	generateContent func(context.Context, []llms.MessageContent, ...llms.CallOption) (*llms.ContentResponse, error)
}

func (m *mockModel) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	return m.generateContent(ctx, messages, options...)
}

func (m *mockModel) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	return "", nil
}

func applyCallOptions(options ...llms.CallOption) llms.CallOptions {
	var callOptions llms.CallOptions
	for _, option := range options {
		option(&callOptions)
	}
	return callOptions
}
