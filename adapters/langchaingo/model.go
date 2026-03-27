// Package langchaingo adapts langchaingo models into piagentgo StreamModel.
package langchaingo

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Icatme/pi-agent-go"
	"github.com/tmc/langchaingo/llms"
)

// Model adapts a langchaingo llms.Model to piagentgo.StreamModel.
type Model struct {
	model llms.Model
}

// NewModel creates a new langchaingo-backed StreamModel.
func NewModel(model llms.Model) *Model {
	return &Model{model: model}
}

// Stream executes a model request and exposes text deltas plus a final assistant message.
func (m *Model) Stream(ctx context.Context, request piagentgo.ModelRequest) (piagentgo.AssistantStream, error) {
	stream := newAssistantStream()

	messages := toLangChainMessages(request)
	tools := toLangChainTools(request.Tools)

	go func() {
		var (
			started  bool
			textOpen bool
			partial  = piagentgo.Message{
				Role:      piagentgo.RoleAssistant,
				Timestamp: time.Now().UTC(),
			}
		)

		streamingFunc := func(_ context.Context, chunk []byte) error {
			if !started {
				started = true
				stream.emit(piagentgo.AssistantEvent{
					Type:    piagentgo.AssistantEventStart,
					Message: partial,
				})
			}
			if !textOpen {
				textOpen = true
				if len(partial.Parts) == 0 {
					partial.Parts = []piagentgo.Part{{Type: piagentgo.PartTypeText, Text: ""}}
				}
				stream.emit(piagentgo.AssistantEvent{
					Type:         piagentgo.AssistantEventTextStart,
					Message:      partial,
					ContentIndex: 0,
				})
			}

			delta := string(chunk)
			partial.Parts = appendText(partial.Parts, delta)
			stream.emit(piagentgo.AssistantEvent{
				Type:         piagentgo.AssistantEventTextDelta,
				Message:      partial,
				Delta:        delta,
				ContentIndex: 0,
			})
			return nil
		}

		opts := []llms.CallOption{llms.WithStreamingFunc(streamingFunc)}
		if len(tools) > 0 {
			opts = append(opts, llms.WithTools(tools), llms.WithToolChoice("auto"))
		}

		resp, err := m.model.GenerateContent(ctx, messages, opts...)
		if err != nil {
			stopReason := piagentgo.StopReasonError
			if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
				stopReason = piagentgo.StopReasonAborted
			}
			final := piagentgo.Message{
				Role:         piagentgo.RoleAssistant,
				Timestamp:    time.Now().UTC(),
				StopReason:   stopReason,
				ErrorMessage: err.Error(),
			}
			stream.emit(piagentgo.AssistantEvent{
				Type:    piagentgo.AssistantEventError,
				Message: final,
				Reason:  stopReason,
				Err:     err,
			})
			stream.finish(final, nil)
			return
		}

		final := responseToMessage(resp)
		if textOpen {
			stream.emit(piagentgo.AssistantEvent{
				Type:         piagentgo.AssistantEventTextEnd,
				Message:      final,
				ContentIndex: 0,
			})
		}
		if len(final.ToolCalls) > 0 {
			if !started {
				started = true
				stream.emit(piagentgo.AssistantEvent{
					Type:    piagentgo.AssistantEventStart,
					Message: final,
				})
			}
			for i := range final.ToolCalls {
				call := final.ToolCalls[i]
				stream.emit(piagentgo.AssistantEvent{
					Type:         piagentgo.AssistantEventToolCallStart,
					Message:      final,
					ToolCall:     &call,
					ContentIndex: i,
				})
				stream.emit(piagentgo.AssistantEvent{
					Type:         piagentgo.AssistantEventToolCallEnd,
					Message:      final,
					ToolCall:     &call,
					ContentIndex: i,
				})
			}
		}
		if !started {
			stream.emit(piagentgo.AssistantEvent{
				Type:    piagentgo.AssistantEventStart,
				Message: final,
			})
		}
		stream.emit(piagentgo.AssistantEvent{
			Type:    piagentgo.AssistantEventDone,
			Message: final,
			Reason:  final.StopReason,
		})
		stream.finish(final, nil)
	}()

	return stream, nil
}

type assistantStream struct {
	events chan piagentgo.AssistantEvent
	done   chan struct{}

	mu      sync.RWMutex
	message piagentgo.Message
	err     error
}

func newAssistantStream() *assistantStream {
	return &assistantStream{
		events: make(chan piagentgo.AssistantEvent, 64),
		done:   make(chan struct{}),
	}
}

func (s *assistantStream) Events() <-chan piagentgo.AssistantEvent {
	return s.events
}

func (s *assistantStream) Wait() (piagentgo.Message, error) {
	<-s.done
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.message, s.err
}

