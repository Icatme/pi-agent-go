package piagentgo

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestEngineContinueNoMessagesReturnsError(t *testing.T) {
	engine := NewEngine()
	definition, err := AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, _ ModelRequest) (AssistantStream, error) {
				t.Fatal("model should not be called")
				return nil, nil
			},
		},
	}.Validate()
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}

	if _, err := engine.Continue(context.Background(), definition, &AgentSnapshot{}, nil); err != ErrNoMessagesToContinue {
		t.Fatalf("expected ErrNoMessagesToContinue, got %v", err)
	}
}

func TestEngineContinueDoesNotEmitExistingUserMessageEvents(t *testing.T) {
	engine := NewEngine()
	definition, err := AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, _ ModelRequest) (AssistantStream, error) {
				return newStaticAssistantStream(Message{
					Role:       RoleAssistant,
					Parts:      []Part{{Type: PartTypeText, Text: "response"}},
					Timestamp:  time.Now().UTC(),
					StopReason: StopReasonStop,
				}, nil), nil
			},
		},
	}.Validate()
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}

	snapshot := &AgentSnapshot{
		Messages: []Message{NewTextMessage(RoleUser, "hello")},
	}

	var events []AgentEvent
	next, err := engine.Continue(context.Background(), definition, snapshot, func(event AgentEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("Continue returned error: %v", err)
	}

	if got := len(next.Messages); got != 2 {
		t.Fatalf("expected 2 messages, got %d", got)
	}

	var messageEnds []AgentEvent
	for _, event := range events {
		if event.Type == EventMessageEnd {
			messageEnds = append(messageEnds, event)
		}
	}
	if got := len(messageEnds); got != 1 {
		t.Fatalf("expected 1 message_end event, got %d", got)
	}
	if messageEnds[0].Message == nil || messageEnds[0].Message.Role != RoleAssistant {
		t.Fatalf("expected only assistant message_end event, got %+v", messageEnds[0].Message)
	}
}

func TestEngineConvertToLLMCanFilterCustomMessages(t *testing.T) {
	engine := NewEngine()
	var received []Message

	definition, err := AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, request ModelRequest) (AssistantStream, error) {
				received = cloneMessages(request.Messages)
				return newStaticAssistantStream(Message{
					Role:       RoleAssistant,
					Parts:      []Part{{Type: PartTypeText, Text: "ok"}},
					Timestamp:  time.Now().UTC(),
					StopReason: StopReasonStop,
				}, nil), nil
			},
		},
		ConvertToLLM: func(_ context.Context, messages []Message) ([]Message, error) {
			converted := make([]Message, 0, len(messages))
			for _, message := range messages {
				if message.Role == RoleCustom {
					continue
				}
				converted = append(converted, cloneMessage(message))
			}
			return converted, nil
		},
	}.Validate()
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}

	snapshot := &AgentSnapshot{
		Messages: []Message{{
			Role:      RoleCustom,
			Kind:      "notification",
			Parts:     []Part{{Type: PartTypeText, Text: "ignore me"}},
			Timestamp: time.Now().UTC(),
		}},
	}

	if _, err := engine.Run(context.Background(), definition, snapshot, []Message{NewTextMessage(RoleUser, "hello")}, nil); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if got := len(received); got != 1 {
		t.Fatalf("expected 1 converted message, got %d", got)
	}
	if received[0].Role != RoleUser || received[0].Parts[0].Text != "hello" {
		t.Fatalf("unexpected converted message %+v", received[0])
	}
}

