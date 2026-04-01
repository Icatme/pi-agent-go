package prebuilt

import core "github.com/Icatme/pi-agent-go"

// CreateAgentOption customizes a native single-agent constructor.
type CreateAgentOption = ChatAgentOption

// CreateAgent builds a native piagentgo.Agent from a model and tool set.
// It is the prebuilt convenience entry point for the common single-agent case.
func CreateAgent(model core.StreamModel, tools []core.ToolDefinition, opts ...CreateAgentOption) (*core.Agent, error) {
	definition := core.AgentDefinition{
		Model: model,
		Tools: cloneToolDefinitions(tools),
	}
	for _, opt := range opts {
		opt(&definition)
	}
	return core.NewAgent(definition)
}
