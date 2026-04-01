package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	pigo "github.com/Icatme/pi-go/pkg/pigo"
)

const defaultSystemPrompt = "You are a helpful Mattermost AI assistant. Answer directly, clearly, and concisely."

type AppConfig struct {
	MattermostURL   string
	MattermostToken string
	AuthRoot        string
	Provider        string
	Model           string
	SystemPrompt    string
	Stdout          io.Writer
	Stderr          io.Writer
}

func (c AppConfig) Normalize() (AppConfig, error) {
	if strings.TrimSpace(c.MattermostURL) == "" {
		c.MattermostURL = strings.TrimSpace(os.Getenv("MATTERMOST_URL"))
	}
	if strings.TrimSpace(c.MattermostToken) == "" {
		c.MattermostToken = strings.TrimSpace(os.Getenv("MATTERMOST_TOKEN"))
	}
	if strings.TrimSpace(c.Provider) == "" {
		c.Provider = "openai-codex"
	}
	if strings.TrimSpace(c.Model) == "" {
		model, err := defaultModelID(c.Provider)
		if err != nil {
			return AppConfig{}, err
		}
		c.Model = model
	}
	if strings.TrimSpace(c.SystemPrompt) == "" {
		c.SystemPrompt = defaultSystemPrompt
	}
	if c.Stdout == nil {
		c.Stdout = os.Stdout
	}
	if c.Stderr == nil {
		c.Stderr = os.Stderr
	}
	if strings.TrimSpace(c.MattermostURL) == "" {
		return AppConfig{}, fmt.Errorf("mattermost-chatbot: mattermost URL is required")
	}
	if strings.TrimSpace(c.MattermostToken) == "" {
		return AppConfig{}, fmt.Errorf("mattermost-chatbot: mattermost token is required")
	}
	return c, nil
}

func defaultModelID(provider string) (string, error) {
	switch provider {
	case "openai-codex":
		return "gpt-5.4", nil
	case "kimi-coding":
		return "kimi-k2-thinking", nil
	case "anthropic":
		return "claude-sonnet-4-5", nil
	}

	models := pigo.GetModels(pigo.Provider(provider))
	if len(models) == 0 {
		return "", fmt.Errorf("mattermost-chatbot: provider %q has no registered models", provider)
	}
	return models[0].ID, nil
}
