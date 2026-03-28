package piagentgo

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestAgentPromptBasic(t *testing.T) {
	agent, err := NewAgent(AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, _ ModelRequest) (AssistantStream, error) {
				return newStaticAssistantStream(Message{
					Role:       RoleAssistant,
					Parts:      []Part{{Type: PartTypeText, Text: "pong"}},
					Timestamp:  time.Now().UTC(),
					StopReason: StopReasonStop,
				}, []AssistantEvent{
					{
						Type:    AssistantEventStart,
						Message: Message{Role: RoleAssistant, Timestamp: time.Now().UTC()},
					},
					{
						Type: AssistantEventTextDelta,
						Message: Message{
							Role:      RoleAssistant,
							Parts:     []Part{{Type: PartTypeText, Text: "pong"}},
							Timestamp: time.Now().UTC(),
						},
						Delta: "pong",
					},
				}), nil
			},
		},
	})
	if err != nil {
		t.Fatalf("NewAgent returned error: %v", err)
	}

	if err := agent.Prompt(context.Background(), NewTextMessage(RoleUser, "ping")); err != nil {
		t.Fatalf("Prompt returned error: %v", err)
	}

	snapshot := agent.Snapshot()
	if got := len(snapshot.Messages); got != 2 {
		t.Fatalf("expected 2 messages, got %d", got)
	}
	if snapshot.Messages[1].Role != RoleAssistant {
		t.Fatalf("expected assistant role, got %s", snapshot.Messages[1].Role)
	}
	if snapshot.Messages[1].Parts[0].Text != "pong" {
		t.Fatalf("expected assistant text %q, got %q", "pong", snapshot.Messages[1].Parts[0].Text)
	}
}

func TestAgentDefaultState(t *testing.T) {
	agent, err := NewAgent(AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, _ ModelRequest) (AssistantStream, error) {
				return nil, errors.New("unused")
			},
		},
	})
	if err != nil {
		t.Fatalf("NewAgent returned error: %v", err)
	}

	state := agent.State()
	if state.SystemPrompt != "" {
		t.Fatalf("expected empty system prompt, got %q", state.SystemPrompt)
	}
	if state.ThinkingLevel != ThinkingOff {
		t.Fatalf("expected thinking level %q, got %q", ThinkingOff, state.ThinkingLevel)
	}
	if len(state.Tools) != 0 {
		t.Fatalf("expected no tools, got %d", len(state.Tools))
	}
	if len(state.Messages) != 0 {
		t.Fatalf("expected no messages, got %d", len(state.Messages))
	}
	if state.IsStreaming {
		t.Fatal("expected agent to be idle")
	}
	if state.StreamMessage != nil {
		t.Fatalf("expected no stream message, got %+v", state.StreamMessage)
	}
	if len(state.PendingToolCalls) != 0 {
		t.Fatalf("expected no pending tool calls, got %d", len(state.PendingToolCalls))
	}
	if state.Error != "" {
		t.Fatalf("expected empty error, got %q", state.Error)
	}
}

func TestAgentMutatorsUpdateState(t *testing.T) {
	agent, err := NewAgent(AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, _ ModelRequest) (AssistantStream, error) {
				return nil, errors.New("unused")
			},
		},
	})
	if err != nil {
		t.Fatalf("NewAgent returned error: %v", err)
	}

	agent.SetSystemPrompt("custom prompt")
	agent.SetModel(ModelRef{Provider: "openai", Model: "gpt-4o-mini"})
	agent.SetThinkingLevel(ThinkingHigh)
	tools := []ToolDefinition{{Name: "test", Description: "test tool"}}
	agent.SetTools(tools)

	messages := []Message{NewTextMessage(RoleUser, "hello")}
	agent.ReplaceMessages(messages)
	state := agent.State()

	if state.SystemPrompt != "custom prompt" {
		t.Fatalf("expected custom prompt, got %q", state.SystemPrompt)
	}
	if state.Model.Provider != "openai" || state.Model.Model != "gpt-4o-mini" {
		t.Fatalf("unexpected model %+v", state.Model)
	}
	if state.ThinkingLevel != ThinkingHigh {
		t.Fatalf("expected thinking level %q, got %q", ThinkingHigh, state.ThinkingLevel)
	}
	if len(state.Tools) != 1 || state.Tools[0].Name != "test" {
		t.Fatalf("unexpected tools %+v", state.Tools)
	}
	if len(state.Messages) != 1 || state.Messages[0].Parts[0].Text != "hello" {
		t.Fatalf("unexpected messages %+v", state.Messages)
	}
	if &state.Messages[0] == &messages[0] {
		t.Fatal("expected ReplaceMessages to clone input slice")
	}

	newMessage := Message{
		Role:      RoleAssistant,
		Parts:     []Part{{Type: PartTypeText, Text: "hi"}},
		Timestamp: time.Now().UTC(),
	}
	agent.AppendMessage(newMessage)

	state = agent.State()
	if got := len(state.Messages); got != 2 {
		t.Fatalf("expected 2 messages after append, got %d", got)
	}
	if state.Messages[1].Role != RoleAssistant || state.Messages[1].Parts[0].Text != "hi" {
		t.Fatalf("unexpected appended message %+v", state.Messages[1])
	}

	agent.ClearMessages()
	state = agent.State()
	if len(state.Messages) != 0 {
		t.Fatalf("expected messages to be cleared, got %d", len(state.Messages))
	}
}

