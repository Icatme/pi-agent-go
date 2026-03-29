package prebuilt

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	core "github.com/Icatme/pi-agent-go"
)

// ChatAgentOption customizes a native ChatAgent.
type ChatAgentOption func(*core.AgentDefinition)

// WithSystemMessage sets the default chat system prompt.
func WithSystemMessage(message string) ChatAgentOption {
	return func(definition *core.AgentDefinition) {
		definition.SystemPrompt = message
	}
}

// WithStateModifier rewrites message history before model invocation.
func WithStateModifier(modifier func([]core.Message) []core.Message) ChatAgentOption {
	return func(definition *core.AgentDefinition) {
		if modifier == nil {
			return
		}
		definition.TransformContext = func(_ context.Context, messages []core.Message) ([]core.Message, error) {
			return modifier(messages), nil
		}
	}
}

// WithMaxIterations limits assistant turns for one chat request.
func WithMaxIterations(maxIterations int) ChatAgentOption {
	return func(definition *core.AgentDefinition) {
		definition.MaxTurns = maxIterations
	}
}

// ChatAgent is a session-oriented single-agent wrapper.
// It reuses the native piagentgo runtime for history, tools, and streaming.
type ChatAgent struct {
	agent        *core.Agent
	threadID     string
	baseResolver core.ToolResolver

	mu           sync.RWMutex
	dynamicTools []core.ToolDefinition
}

// NewChatAgent creates a session-oriented native ChatAgent.
func NewChatAgent(definition core.AgentDefinition, opts ...ChatAgentOption) (*ChatAgent, error) {
	for _, opt := range opts {
		opt(&definition)
	}

	threadID := strings.TrimSpace(definition.SessionID)
	if threadID == "" {
		threadID = newSessionID()
		definition.SessionID = threadID
	}

	chat := &ChatAgent{
		threadID:     threadID,
		baseResolver: definition.ToolResolver,
	}

	definition.ToolResolver = func(ctx context.Context, snapshot core.AgentSnapshot) ([]core.ToolDefinition, error) {
		baseTools, err := chat.resolveBaseTools(ctx, snapshot, definition)
		if err != nil {
			return nil, err
		}

		chat.mu.RLock()
		dynamic := cloneToolDefinitions(chat.dynamicTools)
		chat.mu.RUnlock()

		return append(baseTools, dynamic...), nil
	}

	agent, err := NewPiAgent(definition)
	if err != nil {
		return nil, err
	}
	chat.agent = agent
	return chat, nil
}

// ThreadID returns the stable session identifier for the chat.
func (c *ChatAgent) ThreadID() string {
	return c.threadID
}

// Chat appends a user message, runs one agent interaction, and returns the final assistant text.
func (c *ChatAgent) Chat(ctx context.Context, message string) (string, error) {
	if err := c.agent.PromptText(ctx, message); err != nil {
		return "", err
	}
	return latestAssistantText(c.agent.State().Messages), nil
}

// PrintStream streams the response chunks to a writer-like callback.
func (c *ChatAgent) PrintStream(ctx context.Context, message string, write func(string) error) error {
	chunks, err := c.AsyncChat(ctx, message)
	if err != nil {
		return err
	}
	for chunk := range chunks {
		if err := write(chunk); err != nil {
			return err
		}
	}
	return nil
}

// AsyncChat streams assistant text deltas for one user message.
func (c *ChatAgent) AsyncChat(ctx context.Context, message string) (<-chan string, error) {
	output := make(chan string, 64)
	runCtx, cancel := context.WithCancel(ctx)
	eventCh := make(chan core.AgentEvent, 64)
	errCh := make(chan error, 1)

	unsubscribe := c.agent.Subscribe(func(event core.AgentEvent) {
		select {
		case eventCh <- event:
		case <-runCtx.Done():
		}
	})

	go func() {
		defer close(errCh)
		defer close(eventCh)
		defer unsubscribe()

		if err := c.agent.Prompt(runCtx, core.NewUserTextMessage(message)); err != nil {
			select {
			case errCh <- err:
			default:
			}
		}
	}()

	go func() {
		defer close(output)
		defer cancel()

		var sawDelta bool
		for event := range eventCh {
			switch event.Type {
			case core.EventMessageUpdate:
				if event.Message == nil || event.Message.Role != core.RoleAssistant {
					continue
				}
				if event.AssistantEvent != nil && event.AssistantEvent.Type != core.AssistantEventTextDelta {
					continue
				}
				if event.Delta == "" {
					continue
				}
				sawDelta = true
				select {
				case <-ctx.Done():
					return
				case output <- event.Delta:
				}
			case core.EventMessageEnd:
				if sawDelta || event.Message == nil || event.Message.Role != core.RoleAssistant {
					continue
				}
				text := messageText(*event.Message)
				if text == "" {
					continue
				}
				select {
				case <-ctx.Done():
					return
				case output <- text:
				}
			}
		}

		select {
		case <-ctx.Done():
			return
		case err := <-errCh:
			_ = err
		default:
		}
	}()

	return output, nil
}

