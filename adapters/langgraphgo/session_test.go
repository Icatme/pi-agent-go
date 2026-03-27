package langgraphgo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Icatme/pi-agent-go"
	"github.com/smallnest/langgraphgo/graph"
)

func TestSessionStateNodeConsumesSteeringAfterAssistant(t *testing.T) {
	t.Parallel()

	node := NewSessionStateNode(nil, piagentgo.AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, request piagentgo.ModelRequest) (piagentgo.AssistantStream, error) {
				if len(request.Messages) != 2 {
					t.Fatalf("expected steering prompt to be appended, got %d messages", len(request.Messages))
				}
				if got := request.Messages[1].Parts[0].Text; got != "nudge" {
					t.Fatalf("expected steering message to be consumed first, got %q", got)
				}
				return newStaticAssistantStream(piagentgo.Message{
					Role:       piagentgo.RoleAssistant,
					Parts:      []piagentgo.Part{{Type: piagentgo.PartTypeText, Text: "handled"}},
					Timestamp:  time.Now().UTC(),
					StopReason: piagentgo.StopReasonStop,
				}, nil), nil
			},
		},
	})

	next, err := node(context.Background(), SessionState{
		Snapshot: piagentgo.AgentSnapshot{
			Messages: []piagentgo.Message{
				{
					Role:       piagentgo.RoleAssistant,
					Parts:      []piagentgo.Part{{Type: piagentgo.PartTypeText, Text: "done"}},
					Timestamp:  time.Now().UTC(),
					StopReason: piagentgo.StopReasonStop,
				},
			},
		},
		Steering: []piagentgo.Message{
			piagentgo.NewTextMessage(piagentgo.RoleUser, "nudge"),
		},
		Mode: RunModeContinue,
	})
	if err != nil {
		t.Fatalf("node returned error: %v", err)
	}
	if len(next.Steering) != 0 {
		t.Fatalf("expected steering queue to be cleared, got %d", len(next.Steering))
	}
	if next.Mode != "" {
		t.Fatalf("expected mode to be cleared, got %q", next.Mode)
	}
	if got := len(next.Snapshot.Messages); got != 3 {
		t.Fatalf("expected 3 messages after continue, got %d", got)
	}
	if got := next.Snapshot.Messages[2].Parts[0].Text; got != "handled" {
		t.Fatalf("unexpected assistant text %q", got)
	}
}

func TestCheckpointableSessionStateGraphAutoResumeWithPrompt(t *testing.T) {
	t.Parallel()

	runnable, ctx, threadID := newCheckpointableSessionRunnable(t)

	first, err := runnable.InvokeWithConfig(ctx,
		PromptUpdate(piagentgo.NewTextMessage(piagentgo.RoleUser, "first")),
		graph.WithThreadID(threadID),
	)
	if err != nil {
		t.Fatalf("first invoke failed: %v", err)
	}
	if got := len(first.Snapshot.Messages); got != 2 {
		t.Fatalf("expected first run to produce 2 messages, got %d", got)
	}

	second, err := runnable.InvokeWithConfig(ctx,
		PromptUpdate(piagentgo.NewTextMessage(piagentgo.RoleUser, "second")),
		graph.WithThreadID(threadID),
	)
	if err != nil {
		t.Fatalf("second invoke failed: %v", err)
	}

	if got := len(second.Snapshot.Messages); got != 4 {
		t.Fatalf("expected resumed run to keep history, got %d messages", got)
	}
	if got := second.Snapshot.Messages[3].Parts[0].Text; got != "echo: second" {
		t.Fatalf("unexpected assistant text %q", got)
	}
}

func TestCheckpointableSessionStateGraphUsesThreadIDAsSessionID(t *testing.T) {
	t.Parallel()

	threadID := t.Name()
	g := NewCheckpointableSessionStateGraph(nil, piagentgo.AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, request piagentgo.ModelRequest) (piagentgo.AssistantStream, error) {
				if request.SessionID != threadID {
					t.Fatalf("expected model request session %q, got %q", threadID, request.SessionID)
				}
				return newStaticAssistantStream(piagentgo.Message{
					Role:       piagentgo.RoleAssistant,
					Parts:      []piagentgo.Part{{Type: piagentgo.PartTypeText, Text: "echo: " + lastUserText(request.Messages)}},
					Timestamp:  time.Now().UTC(),
					StopReason: piagentgo.StopReasonStop,
				}, nil), nil
			},
		},
	})

	runnable, err := g.CompileCheckpointable()
	if err != nil {
		t.Fatalf("compile checkpointable failed: %v", err)
	}

	result, err := runnable.InvokeWithConfig(context.Background(),
		PromptUpdate(piagentgo.NewTextMessage(piagentgo.RoleUser, "hello")),
		graph.WithThreadID(threadID),
	)
	if err != nil {
		t.Fatalf("invoke failed: %v", err)
	}
	if result.Snapshot.SessionID != threadID {
		t.Fatalf("expected snapshot session %q, got %q", threadID, result.Snapshot.SessionID)
	}

	loaded, _, err := LoadSessionState(context.Background(), runnable, graph.WithThreadID(threadID))
	if err != nil {
		t.Fatalf("load session state failed: %v", err)
	}
	if loaded.Snapshot.SessionID != threadID {
		t.Fatalf("expected loaded session %q, got %q", threadID, loaded.Snapshot.SessionID)
	}
}

