package piagentgo

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// LoopHooks customize runtime behavior between turns.
type LoopHooks struct {
	ResolveDefinition   func(context.Context, AgentDefinition, AgentSnapshot) (AgentDefinition, error)
	GetSteeringMessages func(context.Context) ([]Message, error)
	GetFollowUpMessages func(context.Context) ([]Message, error)
}

// Engine executes agent turns against a mutable snapshot.
type Engine struct{}

// NewEngine creates a new stateless runtime engine.
func NewEngine() *Engine {
	return &Engine{}
}

// Run appends prompts and executes turns until the agent stops.
func (e *Engine) Run(ctx context.Context, definition AgentDefinition, snapshot *AgentSnapshot, prompts []Message, emit EventSink) (out *AgentSnapshot, err error) {
	return e.RunWithHooks(ctx, definition, snapshot, prompts, emit, LoopHooks{})
}

// RunWithHooks appends prompts and executes turns with runtime hooks.
func (e *Engine) RunWithHooks(ctx context.Context, definition AgentDefinition, snapshot *AgentSnapshot, prompts []Message, emit EventSink, hooks LoopHooks) (out *AgentSnapshot, err error) {
	if len(prompts) == 0 {
		return nil, ErrNoPromptMessages
	}

	definition, err = definition.Validate()
	if err != nil {
		return nil, err
	}

	next := cloneSnapshotPtr(snapshot)
	startLen := len(next.Messages)
	emitEvent(emit, AgentEvent{Type: EventAgentStart})
	emitEvent(emit, AgentEvent{Type: EventTurnStart})
	normalized := normalizeMessages(prompts)
	next.Messages = append(next.Messages, normalized...)
	for i := range normalized {
		msg := cloneMessage(normalized[i])
		emitEvent(emit, AgentEvent{Type: EventMessageStart, Message: &msg})
		emitEvent(emit, AgentEvent{Type: EventMessageEnd, Message: &msg})
	}

	next, err = e.runLoop(ctx, definition, next, emit, hooks, nil)
	if err != nil {
		return next, err
	}
	emitEvent(emit, AgentEvent{Type: EventAgentEnd, Messages: cloneMessages(next.Messages[startLen:])})
	return next, nil
}

// Continue executes turns from the existing snapshot without appending prompts.
func (e *Engine) Continue(ctx context.Context, definition AgentDefinition, snapshot *AgentSnapshot, emit EventSink) (out *AgentSnapshot, err error) {
	return e.ContinueWithHooks(ctx, definition, snapshot, emit, LoopHooks{})
}

// ContinueWithHooks executes turns from the existing snapshot with runtime hooks.
func (e *Engine) ContinueWithHooks(ctx context.Context, definition AgentDefinition, snapshot *AgentSnapshot, emit EventSink, hooks LoopHooks) (out *AgentSnapshot, err error) {
	definition, err = definition.Validate()
	if err != nil {
		return nil, err
	}

	next := cloneSnapshotPtr(snapshot)
	if len(next.Messages) == 0 {
		return nil, ErrNoMessagesToContinue
	}
	if tail := next.Messages[len(next.Messages)-1]; tail.Role == RoleAssistant {
		return nil, ErrCannotContinueFromAssistant
	}

	startLen := len(next.Messages)
	emitEvent(emit, AgentEvent{Type: EventAgentStart})
	emitEvent(emit, AgentEvent{Type: EventTurnStart})

	initialPending, err := dequeueHookMessages(ctx, hooks.GetSteeringMessages)
	if err != nil {
		next.Error = err.Error()
		return next, err
	}

	next, err = e.runLoop(ctx, definition, next, emit, hooks, initialPending)
	if err != nil {
		return next, err
	}
	emitEvent(emit, AgentEvent{Type: EventAgentEnd, Messages: cloneMessages(next.Messages[startLen:])})
	return next, nil
}