func TestEngineTransformContextRunsBeforeConvertToLLM(t *testing.T) {
	engine := NewEngine()
	var (
		transformed []Message
		converted   []Message
	)

	definition, err := AgentDefinition{
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
		TransformContext: func(_ context.Context, messages []Message) ([]Message, error) {
			transformed = cloneMessages(messages[len(messages)-2:])
			return cloneMessages(transformed), nil
		},
		ConvertToLLM: func(_ context.Context, messages []Message) ([]Message, error) {
			converted = cloneMessages(messages)
			return cloneMessages(messages), nil
		},
	}.Validate()
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}

	snapshot := &AgentSnapshot{
		Messages: []Message{
			NewTextMessage(RoleUser, "old message 1"),
			{Role: RoleAssistant, Parts: []Part{{Type: PartTypeText, Text: "old response 1"}}, Timestamp: time.Now().UTC()},
			NewTextMessage(RoleUser, "old message 2"),
			{Role: RoleAssistant, Parts: []Part{{Type: PartTypeText, Text: "old response 2"}}, Timestamp: time.Now().UTC()},
		},
	}

	if _, err := engine.Run(context.Background(), definition, snapshot, []Message{NewTextMessage(RoleUser, "new message")}, nil); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if got := len(transformed); got != 2 {
		t.Fatalf("expected 2 transformed messages, got %d", got)
	}
	if transformed[0].Role != RoleAssistant || transformed[0].Parts[0].Text != "old response 2" {
		t.Fatalf("unexpected first transformed message %+v", transformed[0])
	}
	if transformed[1].Role != RoleUser || transformed[1].Parts[0].Text != "new message" {
		t.Fatalf("unexpected second transformed message %+v", transformed[1])
	}

	if got := len(converted); got != 2 {
		t.Fatalf("expected 2 converted messages, got %d", got)
	}
	if converted[0].Parts[0].Text != transformed[0].Parts[0].Text || converted[1].Parts[0].Text != transformed[1].Parts[0].Text {
		t.Fatalf("expected convertToLLM to receive transformed messages, got %+v", converted)
	}
}

func TestEngineContinueAllowsCustomTailWhenConvertToLLMMapsIt(t *testing.T) {
	engine := NewEngine()
	var received []Message

	definition, err := AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, request ModelRequest) (AssistantStream, error) {
				received = cloneMessages(request.Messages)
				return newStaticAssistantStream(Message{
					Role:       RoleAssistant,
					Parts:      []Part{{Type: PartTypeText, Text: "response to custom"}},
					Timestamp:  time.Now().UTC(),
					StopReason: StopReasonStop,
				}, nil), nil
			},
		},
		ConvertToLLM: func(_ context.Context, messages []Message) ([]Message, error) {
			converted := make([]Message, 0, len(messages))
			for _, message := range messages {
				if message.Role == RoleCustom {
					converted = append(converted, Message{
						Role:      RoleUser,
						Parts:     cloneParts(message.Parts),
						Timestamp: message.Timestamp,
					})
					continue
				}
				converted = append(converted, cloneMessage(message))
			}
			return converted, nil
		},
	}.Validate()
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}

	snapshot := &AgentSnapshot{
		Messages: []Message{{
			Role:      RoleCustom,
			Kind:      "hook",
			Parts:     []Part{{Type: PartTypeText, Text: "Hook content"}},
			Timestamp: time.Now().UTC(),
		}},
	}

	next, err := engine.Continue(context.Background(), definition, snapshot, nil)
	if err != nil {
		t.Fatalf("Continue returned error: %v", err)
	}

	if got := len(received); got != 1 {
		t.Fatalf("expected 1 converted request message, got %d", got)
	}
	if received[0].Role != RoleUser || received[0].Parts[0].Text != "Hook content" {
		t.Fatalf("unexpected converted request message %+v", received[0])
	}
	if got := len(next.Messages); got != 2 {
		t.Fatalf("expected 2 messages after continue, got %d", got)
	}
	if next.Messages[1].Role != RoleAssistant || next.Messages[1].Parts[0].Text != "response to custom" {
		t.Fatalf("unexpected final assistant message %+v", next.Messages[1])
	}
}

