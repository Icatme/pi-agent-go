package langgraphgo

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Icatme/pi-agent-go"
	"github.com/smallnest/langgraphgo/graph"
)

type supervisorState struct {
	Messages []piagentgo.Message
	Next     string
	Steps    []string
	Mode     string
}

func TestCompileSupervisorRoutesToSelectedMember(t *testing.T) {
	t.Parallel()

	router := &supervisorRouterModel{}
	registry, err := NewSupervisorRegistry(
		RegisteredSupervisorMember[supervisorState]{
			Name:        "researcher",
			Description: "Collects facts",
			Runnable:    compileSupervisorWorker(t, "researcher"),
		},
		RegisteredSupervisorMember[supervisorState]{
			Name:        "writer",
			Description: "Produces prose",
			Runnable:    compileSupervisorWorker(t, "writer"),
		},
	)
	if err != nil {
		t.Fatalf("NewSupervisorRegistry returned error: %v", err)
	}

	runnable, err := registry.CompileSupervisor(SupervisorConfig[supervisorState]{
		Router: piagentgo.AgentDefinition{
			Model: router,
		},
		GetMessages: func(state supervisorState) []piagentgo.Message { return state.Messages },
		GetNext:     func(state supervisorState) string { return state.Next },
		SetNext: func(state supervisorState, next string) supervisorState {
			state.Next = next
			return state
		},
	}, "researcher")
	if err != nil {
		t.Fatalf("CompileSupervisor returned error: %v", err)
	}

	result, err := runnable.Invoke(context.Background(), supervisorState{
		Messages: []piagentgo.Message{piagentgo.NewTextMessage(piagentgo.RoleUser, "please research this topic")},
	})
	if err != nil {
		t.Fatalf("Invoke returned error: %v", err)
	}
	if got := len(result.Steps); got != 1 {
		t.Fatalf("expected one worker step, got %d", got)
	}
	if result.Steps[0] != "researcher" {
		t.Fatalf("expected researcher to run, got %q", result.Steps[0])
	}
	if len(router.requests) < 2 {
		t.Fatalf("expected router to be called twice, got %d", len(router.requests))
	}
	if got := routeOptions(router.requests[0]); len(got) != 2 || got[0] != "researcher" || got[1] != supervisorFinish {
		t.Fatalf("unexpected first route options: %#v", got)
	}
}

func TestCompileSupervisorForStateUsesSelector(t *testing.T) {
	t.Parallel()

	router := &supervisorRouterModel{}
	registry, err := NewSupervisorRegistry(
		RegisteredSupervisorMember[supervisorState]{
			Name:     "researcher",
			Runnable: compileSupervisorWorker(t, "researcher"),
		},
		RegisteredSupervisorMember[supervisorState]{
			Name:     "writer",
			Runnable: compileSupervisorWorker(t, "writer"),
		},
	)
	if err != nil {
		t.Fatalf("NewSupervisorRegistry returned error: %v", err)
	}

	initial := supervisorState{
		Messages: []piagentgo.Message{piagentgo.NewTextMessage(piagentgo.RoleUser, "delegate this to the writer")},
		Mode:     "writer-only",
	}
	runnable, err := registry.CompileSupervisorForState(context.Background(), initial, func(_ context.Context, state supervisorState, members []RegisteredSupervisorMember[supervisorState]) ([]string, error) {
		if state.Mode != "writer-only" {
			t.Fatalf("unexpected selector mode %q", state.Mode)
		}
		if len(members) != 2 {
			t.Fatalf("expected selector to receive all members, got %d", len(members))
		}
		return []string{"writer"}, nil
	}, SupervisorConfig[supervisorState]{
		Router: piagentgo.AgentDefinition{
			Model: router,
		},
		GetMessages: func(state supervisorState) []piagentgo.Message { return state.Messages },
		GetNext:     func(state supervisorState) string { return state.Next },
		SetNext: func(state supervisorState, next string) supervisorState {
			state.Next = next
			return state
		},
	})
	if err != nil {
		t.Fatalf("CompileSupervisorForState returned error: %v", err)
	}

	result, err := runnable.Invoke(context.Background(), initial)
	if err != nil {
		t.Fatalf("Invoke returned error: %v", err)
	}
	if got := len(result.Steps); got != 1 || result.Steps[0] != "writer" {
		t.Fatalf("expected writer step, got %#v", result.Steps)
	}
	if got := routeOptions(router.requests[0]); len(got) != 2 || got[0] != "writer" || got[1] != supervisorFinish {
		t.Fatalf("unexpected selected route options: %#v", got)
	}
}

