package piagentgo

import (
	"context"
	"sync"
	"time"
)

// AgentOption mutates agent construction behavior.
type AgentOption func(*Agent)

// WithEngine replaces the default runtime engine.
func WithEngine(engine *Engine) AgentOption {
	return func(agent *Agent) {
		agent.engine = engine
	}
}

// WithSnapshot sets the initial snapshot for a new agent.
func WithSnapshot(snapshot AgentSnapshot) AgentOption {
	return func(agent *Agent) {
		agent.snapshot = cloneSnapshotValue(snapshot)
	}
}

// WithDefinitionResolver resolves an agent definition per run from snapshot state.
func WithDefinitionResolver(resolver DefinitionResolver) AgentOption {
	return func(agent *Agent) {
		agent.resolver = resolver
	}
}

// Agent wraps an Engine with queues, listeners, and in-memory state.
type Agent struct {
	engine           *Engine
	definition       AgentDefinition
	activeDefinition AgentDefinition
	resolver         DefinitionResolver
	snapshot         AgentSnapshot
	state            AgentState
	listeners        map[int]func(AgentEvent)
	nextListenerID   int
	steeringQueue    []Message
	followUpQueue    []Message
	running          bool
	cancel           context.CancelFunc
	idle             chan struct{}
	mu               sync.RWMutex
}

// NewAgent creates a new low-level agent from a validated definition contract.
func NewAgent(definition AgentDefinition, opts ...AgentOption) (*Agent, error) {
	return newAgent(definition, AgentSnapshot{}, opts...)
}

// NewAgentFromDefinition creates a new agent from a validated definition contract.
func NewAgentFromDefinition(definition AgentDefinition, opts ...AgentOption) (*Agent, error) {
	return newAgent(definition, AgentSnapshot{}, opts...)
}

func newAgent(definition AgentDefinition, baseSnapshot AgentSnapshot, opts ...AgentOption) (*Agent, error) {
	definition, err := definition.Validate()
	if err != nil {
		return nil, err
	}

	snapshot := cloneSnapshotValue(baseSnapshot)
	if snapshot.SessionID == "" {
		snapshot.SessionID = definition.SessionID
	}
	if snapshot.SystemPrompt == "" {
		snapshot.SystemPrompt = definition.SystemPrompt
	}
	if snapshot.Model.Model == "" && snapshot.Model.Provider == "" && len(snapshot.Model.Metadata) == 0 {
		snapshot.Model = cloneModelRef(definition.DefaultModel)
	}

	agent := &Agent{
		engine:           NewEngine(),
		definition:       definition,
		activeDefinition: definition,
		snapshot:         snapshot,
		listeners:        make(map[int]func(AgentEvent)),
	}
	for _, opt := range opts {
		opt(agent)
	}
	if agent.engine == nil {
		agent.engine = NewEngine()
	}
	agent.refreshStateLocked()
	return agent, nil
}

// Prompt appends messages and executes the agent loop.
func (a *Agent) Prompt(ctx context.Context, messages ...Message) error {
	runCtx, definition, snapshot, idle, err := a.startRun(ctx)
	if err != nil {
		return err
	}
	a.setActiveDefinition(definition)

	next, runErr := a.engine.RunWithHooks(runCtx, definition, &snapshot, messages, a.processLoopEvent, a.runtimeHooks())
	if runErr != nil {
		errorSnapshot := snapshot
		if next != nil {
			errorSnapshot = cloneSnapshotValue(*next)
		}
		next = a.handleRuntimeError(runCtx, errorSnapshot, runErr)
		runErr = nil
	}
	a.finishRun(next, idle)
	return runErr
}

// PromptMessage appends one message and executes the agent loop.
func (a *Agent) PromptMessage(ctx context.Context, message Message) error {
	return a.Prompt(ctx, message)
}

// PromptText appends one user text message and executes the agent loop.
func (a *Agent) PromptText(ctx context.Context, text string) error {
	return a.Prompt(ctx, NewUserTextMessage(text))
}

// PromptTextWithImages appends one user text message with image parts and executes the agent loop.
func (a *Agent) PromptTextWithImages(ctx context.Context, text string, images ...Part) error {
	return a.Prompt(ctx, NewUserTextMessage(text, images...))
}