func TestEngineToolExecutionParallelPreservesSourceOrder(t *testing.T) {
	engine := NewEngine()

	var (
		mu               sync.Mutex
		firstResolved    bool
		parallelObserved bool
		releaseFirst     chan struct{}
	)
	releaseFirst = make(chan struct{})

	tool := ToolDefinition{
		Name: "echo",
		Execute: func(_ context.Context, _ string, args any, _ ToolUpdateFunc) (ToolResult, error) {
			parsed, ok := args.(map[string]any)
			if !ok {
				t.Fatalf("unexpected args type %T", args)
			}
			value, _ := parsed["value"].(string)
			if value == "first" {
				<-releaseFirst
				mu.Lock()
				firstResolved = true
				mu.Unlock()
			}
			if value == "second" {
				mu.Lock()
				if !firstResolved {
					parallelObserved = true
				}
				mu.Unlock()
			}
			return ToolResult{
				Content: []Part{{Type: PartTypeText, Text: "echoed: " + value}},
				Details: value,
			}, nil
		},
	}

	callIndex := 0
	definition, err := AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, _ ModelRequest) (AssistantStream, error) {
				if callIndex == 0 {
					callIndex++
					stream := newStaticAssistantStream(Message{
						Role: RoleAssistant,
						ToolCalls: []ToolCall{
							{ID: "tool-1", Name: "echo", Arguments: []byte(`{"value":"first"}`)},
							{ID: "tool-2", Name: "echo", Arguments: []byte(`{"value":"second"}`)},
						},
						Timestamp:  time.Now().UTC(),
						StopReason: StopReasonToolUse,
					}, nil)
					go func() {
						time.Sleep(20 * time.Millisecond)
						close(releaseFirst)
					}()
					return stream, nil
				}

				return newStaticAssistantStream(Message{
					Role:       RoleAssistant,
					Parts:      []Part{{Type: PartTypeText, Text: "done"}},
					Timestamp:  time.Now().UTC(),
					StopReason: StopReasonStop,
				}, nil), nil
			},
		},
		Tools:         []ToolDefinition{tool},
		ToolExecution: ToolExecutionParallel,
	}.Validate()
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}

	var events []AgentEvent
	next, err := engine.Run(context.Background(), definition, &AgentSnapshot{}, []Message{NewTextMessage(RoleUser, "echo both")}, func(event AgentEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	var toolResultIDs []string
	for _, event := range events {
		if event.Type != EventMessageEnd || event.Message == nil || event.Message.Role != RoleTool || event.Message.ToolResult == nil {
			continue
		}
		toolResultIDs = append(toolResultIDs, event.Message.ToolResult.ToolCallID)
	}

	mu.Lock()
	observed := parallelObserved
	mu.Unlock()

	if !observed {
		t.Fatal("expected second tool execution to observe parallel execution")
	}
	if got := len(next.PendingToolCalls); got != 0 {
		t.Fatalf("expected pending tool calls to be cleared after parallel execution, got %d", got)
	}
	if got := len(toolResultIDs); got != 2 {
		t.Fatalf("expected 2 tool result messages, got %d", got)
	}
	if toolResultIDs[0] != "tool-1" || toolResultIDs[1] != "tool-2" {
		t.Fatalf("expected tool results in source order, got %v", toolResultIDs)
	}
}

func TestEngineInjectsSteeringAfterAllToolCallsComplete(t *testing.T) {
	engine := NewEngine()
	executed := make([]string, 0, 2)

	tool := ToolDefinition{
		Name: "echo",
		Execute: func(_ context.Context, _ string, args any, _ ToolUpdateFunc) (ToolResult, error) {
			parsed, ok := args.(map[string]any)
			if !ok {
				t.Fatalf("unexpected args type %T", args)
			}
			value, _ := parsed["value"].(string)
			executed = append(executed, value)
			return ToolResult{
				Content: []Part{{Type: PartTypeText, Text: "ok:" + value}},
				Details: value,
			}, nil
		},
	}

	queuedDelivered := false
	callIndex := 0
	sawInterruptInContext := false

	definition, err := AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, request ModelRequest) (AssistantStream, error) {
				if callIndex == 1 {
					for _, message := range request.Messages {
						if message.Role == RoleUser && len(message.Parts) > 0 && message.Parts[0].Text == "interrupt" {
							sawInterruptInContext = true
							break
						}
					}
				}

				if callIndex == 0 {
					callIndex++
					return newStaticAssistantStream(Message{
						Role: RoleAssistant,
						ToolCalls: []ToolCall{
							{ID: "tool-1", Name: "echo", Arguments: []byte(`{"value":"first"}`)},
							{ID: "tool-2", Name: "echo", Arguments: []byte(`{"value":"second"}`)},
						},
						Timestamp:  time.Now().UTC(),
						StopReason: StopReasonToolUse,
					}, nil), nil
				}

				callIndex++
				return newStaticAssistantStream(Message{
					Role:       RoleAssistant,
					Parts:      []Part{{Type: PartTypeText, Text: "done"}},
					Timestamp:  time.Now().UTC(),
					StopReason: StopReasonStop,
				}, nil), nil
			},
		},
		Tools:         []ToolDefinition{tool},
		ToolExecution: ToolExecutionSequential,
	}.Validate()
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}

	hooks := LoopHooks{
		GetSteeringMessages: func(context.Context) ([]Message, error) {
			if len(executed) >= 1 && !queuedDelivered {
				queuedDelivered = true
				return []Message{NewTextMessage(RoleUser, "interrupt")}, nil
			}
			return nil, nil
		},
	}

	var events []AgentEvent
	if _, err := engine.RunWithHooks(context.Background(), definition, &AgentSnapshot{}, []Message{NewTextMessage(RoleUser, "start")}, func(event AgentEvent) {
		events = append(events, event)
	}, hooks); err != nil {
		t.Fatalf("RunWithHooks returned error: %v", err)
	}

	if len(executed) != 2 || executed[0] != "first" || executed[1] != "second" {
		t.Fatalf("expected both tools to execute before steering injection, got %v", executed)
	}

	var eventSequence []string
	for _, event := range events {
		if event.Type != EventMessageStart || event.Message == nil {
			continue
		}
		switch {
		case event.Message.Role == RoleTool && event.Message.ToolResult != nil:
			eventSequence = append(eventSequence, "tool:"+event.Message.ToolResult.ToolCallID)
		case event.Message.Role == RoleUser && len(event.Message.Parts) > 0:
			eventSequence = append(eventSequence, event.Message.Parts[0].Text)
		}
	}

	indexOf := func(target string) int {
		for i, item := range eventSequence {
			if item == target {
				return i
			}
		}
		return -1
	}

	interruptIndex := indexOf("interrupt")
	if interruptIndex == -1 {
		t.Fatalf("expected interrupt message in event sequence, got %v", eventSequence)
	}
	if tool1Index := indexOf("tool:tool-1"); tool1Index == -1 || tool1Index > interruptIndex {
		t.Fatalf("expected tool-1 result before interrupt, got %v", eventSequence)
	}
	if tool2Index := indexOf("tool:tool-2"); tool2Index == -1 || tool2Index > interruptIndex {
		t.Fatalf("expected tool-2 result before interrupt, got %v", eventSequence)
	}
	if !sawInterruptInContext {
		t.Fatal("expected interrupt message to be present in second model request")
	}
}

