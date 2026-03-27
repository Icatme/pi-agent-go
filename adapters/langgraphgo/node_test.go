package langgraphgo

import (
	"context"
	"testing"
	"time"

	"github.com/Icatme/pi-agent-go"
)

func TestNewSessionNodePromptClearsPrompts(t *testing.T) {
	t.Parallel()

	type state struct {
		Snapshot piagentgo.AgentSnapshot
		Prompts  []piagentgo.Message
	}

	node := NewSessionNode(nil, piagentgo.AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, _ piagentgo.ModelRequest) (piagentgo.AssistantStream, error) {
				return newStaticAssistantStream(piagentgo.Message{
					Role:       piagentgo.RoleAssistant,
					Parts:      []piagentgo.Part{{Type: piagentgo.PartTypeText, Text: "hello"}},
					Timestamp:  time.Now().UTC(),
					StopReason: piagentgo.StopReasonStop,
				}, nil), nil
			},
		},
	}, Binder[state]{
		GetSnapshot: func(s state) piagentgo.AgentSnapshot { return s.Snapshot },
		SetSnapshot: func(s state, snapshot piagentgo.AgentSnapshot) state {
			s.Snapshot = snapshot
			return s
		},
		GetPrompts: func(s state) []piagentgo.Message { return s.Prompts },
		SetPrompts: func(s state, prompts []piagentgo.Message) state {
			s.Prompts = prompts
			return s
		},
	})

	next, err := node(context.Background(), state{
		Prompts: []piagentgo.Message{piagentgo.NewTextMessage(piagentgo.RoleUser, "hi")},
	})
	if err != nil {
		t.Fatalf("node returned error: %v", err)
	}
	if len(next.Prompts) != 0 {
		t.Fatalf("expected prompts to be cleared, got %d", len(next.Prompts))
	}
	if got := len(next.Snapshot.Messages); got != 2 {
		t.Fatalf("expected 2 messages, got %d", got)
	}
}

func TestNewSessionNodeContinueMode(t *testing.T) {
	t.Parallel()

	type state struct {
		Snapshot piagentgo.AgentSnapshot
		Prompts  []piagentgo.Message
	}

	node := NewSessionNode(nil, piagentgo.AgentDefinition{
		Model: staticModel{
			streamFn: func(_ context.Context, request piagentgo.ModelRequest) (piagentgo.AssistantStream, error) {
				if len(request.Messages) != 1 || request.Messages[0].Role != piagentgo.RoleUser {
					t.Fatalf("expected continue to reuse existing user message, got %+v", request.Messages)
				}
				return newStaticAssistantStream(piagentgo.Message{
					Role:       piagentgo.RoleAssistant,
					Parts:      []piagentgo.Part{{Type: piagentgo.PartTypeText, Text: "continued"}},
					Timestamp:  time.Now().UTC(),
					StopReason: piagentgo.StopReasonStop,
				}, nil), nil
			},
		},
	}, Binder[state]{
		GetSnapshot: func(s state) piagentgo.AgentSnapshot { return s.Snapshot },
		SetSnapshot: func(s state, snapshot piagentgo.AgentSnapshot) state {
			s.Snapshot = snapshot
			return s
		},
		GetPrompts: func(s state) []piagentgo.Message { return s.Prompts },
	})

	next, err := node(context.Background(), state{
		Snapshot: piagentgo.AgentSnapshot{
			Messages: []piagentgo.Message{piagentgo.NewTextMessage(piagentgo.RoleUser, "resume me")},
		},
	})
	if err != nil {
		t.Fatalf("node returned error: %v", err)
	}
	if got := len(next.Snapshot.Messages); got != 2 {
		t.Fatalf("expected 2 messages, got %d", got)
	}
	if next.Snapshot.Messages[1].Parts[0].Text != "continued" {
		t.Fatalf("unexpected assistant text %q", next.Snapshot.Messages[1].Parts[0].Text)
	}
}

type staticModel struct {
	streamFn func(context.Context, piagentgo.ModelRequest) (piagentgo.AssistantStream, error)
}

func (m staticModel) Stream(ctx context.Context, request piagentgo.ModelRequest) (piagentgo.AssistantStream, error) {
	return m.streamFn(ctx, request)
}

type staticAssistantStream struct {
	events  chan piagentgo.AssistantEvent
	done    chan struct{}
	message piagentgo.Message
	err     error
}

func newStaticAssistantStream(message piagentgo.Message, events []piagentgo.AssistantEvent) *staticAssistantStream {
	stream := &staticAssistantStream{
		events:  make(chan piagentgo.AssistantEvent, len(events)),
		done:    make(chan struct{}),
		message: message,
	}
	go func() {
		for _, event := range events {
			stream.events <- event
		}
		close(stream.events)
		close(stream.done)
	}()
	return stream
}

func (s *staticAssistantStream) Events() <-chan piagentgo.AssistantEvent {
	return s.events
}

func (s *staticAssistantStream) Wait() (piagentgo.Message, error) {
	<-s.done
	return s.message, s.err
}
