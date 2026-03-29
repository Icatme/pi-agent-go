package prebuilt

import core "github.com/Icatme/pi-agent-go"

// PiAgent is the native piagentgo.Agent exposed through the prebuilt package.
// The prebuilt layer should not maintain a second wrapper runtime.
type PiAgent = core.Agent

// PiAgentOption mutates the underlying agent definition before construction.
type PiAgentOption func(*core.AgentDefinition)

// WithPiSystemPrompt sets the default system prompt.
func WithPiSystemPrompt(prompt string) PiAgentOption {
	return func(definition *core.AgentDefinition) {
		definition.SystemPrompt = prompt
	}
}

// WithPiThinkingLevel sets the default reasoning effort.
func WithPiThinkingLevel(level core.ThinkingLevel) PiAgentOption {
	return func(definition *core.AgentDefinition) {
		definition.ThinkingLevel = level
	}
}

// WithPiSteeringMode sets how queued steering messages are dequeued.
func WithPiSteeringMode(mode core.QueueMode) PiAgentOption {
	return func(definition *core.AgentDefinition) {
		definition.SteeringMode = mode
	}
}

// WithPiFollowUpMode sets how queued follow-up messages are dequeued.
func WithPiFollowUpMode(mode core.QueueMode) PiAgentOption {
	return func(definition *core.AgentDefinition) {
		definition.FollowUpMode = mode
	}
}

// WithPiConvertToLLM sets the message conversion hook.
func WithPiConvertToLLM(fn core.ConvertToLLM) PiAgentOption {
	return func(definition *core.AgentDefinition) {
		definition.ConvertToLLM = fn
	}
}

// WithPiTransformContext sets the context transformation hook.
func WithPiTransformContext(fn core.TransformContext) PiAgentOption {
	return func(definition *core.AgentDefinition) {
		definition.TransformContext = fn
	}
}

// WithPiMaxIterations sets the maximum number of assistant turns.
func WithPiMaxIterations(max int) PiAgentOption {
	return func(definition *core.AgentDefinition) {
		definition.MaxTurns = max
	}
}

// NewPiAgent creates a native piagentgo.Agent with optional definition mutators.
func NewPiAgent(definition core.AgentDefinition, opts ...PiAgentOption) (*PiAgent, error) {
	for _, opt := range opts {
		opt(&definition)
	}
	return core.NewAgent(definition)
}
