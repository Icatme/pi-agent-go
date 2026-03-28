package piagentgo

import (
	"context"
	"encoding/json"
	"time"
)

// MessageRole identifies the semantic role of a message in the conversation.
type MessageRole string

const (
	// RoleUser is a user-authored message.
	RoleUser MessageRole = "user"
	// RoleAssistant is an assistant-authored message.
	RoleAssistant MessageRole = "assistant"
	// RoleTool is a tool-result message.
	RoleTool MessageRole = "tool"
	// RoleCustom is an app-defined message that usually needs conversion before model use.
	RoleCustom MessageRole = "custom"
)

// PartType identifies the content type within a message part.
type PartType string

const (
	// PartTypeText represents plain text content.
	PartTypeText PartType = "text"
	// PartTypeImage represents image content.
	PartTypeImage PartType = "image"
	// PartTypeThinking represents reasoning/thinking deltas.
	PartTypeThinking PartType = "thinking"
)

// StopReason describes why an assistant message finished.
type StopReason string

const (
	// StopReasonStop indicates a normal completion.
	StopReasonStop StopReason = "stop"
	// StopReasonLength indicates a token or length bound was reached.
	StopReasonLength StopReason = "length"
	// StopReasonToolUse indicates the assistant requested tool execution.
	StopReasonToolUse StopReason = "tool_use"
	// StopReasonError indicates the model run failed.
	StopReasonError StopReason = "error"
	// StopReasonAborted indicates the run was cancelled.
	StopReasonAborted StopReason = "aborted"
)

// EventType identifies a runtime event emitted by the agent.
type EventType string

const (
	// EventAgentStart is emitted when a run starts.
	EventAgentStart EventType = "agent_start"
	// EventAgentEnd is emitted when a run ends.
	EventAgentEnd EventType = "agent_end"
	// EventTurnStart is emitted when a turn starts.
	EventTurnStart EventType = "turn_start"
	// EventTurnEnd is emitted when a turn ends.
	EventTurnEnd EventType = "turn_end"
	// EventMessageStart is emitted when a message begins.
	EventMessageStart EventType = "message_start"
	// EventMessageUpdate is emitted while an assistant message streams.
	EventMessageUpdate EventType = "message_update"
	// EventMessageEnd is emitted when a message completes.
	EventMessageEnd EventType = "message_end"
	// EventToolExecutionStart is emitted before a tool executes.
	EventToolExecutionStart EventType = "tool_execution_start"
	// EventToolExecutionUpdate is emitted for streaming tool updates.
	EventToolExecutionUpdate EventType = "tool_execution_update"
	// EventToolExecutionEnd is emitted when a tool finishes.
	EventToolExecutionEnd EventType = "tool_execution_end"
)

// AssistantEventType identifies low-level stream events from a model adapter.
type AssistantEventType string

const (
	// AssistantEventStart is emitted when the assistant stream starts.
	AssistantEventStart AssistantEventType = "start"
	// AssistantEventTextStart is emitted when a text block starts.
	AssistantEventTextStart AssistantEventType = "text_start"
	// AssistantEventTextDelta is emitted for text increments.
	AssistantEventTextDelta AssistantEventType = "text_delta"
	// AssistantEventTextEnd is emitted when a text block completes.
	AssistantEventTextEnd AssistantEventType = "text_end"
	// AssistantEventThinkingStart is emitted when a thinking block starts.
	AssistantEventThinkingStart AssistantEventType = "thinking_start"
	// AssistantEventThinkingDelta is emitted for reasoning increments.
	AssistantEventThinkingDelta AssistantEventType = "thinking_delta"
	// AssistantEventThinkingEnd is emitted when a thinking block completes.
	AssistantEventThinkingEnd AssistantEventType = "thinking_end"
	// AssistantEventToolCallStart is emitted when a tool call block starts.
	AssistantEventToolCallStart AssistantEventType = "toolcall_start"
	// AssistantEventToolCallDelta is emitted for streamed tool call argument increments.
	AssistantEventToolCallDelta AssistantEventType = "toolcall_delta"
	// AssistantEventToolCallEnd is emitted when a tool call block completes.
	AssistantEventToolCallEnd AssistantEventType = "toolcall_end"
	// AssistantEventDone is emitted when the stream reaches a normal terminal message.
	AssistantEventDone AssistantEventType = "done"
	// AssistantEventError is emitted when the stream reaches an error terminal message.
	AssistantEventError AssistantEventType = "error"
)

// QueueMode controls how queued steering and follow-up messages are delivered.
type QueueMode string

const (
	// QueueModeAll delivers every queued message in one batch.
	QueueModeAll QueueMode = "all"
	// QueueModeOneAtATime delivers one queued message per dequeue.
	QueueModeOneAtATime QueueMode = "one-at-a-time"
)