func (e *Engine) runLoop(ctx context.Context, definition AgentDefinition, snapshot *AgentSnapshot, emit EventSink, hooks LoopHooks, pendingMessages []Message) (*AgentSnapshot, error) {
	firstTurn := true
	turn := 1

	for {
		hasMoreToolCalls := true

		for hasMoreToolCalls || len(pendingMessages) > 0 {
			if !firstTurn {
				emitEvent(emit, AgentEvent{Type: EventTurnStart})
			}
			firstTurn = false

			if definition.MaxTurns > 0 && turn > definition.MaxTurns {
				err := fmt.Errorf("%w: %d", ErrMaxTurnsExceeded, definition.MaxTurns)
				snapshot.Error = err.Error()
				return snapshot, err
			}

			appendMessagesWithEvents(snapshot, pendingMessages, emit)
			pendingMessages = nil

			resolvedDefinition, err := resolveLoopDefinition(ctx, hooks, definition, *snapshot)
			if err != nil {
				snapshot.Error = err.Error()
				return snapshot, err
			}

			assistantMessage, err := e.generateAssistant(ctx, resolvedDefinition, snapshot, emit)
			if err != nil {
				snapshot.Error = err.Error()
				return snapshot, err
			}

			toolMessages, err := e.executeToolCalls(ctx, resolvedDefinition, snapshot, assistantMessage, emit)
			emitEvent(emit, AgentEvent{
				Type:         EventTurnEnd,
				Message:      &assistantMessage,
				ToolMessages: cloneMessages(toolMessages),
			})
			if err != nil {
				snapshot.Error = err.Error()
				return snapshot, err
			}
			if assistantMessage.StopReason == StopReasonError || assistantMessage.StopReason == StopReasonAborted {
				snapshot.Error = assistantMessage.ErrorMessage
				return snapshot, nil
			}

			hasMoreToolCalls = len(assistantMessage.ToolCalls) > 0
			pendingMessages, err = dequeueHookMessages(ctx, hooks.GetSteeringMessages)
			if err != nil {
				snapshot.Error = err.Error()
				return snapshot, err
			}
			turn++
		}

		followUpMessages, err := dequeueHookMessages(ctx, hooks.GetFollowUpMessages)
		if err != nil {
			snapshot.Error = err.Error()
			return snapshot, err
		}
		if len(followUpMessages) > 0 {
			pendingMessages = followUpMessages
			continue
		}

		snapshot.Error = ""
		return snapshot, nil
	}
}

func (e *Engine) generateAssistant(ctx context.Context, definition AgentDefinition, snapshot *AgentSnapshot, emit EventSink) (Message, error) {
	modelMessages, err := definition.TransformContext(ctx, snapshot.Messages)
	if err != nil {
		return Message{}, err
	}
	modelMessages, err = definition.ConvertToLLM(ctx, modelMessages)
	if err != nil {
		return Message{}, err
	}

	model, modelRef, err := definition.ResolveModel(ctx, *snapshot)
	if err != nil {
		return Message{}, err
	}
	tools, err := definition.ResolveTools(ctx, *snapshot)
	if err != nil {
		return Message{}, err
	}

	systemPrompt := snapshot.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = definition.SystemPrompt
	}

	stream, err := model.Stream(ctx, ModelRequest{
		Model:           modelRef,
		SystemPrompt:    systemPrompt,
		Messages:        modelMessages,
		Tools:           tools,
		ThinkingLevel:   definition.ThinkingLevel,
		SessionID:       snapshot.SessionID,
		Transport:       definition.Transport,
		MaxRetryDelayMs: definition.MaxRetryDelayMs,
		ThinkingBudgets: cloneThinkingBudgets(definition.ThinkingBudgets),
	})
	if err != nil {
		return Message{}, err
	}

	started := false
	for event := range stream.Events() {
		partial := cloneMessage(event.Message)
		if partial.Role == "" {
			partial.Role = RoleAssistant
		}
		if partial.Timestamp.IsZero() {
			partial.Timestamp = time.Now().UTC()
		}

		if event.Type == AssistantEventStart {
			if !started {
				started = true
				emitEvent(emit, AgentEvent{Type: EventMessageStart, Message: &partial})
			}
			continue
		}
		if event.Type == AssistantEventDone || event.Type == AssistantEventError {
			continue
		}
		if !started {
			started = true
			emitEvent(emit, AgentEvent{Type: EventMessageStart, Message: &partial})
		}
		assistantEvent := event
		emitEvent(emit, AgentEvent{
			Type:           EventMessageUpdate,
			Message:        &partial,
			Delta:          event.Delta,
			AssistantEvent: &assistantEvent,
			ToolCall:       cloneToolCallPtr(event.ToolCall),
			Err:            event.Err,
		})
	}

	finalMessage, err := stream.Wait()
	if err != nil && !isErrorAssistantMessage(finalMessage) {
		return Message{}, err
	}
	finalMessage = cloneMessage(finalMessage)
	if finalMessage.Role == "" {
		finalMessage.Role = RoleAssistant
	}
	if finalMessage.Timestamp.IsZero() {
		finalMessage.Timestamp = time.Now().UTC()
	}
	snapshot.Messages = append(snapshot.Messages, finalMessage)

	if !started {
		emitEvent(emit, AgentEvent{Type: EventMessageStart, Message: &finalMessage})
	}
	emitEvent(emit, AgentEvent{Type: EventMessageEnd, Message: &finalMessage})
	return finalMessage, nil
}

