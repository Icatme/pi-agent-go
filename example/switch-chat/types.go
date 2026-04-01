package main

import (
	"io"
	"time"

	core "github.com/Icatme/pi-agent-go"
)

type RuntimeMode string

const (
	RuntimeModeChat       RuntimeMode = "chat"
	RuntimeModeReflection RuntimeMode = "reflection"
)

type PresetSpec struct {
	Name             string      `json:"name"`
	Mode             RuntimeMode `json:"mode"`
	SystemPrompt     string      `json:"system_prompt"`
	ReflectionPrompt string      `json:"reflection_prompt,omitempty"`
}

type SessionRecord struct {
	PresetName   string              `json:"preset_name"`
	Mode         RuntimeMode         `json:"mode"`
	ChatSnapshot *core.AgentSnapshot `json:"chat_snapshot,omitempty"`
	Transcript   []core.Message      `json:"transcript,omitempty"`
	UpdatedAt    time.Time           `json:"updated_at,omitempty"`
}

type RegistryState struct {
	ActivePreset string                   `json:"active_preset"`
	Sessions     map[string]SessionRecord `json:"sessions"`
}

type AppConfig struct {
	AuthRoot           string
	DataDir            string
	Provider           string
	Model              string
	Preset             string
	ChatModel          core.StreamModel
	ReflectionModel    core.StreamModel
	ReflectionCritic   core.StreamModel
	Stdin              io.Reader
	Stdout             io.Writer
	Stderr             io.Writer
	ReflectionMaxTurns int
}