func TestAgentSubscribeAndUnsubscribe(t *testing.T) {
	agent, err := NewAgent(AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, _ ModelRequest) (AssistantStream, error) {
				return nil, errors.New("unused")
			},
		},
	})
	if err != nil {
		t.Fatalf("NewAgent returned error: %v", err)
	}

	eventCount := 0
	unsubscribe := agent.Subscribe(func(AgentEvent) {
		eventCount++
	})

	if eventCount != 0 {
		t.Fatalf("expected no initial event on subscribe, got %d", eventCount)
	}

	agent.SetSystemPrompt("test prompt")
	if eventCount != 0 {
		t.Fatalf("expected mutators to not emit events, got %d", eventCount)
	}

	unsubscribe()
	agent.SetSystemPrompt("another prompt")
	if eventCount != 0 {
		t.Fatalf("expected no events after unsubscribe, got %d", eventCount)
	}
}

func TestAgentQueuesDoNotImmediatelyAppendMessages(t *testing.T) {
	agent, err := NewAgent(AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, _ ModelRequest) (AssistantStream, error) {
				return nil, errors.New("unused")
			},
		},
	})
	if err != nil {
		t.Fatalf("NewAgent returned error: %v", err)
	}

	agent.Steer(NewTextMessage(RoleUser, "steering message"))
	agent.FollowUp(NewTextMessage(RoleUser, "follow-up message"))

	state := agent.State()
	if len(state.Messages) != 0 {
		t.Fatalf("expected queued messages to stay out of history, got %+v", state.Messages)
	}
	if !agent.HasQueuedMessages() {
		t.Fatal("expected queued messages to be reported")
	}

	agent.ClearAllQueues()
	if agent.HasQueuedMessages() {
		t.Fatal("expected queues to be cleared")
	}
}

func TestAgentAbortWithoutRunDoesNotPanic(t *testing.T) {
	agent, err := NewAgent(AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, _ ModelRequest) (AssistantStream, error) {
				return nil, errors.New("unused")
			},
		},
	})
	if err != nil {
		t.Fatalf("NewAgent returned error: %v", err)
	}

	agent.Abort()
}

func TestAgentPromptWithToolLoop(t *testing.T) {
	calculator := ToolDefinition{
		Name:        "calculator",
		Label:       "Calculator",
		Description: "Adds numbers",
		Execute: func(_ context.Context, callID string, args any, _ ToolUpdateFunc) (ToolResult, error) {
			if callID != "call-1" {
				t.Fatalf("unexpected tool call ID %q", callID)
			}
			parsed, ok := args.(map[string]any)
			if !ok || parsed["expression"] != "2+2" {
				t.Fatalf("unexpected args %#v", args)
			}
			return ToolResult{
				Content: []Part{{Type: PartTypeText, Text: "4"}},
				Details: "4",
			}, nil
		},
	}

	agent, err := NewAgent(AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, request ModelRequest) (AssistantStream, error) {
				hasToolResult := false
				for _, message := range request.Messages {
					if message.Role == RoleTool {
						hasToolResult = true
						break
					}
				}

				if !hasToolResult {
					return newStaticAssistantStream(Message{
						Role:      RoleAssistant,
						Timestamp: time.Now().UTC(),
						ToolCalls: []ToolCall{{
							ID:        "call-1",
							Name:      "calculator",
							Arguments: []byte(`{"expression":"2+2"}`),
						}},
						StopReason: StopReasonToolUse,
					}, nil), nil
				}

				return newStaticAssistantStream(Message{
					Role:       RoleAssistant,
					Parts:      []Part{{Type: PartTypeText, Text: "The answer is 4."}},
					Timestamp:  time.Now().UTC(),
					StopReason: StopReasonStop,
				}, nil), nil
			},
		},
		Tools: []ToolDefinition{calculator},
	})
	if err != nil {
		t.Fatalf("NewAgent returned error: %v", err)
	}

	if err := agent.Prompt(context.Background(), NewTextMessage(RoleUser, "what is 2+2")); err != nil {
		t.Fatalf("Prompt returned error: %v", err)
	}

	snapshot := agent.Snapshot()
	if got := len(snapshot.Messages); got != 4 {
		t.Fatalf("expected 4 messages, got %d", got)
	}
	if snapshot.Messages[2].Role != RoleTool {
		t.Fatalf("expected tool message at index 2, got %s", snapshot.Messages[2].Role)
	}
	if snapshot.Messages[3].Parts[0].Text != "The answer is 4." {
		t.Fatalf("unexpected final assistant text %q", snapshot.Messages[3].Parts[0].Text)
	}
}

