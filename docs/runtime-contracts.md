# Runtime Contracts

This document defines the stable runtime contract for `pi-agent-go`.

## Message Model

The canonical runtime envelope is [`Message`](/V:/gitdownload/pi-agent-go/types.go).

- `RoleUser` messages use `Parts`.
- `RoleAssistant` messages use `Parts`, `ToolCalls`, and provider metadata fields:
  `Provider`, `API`, `Model`, `ResponseID`.
- `RoleTool` messages use `ToolResult`.
- `RoleCustom` is allowed in runtime state, but must be mapped by `ConvertToLLM`
  before model execution if the selected model cannot consume it directly.

### Parts

- `PartTypeText` carries plain text in `Part.Text`.
- `PartTypeImage` carries provider-ready base64 data in `Part.Data` and MIME type
  in `Part.MIMEType`.
- `PartTypeThinking` carries reasoning text in `Part.Text`, optional replay
  signature in `Part.Signature`, and optional redaction marker in `Part.Redacted`.

### Image Contract

- Images are accepted only as `data + mime_type`.
- Remote image URLs are not part of the contract.
- `NewImagePart(data, mimeType)` is the supported constructor for image input.

### Provider Metadata Contract

Assistant messages may preserve provider-origin metadata:

- `ResponseID`
- `Provider`
- `API`
- `Model`

These fields are part of the stable runtime shape and may be preserved through
streaming, replay, snapshotting, and resume.

### Tool Replay Contract

Assistant tool calls and tool results preserve both normalized and raw provider ids.

- `ToolCall.ID` is the normalized runtime id.
- `ToolCall.OriginalID` is the provider-native id when available.
- `ToolResultPayload.ToolCallID` is the normalized runtime id.
- `ToolResultPayload.OriginalToolCallID` is the provider-native id when available.
- Replay prefers `OriginalID` / `OriginalToolCallID` when sending historical tool
  blocks back through the built-in `pi-go` provider path.

### Thinking Replay Contract

- Historical thinking content is replayed as `PartTypeThinking`.
- `Part.Signature` is preserved during replay when the provider supports
  signature-aware reasoning blocks.
- `Part.Redacted` marks reasoning that should remain hidden from user-facing
  presentation while still preserving its runtime classification.

## Tool Hook Contract

The tool hook surface lives in [`types.go`](/V:/gitdownload/pi-agent-go/types.go)
and is executed by the runtime engine in [`engine.go`](/V:/gitdownload/pi-agent-go/engine.go).

### `beforeToolCall`

- `BeforeToolCallContext.Args` is mutable.
- Mutations made by `beforeToolCall` are used by the tool body and are visible to
  `afterToolCall`.
- Returning `BeforeToolCallResult{Block: true}` prevents the tool body from
  executing.
- A blocked tool call is encoded as an error tool-result message and emits the
  normal tool execution lifecycle events.
- Returning an error from `beforeToolCall` stops the current turn and is encoded
  as a runtime error path.

### `afterToolCall`

- `afterToolCall` runs after the tool body returns.
- It may override `Result`.
- It may override `IsError`.
- Override order is: tool body result first, then `afterToolCall` may replace the
  result and error flag.
- Returning an error from `afterToolCall` stops the current turn and is encoded
  as a runtime error path.

## Provider Config Contract

The typed provider runtime configuration lives in [`ProviderConfig`](/V:/gitdownload/pi-agent-go/types.go).

- `BaseURL`: override the provider base URL.
- `APIKey`: explicit API key or bearer token.
- `Headers`: additional request headers.
- `Auth`: typed provider auth payload for provider-specific auth flows.

### Resolution Priority

For the built-in default provider path:

1. `ModelRequest.APIKey`
2. `ModelRef.ProviderConfig.APIKey`
3. Provider environment key resolved by the provider implementation

`Auth` is only applied when the selected provider implementation supports and
needs that auth payload.

### Non-Contract Fields

- `ModelRef.Metadata` remains available for general metadata.
- `Metadata` is not the provider runtime configuration surface.
- New provider runtime settings should be added to typed config, not tunneled
  through ad-hoc metadata keys.

## Snapshot And Resume Contract

- `AgentSnapshot` is the durable runtime state shape.
- Snapshot serialization must preserve:
  - `ModelRef.ProviderConfig`
  - `Message.Provider` / `API` / `Model` / `ResponseID`
  - `Part.Signature`
  - `ToolCall.OriginalID`
  - `ToolResultPayload.OriginalToolCallID`
  - `PendingToolCall.OriginalToolCallID`
- The `adapters/langgraphgo` session wrapper must preserve the same fields across
  clone and JSON checkpoint persistence.
