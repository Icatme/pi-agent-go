package main

import (
	"os"
	"path/filepath"
	"testing"

	core "github.com/Icatme/pi-agent-go"
)

func TestResolveModelRefReadsAPIKeyFromDotEnv(t *testing.T) {
	root := t.TempDir()
	supportDir := filepath.Join(root, ".pigo")
	if err := os.MkdirAll(supportDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(supportDir, ".env"), []byte("KIMI_API_KEY=file-kimi-key\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	ref, err := resolveModelRef("kimi-coding", "kimi-k2-thinking", root)
	if err != nil {
		t.Fatalf("resolveModelRef returned error: %v", err)
	}
	if got := ref.ProviderConfig.APIKey; got != "file-kimi-key" {
		t.Fatalf("expected file API key, got %q", got)
	}
}

func TestResolveModelRefReadsOAuthFromAuthFile(t *testing.T) {
	root := t.TempDir()
	supportDir := filepath.Join(root, ".pigo")
	if err := os.MkdirAll(supportDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(supportDir, "auth.json"), []byte("{\"openai-codex\":{\"type\":\"oauth\",\"access\":\"token-123\",\"refresh\":\"refresh-456\",\"expires\":1700000000000}}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	ref, err := resolveModelRef("openai-codex", "gpt-5.4", root)
	if err != nil {
		t.Fatalf("resolveModelRef returned error: %v", err)
	}
	if ref.ProviderConfig.Auth == nil || ref.ProviderConfig.Auth.Type != core.ProviderAuthTypeOAuth {
		t.Fatalf("expected OAuth auth config, got %+v", ref.ProviderConfig.Auth)
	}
	if got := ref.ProviderConfig.Auth.OAuth.AccessToken; got != "token-123" {
		t.Fatalf("expected access token token-123, got %q", got)
	}
}

func TestResolveModelRefPrefersProcessEnvOverDotEnv(t *testing.T) {
	root := t.TempDir()
	supportDir := filepath.Join(root, ".pigo")
	if err := os.MkdirAll(supportDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(supportDir, ".env"), []byte("KIMI_API_KEY=file-kimi-key\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	t.Setenv("KIMI_API_KEY", "env-kimi-key")

	ref, err := resolveModelRef("kimi-coding", "kimi-k2-thinking", root)
	if err != nil {
		t.Fatalf("resolveModelRef returned error: %v", err)
	}
	if got := ref.ProviderConfig.APIKey; got != "env-kimi-key" {
		t.Fatalf("expected env override API key, got %q", got)
	}
}

func TestResolveModelRefErrorsWhenAuthRootIsMissing(t *testing.T) {
	_, err := resolveModelRef("openai-codex", "gpt-5.4", filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatal("expected missing auth root error")
	}
}