func TestAgentDefinitionResolver(t *testing.T) {
	var gotPrompt string

	agent, err := NewAgent(
		AgentDefinition{},
		WithDefinitionResolver(func(_ context.Context, _ AgentSnapshot) (AgentDefinition, error) {
			return AgentDefinition{
				SystemPrompt: "dynamic prompt",
				Model: staticModel{
					streamFn: func(_ context.Context, request ModelRequest) (AssistantStream, error) {
						gotPrompt = request.SystemPrompt
						return newStaticAssistantStream(Message{
							Role:       RoleAssistant,
							Parts:      []Part{{Type: PartTypeText, Text: "ok"}},
							Timestamp:  time.Now().UTC(),
							StopReason: StopReasonStop,
						}, nil), nil
					},
				},
			}, nil
		}),
	)
	if err != nil {
		t.Fatalf("NewAgent returned error: %v", err)
	}

	if err := agent.Prompt(context.Background(), NewTextMessage(RoleUser, "hello")); err != nil {
		t.Fatalf("Prompt returned error: %v", err)
	}
	if gotPrompt != "dynamic prompt" {
		t.Fatalf("expected dynamic prompt, got %q", gotPrompt)
	}
}

func TestAgentSteerDuringRun(t *testing.T) {
	tool := ToolDefinition{
		Name: "noop",
		Execute: func(_ context.Context, _ string, _ any, _ ToolUpdateFunc) (ToolResult, error) {
			return ToolResult{Content: []Part{{Type: PartTypeText, Text: "done"}}}, nil
		},
	}

	agent, err := NewAgent(AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, request ModelRequest) (AssistantStream, error) {
				hasSteering := false
				hasToolResult := false
				for _, message := range request.Messages {
					if message.Role == RoleUser && len(message.Parts) > 0 && message.Parts[0].Text == "steer now" {
						hasSteering = true
					}
					if message.Role == RoleTool {
						hasToolResult = true
					}
				}

				switch {
				case hasSteering:
					return newStaticAssistantStream(Message{
						Role:       RoleAssistant,
						Parts:      []Part{{Type: PartTypeText, Text: "saw steering"}},
						Timestamp:  time.Now().UTC(),
						StopReason: StopReasonStop,
					}, nil), nil
				case hasToolResult:
					t.Fatalf("expected steering message before second assistant turn")
				default:
					return newStaticAssistantStream(Message{
						Role:       RoleAssistant,
						Timestamp:  time.Now().UTC(),
						ToolCalls:  []ToolCall{{ID: "tool-1", Name: "noop"}},
						StopReason: StopReasonToolUse,
					}, nil), nil
				}

				return nil, nil
			},
		},
		Tools: []ToolDefinition{tool},
	})
	if err != nil {
		t.Fatalf("NewAgent returned error: %v", err)
	}

	agent.Subscribe(func(event AgentEvent) {
		if event.Type == EventToolExecutionEnd {
			agent.Steer(NewTextMessage(RoleUser, "steer now"))
		}
	})

	if err := agent.Prompt(context.Background(), NewTextMessage(RoleUser, "start")); err != nil {
		t.Fatalf("Prompt returned error: %v", err)
	}

	snapshot := agent.Snapshot()
	if got := len(snapshot.Messages); got != 5 {
		t.Fatalf("expected 5 messages, got %d", got)
	}
	if snapshot.Messages[3].Role != RoleUser || snapshot.Messages[3].Parts[0].Text != "steer now" {
		t.Fatalf("expected steering user message at index 3, got %+v", snapshot.Messages[3])
	}
	if snapshot.Messages[4].Parts[0].Text != "saw steering" {
		t.Fatalf("unexpected final assistant text %q", snapshot.Messages[4].Parts[0].Text)
	}
}

func TestAgentFollowUpQueue(t *testing.T) {
	callCount := 0
	agent, err := NewAgent(AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, request ModelRequest) (AssistantStream, error) {
				callCount++
				hasFollowUp := false
				for _, message := range request.Messages {
					if message.Role == RoleUser && len(message.Parts) > 0 && message.Parts[0].Text == "follow up" {
						hasFollowUp = true
					}
				}

				text := "first response"
				if hasFollowUp {
					text = "second response"
				}
				return newStaticAssistantStream(Message{
					Role:       RoleAssistant,
					Parts:      []Part{{Type: PartTypeText, Text: text}},
					Timestamp:  time.Now().UTC(),
					StopReason: StopReasonStop,
				}, nil), nil
			},
		},
	})
	if err != nil {
		t.Fatalf("NewAgent returned error: %v", err)
	}

	agent.FollowUp(NewTextMessage(RoleUser, "follow up"))
	if err := agent.Prompt(context.Background(), NewTextMessage(RoleUser, "start")); err != nil {
		t.Fatalf("Prompt returned error: %v", err)
	}

	snapshot := agent.Snapshot()
	if callCount != 2 {
		t.Fatalf("expected 2 model calls, got %d", callCount)
	}
	if got := len(snapshot.Messages); got != 4 {
		t.Fatalf("expected 4 messages, got %d", got)
	}
	if snapshot.Messages[2].Role != RoleUser || snapshot.Messages[2].Parts[0].Text != "follow up" {
		t.Fatalf("expected follow-up user message at index 2, got %+v", snapshot.Messages[2])
	}
	if snapshot.Messages[3].Parts[0].Text != "second response" {
		t.Fatalf("unexpected final assistant text %q", snapshot.Messages[3].Parts[0].Text)
	}
}