func (e *Engine) executeToolCalls(ctx context.Context, definition AgentDefinition, snapshot *AgentSnapshot, assistant Message, emit EventSink) ([]Message, error) {
	if len(assistant.ToolCalls) == 0 {
		return nil, nil
	}

	tools, err := definition.ResolveTools(ctx, *snapshot)
	if err != nil {
		return nil, err
	}
	toolMap := make(map[string]ToolDefinition, len(tools))
	for _, tool := range tools {
		toolMap[tool.Name] = tool
	}
	currentContext := buildAgentContext(definition, *snapshot, tools)

	prepared := make([]preparedToolCall, len(assistant.ToolCalls))
	snapshot.PendingToolCalls = make([]PendingToolCall, 0, len(assistant.ToolCalls))

	for i, call := range assistant.ToolCalls {
		call = cloneToolCall(call)
		snapshot.PendingToolCalls = append(snapshot.PendingToolCalls, PendingToolCall{
			ToolCallID:         call.ID,
			OriginalToolCallID: call.OriginalID,
			ToolName:           call.Name,
		})

		tool, ok := toolMap[call.Name]
		if !ok {
			emitEvent(emit, AgentEvent{
				Type:               EventToolExecutionStart,
				ToolCall:           &call,
				ToolCallID:         call.ID,
				OriginalToolCallID: call.OriginalID,
				ToolName:           call.Name,
			})
			prepared[i] = preparedToolCall{
				call: call,
				outcome: toolOutcome{
					call:    call,
					result:  errorToolResult(fmt.Sprintf("tool %q not found", call.Name)),
					isError: true,
				},
				immediate: true,
			}
			continue
		}

		args, err := parseToolArguments(tool, call)
		emitEvent(emit, AgentEvent{
			Type:               EventToolExecutionStart,
			ToolCall:           &call,
			ToolCallID:         call.ID,
			OriginalToolCallID: call.OriginalID,
			ToolName:           call.Name,
			Args:               cloneAny(args),
		})
		if err != nil {
			prepared[i] = preparedToolCall{
				call: call,
				outcome: toolOutcome{
					call:    call,
					args:    cloneAny(args),
					result:  errorToolResult(err.Error()),
					isError: true,
				},
				immediate: true,
			}
			continue
		}

		if definition.BeforeToolCall != nil {
			beforeResult, err := definition.BeforeToolCall(ctx, BeforeToolCallContext{
				AssistantMessage: cloneMessage(assistant),
				ToolCall:         call,
				Args:             cloneAny(args),
				Context:          cloneAgentContext(currentContext),
			})
			if err != nil {
				return nil, err
			}
			if beforeResult.Block {
				reason := beforeResult.Reason
				if reason == "" {
					reason = "tool execution was blocked"
				}
				prepared[i] = preparedToolCall{
					call: call,
					outcome: toolOutcome{
						call:    call,
						args:    cloneAny(args),
						result:  errorToolResult(reason),
						isError: true,
					},
					immediate: true,
				}
				continue
			}
		}

		prepared[i] = preparedToolCall{
			call:    call,
			tool:    tool,
			args:    cloneAny(args),
			context: cloneAgentContext(currentContext),
		}
	}

	outcomes := make([]toolOutcome, len(prepared))
	switch definition.ToolExecution {
	case ToolExecutionSequential:
		for i, item := range prepared {
			outcome, err := e.executePreparedTool(ctx, definition, assistant, item, emit)
			if err != nil {
				return nil, err
			}
			outcomes[i] = outcome
		}
	default:
		var (
			wg       sync.WaitGroup
			firstErr error
			errMu    sync.Mutex
		)
		for i, item := range prepared {
			if item.immediate {
				outcomes[i] = item.outcome
				emitEvent(emit, AgentEvent{
					Type:               EventToolExecutionEnd,
					ToolCall:           &item.outcome.call,
					ToolCallID:         item.outcome.call.ID,
					OriginalToolCallID: item.outcome.call.OriginalID,
					ToolName:           item.outcome.call.Name,
					Args:               cloneAny(item.outcome.args),
					ToolResult:         &item.outcome.result,
					IsError:            item.outcome.isError,
				})
				continue
			}

			wg.Add(1)
			go func(index int, current preparedToolCall) {
				defer wg.Done()
				outcome, err := e.executePreparedTool(ctx, definition, assistant, current, emit)
				if err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					errMu.Unlock()
					return
				}
				outcomes[index] = outcome
			}(i, item)
		}
		wg.Wait()
		if firstErr != nil {
			return nil, firstErr
		}
	}

	snapshot.PendingToolCalls = nil
	toolMessages := make([]Message, 0, len(outcomes))
	for _, outcome := range outcomes {
		toolMessage := NewToolResultMessage(outcome.call, outcome.result, outcome.isError)
		snapshot.Messages = append(snapshot.Messages, toolMessage)
		toolMessages = append(toolMessages, toolMessage)
		msg := cloneMessage(toolMessage)
		emitEvent(emit, AgentEvent{Type: EventMessageStart, Message: &msg})
		emitEvent(emit, AgentEvent{Type: EventMessageEnd, Message: &msg})
	}
	return toolMessages, nil
}