func TestUpdateSessionStateMergesPromptAndResumes(t *testing.T) {
	t.Parallel()

	runnable, ctx, threadID := newCheckpointableSessionRunnable(t)

	_, err := runnable.InvokeWithConfig(ctx,
		PromptUpdate(piagentgo.NewTextMessage(piagentgo.RoleUser, "first")),
		graph.WithThreadID(threadID),
	)
	if err != nil {
		t.Fatalf("initial invoke failed: %v", err)
	}

	config, err := UpdateSessionState(ctx, runnable, graph.WithThreadID(threadID),
		PromptUpdate(piagentgo.NewTextMessage(piagentgo.RoleUser, "second")),
	)
	if err != nil {
		t.Fatalf("update session state failed: %v", err)
	}
	if config == nil || config.Configurable == nil || config.Configurable["checkpoint_id"] == nil {
		t.Fatal("expected checkpoint_id from UpdateSessionState")
	}

	snapshot, err := runnable.GetState(ctx, config)
	if err != nil {
		t.Fatalf("get state failed: %v", err)
	}
	queuedState := snapshot.Values.(SessionState)
	if got := len(queuedState.Prompts); got != 1 {
		t.Fatalf("expected queued prompt to be checkpointed, got %d", got)
	}

	resumed, err := ResumeSession(ctx, runnable, config)
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if got := len(resumed.Snapshot.Messages); got != 4 {
		t.Fatalf("expected resumed history to contain 4 messages, got %d", got)
	}
	if got := resumed.Snapshot.Messages[3].Parts[0].Text; got != "echo: second" {
		t.Fatalf("unexpected assistant text %q", got)
	}
}

func TestCheckpointableSessionStateGraphInterruptAfterAndResume(t *testing.T) {
	t.Parallel()

	runnable, ctx, threadID := newCheckpointableSessionRunnable(t)
	config := graph.WithThreadID(threadID)
	config.InterruptAfter = []string{SessionNodeName}

	result, err := runnable.InvokeWithConfig(ctx,
		PromptUpdate(piagentgo.NewTextMessage(piagentgo.RoleUser, "phase one")),
		config,
	)
	if err == nil {
		t.Fatal("expected interrupt after session node")
	}

	var interrupt *graph.GraphInterrupt
	if !errors.As(err, &interrupt) {
		t.Fatalf("expected GraphInterrupt, got %v", err)
	}
	if interrupt.Node != SessionNodeName {
		t.Fatalf("expected interrupt at %q, got %q", SessionNodeName, interrupt.Node)
	}
	if got := len(result.Snapshot.Messages); got != 2 {
		t.Fatalf("expected interrupted state to keep session output, got %d messages", got)
	}

	resumeConfig, err := UpdateSessionState(ctx, runnable, graph.WithThreadID(threadID),
		PromptUpdate(piagentgo.NewTextMessage(piagentgo.RoleUser, "phase two")),
	)
	if err != nil {
		t.Fatalf("update session state failed: %v", err)
	}

	resumed, err := ResumeSession(ctx, runnable, resumeConfig)
	if err != nil {
		t.Fatalf("resume after interrupt failed: %v", err)
	}
	if got := len(resumed.Snapshot.Messages); got != 4 {
		t.Fatalf("expected resumed conversation to contain 4 messages, got %d", got)
	}
	if got := resumed.Snapshot.Messages[3].Parts[0].Text; got != "echo: phase two" {
		t.Fatalf("unexpected assistant text %q", got)
	}
}

func TestLoadSessionStateNormalizesThreadIDIntoSnapshot(t *testing.T) {
	t.Parallel()

	runnable, ctx, threadID := newCheckpointableSessionRunnable(t)

	rawState := SessionState{
		Snapshot: piagentgo.AgentSnapshot{
			Messages: []piagentgo.Message{
				piagentgo.NewTextMessage(piagentgo.RoleUser, "hello"),
			},
		},
	}

	config, err := runnable.UpdateState(ctx, graph.WithThreadID(threadID), SessionNodeName, rawState)
	if err != nil {
		t.Fatalf("update state failed: %v", err)
	}

	loaded, _, err := LoadSessionState(ctx, runnable, config)
	if err != nil {
		t.Fatalf("load session state failed: %v", err)
	}
	if loaded.Snapshot.SessionID != threadID {
		t.Fatalf("expected normalized session %q, got %q", threadID, loaded.Snapshot.SessionID)
	}
}

func newCheckpointableSessionRunnable(t *testing.T) (*graph.CheckpointableRunnable[SessionState], context.Context, string) {
	t.Helper()

	g := NewCheckpointableSessionStateGraph(nil, piagentgo.AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, request piagentgo.ModelRequest) (piagentgo.AssistantStream, error) {
				return newStaticAssistantStream(piagentgo.Message{
					Role:       piagentgo.RoleAssistant,
					Parts:      []piagentgo.Part{{Type: piagentgo.PartTypeText, Text: "echo: " + lastUserText(request.Messages)}},
					Timestamp:  time.Now().UTC(),
					StopReason: piagentgo.StopReasonStop,
				}, nil), nil
			},
		},
	})

	runnable, err := g.CompileCheckpointable()
	if err != nil {
		t.Fatalf("compile checkpointable failed: %v", err)
	}
	return runnable, context.Background(), t.Name()
}

func lastUserText(messages []piagentgo.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if message.Role != piagentgo.RoleUser {
			continue
		}
		for _, part := range message.Parts {
			if part.Type == piagentgo.PartTypeText {
				return part.Text
			}
		}
	}
	return ""
}
