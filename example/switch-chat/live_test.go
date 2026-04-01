package main

import (
	"bytes"
	"context"
	"os"
	"testing"
)

func TestLiveSmokeChatAndReflection(t *testing.T) {
	if os.Getenv("PIAGENTGO_EXAMPLE_LIVE") != "1" {
		t.Skip("set PIAGENTGO_EXAMPLE_LIVE=1 to run live switch-chat example tests")
	}

	authRoot := os.Getenv("PIAGENTGO_EXAMPLE_AUTH_ROOT")
	if authRoot == "" {
		t.Skip("set PIAGENTGO_EXAMPLE_AUTH_ROOT to a directory that contains .pigo credentials")
	}

	provider := os.Getenv("PIAGENTGO_EXAMPLE_PROVIDER")
	if provider == "" {
		provider = "openai-codex"
	}
	model := os.Getenv("PIAGENTGO_EXAMPLE_MODEL")
	if model == "" {
		model = "gpt-5.4"
	}

	app, err := NewApp(AppConfig{
		AuthRoot: authRoot,
		DataDir:  t.TempDir(),
		Provider: provider,
		Model:    model,
		Stdout:   &bytes.Buffer{},
		Stderr:   &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("NewApp returned error: %v", err)
	}

	ctx := context.Background()
	if _, err := app.HandleLine(ctx, "Say hello in one short sentence."); err != nil {
		t.Fatalf("chat turn returned error: %v", err)
	}
	if _, err := app.HandleLine(ctx, "/use reflect"); err != nil {
		t.Fatalf("switch to reflect returned error: %v", err)
	}
	if _, err := app.HandleLine(ctx, "Explain why tests matter in one paragraph."); err != nil {
		t.Fatalf("reflection turn returned error: %v", err)
	}
}