// Continue resumes the agent loop from the current snapshot.
func (a *Agent) Continue(ctx context.Context) error {
	runCtx, definition, snapshot, idle, err := a.startRun(ctx)
	if err != nil {
		return err
	}
	a.setActiveDefinition(definition)

	var (
		next   *AgentSnapshot
		runErr error
	)

	if len(snapshot.Messages) == 0 {
		runErr = ErrNoMessagesToContinue
		a.finishRun(next, idle)
		return runErr
	}

	tail := snapshot.Messages[len(snapshot.Messages)-1]
	switch tail.Role {
	case RoleAssistant:
		if queued := a.dequeueSteering(); len(queued) > 0 {
			next, runErr = a.engine.RunWithHooks(runCtx, definition, &snapshot, queued, a.processLoopEvent, a.runtimeHooks())
		} else if queued := a.dequeueFollowUp(); len(queued) > 0 {
			next, runErr = a.engine.RunWithHooks(runCtx, definition, &snapshot, queued, a.processLoopEvent, a.runtimeHooks())
		} else {
			runErr = ErrCannotContinueFromAssistant
			a.finishRun(next, idle)
			return runErr
		}
	default:
		next, runErr = a.engine.ContinueWithHooks(runCtx, definition, &snapshot, a.processLoopEvent, a.runtimeHooks())
	}

	if runErr != nil {
		errorSnapshot := snapshot
		if next != nil {
			errorSnapshot = cloneSnapshotValue(*next)
		}
		next = a.handleRuntimeError(runCtx, errorSnapshot, runErr)
		runErr = nil
	}
	a.finishRun(next, idle)
	return runErr
}

// Steer queues a message to be injected before the next assistant turn.
func (a *Agent) Steer(message Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steeringQueue = append(a.steeringQueue, normalizeMessages([]Message{message})...)
}

// FollowUp queues a message to be injected after the current run would otherwise stop.
func (a *Agent) FollowUp(message Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.followUpQueue = append(a.followUpQueue, normalizeMessages([]Message{message})...)
}

// ClearSteeringQueue clears all queued steering messages.
func (a *Agent) ClearSteeringQueue() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steeringQueue = nil
}

// ClearFollowUpQueue clears all queued follow-up messages.
func (a *Agent) ClearFollowUpQueue() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.followUpQueue = nil
}

// ClearAllQueues clears both steering and follow-up queues.
func (a *Agent) ClearAllQueues() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steeringQueue = nil
	a.followUpQueue = nil
}

// HasQueuedMessages reports whether any steering or follow-up messages are queued.
func (a *Agent) HasQueuedMessages() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.steeringQueue) > 0 || len(a.followUpQueue) > 0
}

// Abort cancels the active run if one exists.
func (a *Agent) Abort() {
	a.mu.RLock()
	cancel := a.cancel
	a.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
}

// WaitForIdle blocks until the active run completes or the context is cancelled.
func (a *Agent) WaitForIdle(ctx context.Context) error {
	a.mu.RLock()
	idle := a.idle
	a.mu.RUnlock()
	if idle == nil {
		return nil
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-idle:
		return nil
	}
}

// Subscribe registers an event listener and returns an unsubscribe function.
func (a *Agent) Subscribe(fn func(AgentEvent)) func() {
	a.mu.Lock()
	defer a.mu.Unlock()

	id := a.nextListenerID
	a.nextListenerID++
	a.listeners[id] = fn
	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		delete(a.listeners, id)
	}
}

// Snapshot returns a cloned copy of the current agent snapshot.
func (a *Agent) Snapshot() AgentSnapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return cloneSnapshotValue(a.snapshot)
}

// State returns a cloned copy of the current in-memory runtime state.
func (a *Agent) State() AgentState {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return cloneAgentState(a.state)
}

// ReplaceMessages replaces the current message history.
func (a *Agent) ReplaceMessages(messages []Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	normalized := normalizeMessages(messages)
	a.snapshot.Messages = normalized
	a.state.Messages = cloneMessages(normalized)
	a.state.StreamMessage = nil
}

// AppendMessage appends one message to the current history.
func (a *Agent) AppendMessage(message Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.appendMessageLocked(message)
}

// SetSystemPrompt updates the effective system prompt in state and snapshot.
func (a *Agent) SetSystemPrompt(prompt string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.snapshot.SystemPrompt = prompt
	a.state.SystemPrompt = prompt
}

// SetModel updates the effective model reference in state and snapshot.
func (a *Agent) SetModel(model ModelRef) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cloned := cloneModelRef(model)
	a.snapshot.Model = cloned
	a.state.Model = cloned
}

// SetThinkingLevel updates the effective thinking level.
func (a *Agent) SetThinkingLevel(level ThinkingLevel) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.definition.ThinkingLevel = level
	a.activeDefinition.ThinkingLevel = level
	a.state.ThinkingLevel = level
}

