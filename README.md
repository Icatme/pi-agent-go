# pi-agent-go

`piagentgo` is a standalone agent runtime for `langgraphgo`.

It is intentionally separate from `prebuilt/` so it can evolve without requiring
changes to the upstream `langgraphgo` framework.

## Project Goals

Core goals:

- Port the original `pi-agent-core` single-agent runtime into idiomatic Go.
- Preserve the original runtime behavior where it matters:
  - prompt / continue / steer / follow-up
  - assistant message lifecycle
  - tool execution lifecycle
  - runtime state and event flow
- Keep `StreamModel` as the stable backend boundary so providers and gateways can
  be integrated without pushing transport logic into the core runtime.
- Use `pi-go` as the built-in default provider implementation when a
  `ModelRef{Provider, Model}` is configured.
- Keep the package usable on its own and also usable as a thin integration layer
  inside `langgraphgo`.

Non-goals:

- This repository is not a replacement for `langgraphgo` graph orchestration.
- This repository does not aim to own multi-agent, supervisor, planner, or
  tree-of-thoughts workflows in the core package.
- This repository should not require modifications to `langgraphgo` core
  packages in order to work.

Success means:

- the core package can run independently in Go
- the core package stays aligned with the original `pi-agent-core` semantics
- integration with `langgraphgo` remains a thin adapter, not a forked runtime
- backend integrations happen behind `StreamModel`, not inside the agent loop

## What It Provides

- A serializable `AgentSnapshot`
- A user-facing `AgentOptions` / `InitialState`
- A low-level `AgentDefinition` / `DefinitionResolver`
- A built-in default `StreamModel` backed by `pi-go` provider implementations
- A turn-based `Engine` with:
  - assistant message streaming
  - tool execution
  - `beforeToolCall` / `afterToolCall`
  - `steer` / `followUp`
  - `continue`
- A higher-level `Agent` wrapper
- Package-level loop façades: `RunAgentLoop`, `RunAgentLoopContinue`
- Adapters for:
  - `langgraphgo` graph nodes
  - a standard checkpoint-friendly `SessionState` graph wrapper

## Package Layout

- `piagentgo/`
  - core runtime types, state, engine, and agent wrapper
- `piagentgo/adapters/langgraphgo`
  - adapts `piagentgo` into `langgraphgo` graph nodes

## Direct Usage

```go
package main

import (
	"context"
	"fmt"

	core "github.com/Icatme/pi-agent-go"
)

type echoModel struct{}

func (echoModel) Stream(ctx context.Context, request core.ModelRequest) (core.AssistantStream, error) {
	panic("implement StreamModel with your provider client")
}

func main() {
	agent, _ := core.NewAgentWithOptions(core.AgentOptions{
		Model: echoModel{},
		InitialState: core.AgentInitialState{
			SystemPrompt: "You are a helpful assistant.",
		},
	})

	agent.Subscribe(func(event core.AgentEvent) {
		if event.Type == core.EventMessageUpdate {
			fmt.Print(event.Delta)
		}
	})

	_ = agent.PromptText(context.Background(), "Hello")
}
```

`StreamModel` is the primary model abstraction. Integrate providers by
implementing that interface directly, or use `StreamFunc` when a function-style
adapter is enough.

If you only need built-in provider execution, set `InitialState.ModelRef` or
`AgentDefinition.DefaultModel` with `Provider` and `Model`. The runtime will
resolve the default `pi-go` provider implementation automatically.

`ModelRef.ProviderConfig` carries typed provider runtime settings when needed:

- `base_url`: override the provider base URL
- `api_key`: explicit API key or bearer token
- `headers`: extra request headers as `map[string]string`
- `auth`: typed provider auth config for provider-specific auth flows

## Graph Usage

For the common case, use the built-in `SessionState` instead of writing a
custom binder. It already includes:

- durable `Snapshot`
- queued `Prompts`
- queued `Steering`
- queued `FollowUps`
- `Mode`

```go
package main

import (
	"context"

	langgraph "github.com/smallnest/langgraphgo/graph"
	"github.com/Icatme/pi-agent-go"
	adapter "github.com/Icatme/pi-agent-go/adapters/langgraphgo"
)

func main() {
	definition := piagentgo.AgentDefinition{
		SystemPrompt: "You are a helpful assistant.",
		// Model: ...
	}

	sessionGraph := adapter.NewCheckpointableSessionStateGraph(nil, definition)
	runnable, _ := sessionGraph.CompileCheckpointable()

	threadConfig := langgraph.WithThreadID("demo-thread")

	result, _ := runnable.InvokeWithConfig(
		context.Background(),
		adapter.PromptUpdate(
			piagentgo.NewTextMessage(piagentgo.RoleUser, "Hello"),
		),
		threadConfig,
	)

	resumeConfig, _ := adapter.UpdateSessionState(
		context.Background(),
		runnable,
		threadConfig,
		adapter.PromptUpdate(
			piagentgo.NewTextMessage(piagentgo.RoleUser, "Continue"),
		),
	)

	result, _ = adapter.ResumeSession(
		context.Background(),
		runnable,
		resumeConfig,
	)
	_ = result
}
```

If you need a custom outer state shape, keep using `Binder[S]`. The recommended
pattern is:

- use `SessionState` directly when the graph state is only agent session data
- use `Binder[S]` when the graph state also carries unrelated workflow fields
- use `SessionStateSchema()` or the same merge rule when you expect checkpoint
  resume or `UpdateState`

## Current Scope

This package is meant to be the single-agent runtime core, not a full
replacement for `langgraphgo` graphs.

Use `piagentgo` for:

- single-agent runtime behavior
- message lifecycle
- tool lifecycle
- assistant messages that preserve `response_id`, `provider`, `api`, `model`,
  thinking signatures, and original tool call ids
- user-facing agent construction via `AgentOptions`
- dynamic low-level agent definition when needed

Use `langgraphgo` for:

- multi-agent orchestration
- conditional routing
- checkpointing / time travel / HITL
- graph composition

## Current Limitations

- The built-in graph wrapper is intentionally a single-session node. Multi-node
  orchestration and supervisor-style routing should still live in `langgraphgo`.
