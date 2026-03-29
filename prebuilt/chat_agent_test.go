package prebuilt

import (
	"context"
	"sync"
	"testing"
	"time"

	core "github.com/Icatme/pi-agent-go"
)

type chatScriptedModel struct {
	mu        sync.Mutex
	responses []chatScriptedResponse
	requests  []core.ModelRequest
	callCount int
}

type chatScriptedResponse struct {
	final  core.Message
	events []core.AssistantEvent
}

func (m *chatScriptedModel) Stream(_ context.Context, request core.ModelRequest) (core.AssistantStream, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.requests = append(m.requests, request)
	index := m.callCount
	if index >= len(m.responses) {
		index = len(m.responses) - 1
	}
	m.callCount++

	response := m.responses[index]
	stream := &chatAssistantStream{
		events: make(chan core.AssistantEvent, len(response.events)),
		final:  response.final,
	}
	for _, event := range response.events {
		stream.events <- event
	}
	close(stream.events)
	return stream, nil
}

type chatAssistantStream struct {
	events chan core.AssistantEvent
	final  core.Message
	err    error
}

func (s *chatAssistantStream) Events() <-chan core.AssistantEvent {
	return s.events
}

func (s *chatAssistantStream) Wait() (core.Message, error) {
	return s.final, s.err
}

func TestChatAgentMaintainsSessionAndHistory(t *testing.T) {
	model := &chatScriptedModel{
		responses: []chatScriptedResponse{
			{
				events: []core.AssistantEvent{
					{Type: core.AssistantEventStart, Message: core.Message{Role: core.RoleAssistant}},
				},
				final: core.Message{
					Role:       core.RoleAssistant,
					Parts:      []core.Part{{Type: core.PartTypeText, Text: "Hello! I am a bot."}},
					StopReason: core.StopReasonStop,
				},
			},
			{
				events: []core.AssistantEvent{
					{Type: core.AssistantEventStart, Message: core.Message{Role: core.RoleAssistant}},
				},
				final: core.Message{
					Role:       core.RoleAssistant,
					Parts:      []core.Part{{Type: core.PartTypeText, Text: "I remember you said hi."}},
					StopReason: core.StopReasonStop,
				},
			},
		},
	}

	agent, err := NewChatAgent(core.AgentDefinition{Model: model})
	if err != nil {
		t.Fatalf("NewChatAgent returned error: %v", err)
	}

	threadID := agent.ThreadID()
	if threadID == "" {
		t.Fatal("expected ThreadID to be initialized")
	}

	first, err := agent.Chat(context.Background(), "Hi")
	if err != nil {
		t.Fatalf("first Chat returned error: %v", err)
	}
	if first != "Hello! I am a bot." {
		t.Fatalf("unexpected first response: %q", first)
	}

	second, err := agent.Chat(context.Background(), "Do you remember me?")
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if second != "I remember you said hi." {
		t.Fatalf("unexpected second response: %q", second)
	}
	if agent.ThreadID() != threadID {
		t.Fatalf("expected ThreadID to remain stable, got %q want %q", agent.ThreadID(), threadID)
	}

	state := agent.agent.State()
	if len(state.Messages) != 4 {
		t.Fatalf("expected 4 messages in chat history, got %d", len(state.Messages))
	}
	if got := len(model.requests); got != 2 {
		t.Fatalf("expected 2 model requests, got %d", got)
	}
	if model.requests[0].SessionID != threadID || model.requests[1].SessionID != threadID {
		t.Fatalf("expected SessionID %q on all requests, got %+v", threadID, model.requests)
	}
	if got := len(model.requests[1].Messages); got != 3 {
		t.Fatalf("expected second request to include prior history, got %d messages", got)
	}
}