func TestCompileSupervisorRouterUsesThreadIDAsSessionID(t *testing.T) {
	t.Parallel()

	threadID := t.Name()
	router := &supervisorRouterModel{}
	registry, err := NewSupervisorRegistry[supervisorState]()
	if err != nil {
		t.Fatalf("NewSupervisorRegistry returned error: %v", err)
	}

	runnable, err := registry.CompileSupervisor(SupervisorConfig[supervisorState]{
		Router: piagentgo.AgentDefinition{
			Model: router,
		},
		GetMessages: func(state supervisorState) []piagentgo.Message { return state.Messages },
		GetNext:     func(state supervisorState) string { return state.Next },
		SetNext: func(state supervisorState, next string) supervisorState {
			state.Next = next
			return state
		},
	})
	if err != nil {
		t.Fatalf("CompileSupervisor returned error: %v", err)
	}

	result, err := runnable.InvokeWithConfig(context.Background(), supervisorState{
		Messages: []piagentgo.Message{piagentgo.NewTextMessage(piagentgo.RoleUser, "nothing to do")},
	}, graph.WithThreadID(threadID))
	if err != nil {
		t.Fatalf("InvokeWithConfig returned error: %v", err)
	}
	if result.Next != supervisorFinish {
		t.Fatalf("expected FINISH, got %q", result.Next)
	}
	if len(router.requests) != 1 {
		t.Fatalf("expected one router request, got %d", len(router.requests))
	}
	if router.requests[0].SessionID != threadID {
		t.Fatalf("expected router SessionID %q, got %q", threadID, router.requests[0].SessionID)
	}
}

func TestCompileSupervisorRejectsUnknownMember(t *testing.T) {
	t.Parallel()

	registry, err := NewSupervisorRegistry(
		RegisteredSupervisorMember[supervisorState]{
			Name:     "writer",
			Runnable: compileSupervisorWorker(t, "writer"),
		},
	)
	if err != nil {
		t.Fatalf("NewSupervisorRegistry returned error: %v", err)
	}

	_, err = registry.CompileSupervisor(SupervisorConfig[supervisorState]{
		Router: piagentgo.AgentDefinition{
			Model: &supervisorRouterModel{},
		},
		GetMessages: func(state supervisorState) []piagentgo.Message { return state.Messages },
		GetNext:     func(state supervisorState) string { return state.Next },
		SetNext: func(state supervisorState, next string) supervisorState {
			state.Next = next
			return state
		},
	}, "missing")
	if err == nil {
		t.Fatal("expected unknown member error")
	}
}

func compileSupervisorWorker(t *testing.T, name string) *graph.StateRunnable[supervisorState] {
	t.Helper()

	workflow := graph.NewStateGraph[supervisorState]()
	workflow.AddNode(name, "worker "+name, func(_ context.Context, state supervisorState) (supervisorState, error) {
		state.Steps = append(state.Steps, name)
		state.Messages = append(state.Messages, piagentgo.Message{
			Role:      piagentgo.RoleAssistant,
			Parts:     []piagentgo.Part{{Type: piagentgo.PartTypeText, Text: "worker:" + name}},
			Timestamp: time.Now().UTC(),
		})
		return state, nil
	})
	workflow.SetEntryPoint(name)
	workflow.AddEdge(name, graph.END)

	runnable, err := workflow.Compile()
	if err != nil {
		t.Fatalf("worker compile failed: %v", err)
	}
	return runnable
}

type supervisorRouterModel struct {
	requests []piagentgo.ModelRequest
}

func (m *supervisorRouterModel) Stream(_ context.Context, request piagentgo.ModelRequest) (piagentgo.AssistantStream, error) {
	m.requests = append(m.requests, request)

	next := supervisorFinish
	options := routeOptions(request)
	lastText := lastTextMessage(request.Messages)
	lastAssistant := lastAssistantText(request.Messages)

	switch {
	case stringsContains(lastAssistant, "worker:"):
		next = supervisorFinish
	case stringsContains(lastText, "research") && containsString(options, "researcher"):
		next = "researcher"
	case stringsContains(lastText, "writer") && containsString(options, "writer"):
		next = "writer"
	case len(options) > 1:
		next = options[0]
	}

	args, _ := json.Marshal(map[string]string{"next": next})
	return newStaticAssistantStream(piagentgo.Message{
		Role:       piagentgo.RoleAssistant,
		ToolCalls:  []piagentgo.ToolCall{{Name: "route", Arguments: args}},
		Timestamp:  time.Now().UTC(),
		StopReason: piagentgo.StopReasonToolUse,
	}, nil), nil
}

func routeOptions(request piagentgo.ModelRequest) []string {
	if len(request.Tools) == 0 {
		return nil
	}
	properties, _ := request.Tools[0].Parameters["properties"].(map[string]any)
	next, _ := properties["next"].(map[string]any)
	switch values := next["enum"].(type) {
	case []string:
		return values
	case []any:
		options := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				options = append(options, text)
			}
		}
		return options
	default:
		return nil
	}
}

func lastTextMessage(messages []piagentgo.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != piagentgo.RoleUser {
			continue
		}
		return messageText(messages[i])
	}
	return ""
}

func lastAssistantText(messages []piagentgo.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != piagentgo.RoleAssistant {
			continue
		}
		return messageText(messages[i])
	}
	return ""
}

func stringsContains(text string, fragment string) bool {
	return text != "" && fragment != "" && strings.Contains(text, fragment)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
