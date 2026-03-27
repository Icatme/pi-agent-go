// Package langgraphgo adapts piagentgo runtimes into langgraphgo nodes.
package langgraphgo

import (
	"context"

	"github.com/Icatme/pi-agent-go"
	"github.com/smallnest/langgraphgo/graph"
)

// RunMode controls how a graph-bound agent node should execute.
type RunMode string

const (
	// RunModePrompt appends prompts and executes the runtime.
	RunModePrompt RunMode = "prompt"
	// RunModeContinue resumes execution from existing snapshot state.
	RunModeContinue RunMode = "continue"
	// RunModeSkip leaves state unchanged.
	RunModeSkip RunMode = "skip"
)

// Binder maps graph state into and out of a piagentgo snapshot.
type Binder[S any] struct {
	GetSnapshot         func(S) piagentgo.AgentSnapshot
	SetSnapshot         func(S, piagentgo.AgentSnapshot) S
	GetPrompts          func(S) []piagentgo.Message
	SetPrompts          func(S, []piagentgo.Message) S
	GetMode             func(S) RunMode
	SetMode             func(S, RunMode) S
	SelectMode          func(S, piagentgo.AgentSnapshot, []piagentgo.Message) RunMode
	ResolveDefinition   func(context.Context, S, piagentgo.AgentSnapshot, piagentgo.AgentDefinition) (piagentgo.AgentDefinition, error)
	GetSteeringMessages func(context.Context, S, piagentgo.AgentSnapshot) ([]piagentgo.Message, error)
	SetSteeringMessages func(S, []piagentgo.Message) S
	GetFollowUpMessages func(context.Context, S, piagentgo.AgentSnapshot) ([]piagentgo.Message, error)
	SetFollowUpMessages func(S, []piagentgo.Message) S
}

// NewTurnNode wraps a piagentgo engine into a single langgraphgo node.
func NewTurnNode[S any](engine *piagentgo.Engine, definition piagentgo.AgentDefinition, binder Binder[S]) func(context.Context, S) (S, error) {
	binder.SelectMode = func(_ S, _ piagentgo.AgentSnapshot, _ []piagentgo.Message) RunMode {
		return RunModePrompt
	}
	return NewSessionNode(engine, definition, binder)
}

// NewSessionNode wraps a piagentgo engine into a stateful graph node that can prompt, continue, or skip.
func NewSessionNode[S any](engine *piagentgo.Engine, definition piagentgo.AgentDefinition, binder Binder[S]) func(context.Context, S) (S, error) {
	if engine == nil {
		engine = piagentgo.NewEngine()
	}

	return func(ctx context.Context, state S) (S, error) {
		threadID := threadIDFromContext(ctx)
		snapshot := normalizeSnapshotSessionID(binder.GetSnapshot(state), threadID)
		prompts := binder.GetPrompts(state)
		mode := defaultRunMode(state, binder, snapshot, prompts)

		var (
			nextSnapshot *piagentgo.AgentSnapshot
			err          error
		)
		switch mode {
		case RunModeSkip:
			nextSnapshot = &snapshot
		case RunModeContinue:
			pending, nextState, pendingErr := consumeContinueMessages(ctx, state, snapshot, binder)
			if pendingErr != nil {
				return state, pendingErr
			}
			state = nextState
			hooks := loopHooks(state, snapshot, binder)
			if len(pending) > 0 {
				nextSnapshot, err = engine.RunWithHooks(ctx, definition, &snapshot, pending, nil, hooks)
			} else {
				nextSnapshot, err = engine.ContinueWithHooks(ctx, definition, &snapshot, nil, hooks)
			}
		default:
			hooks := loopHooks(state, snapshot, binder)
			nextSnapshot, err = engine.RunWithHooks(ctx, definition, &snapshot, prompts, nil, hooks)
		}
		if err != nil {
			return state, err
		}
		normalizedSnapshot := normalizeSnapshotSessionID(*nextSnapshot, threadID)

		if binder.SetPrompts != nil {
			state = binder.SetPrompts(state, nil)
		}
		if binder.SetSteeringMessages != nil {
			state = binder.SetSteeringMessages(state, nil)
		}
		if binder.SetFollowUpMessages != nil {
			state = binder.SetFollowUpMessages(state, nil)
		}
		if binder.SetMode != nil {
			state = binder.SetMode(state, "")
		}
		return binder.SetSnapshot(state, normalizedSnapshot), nil
	}
}

