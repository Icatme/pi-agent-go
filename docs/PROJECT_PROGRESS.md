# Project Progress

## Project Goals

### Core Goal

Build `pi-agent-go` as a standalone Go port of the original `pi-agent-core`,
with focus on the single-agent runtime rather than graph orchestration.

### In Scope

- Port the original single-agent runtime semantics into Go.
- Keep the prompt / continue / steer / follow-up model aligned with the source project.
- Preserve tool execution flow, runtime state, and event contracts as closely as practical.
- Provide a stable backend boundary through `StreamModel`.
- Allow optional integration with `langgraphgo` without changing `langgraphgo` core packages.

### Out of Scope

- Replacing `langgraphgo` as a graph runtime.
- Adding multi-agent, supervisor, planner, or tree-of-thoughts logic into the core package.
- Building provider-specific behavior directly into the core runtime.
- Carrying compatibility layers that do not solve a current, concrete need.

### Success Criteria

- `pi-agent-go` can be used as an independent Go package.
- Core runtime behavior stays close to the original `pi-agent-core`.
- Optional adapters stay thin and do not redefine the core contract.
- Backend integrations remain behind `StreamModel`.

## Current Status

The repository has been split out as an independent public project and can be built and tested on its own.

- GitHub repository: `github.com/Icatme/pi-agent-go`
- Module path: `github.com/Icatme/pi-agent-go`
- Local verification: `go test ./...`

## Completed Work

### Core Runtime

- Implemented the core agent runtime with prompt, continue, steer, follow-up, abort, and idle waiting behavior.
- Implemented runtime state tracking, including streaming status, pending tool calls, and error state.
- Extended pending tool-call tracking so runtime state can retain original provider tool ids alongside normalized ids.
- Implemented tool execution flow with before/after hooks and sequential or parallel execution modes.
- Implemented message conversion and context transformation boundaries.
- Expanded the message model to preserve provider/runtime fields such as `response_id`, `provider`, `api`, `model`, thinking signatures, and original tool call ids.

### Public API

- Added `AgentOptions` and initial-state oriented construction.
- Added package-level loop helpers mirroring the original runtime shape.
- Added prompt convenience methods for text and image input.
- Added custom message helpers without copying TypeScript-only declaration-merging patterns.
- Added built-in default provider resolution through `pi-go` when a `ModelRef{Provider, Model}` is configured.
- Added typed `ProviderConfig` / auth config on `ModelRef` so default provider execution no longer depends on ad-hoc metadata keys.

### Event Model

- Added assistant event types for start, text lifecycle, tool-call lifecycle, done, and error.
- Tightened runtime tests around streaming state, abort behavior, turn events, and tool-execution events.
- Verified the default `pi-go` provider path preserves reasoning deltas, tool-call deltas, replay signatures, raw tool ids, and provider response ids.

### Integration Layers

- Added a `langgraphgo` adapter layer for node/session integration.
- Normalized LangGraphGo integration so `SessionID == thread_id`.

## Current Boundaries

- Core focus remains the original `pi-agent-core` runtime.
- Multi-agent, supervisor, planner, and graph-native orchestration are out of scope for core.
- LangGraphGo integration is optional and should remain a thin adapter layer.
- `StreamModel` is the main long-term interface for model backends, with `pi-go` as the built-in default provider implementation.

## Known Design Notes

- Keeping optional adapters in the main module still pulls their dependency graph into the root `go.mod`.
- Provider-specific streaming fidelity now flows through the built-in `pi-go` path for supported providers, while custom `StreamModel` implementations can still define their own fidelity.
- Some integration-oriented files exist because the project was extracted from earlier work inside another repository; they should continue to be treated as optional layers.

## Recommended Next Steps

1. Continue checking remaining differences against the original `pi-agent-core` tests and contracts.
2. Tighten README wording so the core package, optional adapters, and non-goals are clearly separated.
3. Split optional adapters into separate modules if core dependency isolation becomes a priority.
4. Keep future backend integrations behind `StreamModel` instead of pushing provider details into the core runtime.