func TestChatAgentDynamicTools(t *testing.T) {
	model := &chatScriptedModel{
		responses: []chatScriptedResponse{
			{
				events: []core.AssistantEvent{{Type: core.AssistantEventStart, Message: core.Message{Role: core.RoleAssistant}}},
				final:  core.Message{Role: core.RoleAssistant, Parts: []core.Part{{Type: core.PartTypeText, Text: "ok"}}, StopReason: core.StopReasonStop},
			},
		},
	}

	agent, err := NewChatAgent(core.AgentDefinition{Model: model})
	if err != nil {
		t.Fatalf("NewChatAgent returned error: %v", err)
	}

	if len(agent.GetTools()) != 0 {
		t.Fatalf("expected no dynamic tools initially")
	}

	tool1 := core.ToolDefinition{Name: "tool1", Description: "first"}
	tool2 := core.ToolDefinition{Name: "tool2", Description: "second"}
	agent.AddTool(tool1)
	agent.AddTool(tool2)
	agent.AddTool(core.ToolDefinition{Name: "tool1", Description: "updated"})

	tools := agent.GetTools()
	if len(tools) != 2 {
		t.Fatalf("expected 2 dynamic tools, got %d", len(tools))
	}
	if tools[0].Name != "tool1" || tools[1].Name != "tool2" {
		t.Fatalf("unexpected tool order: %+v", tools)
	}
	if tools[0].Description != "updated" {
		t.Fatalf("expected replacement by name, got %+v", tools[0])
	}

	if !agent.RemoveTool("tool1") {
		t.Fatal("expected RemoveTool to remove existing tool")
	}
	if agent.RemoveTool("missing") {
		t.Fatal("expected RemoveTool to return false for unknown tool")
	}

	agent.SetTools([]core.ToolDefinition{{Name: "tool3"}, {Name: "tool4"}})
	if got := len(agent.GetTools()); got != 2 {
		t.Fatalf("expected SetTools to replace dynamic tools, got %d", got)
	}
	agent.ClearTools()
	if len(agent.GetTools()) != 0 {
		t.Fatal("expected ClearTools to remove all dynamic tools")
	}
}

func TestChatAgentIncludesDynamicToolsInRequests(t *testing.T) {
	model := &chatScriptedModel{
		responses: []chatScriptedResponse{
			{
				events: []core.AssistantEvent{{Type: core.AssistantEventStart, Message: core.Message{Role: core.RoleAssistant}}},
				final:  core.Message{Role: core.RoleAssistant, Parts: []core.Part{{Type: core.PartTypeText, Text: "Using tool"}}, StopReason: core.StopReasonStop},
			},
		},
	}

	agent, err := NewChatAgent(core.AgentDefinition{
		Model: model,
		Tools: []core.ToolDefinition{{Name: "base_tool"}},
	})
	if err != nil {
		t.Fatalf("NewChatAgent returned error: %v", err)
	}

	agent.AddTool(core.ToolDefinition{Name: "dynamic_tool"})
	if _, err := agent.Chat(context.Background(), "hello"); err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}

	if got := len(model.requests); got != 1 {
		t.Fatalf("expected one model request, got %d", got)
	}
	if got := len(model.requests[0].Tools); got != 2 {
		t.Fatalf("expected base and dynamic tools in request, got %d", got)
	}
}

func TestChatAgentAsyncChatStreamsDeltas(t *testing.T) {
	model := &chatScriptedModel{
		responses: []chatScriptedResponse{
			{
				events: []core.AssistantEvent{
					{Type: core.AssistantEventStart, Message: core.Message{Role: core.RoleAssistant}},
					{Type: core.AssistantEventTextDelta, Message: core.Message{Role: core.RoleAssistant}, Delta: "Hello "},
					{Type: core.AssistantEventTextDelta, Message: core.Message{Role: core.RoleAssistant}, Delta: "world"},
				},
				final: core.Message{Role: core.RoleAssistant, Parts: []core.Part{{Type: core.PartTypeText, Text: "Hello world"}}, StopReason: core.StopReasonStop},
			},
		},
	}

	agent, err := NewChatAgent(core.AgentDefinition{Model: model})
	if err != nil {
		t.Fatalf("NewChatAgent returned error: %v", err)
	}

	chunks, err := agent.AsyncChat(context.Background(), "Hi")
	if err != nil {
		t.Fatalf("AsyncChat returned error: %v", err)
	}

	var full string
	for chunk := range chunks {
		full += chunk
	}
	if full != "Hello world" {
		t.Fatalf("expected streamed response %q, got %q", "Hello world", full)
	}
}

