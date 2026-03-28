package piagentgo

import "context"

// AgentDefinition describes an agent runtime blueprint.
type AgentDefinition struct {
	Name             string             `json:"name,omitempty"`
	SystemPrompt     string             `json:"system_prompt,omitempty"`
	DefaultModel     ModelRef           `json:"default_model,omitempty"`
	ThinkingLevel    ThinkingLevel      `json:"thinking_level,omitempty"`
	SessionID        string             `json:"session_id,omitempty"`
	Transport        Transport          `json:"transport,omitempty"`
	MaxRetryDelayMs  int                `json:"max_retry_delay_ms,omitempty"`
	ThinkingBudgets  ThinkingBudgets    `json:"thinking_budgets,omitempty"`
	Model            StreamModel        `json:"-"`
	ModelResolver    ModelResolver      `json:"-"`
	Tools            []ToolDefinition   `json:"-"`
	ToolResolver     ToolResolver       `json:"-"`
	TransformContext TransformContext   `json:"-"`
	ConvertToLLM     ConvertToLLM       `json:"-"`
	BeforeToolCall   BeforeToolCallHook `json:"-"`
	AfterToolCall    AfterToolCallHook  `json:"-"`
	ToolExecution    ToolExecutionMode  `json:"tool_execution,omitempty"`
	SteeringMode     QueueMode          `json:"steering_mode,omitempty"`
	FollowUpMode     QueueMode          `json:"follow_up_mode,omitempty"`
	MaxTurns         int                `json:"max_turns,omitempty"`
}

// Validate returns a copy of the definition with defaults applied.
func (d AgentDefinition) Validate() (AgentDefinition, error) {
	if d.ToolExecution == "" {
		d.ToolExecution = ToolExecutionParallel
	}
	if d.SteeringMode == "" {
		d.SteeringMode = QueueModeOneAtATime
	}
	if d.FollowUpMode == "" {
		d.FollowUpMode = QueueModeOneAtATime
	}
	if d.MaxTurns == 0 {
		d.MaxTurns = 20
	}
	if d.ThinkingLevel == "" {
		d.ThinkingLevel = ThinkingOff
	}
	if d.Transport == "" {
		d.Transport = TransportSSE
	}
	if d.TransformContext == nil {
		d.TransformContext = DefaultTransformContext
	}
	if d.ConvertToLLM == nil {
		d.ConvertToLLM = DefaultConvertToLLM
	}
	return d, nil
}

// ResolveModel returns the effective runtime model for a snapshot.
func (d AgentDefinition) ResolveModel(ctx context.Context, snapshot AgentSnapshot) (StreamModel, ModelRef, error) {
	if d.Model != nil {
		ref := snapshot.Model
		if ref.Model == "" && d.DefaultModel.Model != "" {
			ref = cloneModelRef(d.DefaultModel)
		}
		return d.Model, ref, nil
	}

	ref := snapshot.Model
	if ref.Model == "" {
		ref = cloneModelRef(d.DefaultModel)
	}
	if d.ModelResolver == nil {
		if ref.Provider != "" && ref.Model != "" {
			return defaultProviderStreamModel, ref, nil
		}
		return nil, ModelRef{}, ErrModelNotConfigured
	}

	model, err := d.ModelResolver(ctx, ref, snapshot)
	if err != nil {
		return nil, ModelRef{}, err
	}
	return model, ref, nil
}

// ResolveTools returns the effective tools for a snapshot.
func (d AgentDefinition) ResolveTools(ctx context.Context, snapshot AgentSnapshot) ([]ToolDefinition, error) {
	if d.ToolResolver != nil {
		return d.ToolResolver(ctx, snapshot)
	}
	return cloneTools(d.Tools), nil
}

// DefaultTransformContext returns a shallow-cloned message slice.
func DefaultTransformContext(_ context.Context, messages []Message) ([]Message, error) {
	return cloneMessages(messages), nil
}

// DefaultConvertToLLM keeps user, assistant, and tool messages and drops custom messages.
func DefaultConvertToLLM(_ context.Context, messages []Message) ([]Message, error) {
	converted := make([]Message, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case RoleUser, RoleAssistant, RoleTool:
			converted = append(converted, cloneMessage(message))
		}
	}
	return converted, nil
}
