package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	core "github.com/Icatme/pi-agent-go"
)

func TestChatSessionsPersistAcrossSwitchAndRestart(t *testing.T) {
	dataDir := t.TempDir()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	app, err := NewApp(AppConfig{
		DataDir:         dataDir,
		Provider:        "openai-codex",
		Model:           "gpt-5.4",
		ChatModel:       fakeChatModel("chat"),
		ReflectionModel: fakeReflectionModel(),
		Stdout:          stdout,
		Stderr:          stderr,
	})
	if err != nil {
		t.Fatalf("NewApp returned error: %v", err)
	}

	if _, err := app.HandleLine(context.Background(), "hello"); err != nil {
		t.Fatalf("first chat message returned error: %v", err)
	}
	if _, err := app.HandleLine(context.Background(), "/use coder"); err != nil {
		t.Fatalf("switch to coder returned error: %v", err)
	}
	if _, err := app.HandleLine(context.Background(), "ship it"); err != nil {
		t.Fatalf("coder message returned error: %v", err)
	}
	if _, err := app.HandleLine(context.Background(), "/use chat"); err != nil {
		t.Fatalf("switch back to chat returned error: %v", err)
	}

	chatSession := app.registry.Sessions["chat"]
	coderSession := app.registry.Sessions["coder"]
	if chatSession.ChatSnapshot == nil || len(chatSession.ChatSnapshot.Messages) != 2 {
		t.Fatalf("expected chat snapshot to contain one turn, got %+v", chatSession.ChatSnapshot)
	}
	if coderSession.ChatSnapshot == nil || len(coderSession.ChatSnapshot.Messages) != 2 {
		t.Fatalf("expected coder snapshot to contain one turn, got %+v", coderSession.ChatSnapshot)
	}

	reloaded, err := NewApp(AppConfig{
		DataDir:         dataDir,
		Provider:        "openai-codex",
		Model:           "gpt-5.4",
		ChatModel:       fakeChatModel("chat"),
		ReflectionModel: fakeReflectionModel(),
		Stdout:          &bytes.Buffer{},
		Stderr:          &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("reloaded NewApp returned error: %v", err)
	}
	if _, err := reloaded.HandleLine(context.Background(), "again"); err != nil {
		t.Fatalf("reloaded chat message returned error: %v", err)
	}

	reloadedChat := reloaded.registry.Sessions["chat"]
	if reloadedChat.ChatSnapshot == nil {
		t.Fatal("expected reloaded chat snapshot")
	}
	if got := len(reloadedChat.ChatSnapshot.Messages); got != 4 {
		t.Fatalf("expected reloaded chat session to continue from previous transcript, got %d messages", got)
	}
}

func TestReflectionTranscriptIsPersistedWithoutLiveSnapshot(t *testing.T) {
	dataDir := t.TempDir()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	app, err := NewApp(AppConfig{
		DataDir:         dataDir,
		Provider:        "openai-codex",
		Model:           "gpt-5.4",
		ChatModel:       fakeChatModel("chat"),
		ReflectionModel: fakeReflectionModel(),
		Preset:          "reflect",
		Stdout:          stdout,
		Stderr:          stderr,
	})
	if err != nil {
		t.Fatalf("NewApp returned error: %v", err)
	}

	if _, err := app.HandleLine(context.Background(), "alpha"); err != nil {
		t.Fatalf("first reflection message returned error: %v", err)
	}
	if _, err := app.HandleLine(context.Background(), "beta"); err != nil {
		t.Fatalf("second reflection message returned error: %v", err)
	}

	session := app.registry.Sessions["reflect"]
	if session.ChatSnapshot != nil {
		t.Fatalf("expected reflection mode to avoid snapshot persistence, got %+v", session.ChatSnapshot)
	}
	if got := len(session.Transcript); got != 4 {
		t.Fatalf("expected reflection transcript to store two visible turns, got %d messages", got)
	}
	if text := messageText(session.Transcript[3]); !strings.Contains(text, "draft:beta") {
		t.Fatalf("expected final reflection transcript entry to be the latest draft, got %q", text)
	}
}

func TestResetCreatesFreshChatSession(t *testing.T) {
	dataDir := t.TempDir()
	app, err := NewApp(AppConfig{
		DataDir:         dataDir,
		Provider:        "openai-codex",
		Model:           "gpt-5.4",
		ChatModel:       fakeChatModel("chat"),
		ReflectionModel: fakeReflectionModel(),
		Stdout:          &bytes.Buffer{},
		Stderr:          &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("NewApp returned error: %v", err)
	}

	if _, err := app.HandleLine(context.Background(), "hello"); err != nil {
		t.Fatalf("chat message returned error: %v", err)
	}

	before := app.registry.Sessions["chat"].ChatSnapshot.SessionID
	if _, err := app.HandleLine(context.Background(), "/reset"); err != nil {
		t.Fatalf("reset returned error: %v", err)
	}

	after := app.registry.Sessions["chat"].ChatSnapshot.SessionID
	if before == after {
		t.Fatalf("expected reset to create a fresh session id, kept %q", after)
	}
	if got := len(app.registry.Sessions["chat"].Transcript); got != 0 {
		t.Fatalf("expected reset transcript to be empty, got %d messages", got)
	}
}

func fakeChatModel(prefix string) core.StreamModel {
	return fakeStreamModel(func(request core.ModelRequest) string {
		return prefix + ":" + messageText(request.Messages[len(request.Messages)-1])
	})
}

func fakeReflectionModel() core.StreamModel {
	return fakeStreamModel(func(request core.ModelRequest) string {
		text := messageText(request.Messages[len(request.Messages)-1])
		if strings.HasPrefix(text, "Request: ") && strings.Contains(text, "\nResponse: ") {
			return "no major issues"
		}
		return "draft:" + text
	})
}

func fakeStreamModel(fn func(core.ModelRequest) string) core.StreamModel {
	return core.StreamFunc(func(_ context.Context, request core.ModelRequest) (core.AssistantStream, error) {
		text := fn(request)
		message := core.Message{
			Role: core.RoleAssistant,
			Parts: []core.Part{
				{Type: core.PartTypeText, Text: text},
			},
		}
		stream := &fakeAssistantStream{
			events: make(chan core.AssistantEvent, 2),
			final:  message,
		}
		stream.events <- core.AssistantEvent{Type: core.AssistantEventStart, Message: message}
		stream.events <- core.AssistantEvent{Type: core.AssistantEventTextDelta, Message: message, Delta: text}
		close(stream.events)
		return stream, nil
	})
}

type fakeAssistantStream struct {
	events chan core.AssistantEvent
	final  core.Message
}

func (s *fakeAssistantStream) Events() <-chan core.AssistantEvent {
	return s.events
}

func (s *fakeAssistantStream) Wait() (core.Message, error) {
	return s.final, nil
}
