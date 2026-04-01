package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

func main() {
	var config AppConfig

	flag.StringVar(&config.MattermostURL, "mattermost-url", "", "Mattermost server URL (or MATTERMOST_URL)")
	flag.StringVar(&config.MattermostToken, "mattermost-token", "", "Mattermost bot token (or MATTERMOST_TOKEN)")
	flag.StringVar(&config.AuthRoot, "auth-root", "", "path to the directory that contains .pigo/auth.json and .pigo/.env")
	flag.StringVar(&config.Provider, "provider", "openai-codex", "provider id")
	flag.StringVar(&config.Model, "model", "", "model id")
	flag.StringVar(&config.SystemPrompt, "system-prompt", "", "system prompt for the AI bot")
	flag.Parse()

	config.Stdout = os.Stdout
	config.Stderr = os.Stderr

	app, err := NewApp(config, AppDeps{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := app.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