func TestEngineBeforeToolCallMutatesExecutionArgsWithoutRevalidation(t *testing.T) {
	engine := NewEngine()

	var (
		afterArgs any
		callIndex int
		executed  any
	)
	definition, err := AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, _ ModelRequest) (AssistantStream, error) {
				if callIndex == 0 {
					callIndex++
					return newStaticAssistantStream(Message{
						Role: RoleAssistant,
						ToolCalls: []ToolCall{
							{ID: "tool-1", Name: "echo", Arguments: []byte(`{"value":"hello"}`)},
						},
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
			Name: "echo",
			Execute: func(_ context.Context, _ string, args any, _ ToolUpdateFunc) (ToolResult, error) {
				parsed, ok := args.(map[string]any)
				if !ok {
					t.Fatalf("unexpected args type %T", args)
				}
				executed = parsed["value"]
				return ToolResult{
					Content: []Part{{Type: PartTypeText, Text: "done"}},
				}, nil
			},
		}},
		BeforeToolCall: func(_ context.Context, input BeforeToolCallContext) (BeforeToolCallResult, error) {
			parsed, ok := input.Args.(map[string]any)
			if !ok {
				t.Fatalf("unexpected before-tool args type %T", input.Args)
			}
			parsed["value"] = 123
			return BeforeToolCallResult{}, nil
		},
		AfterToolCall: func(_ context.Context, input AfterToolCallContext) (AfterToolCallResult, error) {
			afterArgs = input.Args
			return AfterToolCallResult{}, nil
		},
	}.Validate()
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}

	next, err := engine.Run(context.Background(), definition, &AgentSnapshot{}, []Message{NewTextMessage(RoleUser, "run")}, nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if executed != 123 {
		t.Fatalf("expected mutated before-tool args to be executed, got %#v", executed)
	}
	afterParsed, ok := afterArgs.(map[string]any)
	if !ok || afterParsed["value"] != 123 {
		t.Fatalf("expected after-tool hook to see mutated args, got %#v", afterArgs)
	}
	if len(next.Messages) != 4 {
		t.Fatalf("expected user, assistant, tool-result, assistant messages, got %d", len(next.Messages))
	}
	if next.Messages[2].Role != RoleTool {
		t.Fatalf("expected tool result message, got %+v", next.Messages[2])
	}
	if next.Messages[3].Role != RoleAssistant || next.Messages[3].Parts[0].Text != "done" {
		t.Fatalf("expected final assistant response, got %+v", next.Messages[3])
	}
}