func TestAgentPromptEncodesRuntimeErrorsAsAssistantMessages(t *testing.T) {
	agent, err := NewAgent(AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, _ ModelRequest) (AssistantStream, error) {
				return nil, errors.New("boom")
			},
		},
	})
	if err != nil {
		t.Fatalf("NewAgent returned error: %v", err)
	}

	var endEvent AgentEvent
	agent.Subscribe(func(event AgentEvent) {
		if event.Type == EventAgentEnd {
			endEvent = event
		}
	})

	if err := agent.Prompt(context.Background(), NewTextMessage(RoleUser, "start")); err != nil {
		t.Fatalf("Prompt should not fail for runtime error, got %v", err)
	}

	snapshot := agent.Snapshot()
	if got := len(snapshot.Messages); got != 2 {
		t.Fatalf("expected 2 messages, got %d", got)
	}
	if snapshot.Messages[1].StopReason != StopReasonError {
		t.Fatalf("expected error stop reason, got %s", snapshot.Messages[1].StopReason)
	}
	if snapshot.Messages[1].ErrorMessage != "boom" {
		t.Fatalf("expected error message %q, got %q", "boom", snapshot.Messages[1].ErrorMessage)
	}
	if snapshot.Error != "boom" {
		t.Fatalf("expected snapshot error %q, got %q", "boom", snapshot.Error)
	}

	state := agent.State()
	if state.IsStreaming {
		t.Fatal("expected agent to be idle after runtime error")
	}
	if state.Error != "boom" {
		t.Fatalf("expected state error %q, got %q", "boom", state.Error)
	}
	if len(endEvent.Messages) != 1 || endEvent.Messages[0].ErrorMessage != "boom" {
		t.Fatalf("expected agent_end to contain runtime error message, got %+v", endEvent.Messages)
	}
}

func TestAgentPromptWhileRunningReturnsErrAlreadyRunning(t *testing.T) {
	started := make(chan struct{})
	agent, err := NewAgent(AgentDefinition{
		Model: blockingModel{started: started},
	})
	if err != nil {
		t.Fatalf("NewAgent returned error: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Prompt(context.Background(), NewTextMessage(RoleUser, "first"))
	}()

	<-started

	if err := agent.Prompt(context.Background(), NewTextMessage(RoleUser, "second")); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("expected ErrAlreadyRunning, got %v", err)
	}

	agent.Abort()
	if err := <-errCh; err != nil {
		t.Fatalf("expected first prompt to finish cleanly after abort, got %v", err)
	}
}

func TestAgentContinueWhileRunningReturnsErrAlreadyRunning(t *testing.T) {
	started := make(chan struct{})
	agent, err := NewAgent(AgentDefinition{
		Model: blockingModel{started: started},
	})
	if err != nil {
		t.Fatalf("NewAgent returned error: %v", err)
	}

	agent.ReplaceMessages([]Message{NewTextMessage(RoleUser, "first")})

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Continue(context.Background())
	}()

	<-started

	if err := agent.Continue(context.Background()); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("expected ErrAlreadyRunning, got %v", err)
	}

	agent.Abort()
	if err := <-errCh; err != nil {
		t.Fatalf("expected first continue to finish cleanly after abort, got %v", err)
	}
}

func TestAgentContinueNoMessagesReturnsError(t *testing.T) {
	agent, err := NewAgent(AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, _ ModelRequest) (AssistantStream, error) {
				t.Fatal("model should not be called")
				return nil, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("NewAgent returned error: %v", err)
	}

	if err := agent.Continue(context.Background()); !errors.Is(err, ErrNoMessagesToContinue) {
		t.Fatalf("expected ErrNoMessagesToContinue, got %v", err)
	}
}

func TestAgentContinueFromAssistantWithoutQueuedMessagesReturnsError(t *testing.T) {
	agent, err := NewAgent(AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, _ ModelRequest) (AssistantStream, error) {
				t.Fatal("model should not be called")
				return nil, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("NewAgent returned error: %v", err)
	}

	agent.ReplaceMessages([]Message{
		NewTextMessage(RoleUser, "hello"),
		{
			Role:       RoleAssistant,
			Parts:      []Part{{Type: PartTypeText, Text: "done"}},
			Timestamp:  time.Now().UTC(),
			StopReason: StopReasonStop,
		},
	})

	if err := agent.Continue(context.Background()); !errors.Is(err, ErrCannotContinueFromAssistant) {
		t.Fatalf("expected ErrCannotContinueFromAssistant, got %v", err)
	}
}