func (s *assistantStream) emit(event piagentgo.AssistantEvent) {
	s.events <- event
}

func (s *assistantStream) finish(message piagentgo.Message, err error) {
	s.mu.Lock()
	s.message = message
	s.err = err
	s.mu.Unlock()

	close(s.events)
	close(s.done)
}

func toLangChainMessages(request piagentgo.ModelRequest) []llms.MessageContent {
	messages := make([]llms.MessageContent, 0, len(request.Messages)+1)
	if request.SystemPrompt != "" {
		messages = append(messages, llms.TextParts(llms.ChatMessageTypeSystem, request.SystemPrompt))
	}

	for _, message := range request.Messages {
		switch message.Role {
		case piagentgo.RoleUser:
			messages = append(messages, llms.MessageContent{
				Role:  llms.ChatMessageTypeHuman,
				Parts: partsToContentParts(message.Parts),
			})
		case piagentgo.RoleAssistant:
			parts := partsToContentParts(message.Parts)
			for _, call := range message.ToolCalls {
				parts = append(parts, llms.ToolCall{
					ID: call.ID,
					FunctionCall: &llms.FunctionCall{
						Name:      call.Name,
						Arguments: string(call.Arguments),
					},
				})
			}
			messages = append(messages, llms.MessageContent{
				Role:  llms.ChatMessageTypeAI,
				Parts: parts,
			})
		case piagentgo.RoleTool:
			if message.ToolResult == nil {
				continue
			}
			messages = append(messages, llms.MessageContent{
				Role: llms.ChatMessageTypeTool,
				Parts: []llms.ContentPart{
					llms.ToolCallResponse{
						ToolCallID: message.ToolResult.ToolCallID,
						Name:       message.ToolResult.ToolName,
						Content:    joinText(message.ToolResult.Content),
					},
				},
			})
		}
	}

	return messages
}

func partsToContentParts(parts []piagentgo.Part) []llms.ContentPart {
	content := make([]llms.ContentPart, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case piagentgo.PartTypeText, piagentgo.PartTypeThinking:
			content = append(content, llms.TextPart(part.Text))
		case piagentgo.PartTypeImage:
			if part.ImageURL != "" {
				content = append(content, llms.ImageURLPart(part.ImageURL))
			}
		}
	}
	return content
}

func toLangChainTools(tools []piagentgo.ToolDefinition) []llms.Tool {
	converted := make([]llms.Tool, 0, len(tools))
	for _, tool := range tools {
		converted = append(converted, llms.Tool{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		})
	}
	return converted
}

func responseToMessage(response *llms.ContentResponse) piagentgo.Message {
	message := piagentgo.Message{
		Role:      piagentgo.RoleAssistant,
		Timestamp: time.Now().UTC(),
	}
	if response == nil || len(response.Choices) == 0 {
		return message
	}

	choice := response.Choices[0]
	if choice.ReasoningContent != "" {
		message.Parts = append(message.Parts, piagentgo.Part{
			Type: piagentgo.PartTypeThinking,
			Text: choice.ReasoningContent,
		})
	}
	if choice.Content != "" {
		message.Parts = append(message.Parts, piagentgo.Part{Type: piagentgo.PartTypeText, Text: choice.Content})
	}
	if len(choice.ToolCalls) > 0 {
		message.StopReason = piagentgo.StopReasonToolUse
		for _, toolCall := range choice.ToolCalls {
			message.ToolCalls = append(message.ToolCalls, piagentgo.ToolCall{
				ID:        toolCall.ID,
				Name:      toolCall.FunctionCall.Name,
				Arguments: []byte(toolCall.FunctionCall.Arguments),
			})
		}
	} else {
		message.StopReason = stopReasonFromChoice(choice.StopReason)
	}
	return message
}

func appendText(parts []piagentgo.Part, delta string) []piagentgo.Part {
	if len(parts) == 0 {
		return []piagentgo.Part{{Type: piagentgo.PartTypeText, Text: delta}}
	}
	last := parts[len(parts)-1]
	if last.Type != piagentgo.PartTypeText {
		return append(parts, piagentgo.Part{Type: piagentgo.PartTypeText, Text: delta})
	}
	parts = append([]piagentgo.Part(nil), parts...)
	parts[len(parts)-1].Text += delta
	return parts
}

func joinText(parts []piagentgo.Part) string {
	text := ""
	for _, part := range parts {
		if part.Type == piagentgo.PartTypeText || part.Type == piagentgo.PartTypeThinking {
			text += part.Text
		}
	}
	return text
}

func stopReasonFromChoice(reason string) piagentgo.StopReason {
	switch reason {
	case "length", "max_tokens":
		return piagentgo.StopReasonLength
	case "tool_use", "tool_calls":
		return piagentgo.StopReasonToolUse
	default:
		return piagentgo.StopReasonStop
	}
}