func TestEngineBeforeToolCallBlockProducesErrorToolResult(t *testing.T) {
	engine := NewEngine()

	var (
		callIndex     int
		executeCalled bool
	)
	definition, err := AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, _ ModelRequest) (AssistantStream, error) {
				if callIndex == 0 {
					callIndex++
					return newStaticAssistantStream(Message{
						Role: RoleAssistant,
						ToolCalls: []ToolCall{
							{ID: "tool-1", Name: "echo", Arguments: []byte(`{"value":"hello"}`)},
						},
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
			Name: "echo",
			Execute: func(_ context.Context, _ string, _ any, _ ToolUpdateFunc) (ToolResult, error) {
				executeCalled = true
				return ToolResult{}, nil
			},
		}},
		BeforeToolCall: func(_ context.Context, _ BeforeToolCallContext) (BeforeToolCallResult, error) {
			return BeforeToolCallResult{
				Block:  true,
				Reason: "blocked by policy",
			}, nil
		},
	}.Validate()
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}

	next, err := engine.Run(context.Background(), definition, &AgentSnapshot{}, []Message{NewTextMessage(RoleUser, "run")}, nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if executeCalled {
		t.Fatal("expected blocked tool call to skip execution")
	}
	if len(next.Messages) != 4 {
		t.Fatalf("expected user, assistant, blocked tool-result, assistant messages, got %d", len(next.Messages))
	}
	toolResult := next.Messages[2]
	if toolResult.Role != RoleTool || toolResult.ToolResult == nil {
		t.Fatalf("expected blocked tool result message, got %+v", toolResult)
	}
	if !toolResult.ToolResult.IsError || toolResult.ToolResult.Content[0].Text != "blocked by policy" {
		t.Fatalf("expected blocked tool result payload, got %+v", toolResult.ToolResult)
	}
}

func TestEngineBeforeToolCallErrorStopsRun(t *testing.T) {
	engine := NewEngine()

	definition, err := AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, _ ModelRequest) (AssistantStream, error) {
				return newStaticAssistantStream(Message{
					Role: RoleAssistant,
					ToolCalls: []ToolCall{
						{ID: "tool-1", Name: "echo", Arguments: []byte(`{"value":"hello"}`)},
					},
					Timestamp:  time.Now().UTC(),
					StopReason: StopReasonToolUse,
				}, nil), nil
			},
		},
		Tools: []ToolDefinition{{Name: "echo"}},
		BeforeToolCall: func(_ context.Context, _ BeforeToolCallContext) (BeforeToolCallResult, error) {
			return BeforeToolCallResult{}, errors.New("before hook boom")
		},
	}.Validate()
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}

	next, err := engine.Run(context.Background(), definition, &AgentSnapshot{}, []Message{NewTextMessage(RoleUser, "run")}, nil)
	if err == nil || err.Error() != "before hook boom" {
		t.Fatalf("expected before-hook error, got %v", err)
	}
	if next == nil || next.Error != "before hook boom" {
		t.Fatalf("expected snapshot error to be preserved, got %+v", next)
	}
	if len(next.Messages) != 2 {
		t.Fatalf("expected run to stop before tool result append, got %d messages", len(next.Messages))
	}
}

func TestEngineAfterToolCallOverridesResultAndErrorFlag(t *testing.T) {
	engine := NewEngine()

	callIndex := 0
	definition, err := AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, _ ModelRequest) (AssistantStream, error) {
				if callIndex == 0 {
					callIndex++
					return newStaticAssistantStream(Message{
						Role: RoleAssistant,
						ToolCalls: []ToolCall{
							{ID: "tool-1", Name: "echo", Arguments: []byte(`{"value":"hello"}`)},
						},
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
			Name: "echo",
			Execute: func(_ context.Context, _ string, _ any, _ ToolUpdateFunc) (ToolResult, error) {
				return ToolResult{
					Content: []Part{{Type: PartTypeText, Text: "raw"}},
				}, nil
			},
		}},
		AfterToolCall: func(_ context.Context, input AfterToolCallContext) (AfterToolCallResult, error) {
			isError := true
			return AfterToolCallResult{
				Result: &ToolResult{
					Content: []Part{{Type: PartTypeText, Text: "overridden"}},
				},
				IsError: &isError,
			}, nil
		},
	}.Validate()
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}

	next, err := engine.Run(context.Background(), definition, &AgentSnapshot{}, []Message{NewTextMessage(RoleUser, "run")}, nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(next.Messages) != 4 {
		t.Fatalf("expected user, assistant, tool-result, assistant messages, got %d", len(next.Messages))
	}
	toolResult := next.Messages[2]
	if toolResult.Role != RoleTool || toolResult.ToolResult == nil {
		t.Fatalf("expected tool result message, got %+v", toolResult)
	}
	if !toolResult.ToolResult.IsError || toolResult.ToolResult.Content[0].Text != "overridden" {
		t.Fatalf("expected overridden tool result payload, got %+v", toolResult.ToolResult)
	}
}