// SetSteeringMode updates how steering messages are dequeued.
func (a *Agent) SetSteeringMode(mode QueueMode) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.definition.SteeringMode = mode
	a.activeDefinition.SteeringMode = mode
}

// SteeringMode returns the current steering dequeue mode.
func (a *Agent) SteeringMode() QueueMode {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.definition.SteeringMode
}

// SetFollowUpMode updates how follow-up messages are dequeued.
func (a *Agent) SetFollowUpMode(mode QueueMode) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.definition.FollowUpMode = mode
	a.activeDefinition.FollowUpMode = mode
}

// FollowUpMode returns the current follow-up dequeue mode.
func (a *Agent) FollowUpMode() QueueMode {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.definition.FollowUpMode
}

// SetTools updates the available tools for future turns.
func (a *Agent) SetTools(tools []ToolDefinition) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.definition.Tools = cloneTools(tools)
	a.activeDefinition.Tools = cloneTools(tools)
	a.state.Tools = cloneTools(tools)
}

// SetSessionID updates the session identifier used by future model requests.
func (a *Agent) SetSessionID(sessionID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.snapshot.SessionID = sessionID
	a.definition.SessionID = sessionID
	a.activeDefinition.SessionID = sessionID
	a.state.SessionID = sessionID
}

// SetTransport updates the preferred model transport.
func (a *Agent) SetTransport(transport Transport) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.definition.Transport = transport
	a.activeDefinition.Transport = transport
	a.state.Transport = transport
}

// SetMaxRetryDelayMs updates the maximum accepted model retry delay.
func (a *Agent) SetMaxRetryDelayMs(maxRetryDelayMs int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.definition.MaxRetryDelayMs = maxRetryDelayMs
	a.activeDefinition.MaxRetryDelayMs = maxRetryDelayMs
	a.state.MaxRetryDelayMs = maxRetryDelayMs
}

// SetThinkingBudgets updates optional thinking token budgets.
func (a *Agent) SetThinkingBudgets(budgets ThinkingBudgets) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cloned := cloneThinkingBudgets(budgets)
	a.definition.ThinkingBudgets = cloned
	a.activeDefinition.ThinkingBudgets = cloneThinkingBudgets(cloned)
	a.state.ThinkingBudgets = cloneThinkingBudgets(cloned)
}

// SetBeforeToolCall updates the before-tool hook.
func (a *Agent) SetBeforeToolCall(hook BeforeToolCallHook) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.definition.BeforeToolCall = hook
	a.activeDefinition.BeforeToolCall = hook
}

// SetAfterToolCall updates the after-tool hook.
func (a *Agent) SetAfterToolCall(hook AfterToolCallHook) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.definition.AfterToolCall = hook
	a.activeDefinition.AfterToolCall = hook
}

// ClearMessages removes all conversation history.
func (a *Agent) ClearMessages() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.snapshot.Messages = nil
	a.state.Messages = nil
	a.state.StreamMessage = nil
	a.snapshot.Error = ""
	a.state.Error = ""
}

// Reset clears runtime conversation state while preserving configuration.
func (a *Agent) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.snapshot.Messages = nil
	a.snapshot.PendingToolCalls = nil
	a.snapshot.Error = ""
	a.state.Messages = nil
	a.state.StreamMessage = nil
	a.state.PendingToolCalls = make(map[string]PendingToolCall)
	a.state.Error = ""
	a.state.IsStreaming = false
	a.steeringQueue = nil
	a.followUpQueue = nil
}

func (a *Agent) startRun(ctx context.Context) (context.Context, AgentDefinition, AgentSnapshot, chan struct{}, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		return nil, AgentDefinition{}, AgentSnapshot{}, nil, ErrAlreadyRunning
	}

	runCtx, cancel := context.WithCancel(ctx)
	idle := make(chan struct{})
	a.running = true
	a.cancel = cancel
	a.idle = idle
	a.activeDefinition = a.definition
	a.state.IsStreaming = true
	a.state.StreamMessage = nil
	a.state.Error = ""
	a.snapshot.Error = ""
	a.state.PendingToolCalls = make(map[string]PendingToolCall)

	return runCtx, a.definition, cloneSnapshotValue(a.snapshot), idle, nil
}