func TestChatAgentAsyncChatWithChunks(t *testing.T) {
	model := &chatScriptedModel{
		responses: []chatScriptedResponse{
			{
				events: []core.AssistantEvent{{Type: core.AssistantEventStart, Message: core.Message{Role: core.RoleAssistant}}},
				final:  core.Message{Role: core.RoleAssistant, Parts: []core.Part{{Type: core.PartTypeText, Text: "Hello world, this is a test response."}}, StopReason: core.StopReasonStop},
			},
		},
	}

	agent, err := NewChatAgent(core.AgentDefinition{Model: model})
	if err != nil {
		t.Fatalf("NewChatAgent returned error: %v", err)
	}

	chunks, err := agent.AsyncChatWithChunks(context.Background(), "Hi")
	if err != nil {
		t.Fatalf("AsyncChatWithChunks returned error: %v", err)
	}

	var (
		collected []string
		full      string
	)
	for chunk := range chunks {
		collected = append(collected, chunk)
		full += chunk
	}

	if full != "Hello world, this is a test response." {
		t.Fatalf("unexpected chunked response: %q", full)
	}
	if len(collected) < 3 {
		t.Fatalf("expected multiple chunks, got %v", collected)
	}
}

func TestSplitIntoWords(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{name: "simple", input: "Hello world", expected: []string{"Hello", "world"}},
		{name: "punctuation", input: "Hello, world!", expected: []string{"Hello,", "world!"}},
		{name: "multiple spaces", input: "Hello   world", expected: []string{"Hello", "world"}},
		{name: "empty", input: "", expected: []string{}},
		{name: "single", input: "Hello", expected: []string{"Hello"}},
		{name: "newlines", input: "Hello\nworld", expected: []string{"Hello", "world"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			words := splitIntoWords(tt.input)
			if len(words) != len(tt.expected) {
				t.Fatalf("expected %d words, got %d (%v)", len(tt.expected), len(words), words)
			}
			for i := range tt.expected {
				if words[i] != tt.expected[i] {
					t.Fatalf("word %d mismatch: want %q got %q", i, tt.expected[i], words[i])
				}
			}
		})
	}
}

func TestChatAgentAsyncChatRespectsCancellation(t *testing.T) {
	model := &chatScriptedModel{
		responses: []chatScriptedResponse{
			{
				events: []core.AssistantEvent{
					{Type: core.AssistantEventStart, Message: core.Message{Role: core.RoleAssistant}},
					{Type: core.AssistantEventTextDelta, Message: core.Message{Role: core.RoleAssistant}, Delta: "This "},
					{Type: core.AssistantEventTextDelta, Message: core.Message{Role: core.RoleAssistant}, Delta: "is "},
					{Type: core.AssistantEventTextDelta, Message: core.Message{Role: core.RoleAssistant}, Delta: "a "},
					{Type: core.AssistantEventTextDelta, Message: core.Message{Role: core.RoleAssistant}, Delta: "long "},
					{Type: core.AssistantEventTextDelta, Message: core.Message{Role: core.RoleAssistant}, Delta: "response"},
				},
				final: core.Message{Role: core.RoleAssistant, Parts: []core.Part{{Type: core.PartTypeText, Text: "This is a long response"}}, StopReason: core.StopReasonStop},
			},
		},
	}

	agent, err := NewChatAgent(core.AgentDefinition{Model: model})
	if err != nil {
		t.Fatalf("NewChatAgent returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	chunks, err := agent.AsyncChat(ctx, "Hi")
	if err != nil {
		t.Fatalf("AsyncChat returned error: %v", err)
	}

	count := 0
	for chunk := range chunks {
		_ = chunk
		count++
		if count == 2 {
			cancel()
		}
	}

	select {
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected AsyncChat goroutine to exit after cancellation")
	default:
	}
}