// NewTurnGraph creates a minimal graph with a single piagentgo-backed turn node.
func NewTurnGraph[S any](engine *piagentgo.Engine, definition piagentgo.AgentDefinition, binder Binder[S]) *graph.StateGraph[S] {
	binder.SelectMode = func(_ S, _ piagentgo.AgentSnapshot, _ []piagentgo.Message) RunMode {
		return RunModePrompt
	}
	return NewSessionGraph(engine, definition, binder)
}

// NewSessionGraph creates a minimal graph with a single piagentgo-backed session node.
func NewSessionGraph[S any](engine *piagentgo.Engine, definition piagentgo.AgentDefinition, binder Binder[S]) *graph.StateGraph[S] {
	g := graph.NewStateGraph[S]()
	g.AddNode(SessionNodeName, SessionNodeDescription, NewSessionNode(engine, definition, binder))
	g.SetEntryPoint(SessionNodeName)
	g.AddEdge(SessionNodeName, graph.END)
	return g
}

func defaultRunMode[S any](state S, binder Binder[S], snapshot piagentgo.AgentSnapshot, prompts []piagentgo.Message) RunMode {
	if binder.SelectMode != nil {
		return binder.SelectMode(state, snapshot, prompts)
	}
	if binder.GetMode != nil {
		if mode := binder.GetMode(state); mode != "" {
			return mode
		}
	}
	if len(prompts) > 0 {
		return RunModePrompt
	}
	if len(snapshot.Messages) == 0 {
		return RunModeSkip
	}
	if snapshot.Messages[len(snapshot.Messages)-1].Role == piagentgo.RoleAssistant {
		return RunModeSkip
	}
	return RunModeContinue
}

func loopHooks[S any](state S, snapshot piagentgo.AgentSnapshot, binder Binder[S]) piagentgo.LoopHooks {
	return piagentgo.LoopHooks{
		ResolveDefinition: func(ctx context.Context, current piagentgo.AgentDefinition, snapshot piagentgo.AgentSnapshot) (piagentgo.AgentDefinition, error) {
			if binder.ResolveDefinition == nil {
				return current, nil
			}
			return binder.ResolveDefinition(ctx, state, snapshot, current)
		},
		GetSteeringMessages: func(ctx context.Context) ([]piagentgo.Message, error) {
			if binder.GetSteeringMessages == nil {
				return nil, nil
			}
			return binder.GetSteeringMessages(ctx, state, snapshot)
		},
		GetFollowUpMessages: func(ctx context.Context) ([]piagentgo.Message, error) {
			if binder.GetFollowUpMessages == nil {
				return nil, nil
			}
			return binder.GetFollowUpMessages(ctx, state, snapshot)
		},
	}
}

func consumeContinueMessages[S any](ctx context.Context, state S, snapshot piagentgo.AgentSnapshot, binder Binder[S]) ([]piagentgo.Message, S, error) {
	if binder.GetSteeringMessages != nil && binder.SetSteeringMessages != nil {
		messages, err := binder.GetSteeringMessages(ctx, state, snapshot)
		if err != nil {
			return nil, state, err
		}
		if len(messages) > 0 {
			return messages, binder.SetSteeringMessages(state, nil), nil
		}
	}

	if binder.GetFollowUpMessages != nil && binder.SetFollowUpMessages != nil {
		messages, err := binder.GetFollowUpMessages(ctx, state, snapshot)
		if err != nil {
			return nil, state, err
		}
		if len(messages) > 0 {
			return messages, binder.SetFollowUpMessages(state, nil), nil
		}
	}

	return nil, state, nil
}

func threadIDFromContext(ctx context.Context) string {
	return threadIDFromConfig(graph.GetConfig(ctx))
}

func threadIDFromConfig(config *graph.Config) string {
	if config == nil || config.Configurable == nil {
		return ""
	}
	threadID, _ := config.Configurable["thread_id"].(string)
	return threadID
}

func normalizeSnapshotSessionID(snapshot piagentgo.AgentSnapshot, threadID string) piagentgo.AgentSnapshot {
	if threadID == "" {
		return snapshot
	}
	snapshot.SessionID = threadID
	return snapshot
}
