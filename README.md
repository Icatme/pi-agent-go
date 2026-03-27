# pi-agent-go

`piagentgo` is a standalone agent runtime for `langgraphgo`.

It is intentionally separate from `prebuilt/` so it can evolve without requiring
changes to the upstream `langgraphgo` framework.

## What It Provides

- A serializable `AgentSnapshot`
- A user-facing `AgentOptions` / `InitialState`
- A low-level `AgentDefinition` / `DefinitionResolver`
- A turn-based `Engine` with:
  - assistant message streaming
  - tool execution
  - `beforeToolCall` / `afterToolCall`
  - `steer` / `followUp`
  - `continue`
- A higher-level `Agent` wrapper
- Package-level loop façades: `RunAgentLoop`, `RunAgentLoopContinue`
- Adapters for:
  - `langchaingo` models
  - `langgraphgo` graph nodes
  - a standard checkpoint-friendly `SessionState` graph wrapper

## Package Layout

- `piagentgo/`
  - core runtime types, state, engine, and agent wrapper
- `piagentgo/adapters/langchaingo`
  - adapts `llms.Model` to `piagentgo.StreamModel`
- `piagentgo/adapters/langgraphgo`
  - adapts `piagentgo` into `langgraphgo` graph nodes

## Direct Usage

```go
package main

import (
	"context"
	"fmt"

	core "github.com/Icatme/pi-agent-go"
	langchaingo "github.com/Icatme/pi-agent-go/adapters/langchaingo"
	"github.com/tmc/langchaingo/llms/openai"
)

func main() {
	model, _ := openai.New()

	agent, _ := core.NewAgentWithOptions(core.AgentOptions{
		Model: core.StreamFunc(func(ctx context.Context, request core.ModelRequest) (core.AssistantStream, error) {
			return langchaingo.NewModel(model).Stream(ctx, request)
		}),
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

`StreamModel` is the primary model abstraction. If you already have an
Entgateway-backed client, pass it directly. `StreamFunc` is only a convenience
adapter for function-style integration.

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
- user-facing agent construction via `AgentOptions`
- dynamic low-level agent definition when needed

Use `langgraphgo` for:

- multi-agent orchestration
- conditional routing
- checkpointing / time travel / HITL
- graph composition

## Current Limitations

- The `langchaingo` adapter maps text streaming directly, but provider-specific
  low-level delta types are still normalized when the backend cannot expose a
  richer event stream.
- The built-in graph wrapper is intentionally a single-session node. Multi-node
  orchestration and supervisor-style routing should still live in `langgraphgo`.