// AsyncChatWithChunks streams the final response in word-sized chunks.
func (c *ChatAgent) AsyncChatWithChunks(ctx context.Context, message string) (<-chan string, error) {
	output := make(chan string, 64)

	go func() {
		defer close(output)

		response, err := c.Chat(ctx, message)
		if err != nil {
			return
		}

		words := splitIntoWords(response)
		for i, word := range words {
			select {
			case <-ctx.Done():
				return
			case output <- word:
			}
			if i < len(words)-1 {
				select {
				case <-ctx.Done():
					return
				case output <- " ":
				}
			}
		}
	}()

	return output, nil
}

// SetTools replaces all dynamic tools.
func (c *ChatAgent) SetTools(newTools []core.ToolDefinition) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dynamicTools = cloneToolDefinitions(newTools)
}

// AddTool adds or replaces one dynamic tool by name.
func (c *ChatAgent) AddTool(tool core.ToolDefinition) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i := range c.dynamicTools {
		if c.dynamicTools[i].Name == tool.Name {
			c.dynamicTools[i] = cloneToolDefinition(tool)
			return
		}
	}
	c.dynamicTools = append(c.dynamicTools, cloneToolDefinition(tool))
}

// RemoveTool removes one dynamic tool by name.
func (c *ChatAgent) RemoveTool(toolName string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i := range c.dynamicTools {
		if c.dynamicTools[i].Name != toolName {
			continue
		}
		c.dynamicTools = append(c.dynamicTools[:i], c.dynamicTools[i+1:]...)
		return true
	}
	return false
}

// GetTools returns a copy of the dynamic tool list.
func (c *ChatAgent) GetTools() []core.ToolDefinition {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneToolDefinitions(c.dynamicTools)
}

// ClearTools removes all dynamic tools.
func (c *ChatAgent) ClearTools() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dynamicTools = nil
}

func (c *ChatAgent) resolveBaseTools(ctx context.Context, snapshot core.AgentSnapshot, definition core.AgentDefinition) ([]core.ToolDefinition, error) {
	if c.baseResolver != nil {
		return c.baseResolver(ctx, snapshot)
	}
	return cloneToolDefinitions(definition.Tools), nil
}

func latestAssistantText(messages []core.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == core.RoleAssistant {
			return messageText(messages[i])
		}
	}
	return ""
}

func messageText(message core.Message) string {
	var builder strings.Builder
	for _, part := range message.Parts {
		if part.Type == core.PartTypeText {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}

func splitIntoWords(text string) []string {
	return strings.Fields(text)
}

func newSessionID() string {
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err == nil {
		return "chat-" + hex.EncodeToString(bytes[:])
	}
	return "chat-" + time.Now().UTC().Format("20060102150405.000000000")
}

func cloneToolDefinitions(tools []core.ToolDefinition) []core.ToolDefinition {
	if len(tools) == 0 {
		return nil
	}
	cloned := make([]core.ToolDefinition, len(tools))
	for i := range tools {
		cloned[i] = cloneToolDefinition(tools[i])
	}
	return cloned
}

func cloneToolDefinition(tool core.ToolDefinition) core.ToolDefinition {
	cloned := tool
	if tool.Parameters != nil {
		cloned.Parameters = cloneStringAnyMap(tool.Parameters)
	}
	return cloned
}

func cloneStringAnyMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = cloneAny(value)
	}
	return cloned
}

func cloneAny(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneStringAnyMap(typed)
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
