# Testing

This repository uses two test layers:

- default offline tests, which must pass in normal development and CI
- gated live provider tests, which are intended for release checks and manual verification

## Default Offline Coverage

Run from the root module:

```powershell
go test ./...
```

Run the optional adapter module separately:

```powershell
cd adapters/langgraphgo
go test ./...
```

The default offline suite covers:

- core prompt / continue / steer / follow-up behavior
- tool execution lifecycle, including `beforeToolCall` / `afterToolCall`
- mixed replay invariants for provider metadata, reasoning signatures, images,
  raw tool ids, and tool results
- provider error mapping on the built-in `pi-go` path
- snapshot JSON round-trip for runtime state
- `adapters/langgraphgo` clone and JSON persistence round-trip
- concurrent request rejection and state consistency under burst message updates

## Race Checks

Recommended pre-release verification:

```powershell
go test ./... -race
cd adapters/langgraphgo
go test ./... -race
```

## Live Provider Tests

Live tests are gated and are not part of the default offline suite.

Enable them with:

```powershell
$env:PIAGENTGO_LIVE_TEST = "1"
go test ./... -run Live
```

Supported live providers:

- `anthropic / claude-sonnet-4-5`
- `kimi-coding / k2p5`
- `openai-codex / gpt-5.4`

Credentials are read from:

- Anthropic: `ANTHROPIC_API_KEY`
- Kimi: `KIMI_API_KEY`
- OpenAI Codex: `PIAGENTGO_OPENAI_CODEX_TOKEN` or `OPENAI_CODEX_TOKEN`

Expected behavior:

- If `PIAGENTGO_LIVE_TEST != 1`, live tests skip.
- If a provider credential is missing, only that provider case skips.
- Live tests are intended to validate:
  - basic prompt
  - multi-turn context retention
  - tool execution loop
  - abort after first streamed delta
  - continue from a user tail
  - continue from a tool-result tail

## Stable API Surface

The current stable core runtime surface is:

- `Agent`
- `Engine`
- `AgentDefinition`
- `ModelRef`
- `ProviderConfig`
- `Message`
- `ToolCall`
- `ToolResultPayload`
- `StreamModel`

Secondary integration surfaces:

- `prebuilt`
- `adapters/langgraphgo`

Non-goals for this test plan:

- no compatibility shim for legacy image URLs
- no provider matrix beyond providers supported by the built-in `pi-go` path