func (e *Engine) executePreparedTool(ctx context.Context, definition AgentDefinition, assistant Message, prepared preparedToolCall, emit EventSink) (toolOutcome, error) {
	if prepared.immediate {
		emitEvent(emit, AgentEvent{
			Type:               EventToolExecutionEnd,
			ToolCall:           &prepared.outcome.call,
			ToolCallID:         prepared.outcome.call.ID,
			OriginalToolCallID: prepared.outcome.call.OriginalID,
			ToolName:           prepared.outcome.call.Name,
			Args:               cloneAny(prepared.outcome.args),
			ToolResult:         &prepared.outcome.result,
			IsError:            prepared.outcome.isError,
		})
		return prepared.outcome, nil
	}

	result, execErr := prepared.tool.Execute(ctx, prepared.call.ID, cloneAny(prepared.args), func(partial ToolResult) {
		emitEvent(emit, AgentEvent{
			Type:               EventToolExecutionUpdate,
			ToolCall:           &prepared.call,
			ToolCallID:         prepared.call.ID,
			OriginalToolCallID: prepared.call.OriginalID,
			ToolName:           prepared.call.Name,
			Args:               cloneAny(prepared.args),
			ToolResult:         &partial,
			PartialToolResult:  &partial,
		})
	})

	outcome := toolOutcome{call: prepared.call, args: cloneAny(prepared.args)}
	if execErr != nil {
		outcome.result = errorToolResult(execErr.Error())
		outcome.isError = true
	} else {
		outcome.result = cloneToolResult(result)
	}

	if definition.AfterToolCall != nil {
		override, err := definition.AfterToolCall(ctx, AfterToolCallContext{
			AssistantMessage: cloneMessage(assistant),
			ToolCall:         cloneToolCall(prepared.call),
			Args:             cloneAny(prepared.args),
			Context:          cloneAgentContext(prepared.context),
			Result:           cloneToolResult(outcome.result),
			IsError:          outcome.isError,
		})
		if err != nil {
			return toolOutcome{}, err
		}
		if override.Result != nil {
			outcome.result = cloneToolResult(*override.Result)
		}
		if override.IsError != nil {
			outcome.isError = *override.IsError
		}
	}

	emitEvent(emit, AgentEvent{
		Type:               EventToolExecutionEnd,
		ToolCall:           &outcome.call,
		ToolCallID:         outcome.call.ID,
		OriginalToolCallID: outcome.call.OriginalID,
		ToolName:           outcome.call.Name,
		Args:               cloneAny(outcome.args),
		ToolResult:         &outcome.result,
		IsError:            outcome.isError,
	})
	return outcome, nil
}