// ToolExecutionMode controls how tool calls from one assistant message are executed.
type ToolExecutionMode string

const (
	// ToolExecutionSequential executes tool calls one by one.
	ToolExecutionSequential ToolExecutionMode = "sequential"
	// ToolExecutionParallel executes preflight sequentially and tool bodies concurrently.
	ToolExecutionParallel ToolExecutionMode = "parallel"
)

// ThinkingLevel controls how much reasoning a model should perform.
type ThinkingLevel string

const (
	// ThinkingOff disables explicit reasoning.
	ThinkingOff ThinkingLevel = "off"
	// ThinkingMinimal requests minimal reasoning.
	ThinkingMinimal ThinkingLevel = "minimal"
	// ThinkingLow requests low reasoning effort.
	ThinkingLow ThinkingLevel = "low"
	// ThinkingMedium requests medium reasoning effort.
	ThinkingMedium ThinkingLevel = "medium"
	// ThinkingHigh requests high reasoning effort.
	ThinkingHigh ThinkingLevel = "high"
	// ThinkingXHigh requests extra high reasoning effort.
	ThinkingXHigh ThinkingLevel = "xhigh"
)

// Transport identifies the preferred model transport.
type Transport string

const (
	// TransportSSE prefers server-sent events style streaming.
	TransportSSE Transport = "sse"
	// TransportWebSocket prefers a websocket stream when the provider supports it.
	TransportWebSocket Transport = "websocket"
	// TransportAuto lets the provider choose the best available stream transport.
	TransportAuto Transport = "auto"
)

// ThinkingBudgets stores optional token budgets per thinking level.
type ThinkingBudgets map[ThinkingLevel]int

// Part is a single content fragment inside a message.
// Image parts store provider-ready base64 data plus MIME type.
type Part struct {
	Type      PartType `json:"type"`
	Text      string   `json:"text,omitempty"`
	Data      string   `json:"data,omitempty"`
	MIMEType  string   `json:"mime_type,omitempty"`
	Signature string   `json:"signature,omitempty"`
	Redacted  bool     `json:"redacted,omitempty"`
}

// ToolCall is an assistant-emitted tool invocation request.
type ToolCall struct {
	ID               string          `json:"id"`
	OriginalID       string          `json:"original_id,omitempty"`
	Name             string          `json:"name"`
	Arguments        json.RawMessage `json:"arguments,omitempty"`
	ParsedArgs       map[string]any  `json:"parsed_args,omitempty"`
	ThoughtSignature string          `json:"thought_signature,omitempty"`
}

// ToolResult is the normalized output of a tool execution.
type ToolResult struct {
	Content []Part `json:"content,omitempty"`
	Details any    `json:"details,omitempty"`
}

// ToolResultPayload stores tool-result specific message data.
type ToolResultPayload struct {
	ToolCallID         string `json:"tool_call_id"`
	OriginalToolCallID string `json:"original_tool_call_id,omitempty"`
	ToolName           string `json:"tool_name"`
	Content            []Part `json:"content,omitempty"`
	Details            any    `json:"details,omitempty"`
	IsError            bool   `json:"is_error"`
}

// Message is the canonical runtime message envelope.
type Message struct {
	ID           string             `json:"id,omitempty"`
	Role         MessageRole        `json:"role"`
	Kind         string             `json:"kind,omitempty"`
	Parts        []Part             `json:"parts,omitempty"`
	ToolCalls    []ToolCall         `json:"tool_calls,omitempty"`
	ToolResult   *ToolResultPayload `json:"tool_result,omitempty"`
	Timestamp    time.Time          `json:"timestamp"`
	API          string             `json:"api,omitempty"`
	Provider     string             `json:"provider,omitempty"`
	Model        string             `json:"model,omitempty"`
	ResponseID   string             `json:"response_id,omitempty"`
	Metadata     map[string]any     `json:"metadata,omitempty"`
	Payload      map[string]any     `json:"payload,omitempty"`
	StopReason   StopReason         `json:"stop_reason,omitempty"`
	ErrorMessage string             `json:"error_message,omitempty"`
}

// PendingToolCall tracks tool calls that are currently in-flight.
type PendingToolCall struct {
	ToolCallID         string `json:"tool_call_id"`
	OriginalToolCallID string `json:"original_tool_call_id,omitempty"`
	ToolName           string `json:"tool_name"`
}

// ProviderAuthType identifies the provider credential strategy.
type ProviderAuthType string

const (
	// ProviderAuthTypeAPIKey uses a static API key or bearer token.
	ProviderAuthTypeAPIKey ProviderAuthType = "apiKey"
	// ProviderAuthTypeOAuth uses OAuth credentials.
	ProviderAuthTypeOAuth ProviderAuthType = "oauth"
)

// OAuthCredentials stores provider OAuth tokens.
type OAuthCredentials struct {
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresUnix  int64  `json:"expires_unix,omitempty"`
}

