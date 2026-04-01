package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

func main() {
	var config AppConfig

	flag.StringVar(&config.AuthRoot, "auth-root", "", "path to the directory that contains .pigo/auth.json and .pigo/.env")
	flag.StringVar(&config.Provider, "provider", "openai-codex", "provider id")
	flag.StringVar(&config.Model, "model", "", "model id")
	flag.StringVar(&config.DataDir, "data-dir", "", "directory for persisted sessions")
	flag.StringVar(&config.Preset, "preset", defaultPresetName(), "initial preset")
	flag.IntVar(&config.ReflectionMaxTurns, "reflection-max-turns", 3, "maximum reflection iterations")
	flag.Parse()

	config.Stdin = os.Stdin
	config.Stdout = os.Stdout
	config.Stderr = os.Stderr

	app, err := NewApp(config)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := app.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