type preparedToolCall struct {
	call      ToolCall
	tool      ToolDefinition
	args      any
	context   AgentContext
	outcome   toolOutcome
	immediate bool
}

type toolOutcome struct {
	call    ToolCall
	args    any
	result  ToolResult
	isError bool
}

func emitEvent(emit EventSink, event AgentEvent) {
	if emit == nil {
		return
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	emit(event)
}

func errorToolResult(message string) ToolResult {
	return ToolResult{
		Content: []Part{{Type: PartTypeText, Text: message}},
	}
}

func normalizeMessages(messages []Message) []Message {
	normalized := make([]Message, 0, len(messages))
	for _, message := range messages {
		msg := cloneMessage(message)
		if msg.Timestamp.IsZero() {
			msg.Timestamp = time.Now().UTC()
		}
		normalized = append(normalized, msg)
	}
	return normalized
}

func cloneSnapshotPtr(snapshot *AgentSnapshot) *AgentSnapshot {
	if snapshot == nil {
		return &AgentSnapshot{}
	}
	value := cloneSnapshotValue(*snapshot)
	return &value
}

func cloneSnapshotValue(snapshot AgentSnapshot) AgentSnapshot {
	return AgentSnapshot{
		SessionID:        snapshot.SessionID,
		SystemPrompt:     snapshot.SystemPrompt,
		Model:            cloneModelRef(snapshot.Model),
		Messages:         cloneMessages(snapshot.Messages),
		PendingToolCalls: clonePendingToolCalls(snapshot.PendingToolCalls),
		Error:            snapshot.Error,
		Metadata:         cloneStringAnyMap(snapshot.Metadata),
	}
}

func cloneMessages(messages []Message) []Message {
	cloned := make([]Message, 0, len(messages))
	for _, message := range messages {
		cloned = append(cloned, cloneMessage(message))
	}
	return cloned
}

func cloneMessage(message Message) Message {
	return Message{
		ID:           message.ID,
		Role:         message.Role,
		Kind:         message.Kind,
		Parts:        cloneParts(message.Parts),
		ToolCalls:    cloneToolCalls(message.ToolCalls),
		ToolResult:   cloneToolResultPayload(message.ToolResult),
		Timestamp:    message.Timestamp,
		API:          message.API,
		Provider:     message.Provider,
		Model:        message.Model,
		ResponseID:   message.ResponseID,
		Metadata:     cloneStringAnyMap(message.Metadata),
		Payload:      cloneStringAnyMap(message.Payload),
		StopReason:   message.StopReason,
		ErrorMessage: message.ErrorMessage,
	}
}

func cloneParts(parts []Part) []Part {
	cloned := make([]Part, len(parts))
	copy(cloned, parts)
	return cloned
}

func cloneToolCalls(calls []ToolCall) []ToolCall {
	cloned := make([]ToolCall, 0, len(calls))
	for _, call := range calls {
		cloned = append(cloned, cloneToolCall(call))
	}
	return cloned
}

func cloneToolCall(call ToolCall) ToolCall {
	var arguments []byte
	if len(call.Arguments) > 0 {
		arguments = append(arguments, call.Arguments...)
	}
	return ToolCall{
		ID:               call.ID,
		OriginalID:       call.OriginalID,
		Name:             call.Name,
		Arguments:        arguments,
		ParsedArgs:       cloneStringAnyMap(call.ParsedArgs),
		ThoughtSignature: call.ThoughtSignature,
	}
}

func cloneToolCallPtr(call *ToolCall) *ToolCall {
	if call == nil {
		return nil
	}
	cloned := cloneToolCall(*call)
	return &cloned
}

func cloneToolResult(result ToolResult) ToolResult {
	return ToolResult{
		Content: cloneParts(result.Content),
		Details: result.Details,
	}
}

func cloneToolResultPayload(payload *ToolResultPayload) *ToolResultPayload {
	if payload == nil {
		return nil
	}
	cloned := *payload
	cloned.Content = cloneParts(payload.Content)
	return &cloned
}

func clonePendingToolCalls(calls []PendingToolCall) []PendingToolCall {
	cloned := make([]PendingToolCall, len(calls))
	copy(cloned, calls)
	return cloned
}

func cloneStringAnyMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func cloneTools(tools []ToolDefinition) []ToolDefinition {
	cloned := make([]ToolDefinition, len(tools))
	copy(cloned, tools)
	return cloned
}

func cloneModelRef(ref ModelRef) ModelRef {
	return ModelRef{
		Provider:       ref.Provider,
		Model:          ref.Model,
		ProviderConfig: cloneProviderConfig(ref.ProviderConfig),
		Metadata:       cloneStringAnyMap(ref.Metadata),
	}
}

func cloneProviderConfig(config ProviderConfig) ProviderConfig {
	return ProviderConfig{
		BaseURL: config.BaseURL,
		APIKey:  config.APIKey,
		Headers: cloneStringMap(config.Headers),
		Auth:    cloneProviderAuthConfig(config.Auth),
	}
}

func cloneProviderAuthConfig(config *ProviderAuthConfig) *ProviderAuthConfig {
	if config == nil {
		return nil
	}

	cloned := *config
	cloned.OAuth = cloneOAuthCredentials(config.OAuth)
	return &cloned
}

func cloneOAuthCredentials(credentials *OAuthCredentials) *OAuthCredentials {
	if credentials == nil {
		return nil
	}

	cloned := *credentials
	return &cloned
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func cloneThinkingBudgets(budgets ThinkingBudgets) ThinkingBudgets {
	if len(budgets) == 0 {
		return nil
	}
	cloned := make(ThinkingBudgets, len(budgets))
	for level, budget := range budgets {
		cloned[level] = budget
	}
	return cloned
}

func resolveLoopDefinition(ctx context.Context, hooks LoopHooks, definition AgentDefinition, snapshot AgentSnapshot) (AgentDefinition, error) {
	if hooks.ResolveDefinition == nil {
		return definition, nil
	}

	resolved, err := hooks.ResolveDefinition(ctx, definition, cloneSnapshotValue(snapshot))
	if err != nil {
		return AgentDefinition{}, err
	}
	return resolved.Validate()
}

func dequeueHookMessages(ctx context.Context, getter func(context.Context) ([]Message, error)) ([]Message, error) {
	if getter == nil {
		return nil, nil
	}

	messages, err := getter(ctx)
	if err != nil {
		return nil, err
	}
	return normalizeMessages(messages), nil
}

func appendMessagesWithEvents(snapshot *AgentSnapshot, messages []Message, emit EventSink) {
	if len(messages) == 0 {
		return
	}

	normalized := normalizeMessages(messages)
	snapshot.Messages = append(snapshot.Messages, normalized...)
	for i := range normalized {
		msg := cloneMessage(normalized[i])
		emitEvent(emit, AgentEvent{Type: EventMessageStart, Message: &msg})
		emitEvent(emit, AgentEvent{Type: EventMessageEnd, Message: &msg})
	}
}

func buildAgentContext(definition AgentDefinition, snapshot AgentSnapshot, tools []ToolDefinition) AgentContext {
	systemPrompt := snapshot.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = definition.SystemPrompt
	}
	return AgentContext{
		SystemPrompt: systemPrompt,
		Messages:     cloneMessages(snapshot.Messages),
		Tools:        cloneTools(tools),
	}
}

func cloneAgentContext(context AgentContext) AgentContext {
	return AgentContext{
		SystemPrompt: context.SystemPrompt,
		Messages:     cloneMessages(context.Messages),
		Tools:        cloneTools(context.Tools),
	}
}

func parseToolArguments(tool ToolDefinition, call ToolCall) (any, error) {
	if tool.ParseArguments != nil {
		return tool.ParseArguments(cloneToolCall(call))
	}
	if call.ParsedArgs != nil {
		return cloneStringAnyMap(call.ParsedArgs), nil
	}
	if len(call.Arguments) == 0 {
		return map[string]any{}, nil
	}

	var parsed any
	if err := json.Unmarshal(call.Arguments, &parsed); err != nil {
		return nil, err
	}
	return cloneAny(parsed), nil
}

func isErrorAssistantMessage(message Message) bool {
	return message.StopReason == StopReasonError || message.StopReason == StopReasonAborted || message.ErrorMessage != ""
}

func cloneAny(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, item := range typed {
			cloned[key] = cloneAny(item)
		}
		return cloned
	case []any:
		cloned := make([]any, len(typed))
		for i, item := range typed {
			cloned[i] = cloneAny(item)
		}
		return cloned
	default:
		return typed
	}
}