func TestAgentContinueProcessesQueuedFollowUpFromAssistantTail(t *testing.T) {
	callCount := 0
	agent, err := NewAgent(AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, request ModelRequest) (AssistantStream, error) {
				callCount++
				hasFollowUp := false
				for _, message := range request.Messages {
					if message.Role == RoleUser && len(message.Parts) > 0 && message.Parts[0].Text == "follow up" {
						hasFollowUp = true
					}
				}

				text := "first response"
				if hasFollowUp {
					text = "processed follow up"
				}
				return newStaticAssistantStream(Message{
					Role:       RoleAssistant,
					Parts:      []Part{{Type: PartTypeText, Text: text}},
					Timestamp:  time.Now().UTC(),
					StopReason: StopReasonStop,
				}, nil), nil
			},
		},
	})
	if err != nil {
		t.Fatalf("NewAgent returned error: %v", err)
	}

	agent.ReplaceMessages([]Message{
		NewTextMessage(RoleUser, "initial"),
		{
			Role:       RoleAssistant,
			Parts:      []Part{{Type: PartTypeText, Text: "initial response"}},
			Timestamp:  time.Now().UTC(),
			StopReason: StopReasonStop,
		},
	})
	agent.FollowUp(NewTextMessage(RoleUser, "follow up"))

	if err := agent.Continue(context.Background()); err != nil {
		t.Fatalf("Continue returned error: %v", err)
	}

	snapshot := agent.Snapshot()
	if callCount != 1 {
		t.Fatalf("expected 1 model call, got %d", callCount)
	}
	if got := len(snapshot.Messages); got != 4 {
		t.Fatalf("expected 4 messages, got %d", got)
	}
	if snapshot.Messages[2].Role != RoleUser || snapshot.Messages[2].Parts[0].Text != "follow up" {
		t.Fatalf("expected follow-up user message at index 2, got %+v", snapshot.Messages[2])
	}
	if snapshot.Messages[3].Parts[0].Text != "processed follow up" {
		t.Fatalf("unexpected final assistant text %q", snapshot.Messages[3].Parts[0].Text)
	}
}

func TestAgentContinueKeepsOneAtATimeSteeringFromAssistantTail(t *testing.T) {
	callCount := 0
	agent, err := NewAgent(AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, request ModelRequest) (AssistantStream, error) {
				callCount++
				seen := ""
				for i := len(request.Messages) - 1; i >= 0; i-- {
					message := request.Messages[i]
					if message.Role == RoleUser && len(message.Parts) > 0 {
						seen = message.Parts[0].Text
						break
					}
				}

				return newStaticAssistantStream(Message{
					Role:       RoleAssistant,
					Parts:      []Part{{Type: PartTypeText, Text: "processed " + seen}},
					Timestamp:  time.Now().UTC(),
					StopReason: StopReasonStop,
				}, nil), nil
			},
		},
	})
	if err != nil {
		t.Fatalf("NewAgent returned error: %v", err)
	}

	agent.ReplaceMessages([]Message{
		NewTextMessage(RoleUser, "initial"),
		{
			Role:       RoleAssistant,
			Parts:      []Part{{Type: PartTypeText, Text: "initial response"}},
			Timestamp:  time.Now().UTC(),
			StopReason: StopReasonStop,
		},
	})
	agent.Steer(NewTextMessage(RoleUser, "steering 1"))
	agent.Steer(NewTextMessage(RoleUser, "steering 2"))

	if err := agent.Continue(context.Background()); err != nil {
		t.Fatalf("Continue returned error: %v", err)
	}

	snapshot := agent.Snapshot()
	if callCount != 2 {
		t.Fatalf("expected 2 model calls, got %d", callCount)
	}
	if got := len(snapshot.Messages); got != 6 {
		t.Fatalf("expected 6 messages, got %d", got)
	}

	recent := snapshot.Messages[2:]
	gotRoles := []MessageRole{recent[0].Role, recent[1].Role, recent[2].Role, recent[3].Role}
	wantRoles := []MessageRole{RoleUser, RoleAssistant, RoleUser, RoleAssistant}
	for i := range wantRoles {
		if gotRoles[i] != wantRoles[i] {
			t.Fatalf("expected recent role %d to be %s, got %s", i, wantRoles[i], gotRoles[i])
		}
	}
	if recent[1].Parts[0].Text != "processed steering 1" {
		t.Fatalf("unexpected first steering response %q", recent[1].Parts[0].Text)
	}
	if recent[3].Parts[0].Text != "processed steering 2" {
		t.Fatalf("unexpected second steering response %q", recent[3].Parts[0].Text)
	}
}

func TestAgentForwardsSessionIDToModelRequest(t *testing.T) {
	var received []string
	agent, err := NewAgent(AgentDefinition{
		SessionID: "session-abc",
		Model: staticModel{
			streamFn: func(_ context.Context, request ModelRequest) (AssistantStream, error) {
				received = append(received, request.SessionID)
				return newStaticAssistantStream(Message{
					Role:       RoleAssistant,
					Parts:      []Part{{Type: PartTypeText, Text: "ok"}},
					Timestamp:  time.Now().UTC(),
					StopReason: StopReasonStop,
				}, nil), nil
			},
		},
	})
	if err != nil {
		t.Fatalf("NewAgent returned error: %v", err)
	}

	if err := agent.Prompt(context.Background(), NewTextMessage(RoleUser, "hello")); err != nil {
		t.Fatalf("Prompt returned error: %v", err)
	}

	agent.SetSessionID("session-def")
	if err := agent.Prompt(context.Background(), NewTextMessage(RoleUser, "hello again")); err != nil {
		t.Fatalf("Prompt returned error: %v", err)
	}

	if len(received) != 2 {
		t.Fatalf("expected 2 model calls, got %d", len(received))
	}
	if received[0] != "session-abc" {
		t.Fatalf("expected first session id %q, got %q", "session-abc", received[0])
	}
	if received[1] != "session-def" {
		t.Fatalf("expected second session id %q, got %q", "session-def", received[1])
	}
}

