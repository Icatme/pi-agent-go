package piagentgo

// AgentInitialState stores the user-facing initial state used to create an Agent.
type AgentInitialState struct {
	SystemPrompt  string           `json:"system_prompt,omitempty"`
	ModelRef      ModelRef         `json:"model,omitempty"`
	ThinkingLevel ThinkingLevel    `json:"thinking_level,omitempty"`
	Tools         []ToolDefinition `json:"-"`
	Messages      []Message        `json:"messages,omitempty"`
	SessionID     string           `json:"session_id,omitempty"`
	Metadata      map[string]any   `json:"metadata,omitempty"`
}

// AgentOptions is the higher-level user-facing constructor contract for Agent.
type AgentOptions struct {
	InitialState     AgentInitialState  `json:"initial_state,omitempty"`
	Model            StreamModel        `json:"-"`
	ModelResolver    ModelResolver      `json:"-"`
	Stream           StreamFunc         `json:"-"`
	ConvertToLLM     ConvertToLLM       `json:"-"`
	TransformContext TransformContext   `json:"-"`
	SteeringMode     QueueMode          `json:"steering_mode,omitempty"`
	FollowUpMode     QueueMode          `json:"follow_up_mode,omitempty"`
	ThinkingBudgets  ThinkingBudgets    `json:"thinking_budgets,omitempty"`
	Transport        Transport          `json:"transport,omitempty"`
	MaxRetryDelayMs  int                `json:"max_retry_delay_ms,omitempty"`
	ToolExecution    ToolExecutionMode  `json:"tool_execution,omitempty"`
	BeforeToolCall   BeforeToolCallHook `json:"-"`
	AfterToolCall    AfterToolCallHook  `json:"-"`
}

// NewAgentWithOptions creates an Agent from a higher-level AgentOptions contract.
func NewAgentWithOptions(options AgentOptions, opts ...AgentOption) (*Agent, error) {
	definition, snapshot := options.build()
	return newAgent(definition, snapshot, opts...)
}

func (o AgentOptions) build() (AgentDefinition, AgentSnapshot) {
	model := o.Model
	if model == nil && o.Stream != nil {
		model = o.Stream
	}

	definition := AgentDefinition{
		SystemPrompt:     o.InitialState.SystemPrompt,
		DefaultModel:     cloneModelRef(o.InitialState.ModelRef),
		ThinkingLevel:    o.InitialState.ThinkingLevel,
		SessionID:        o.InitialState.SessionID,
		Transport:        o.Transport,
		MaxRetryDelayMs:  o.MaxRetryDelayMs,
		ThinkingBudgets:  cloneThinkingBudgets(o.ThinkingBudgets),
		Model:            model,
		ModelResolver:    o.ModelResolver,
		Tools:            cloneTools(o.InitialState.Tools),
		TransformContext: o.TransformContext,
		ConvertToLLM:     o.ConvertToLLM,
		BeforeToolCall:   o.BeforeToolCall,
		AfterToolCall:    o.AfterToolCall,
		ToolExecution:    o.ToolExecution,
		SteeringMode:     o.SteeringMode,
		FollowUpMode:     o.FollowUpMode,
	}

	snapshot := AgentSnapshot{
		SessionID:    o.InitialState.SessionID,
		SystemPrompt: o.InitialState.SystemPrompt,
		Model:        cloneModelRef(o.InitialState.ModelRef),
		Messages:     cloneMessages(o.InitialState.Messages),
		Metadata:     cloneStringAnyMap(o.InitialState.Metadata),
	}

	return definition, snapshot
}
