package prebuilt

import (
	"context"
	"errors"
	"fmt"
	"strings"

	core "github.com/Icatme/pi-agent-go"
)

// ReflectionAgentConfig configures the sequential reflection helper.
type ReflectionAgentConfig struct {
	Model            core.StreamModel
	ReflectionModel  core.StreamModel
	MaxIterations    int
	SystemMessage    string
	ReflectionPrompt string
}

// ReflectionResult captures the final state of one reflection run.
type ReflectionResult struct {
	Messages   []core.Message
	Draft      string
	Reflection string
	Iteration  int
}

// ReflectionAgent runs generate-and-reflect passes on top of native agents.
type ReflectionAgent struct {
	generatorDefinition core.AgentDefinition
	reflectorDefinition core.AgentDefinition
	maxIterations       int
}

// CreateReflectionAgent creates a native reflection helper.
func CreateReflectionAgent(config ReflectionAgentConfig) (*ReflectionAgent, error) {
	resolved, err := normalizeReflectionConfig(config)
	if err != nil {
		return nil, err
	}

	return &ReflectionAgent{
		generatorDefinition: core.AgentDefinition{
			Model:        resolved.Model,
			SystemPrompt: resolved.SystemMessage,
			MaxTurns:     1,
		},
		reflectorDefinition: core.AgentDefinition{
			Model:        resolved.ReflectionModel,
			SystemPrompt: resolved.ReflectionPrompt,
			MaxTurns:     1,
		},
		maxIterations: resolved.MaxIterations,
	}, nil
}

// PromptText runs the reflection loop from one user text prompt.
func (a *ReflectionAgent) PromptText(ctx context.Context, text string) (ReflectionResult, error) {
	return a.Run(ctx, []core.Message{core.NewUserTextMessage(text)})
}

// Run executes reflection passes over the provided initial messages.
func (a *ReflectionAgent) Run(ctx context.Context, messages []core.Message) (ReflectionResult, error) {
	if len(messages) == 0 {
		return ReflectionResult{}, errors.New("prebuilt: reflection agent requires at least one message")
	}

	generator, err := core.NewAgent(a.generatorDefinition)
	if err != nil {
		return ReflectionResult{}, err
	}
	reflector, err := core.NewAgent(a.reflectorDefinition)
	if err != nil {
		return ReflectionResult{}, err
	}

	result := ReflectionResult{
		Messages: append([]core.Message(nil), messages...),
	}
	originalRequest := originalUserRequest(messages)

	for iteration := 0; iteration < a.maxIterations; iteration++ {
		if iteration == 0 {
			generator.Reset()
			generator.ReplaceMessages(messages)
		} else {
			revisionPrompt := fmt.Sprintf(
				"Revise based on reflection:\nRequest: %s\nDraft: %s\nReflection: %s",
				originalRequest,
				result.Draft,
				result.Reflection,
			)
			generator.Reset()
			generator.ReplaceMessages([]core.Message{core.NewUserTextMessage(revisionPrompt)})
		}

		if err := generator.Continue(ctx); err != nil {
			return result, err
		}

		draftMessage, ok := lastAssistantMessage(generator.State().Messages)
		if !ok {
			return result, errors.New("prebuilt: reflection generation produced no assistant message")
		}

		result.Draft = messageText(draftMessage)
		result.Iteration = iteration + 1
		result.Messages = append(result.Messages, draftMessage)

		if result.Iteration >= a.maxIterations {
			break
		}

		reflectionPrompt := fmt.Sprintf("Request: %s\nResponse: %s", originalRequest, result.Draft)
		reflector.Reset()
		if err := reflector.PromptText(ctx, reflectionPrompt); err != nil {
			return result, err
		}

		reflectionMessage, ok := lastAssistantMessage(reflector.State().Messages)
		if !ok {
			return result, errors.New("prebuilt: reflection pass produced no assistant message")
		}

		result.Reflection = messageText(reflectionMessage)
		if isResponseSatisfactory(result.Reflection) {
			break
		}
	}

	return result, nil
}

func normalizeReflectionConfig(config ReflectionAgentConfig) (ReflectionAgentConfig, error) {
	if config.Model == nil {
		return ReflectionAgentConfig{}, errors.New("prebuilt: reflection agent model is required")
	}
	if config.ReflectionModel == nil {
		config.ReflectionModel = config.Model
	}
	if config.MaxIterations == 0 {
		config.MaxIterations = 3
	}
	if config.SystemMessage == "" {
		config.SystemMessage = "You are a helpful assistant. Generate a high-quality response to the user's request."
	}
	if config.ReflectionPrompt == "" {
		config.ReflectionPrompt = buildDefaultReflectionPrompt()
	}
	return config, nil
}

func originalUserRequest(messages []core.Message) string {
	for _, message := range messages {
		if message.Role != core.RoleUser {
			continue
		}
		text := messageText(message)
		if text != "" {
			return text
		}
	}
	return ""
}

func lastAssistantMessage(messages []core.Message) (core.Message, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == core.RoleAssistant {
			return messages[i], true
		}
	}
	return core.Message{}, false
}

func isResponseSatisfactory(reflection string) bool {
	reflectionLower := strings.ToLower(reflection)
	satisfactoryKeywords := []string{
		"excellent",
		"satisfactory",
		"no major issues",
		"well done",
		"accurate",
		"meets all requirements",
	}
	for _, keyword := range satisfactoryKeywords {
		if strings.Contains(reflectionLower, keyword) {
			return true
		}
	}
	return false
}

func buildDefaultReflectionPrompt() string {
	return "You are a critical reviewer. Evaluate the response and provide strengths, weaknesses and suggestions."
}