func TestAgentForwardsTransportThinkingBudgetsAndRetryDelay(t *testing.T) {
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
		InitialState: AgentInitialState{
			ThinkingLevel: ThinkingLow,
		},
		Transport:       TransportSSE,
		MaxRetryDelayMs: 111,
		ThinkingBudgets: ThinkingBudgets{
			ThinkingLow: 123,
		},
	})
	if err != nil {
		t.Fatalf("NewAgentWithOptions returned error: %v", err)
	}

	if err := agent.Prompt(context.Background(), NewTextMessage(RoleUser, "first")); err != nil {
		t.Fatalf("Prompt returned error: %v", err)
	}

	agent.SetThinkingLevel(ThinkingHigh)
	agent.SetTransport(Transport("custom"))
	agent.SetMaxRetryDelayMs(222)
	agent.SetThinkingBudgets(ThinkingBudgets{
		ThinkingHigh: 456,
	})

	if err := agent.Prompt(context.Background(), NewTextMessage(RoleUser, "second")); err != nil {
		t.Fatalf("Prompt returned error: %v", err)
	}

	if got := len(requests); got != 2 {
		t.Fatalf("expected 2 model requests, got %d", got)
	}
	if requests[0].ThinkingLevel != ThinkingLow {
		t.Fatalf("expected first thinking level %q, got %q", ThinkingLow, requests[0].ThinkingLevel)
	}
	if requests[0].Transport != TransportSSE {
		t.Fatalf("expected first transport %q, got %q", TransportSSE, requests[0].Transport)
	}
	if requests[0].MaxRetryDelayMs != 111 {
		t.Fatalf("expected first max retry delay 111, got %d", requests[0].MaxRetryDelayMs)
	}
	if requests[0].ThinkingBudgets[ThinkingLow] != 123 {
		t.Fatalf("expected first thinking budget 123, got %+v", requests[0].ThinkingBudgets)
	}

	if requests[1].ThinkingLevel != ThinkingHigh {
		t.Fatalf("expected second thinking level %q, got %q", ThinkingHigh, requests[1].ThinkingLevel)
	}
	if requests[1].Transport != Transport("custom") {
		t.Fatalf("expected second transport %q, got %q", Transport("custom"), requests[1].Transport)
	}
	if requests[1].MaxRetryDelayMs != 222 {
		t.Fatalf("expected second max retry delay 222, got %d", requests[1].MaxRetryDelayMs)
	}
	if requests[1].ThinkingBudgets[ThinkingHigh] != 456 {
		t.Fatalf("expected second thinking budget 456, got %+v", requests[1].ThinkingBudgets)
	}
}