func (a *Agent) resolveDefinition(ctx context.Context, definition AgentDefinition, snapshot AgentSnapshot) (AgentDefinition, error) {
	a.mu.RLock()
	resolver := a.resolver
	a.mu.RUnlock()
	if resolver == nil {
		return definition, nil
	}

	resolved, err := resolver(ctx, cloneSnapshotValue(snapshot))
	if err != nil {
		return AgentDefinition{}, err
	}
	return resolved.Validate()
}

func (a *Agent) finishRun(snapshot *AgentSnapshot, idle chan struct{}) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if snapshot != nil {
		a.snapshot = cloneSnapshotValue(*snapshot)
	}
	a.refreshStateLocked()
	a.state.IsStreaming = false
	a.state.StreamMessage = nil
	a.state.PendingToolCalls = make(map[string]PendingToolCall)
	a.running = false
	a.cancel = nil
	a.activeDefinition = a.definition
	if a.idle == idle {
		close(a.idle)
		a.idle = nil
	}
}

func (a *Agent) handleRuntimeError(ctx context.Context, snapshot AgentSnapshot, runErr error) *AgentSnapshot {
	stopReason := StopReasonError
	if ctx.Err() == context.Canceled {
		stopReason = StopReasonAborted
	}

	errorMessage := Message{
		Role:         RoleAssistant,
		Parts:        []Part{{Type: PartTypeText, Text: ""}},
		Timestamp:    time.Now().UTC(),
		StopReason:   stopReason,
		ErrorMessage: runErr.Error(),
	}

	next := cloneSnapshotPtr(&snapshot)
	next.PendingToolCalls = nil
	next.Error = runErr.Error()
	next.Messages = append(next.Messages, errorMessage)

	a.mu.Lock()
	a.snapshot = cloneSnapshotValue(*next)
	a.refreshStateLocked()
	a.state.IsStreaming = false
	a.state.StreamMessage = nil
	a.state.PendingToolCalls = make(map[string]PendingToolCall)
	a.mu.Unlock()

	a.dispatchEvent(AgentEvent{
		Type:     EventAgentEnd,
		Messages: []Message{cloneMessage(errorMessage)},
	})

	return next
}

func (a *Agent) processLoopEvent(event AgentEvent) {
	a.mu.Lock()
	switch event.Type {
	case EventAgentStart:
		a.state.IsStreaming = true
		a.state.StreamMessage = nil
		a.state.Error = ""
		a.snapshot.Error = ""
	case EventMessageStart:
		if event.Message != nil {
			message := cloneMessage(*event.Message)
			a.state.StreamMessage = &message
		}
	case EventMessageUpdate:
		if event.Message != nil {
			message := cloneMessage(*event.Message)
			a.state.StreamMessage = &message
		}
	case EventMessageEnd:
		a.state.StreamMessage = nil
		if event.Message != nil {
			a.appendMessageLocked(*event.Message)
		}
	case EventToolExecutionStart:
		if a.state.PendingToolCalls == nil {
			a.state.PendingToolCalls = make(map[string]PendingToolCall)
		}
		toolCallID := event.ToolCallID
		toolName := event.ToolName
		if event.ToolCall != nil {
			if toolCallID == "" {
				toolCallID = event.ToolCall.ID
			}
			if toolName == "" {
				toolName = event.ToolCall.Name
			}
		}
		if toolCallID != "" {
			a.state.PendingToolCalls[toolCallID] = PendingToolCall{
				ToolCallID: toolCallID,
				ToolName:   toolName,
			}
		}
	case EventToolExecutionEnd:
		delete(a.state.PendingToolCalls, event.ToolCallID)
	case EventTurnEnd:
		if event.Message != nil && isErrorAssistantMessage(*event.Message) {
			a.snapshot.Error = event.Message.ErrorMessage
			a.state.Error = event.Message.ErrorMessage
		}
	case EventAgentEnd:
		a.state.IsStreaming = false
		a.state.StreamMessage = nil
		a.state.PendingToolCalls = make(map[string]PendingToolCall)
	}
	a.mu.Unlock()

	a.dispatchEvent(event)
}

func (a *Agent) dispatchEvent(event AgentEvent) {
	a.mu.RLock()
	listeners := make([]func(AgentEvent), 0, len(a.listeners))
	for _, listener := range a.listeners {
		listeners = append(listeners, listener)
	}
	a.mu.RUnlock()

	for _, listener := range listeners {
		listener(event)
	}
}

func (a *Agent) appendMessageLocked(message Message) {
	normalized := normalizeMessages([]Message{message})
	if len(normalized) == 0 {
		return
	}
	a.snapshot.Messages = append(a.snapshot.Messages, normalized[0])
	a.state.Messages = append(a.state.Messages, cloneMessage(normalized[0]))
}