func TestEngineAfterToolCallErrorStopsRun(t *testing.T) {
	engine := NewEngine()

	definition, err := AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, _ ModelRequest) (AssistantStream, error) {
				return newStaticAssistantStream(Message{
					Role: RoleAssistant,
					ToolCalls: []ToolCall{
						{ID: "tool-1", Name: "echo", Arguments: []byte(`{"value":"hello"}`)},
					},
					Timestamp:  time.Now().UTC(),
					StopReason: StopReasonToolUse,
				}, nil), nil
			},
		},
		Tools: []ToolDefinition{{
			Name: "echo",
			Execute: func(_ context.Context, _ string, _ any, _ ToolUpdateFunc) (ToolResult, error) {
				return ToolResult{
					Content: []Part{{Type: PartTypeText, Text: "raw"}},
				}, nil
			},
		}},
		AfterToolCall: func(_ context.Context, _ AfterToolCallContext) (AfterToolCallResult, error) {
			return AfterToolCallResult{}, errors.New("after hook boom")
		},
	}.Validate()
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}

	next, err := engine.Run(context.Background(), definition, &AgentSnapshot{}, []Message{NewTextMessage(RoleUser, "run")}, nil)
	if err == nil || err.Error() != "after hook boom" {
		t.Fatalf("expected after-hook error, got %v", err)
	}
	if next == nil || next.Error != "after hook boom" {
		t.Fatalf("expected snapshot error to be preserved, got %+v", next)
	}
	if len(next.Messages) != 2 {
		t.Fatalf("expected run to stop before tool result append, got %d messages", len(next.Messages))
	}
}

func TestEngineContinueAllowsMixedMessagesWhenConvertToLLMMapsCustomTail(t *testing.T) {
	engine := NewEngine()

	var (
		converted []Message
		received  []Message
	)
	definition, err := AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, request ModelRequest) (AssistantStream, error) {
				received = cloneMessages(request.Messages)
				return newStaticAssistantStream(Message{
					Role:       RoleAssistant,
					Parts:      []Part{{Type: PartTypeText, Text: "done"}},
					Timestamp:  time.Now().UTC(),
					StopReason: StopReasonStop,
				}, nil), nil
			},
		},
		TransformContext: func(_ context.Context, messages []Message) ([]Message, error) {
			return cloneMessages(messages), nil
		},
		ConvertToLLM: func(_ context.Context, messages []Message) ([]Message, error) {
			converted = cloneMessages(messages)
			result := make([]Message, 0, len(messages))
			for _, message := range messages {
				if message.Role == RoleCustom {
					result = append(result, Message{
						Role: RoleUser,
						Parts: []Part{
							{Type: PartTypeText, Text: "converted custom"},
							NewImagePart("custom-image", "image/png"),
						},
						Timestamp: message.Timestamp,
					})
					continue
				}
				result = append(result, cloneMessage(message))
			}
			return result, nil
		},
	}.Validate()
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}

	snapshot := &AgentSnapshot{
		Messages: []Message{
			NewUserTextMessage("look", NewImagePart("user-image", "image/png")),
			{
				Role:      RoleAssistant,
				Parts:     []Part{{Type: PartTypeText, Text: "I should inspect this."}, {Type: PartTypeThinking, Text: "Need a tool.", Signature: "sig_hist"}},
				ToolCalls: []ToolCall{{ID: "tool_norm_1", OriginalID: "tool_raw_1", Name: "inspect", Arguments: []byte(`{"target":"chart"}`)}},
				Timestamp: time.Now().UTC(),
			},
			{
				Role: RoleTool,
				ToolResult: &ToolResultPayload{
					ToolCallID:         "tool_norm_1",
					OriginalToolCallID: "tool_raw_1",
					ToolName:           "inspect",
					Content: []Part{
						{Type: PartTypeText, Text: "Detected a line chart."},
						NewImagePart("tool-image", "image/png"),
					},
				},
				Timestamp: time.Now().UTC(),
			},
			{
				Role:      RoleCustom,
				Kind:      "attachment",
				Parts:     []Part{{Type: PartTypeText, Text: "custom attachment"}},
				Timestamp: time.Now().UTC(),
			},
		},
	}

	next, err := engine.Continue(context.Background(), definition, snapshot, nil)
	if err != nil {
		t.Fatalf("Continue returned error: %v", err)
	}

	if len(converted) != 4 {
		t.Fatalf("expected transform/convert to receive all mixed messages, got %d", len(converted))
	}
	if len(converted[0].Parts) != 2 || converted[0].Parts[1].Type != PartTypeImage {
		t.Fatalf("expected user image content to reach convertToLLM, got %+v", converted[0])
	}
	if len(converted[1].Parts) != 2 || converted[1].Parts[1].Signature != "sig_hist" || len(converted[1].ToolCalls) != 1 {
		t.Fatalf("expected assistant thinking/tool call to reach convertToLLM, got %+v", converted[1])
	}
	if converted[2].ToolResult == nil || len(converted[2].ToolResult.Content) != 2 || converted[2].ToolResult.Content[1].Type != PartTypeImage {
		t.Fatalf("expected tool result image content to reach convertToLLM, got %+v", converted[2])
	}
	if len(received) != 4 {
		t.Fatalf("expected converted messages to reach model request, got %d", len(received))
	}
	if received[3].Role != RoleUser || len(received[3].Parts) != 2 || received[3].Parts[1].Type != PartTypeImage {
		t.Fatalf("expected custom message to map into user+image content, got %+v", received[3])
	}
	if len(next.Messages) != 5 || next.Messages[4].Role != RoleAssistant {
		t.Fatalf("expected continue to append assistant response, got %+v", next.Messages)
	}
}