func TestAgentAbortProducesAbortedAssistantMessage(t *testing.T) {
	started := make(chan struct{})
	agent, err := NewAgent(AgentDefinition{
		Model: blockingModel{started: started},
	})
	if err != nil {
		t.Fatalf("NewAgent returned error: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Prompt(context.Background(), NewTextMessage(RoleUser, "first"))
	}()

	<-started
	agent.Abort()

	if err := <-errCh; err != nil {
		t.Fatalf("expected prompt to finish cleanly after abort, got %v", err)
	}

	state := agent.State()
	if state.IsStreaming {
		t.Fatal("expected agent to be idle after abort")
	}
	if got := len(state.Messages); got != 2 {
		t.Fatalf("expected 2 messages after abort, got %d", got)
	}
	last := state.Messages[1]
	if last.StopReason != StopReasonAborted {
		t.Fatalf("expected abort stop reason, got %q", last.StopReason)
	}
	if last.ErrorMessage == "" {
		t.Fatal("expected aborted message to carry error message")
	}
	if state.Error != last.ErrorMessage {
		t.Fatalf("expected state error %q, got %q", last.ErrorMessage, state.Error)
	}
}

func TestAgentStateUpdatesWhileStreaming(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	agent, err := NewAgent(AgentDefinition{
		Model: streamingStateModel{
			started: started,
			release: release,
		},
	})
	if err != nil {
		t.Fatalf("NewAgent returned error: %v", err)
	}

	var (
		sawStreaming bool
		sawDelta     bool
	)
	agent.Subscribe(func(event AgentEvent) {
		state := agent.State()
		switch event.Type {
		case EventMessageStart:
			if state.IsStreaming && state.StreamMessage != nil && state.StreamMessage.Role == RoleAssistant {
				sawStreaming = true
			}
		case EventMessageUpdate:
			if state.StreamMessage != nil && len(state.StreamMessage.Parts) > 0 && state.StreamMessage.Parts[0].Text == "hel" {
				sawDelta = true
			}
		}
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Prompt(context.Background(), NewTextMessage(RoleUser, "hello"))
	}()

	<-started
	close(release)

	if err := <-errCh; err != nil {
		t.Fatalf("Prompt returned error: %v", err)
	}
	if !sawStreaming {
		t.Fatal("expected state to report streaming assistant message during start event")
	}
	if !sawDelta {
		t.Fatal("expected state to expose partial stream message during update event")
	}

	state := agent.State()
	if state.IsStreaming {
		t.Fatal("expected agent to be idle after completion")
	}
	if state.StreamMessage != nil {
		t.Fatalf("expected stream message to be cleared, got %+v", state.StreamMessage)
	}
}

func TestAgentPendingToolCallsUpdateDuringExecution(t *testing.T) {
	releaseTool := make(chan struct{})
	agent, err := NewAgent(AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, request ModelRequest) (AssistantStream, error) {
				hasToolResult := false
				for _, message := range request.Messages {
					if message.Role == RoleTool {
						hasToolResult = true
						break
					}
				}
				if !hasToolResult {
					return newStaticAssistantStream(Message{
						Role: RoleAssistant,
						ToolCalls: []ToolCall{{
							ID:         "tool-1",
							OriginalID: "tool-raw-1",
							Name:       "slow",
							Arguments:  []byte(`{"value":"x"}`),
						}},
						Timestamp:  time.Now().UTC(),
						StopReason: StopReasonToolUse,
					}, nil), nil
				}
				return newStaticAssistantStream(Message{
					Role:       RoleAssistant,
					Parts:      []Part{{Type: PartTypeText, Text: "done"}},
					Timestamp:  time.Now().UTC(),
					StopReason: StopReasonStop,
				}, nil), nil
			},
		},
		Tools: []ToolDefinition{{
			Name: "slow",
			Execute: func(_ context.Context, _ string, _ any, _ ToolUpdateFunc) (ToolResult, error) {
				<-releaseTool
				return ToolResult{Content: []Part{{Type: PartTypeText, Text: "ok"}}}, nil
			},
		}},
	})
	if err != nil {
		t.Fatalf("NewAgent returned error: %v", err)
	}

	toolStarted := make(chan struct{}, 1)
	toolEnded := make(chan struct{}, 1)
	agent.Subscribe(func(event AgentEvent) {
		switch event.Type {
		case EventToolExecutionStart:
			state := agent.State()
			pending, ok := state.PendingToolCalls["tool-1"]
			if !ok {
				t.Fatalf("expected pending tool call to be present during start, got %+v", state.PendingToolCalls)
			}
			if pending.OriginalToolCallID != "tool-raw-1" {
				t.Fatalf("expected pending original tool call id %q, got %+v", "tool-raw-1", pending)
			}
			if event.OriginalToolCallID != "tool-raw-1" {
				t.Fatalf("expected event original tool call id %q, got %+v", "tool-raw-1", event)
			}
			toolStarted <- struct{}{}
		case EventToolExecutionEnd:
			state := agent.State()
			if _, ok := state.PendingToolCalls["tool-1"]; ok {
				t.Fatalf("expected pending tool call to be cleared during end, got %+v", state.PendingToolCalls)
			}
			toolEnded <- struct{}{}
		}
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Prompt(context.Background(), NewTextMessage(RoleUser, "run"))
	}()

	<-toolStarted
	close(releaseTool)
	<-toolEnded

	if err := <-errCh; err != nil {
		t.Fatalf("Prompt returned error: %v", err)
	}
}

func TestAgentContinueFromUserMessage(t *testing.T) {
	agent, err := NewAgent(AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, request ModelRequest) (AssistantStream, error) {
				if got := len(request.Messages); got != 1 {
					t.Fatalf("expected 1 request message, got %d", got)
				}
				return newStaticAssistantStream(Message{
					Role:       RoleAssistant,
					Parts:      []Part{{Type: PartTypeText, Text: "HELLO WORLD"}},
					Timestamp:  time.Now().UTC(),
					StopReason: StopReasonStop,
				}, nil), nil
			},
		},
	})
	if err != nil {
		t.Fatalf("NewAgent returned error: %v", err)
	}

	agent.ReplaceMessages([]Message{NewTextMessage(RoleUser, "Say exactly: HELLO WORLD")})

	if err := agent.Continue(context.Background()); err != nil {
		t.Fatalf("Continue returned error: %v", err)
	}

	state := agent.State()
	if state.IsStreaming {
		t.Fatal("expected agent to be idle")
	}
	if got := len(state.Messages); got != 2 {
		t.Fatalf("expected 2 messages, got %d", got)
	}
	if state.Messages[1].Role != RoleAssistant || state.Messages[1].Parts[0].Text != "HELLO WORLD" {
		t.Fatalf("unexpected assistant message %+v", state.Messages[1])
	}
}