// ProviderAuthConfig stores the auth payload for one provider.
type ProviderAuthConfig struct {
	Type   ProviderAuthType  `json:"type,omitempty"`
	APIKey string            `json:"api_key,omitempty"`
	OAuth  *OAuthCredentials `json:"oauth,omitempty"`
}

// ProviderConfig stores typed runtime configuration for the selected provider.
type ProviderConfig struct {
	BaseURL string              `json:"base_url,omitempty"`
	APIKey  string              `json:"api_key,omitempty"`
	Headers map[string]string   `json:"headers,omitempty"`
	Auth    *ProviderAuthConfig `json:"auth,omitempty"`
}

// ModelRef identifies a model without storing provider runtime objects in snapshot state.
type ModelRef struct {
	Provider       string         `json:"provider,omitempty"`
	Model          string         `json:"model,omitempty"`
	ProviderConfig ProviderConfig `json:"provider_config,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

// AgentSnapshot is the serializable runtime state for a session.
type AgentSnapshot struct {
	SessionID        string            `json:"session_id,omitempty"`
	SystemPrompt     string            `json:"system_prompt,omitempty"`
	Model            ModelRef          `json:"model,omitempty"`
	Messages         []Message         `json:"messages,omitempty"`
	PendingToolCalls []PendingToolCall `json:"pending_tool_calls,omitempty"`
	Error            string            `json:"error,omitempty"`
	Metadata         map[string]any    `json:"metadata,omitempty"`
}

// AgentContext is the message/tool context consumed by one agent turn.
type AgentContext struct {
	SystemPrompt string           `json:"system_prompt,omitempty"`
	Messages     []Message        `json:"messages,omitempty"`
	Tools        []ToolDefinition `json:"tools,omitempty"`
}

// AgentState is the in-memory runtime state exposed by Agent.
type AgentState struct {
	SystemPrompt     string                     `json:"system_prompt,omitempty"`
	Model            ModelRef                   `json:"model,omitempty"`
	ThinkingLevel    ThinkingLevel              `json:"thinking_level,omitempty"`
	Tools            []ToolDefinition           `json:"-"`
	Messages         []Message                  `json:"messages,omitempty"`
	IsStreaming      bool                       `json:"is_streaming"`
	StreamMessage    *Message                   `json:"stream_message,omitempty"`
	PendingToolCalls map[string]PendingToolCall `json:"pending_tool_calls,omitempty"`
	Error            string                     `json:"error,omitempty"`
	SessionID        string                     `json:"session_id,omitempty"`
	Transport        Transport                  `json:"transport,omitempty"`
	MaxRetryDelayMs  int                        `json:"max_retry_delay_ms,omitempty"`
	ThinkingBudgets  ThinkingBudgets            `json:"thinking_budgets,omitempty"`
	Metadata         map[string]any             `json:"metadata,omitempty"`
}

// AgentEvent is emitted to subscribers for lifecycle, message, and tool updates.
type AgentEvent struct {
	Type               EventType       `json:"type"`
	Timestamp          time.Time       `json:"timestamp"`
	Message            *Message        `json:"message,omitempty"`
	Messages           []Message       `json:"messages,omitempty"`
	Delta              string          `json:"delta,omitempty"`
	AssistantEvent     *AssistantEvent `json:"assistant_event,omitempty"`
	ToolCall           *ToolCall       `json:"tool_call,omitempty"`
	ToolCallID         string          `json:"tool_call_id,omitempty"`
	OriginalToolCallID string          `json:"original_tool_call_id,omitempty"`
	ToolName           string          `json:"tool_name,omitempty"`
	Args               any             `json:"args,omitempty"`
	ToolResult         *ToolResult     `json:"tool_result,omitempty"`
	PartialToolResult  *ToolResult     `json:"partial_tool_result,omitempty"`
	ToolMessages       []Message       `json:"tool_messages,omitempty"`
	IsError            bool            `json:"is_error,omitempty"`
	Err                error           `json:"-"`
}

// EventSink receives runtime events from an engine.
type EventSink func(AgentEvent)

// ToolUpdateFunc receives partial tool output during execution.
type ToolUpdateFunc func(ToolResult)

// ToolExecutorFunc executes a tool call with validated arguments.
type ToolExecutorFunc func(context.Context, string, any, ToolUpdateFunc) (ToolResult, error)

// ToolDefinition defines a tool available to the agent runtime.
type ToolDefinition struct {
	Name           string                      `json:"name"`
	Label          string                      `json:"label,omitempty"`
	Description    string                      `json:"description,omitempty"`
	Parameters     map[string]any              `json:"parameters,omitempty"`
	ParseArguments func(ToolCall) (any, error) `json:"-"`
	Execute        ToolExecutorFunc            `json:"-"`
}

// BeforeToolCallContext is passed to a before-tool hook.
type BeforeToolCallContext struct {
	AssistantMessage Message      `json:"assistant_message"`
	ToolCall         ToolCall     `json:"tool_call"`
	Args             any          `json:"args,omitempty"`
	Context          AgentContext `json:"context"`
}

// BeforeToolCallResult can block tool execution during preflight.
type BeforeToolCallResult struct {
	Block  bool   `json:"block,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// BeforeToolCallHook runs before a tool body executes.
type BeforeToolCallHook func(context.Context, BeforeToolCallContext) (BeforeToolCallResult, error)

// AfterToolCallContext is passed to an after-tool hook.
type AfterToolCallContext struct {
	AssistantMessage Message      `json:"assistant_message"`
	ToolCall         ToolCall     `json:"tool_call"`
	Args             any          `json:"args,omitempty"`
	Context          AgentContext `json:"context"`
	Result           ToolResult   `json:"result"`
	IsError          bool         `json:"is_error"`
}

// AfterToolCallResult can override a tool result before it is emitted.
type AfterToolCallResult struct {
	Result  *ToolResult `json:"result,omitempty"`
	IsError *bool       `json:"is_error,omitempty"`
}

// AfterToolCallHook runs after a tool body completes.
type AfterToolCallHook func(context.Context, AfterToolCallContext) (AfterToolCallResult, error)

// TransformContext allows pruning or enriching messages before model conversion.
type TransformContext func(context.Context, []Message) ([]Message, error)

// ConvertToLLM filters or normalizes messages into model-compatible messages.
type ConvertToLLM func(context.Context, []Message) ([]Message, error)

// ModelRequest is the normalized request given to a model adapter.
type ModelRequest struct {
	Model           ModelRef         `json:"model"`
	SystemPrompt    string           `json:"system_prompt,omitempty"`
	Messages        []Message        `json:"messages,omitempty"`
	Tools           []ToolDefinition `json:"tools,omitempty"`
	ThinkingLevel   ThinkingLevel    `json:"thinking_level,omitempty"`
	SessionID       string           `json:"session_id,omitempty"`
	APIKey          string           `json:"api_key,omitempty"`
	Transport       Transport        `json:"transport,omitempty"`
	MaxRetryDelayMs int              `json:"max_retry_delay_ms,omitempty"`
	ThinkingBudgets ThinkingBudgets  `json:"thinking_budgets,omitempty"`
}

// AssistantEvent is a single low-level event from a model stream.
type AssistantEvent struct {
	Type         AssistantEventType `json:"type"`
	Message      Message            `json:"message"`
	Delta        string             `json:"delta,omitempty"`
	ToolCall     *ToolCall          `json:"tool_call,omitempty"`
	ContentIndex int                `json:"content_index,omitempty"`
	Reason       StopReason         `json:"reason,omitempty"`
	Err          error              `json:"-"`
}

// AssistantStream streams assistant deltas and returns a final assistant message.
type AssistantStream interface {
	Events() <-chan AssistantEvent
	Wait() (Message, error)
}

// StreamModel is the model contract consumed by the runtime engine.
type StreamModel interface {
	Stream(context.Context, ModelRequest) (AssistantStream, error)
}

// StreamFunc adapts a function to the StreamModel interface.
type StreamFunc func(context.Context, ModelRequest) (AssistantStream, error)

// Stream executes the function as a StreamModel implementation.
func (f StreamFunc) Stream(ctx context.Context, request ModelRequest) (AssistantStream, error) {
	return f(ctx, request)
}

// ModelResolver resolves a runtime model for the current snapshot.
type ModelResolver func(context.Context, ModelRef, AgentSnapshot) (StreamModel, error)

// ToolResolver resolves tools for the current snapshot.
type ToolResolver func(context.Context, AgentSnapshot) ([]ToolDefinition, error)

// DefinitionResolver produces an AgentDefinition from snapshot state.
type DefinitionResolver func(context.Context, AgentSnapshot) (AgentDefinition, error)

// NewTextMessage creates a single-text message with the provided role.
func NewTextMessage(role MessageRole, text string) Message {
	return Message{
		Role:      role,
		Parts:     []Part{{Type: PartTypeText, Text: text}},
		Timestamp: time.Now().UTC(),
	}
}

// NewToolResultMessage converts a tool result into a tool-role message.
func NewToolResultMessage(call ToolCall, result ToolResult, isError bool) Message {
	return Message{
		Role:  RoleTool,
		Parts: cloneParts(result.Content),
		ToolResult: &ToolResultPayload{
			ToolCallID:         call.ID,
			OriginalToolCallID: call.OriginalID,
			ToolName:           call.Name,
			Content:            cloneParts(result.Content),
			Details:            result.Details,
			IsError:            isError,
		},
		Timestamp: time.Now().UTC(),
	}
}