func TestEngineEmitsAssistantMessageUpdateSequence(t *testing.T) {
	engine := NewEngine()

	definition, err := AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, _ ModelRequest) (AssistantStream, error) {
				return newStaticAssistantStream(Message{
					Role:       RoleAssistant,
					Parts:      []Part{{Type: PartTypeText, Text: "hello"}},
					Timestamp:  time.Now().UTC(),
					StopReason: StopReasonStop,
				}, []AssistantEvent{
					{
						Type:    AssistantEventStart,
						Message: Message{Role: RoleAssistant, Timestamp: time.Now().UTC()},
					},
					{
						Type: AssistantEventTextStart,
						Message: Message{
							Role:      RoleAssistant,
							Parts:     []Part{{Type: PartTypeText, Text: ""}},
							Timestamp: time.Now().UTC(),
						},
						ContentIndex: 0,
					},
					{
						Type: AssistantEventTextDelta,
						Message: Message{
							Role:      RoleAssistant,
							Parts:     []Part{{Type: PartTypeText, Text: "hel"}},
							Timestamp: time.Now().UTC(),
						},
						Delta:        "hel",
						ContentIndex: 0,
					},
					{
						Type: AssistantEventTextEnd,
						Message: Message{
							Role:      RoleAssistant,
							Parts:     []Part{{Type: PartTypeText, Text: "hello"}},
							Timestamp: time.Now().UTC(),
						},
						ContentIndex: 0,
					},
					{
						Type: AssistantEventDone,
						Message: Message{
							Role:       RoleAssistant,
							Parts:      []Part{{Type: PartTypeText, Text: "hello"}},
							Timestamp:  time.Now().UTC(),
							StopReason: StopReasonStop,
						},
						Reason: StopReasonStop,
					},
				}), nil
			},
		},
	}.Validate()
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}

	var (
		eventTypes      []EventType
		assistantEvents []AssistantEventType
		deltas          []string
	)
	_, err = engine.Run(context.Background(), definition, &AgentSnapshot{}, []Message{NewTextMessage(RoleUser, "hello")}, func(event AgentEvent) {
		eventTypes = append(eventTypes, event.Type)
		if event.AssistantEvent != nil {
			assistantEvents = append(assistantEvents, event.AssistantEvent.Type)
			deltas = append(deltas, event.Delta)
		}
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	wantEventTypes := []EventType{
		EventAgentStart,
		EventTurnStart,
		EventMessageStart,
		EventMessageEnd,
		EventMessageStart,
		EventMessageUpdate,
		EventMessageUpdate,
		EventMessageUpdate,
		EventMessageEnd,
		EventTurnEnd,
		EventAgentEnd,
	}
	if len(eventTypes) != len(wantEventTypes) {
		t.Fatalf("expected event types %v, got %v", wantEventTypes, eventTypes)
	}
	for i := range wantEventTypes {
		if eventTypes[i] != wantEventTypes[i] {
			t.Fatalf("expected event type %d to be %q, got %q", i, wantEventTypes[i], eventTypes[i])
		}
	}

	wantAssistantEvents := []AssistantEventType{
		AssistantEventTextStart,
		AssistantEventTextDelta,
		AssistantEventTextEnd,
	}
	if len(assistantEvents) != len(wantAssistantEvents) {
		t.Fatalf("expected assistant events %v, got %v", wantAssistantEvents, assistantEvents)
	}
	for i := range wantAssistantEvents {
		if assistantEvents[i] != wantAssistantEvents[i] {
			t.Fatalf("expected assistant event %d to be %q, got %q", i, wantAssistantEvents[i], assistantEvents[i])
		}
	}
	if deltas[0] != "" || deltas[1] != "hel" || deltas[2] != "" {
		t.Fatalf("unexpected deltas %v", deltas)
	}
}

func TestEngineToolExecutionUpdateAndTurnEndToolMessages(t *testing.T) {
	engine := NewEngine()

	callIndex := 0
	definition, err := AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, _ ModelRequest) (AssistantStream, error) {
				if callIndex == 0 {
					callIndex++
					return newStaticAssistantStream(Message{
						Role: RoleAssistant,
						ToolCalls: []ToolCall{
							{ID: "tool-1", Name: "echo", Arguments: []byte(`{"value":"hello"}`)},
						},
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
			Name: "echo",
			Execute: func(_ context.Context, _ string, _ any, update ToolUpdateFunc) (ToolResult, error) {
				update(ToolResult{
					Content: []Part{{Type: PartTypeText, Text: "partial"}},
					Details: "partial",
				})
				return ToolResult{
					Content: []Part{{Type: PartTypeText, Text: "final"}},
					Details: "final",
				}, nil
			},
		}},
	}.Validate()
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}

	var (
		updateEvent *AgentEvent
		turnEnds    []AgentEvent
	)
	_, err = engine.Run(context.Background(), definition, &AgentSnapshot{}, []Message{NewTextMessage(RoleUser, "start")}, func(event AgentEvent) {
		switch event.Type {
		case EventToolExecutionUpdate:
			current := event
			updateEvent = &current
		case EventTurnEnd:
			turnEnds = append(turnEnds, event)
		}
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if updateEvent == nil {
		t.Fatal("expected tool_execution_update event")
	}
	if updateEvent.ToolCallID != "tool-1" {
		t.Fatalf("expected tool call id %q, got %q", "tool-1", updateEvent.ToolCallID)
	}
	if updateEvent.PartialToolResult == nil || len(updateEvent.PartialToolResult.Content) != 1 || updateEvent.PartialToolResult.Content[0].Text != "partial" {
		t.Fatalf("unexpected partial tool result %+v", updateEvent.PartialToolResult)
	}
	if updateEvent.ToolResult == nil || len(updateEvent.ToolResult.Content) != 1 || updateEvent.ToolResult.Content[0].Text != "partial" {
		t.Fatalf("unexpected tool result payload on update %+v", updateEvent.ToolResult)
	}

	if len(turnEnds) != 2 {
		t.Fatalf("expected 2 turn_end events, got %d", len(turnEnds))
	}
	firstTurn := turnEnds[0]
	if len(firstTurn.ToolMessages) != 1 {
		t.Fatalf("expected first turn to include 1 tool message, got %d", len(firstTurn.ToolMessages))
	}
	if firstTurn.ToolMessages[0].Role != RoleTool || firstTurn.ToolMessages[0].ToolResult == nil || firstTurn.ToolMessages[0].ToolResult.Content[0].Text != "final" {
		t.Fatalf("unexpected tool message %+v", firstTurn.ToolMessages[0])
	}
	secondTurn := turnEnds[1]
	if len(secondTurn.ToolMessages) != 0 {
		t.Fatalf("expected second turn to have no tool messages, got %+v", secondTurn.ToolMessages)
	}
}