func TestAgentContinueFromToolResult(t *testing.T) {
	agent, err := NewAgent(AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, request ModelRequest) (AssistantStream, error) {
				last := request.Messages[len(request.Messages)-1]
				if last.Role != RoleTool || last.ToolResult == nil {
					t.Fatalf("expected tool tail message, got %+v", last)
				}
				return newStaticAssistantStream(Message{
					Role:       RoleAssistant,
					Parts:      []Part{{Type: PartTypeText, Text: "The answer is 8."}},
					Timestamp:  time.Now().UTC(),
					StopReason: StopReasonStop,
				}, nil), nil
			},
		},
	})
	if err != nil {
		t.Fatalf("NewAgent returned error: %v", err)
	}

	call := ToolCall{ID: "calc-1", Name: "calculate"}
	agent.ReplaceMessages([]Message{
		NewTextMessage(RoleUser, "What is 5 + 3?"),
		{
			Role:      RoleAssistant,
			Parts:     []Part{{Type: PartTypeText, Text: "Let me calculate that."}},
			ToolCalls: []ToolCall{call},
			Timestamp: time.Now().UTC(),
		},
		NewToolResultMessage(call, ToolResult{
			Content: []Part{{Type: PartTypeText, Text: "5 + 3 = 8"}},
			Details: "8",
		}, false),
	})

	if err := agent.Continue(context.Background()); err != nil {
		t.Fatalf("Continue returned error: %v", err)
	}

	state := agent.State()
	if got := len(state.Messages); got != 4 {
		t.Fatalf("expected 4 messages, got %d", got)
	}
	last := state.Messages[3]
	if last.Role != RoleAssistant || last.Parts[0].Text != "The answer is 8." {
		t.Fatalf("unexpected final assistant message %+v", last)
	}
}

type staticModel struct {
	streamFn func(context.Context, ModelRequest) (AssistantStream, error)
}

func (m staticModel) Stream(ctx context.Context, request ModelRequest) (AssistantStream, error) {
	return m.streamFn(ctx, request)
}

type staticAssistantStream struct {
	events  chan AssistantEvent
	done    chan struct{}
	message Message
	err     error
}

func newStaticAssistantStream(message Message, events []AssistantEvent) *staticAssistantStream {
	stream := &staticAssistantStream{
		events:  make(chan AssistantEvent, len(events)),
		done:    make(chan struct{}),
		message: message,
	}

	go func() {
		for _, event := range events {
			stream.events <- event
		}
		close(stream.events)
		close(stream.done)
	}()

	return stream
}

func (s *staticAssistantStream) Events() <-chan AssistantEvent {
	return s.events
}

func (s *staticAssistantStream) Wait() (Message, error) {
	<-s.done
	return s.message, s.err
}

type blockingModel struct {
	started chan struct{}
	once    sync.Once
}

func (m blockingModel) Stream(ctx context.Context, _ ModelRequest) (AssistantStream, error) {
	return newBlockingAssistantStream(ctx, m.started, &m.once), nil
}

type blockingAssistantStream struct {
	events  chan AssistantEvent
	done    chan struct{}
	message Message
}

func newBlockingAssistantStream(ctx context.Context, started chan struct{}, once *sync.Once) *blockingAssistantStream {
	stream := &blockingAssistantStream{
		events: make(chan AssistantEvent, 1),
		done:   make(chan struct{}),
	}

	go func() {
		stream.events <- AssistantEvent{
			Type: AssistantEventStart,
			Message: Message{
				Role:      RoleAssistant,
				Timestamp: time.Now().UTC(),
			},
		}
		if started != nil && once != nil {
			once.Do(func() {
				close(started)
			})
		}

		<-ctx.Done()
		stream.message = Message{
			Role:         RoleAssistant,
			Timestamp:    time.Now().UTC(),
			StopReason:   StopReasonAborted,
			ErrorMessage: ctx.Err().Error(),
		}
		close(stream.events)
		close(stream.done)
	}()

	return stream
}

func (s *blockingAssistantStream) Events() <-chan AssistantEvent {
	return s.events
}

func (s *blockingAssistantStream) Wait() (Message, error) {
	<-s.done
	return s.message, nil
}

type streamingStateModel struct {
	started chan struct{}
	release chan struct{}
}

func (m streamingStateModel) Stream(_ context.Context, _ ModelRequest) (AssistantStream, error) {
	return newStreamingStateAssistantStream(m.started, m.release), nil
}

type streamingStateAssistantStream struct {
	events  chan AssistantEvent
	done    chan struct{}
	message Message
}

func newStreamingStateAssistantStream(started chan struct{}, release chan struct{}) *streamingStateAssistantStream {
	stream := &streamingStateAssistantStream{
		events: make(chan AssistantEvent, 2),
		done:   make(chan struct{}),
	}

	go func() {
		partial := Message{
			Role:      RoleAssistant,
			Timestamp: time.Now().UTC(),
		}
		stream.events <- AssistantEvent{
			Type:    AssistantEventStart,
			Message: partial,
		}
		stream.events <- AssistantEvent{
			Type: AssistantEventTextDelta,
			Message: Message{
				Role:      RoleAssistant,
				Parts:     []Part{{Type: PartTypeText, Text: "hel"}},
				Timestamp: time.Now().UTC(),
			},
			Delta: "hel",
		}
		if started != nil {
			close(started)
		}
		<-release
		stream.message = Message{
			Role:       RoleAssistant,
			Parts:      []Part{{Type: PartTypeText, Text: "hello"}},
			Timestamp:  time.Now().UTC(),
			StopReason: StopReasonStop,
		}
		close(stream.events)
		close(stream.done)
	}()

	return stream
}

func (s *streamingStateAssistantStream) Events() <-chan AssistantEvent {
	return s.events
}

func (s *streamingStateAssistantStream) Wait() (Message, error) {
	<-s.done
	return s.message, nil
}