func (a *Agent) refreshStateLocked() {
	a.state = AgentState{
		SystemPrompt:     effectiveSystemPrompt(a.snapshot, a.definition),
		Model:            effectiveModelRef(a.snapshot, a.definition),
		ThinkingLevel:    a.definition.ThinkingLevel,
		Tools:            cloneTools(a.definition.Tools),
		Messages:         cloneMessages(a.snapshot.Messages),
		IsStreaming:      a.state.IsStreaming,
		StreamMessage:    cloneMessagePtr(a.state.StreamMessage),
		PendingToolCalls: clonePendingToolCallMap(a.state.PendingToolCalls),
		Error:            a.snapshot.Error,
		SessionID:        a.snapshot.SessionID,
		Transport:        a.definition.Transport,
		MaxRetryDelayMs:  a.definition.MaxRetryDelayMs,
		ThinkingBudgets:  cloneThinkingBudgets(a.definition.ThinkingBudgets),
		Metadata:         cloneStringAnyMap(a.snapshot.Metadata),
	}
}

func (a *Agent) dequeueSteering() []Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	return dequeueByMode(&a.steeringQueue, a.activeDefinition.SteeringMode)
}

func (a *Agent) dequeueFollowUp() []Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	return dequeueByMode(&a.followUpQueue, a.activeDefinition.FollowUpMode)
}

func (a *Agent) runtimeHooks() LoopHooks {
	return LoopHooks{
		ResolveDefinition: func(ctx context.Context, current AgentDefinition, snapshot AgentSnapshot) (AgentDefinition, error) {
			resolved, err := a.resolveDefinition(ctx, current, snapshot)
			if err != nil {
				return AgentDefinition{}, err
			}
			a.setActiveDefinition(resolved)
			return resolved, nil
		},
		GetSteeringMessages: func(context.Context) ([]Message, error) {
			return a.dequeueSteering(), nil
		},
		GetFollowUpMessages: func(context.Context) ([]Message, error) {
			return a.dequeueFollowUp(), nil
		},
	}
}

func (a *Agent) setActiveDefinition(definition AgentDefinition) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.activeDefinition = definition
}

func dequeueByMode(queue *[]Message, mode QueueMode) []Message {
	if len(*queue) == 0 {
		return nil
	}
	if mode == QueueModeAll {
		items := cloneMessages(*queue)
		*queue = nil
		return items
	}

	item := cloneMessage((*queue)[0])
	*queue = append([]Message(nil), (*queue)[1:]...)
	return []Message{item}
}

func cloneAgentState(state AgentState) AgentState {
	return AgentState{
		SystemPrompt:     state.SystemPrompt,
		Model:            cloneModelRef(state.Model),
		ThinkingLevel:    state.ThinkingLevel,
		Tools:            cloneTools(state.Tools),
		Messages:         cloneMessages(state.Messages),
		IsStreaming:      state.IsStreaming,
		StreamMessage:    cloneMessagePtr(state.StreamMessage),
		PendingToolCalls: clonePendingToolCallMap(state.PendingToolCalls),
		Error:            state.Error,
		SessionID:        state.SessionID,
		Transport:        state.Transport,
		MaxRetryDelayMs:  state.MaxRetryDelayMs,
		ThinkingBudgets:  cloneThinkingBudgets(state.ThinkingBudgets),
		Metadata:         cloneStringAnyMap(state.Metadata),
	}
}

func cloneMessagePtr(message *Message) *Message {
	if message == nil {
		return nil
	}
	cloned := cloneMessage(*message)
	return &cloned
}

func clonePendingToolCallMap(values map[string]PendingToolCall) map[string]PendingToolCall {
	if len(values) == 0 {
		return make(map[string]PendingToolCall)
	}
	cloned := make(map[string]PendingToolCall, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func effectiveSystemPrompt(snapshot AgentSnapshot, definition AgentDefinition) string {
	if snapshot.SystemPrompt != "" {
		return snapshot.SystemPrompt
	}
	return definition.SystemPrompt
}

func effectiveModelRef(snapshot AgentSnapshot, definition AgentDefinition) ModelRef {
	if snapshot.Model.Model != "" || snapshot.Model.Provider != "" || len(snapshot.Model.Metadata) > 0 {
		return cloneModelRef(snapshot.Model)
	}
	return cloneModelRef(definition.DefaultModel)
}
