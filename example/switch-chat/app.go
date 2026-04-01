package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	core "github.com/Icatme/pi-agent-go"
	"github.com/Icatme/pi-agent-go/prebuilt"
	pigo "github.com/Icatme/pi-go/pkg/pigo"
)

type App struct {
	config      AppConfig
	presets     []PresetSpec
	presetIndex map[string]PresetSpec
	registry    RegistryState
	now         func() time.Time
}

func NewApp(config AppConfig) (*App, error) {
	if config.Provider == "" {
		config.Provider = "openai-codex"
	}
	if config.Model == "" {
		model, err := defaultModelID(config.Provider)
		if err != nil {
			return nil, err
		}
		config.Model = model
	}
	if config.DataDir == "" {
		config.DataDir = filepath.Join("example", "switch-chat", ".data")
	}
	if config.Preset == "" {
		config.Preset = defaultPresetName()
	}
	if config.Stdout == nil {
		config.Stdout = os.Stdout
	}
	if config.Stderr == nil {
		config.Stderr = os.Stderr
	}
	if config.Stdin == nil {
		config.Stdin = os.Stdin
	}
	if config.ReflectionMaxTurns == 0 {
		config.ReflectionMaxTurns = 3
	}

	presets := builtInPresets()
	presetIndex := make(map[string]PresetSpec, len(presets))
	for _, preset := range presets {
		presetIndex[preset.Name] = preset
	}
	if _, ok := presetIndex[config.Preset]; !ok {
		return nil, fmt.Errorf("switch-chat: unknown preset %q", config.Preset)
	}

	registry, err := loadRegistry(config.DataDir)
	if err != nil {
		return nil, err
	}
	if registry.Sessions == nil {
		registry.Sessions = map[string]SessionRecord{}
	}
	if registry.ActivePreset == "" {
		registry.ActivePreset = config.Preset
	}
	if _, ok := presetIndex[registry.ActivePreset]; !ok {
		registry.ActivePreset = config.Preset
	}

	return &App{
		config:      config,
		presets:     presets,
		presetIndex: presetIndex,
		registry:    registry,
		now:         time.Now,
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	fmt.Fprintf(a.config.Stdout, "switch-chat ready. provider=%s model=%s active=%s\n", a.config.Provider, a.config.Model, a.registry.ActivePreset)
	fmt.Fprintln(a.config.Stdout, "Commands: /agents /use <preset> /show /reset /exit")

	scanner := bufio.NewScanner(a.config.Stdin)
	scanner.Buffer(make([]byte, 0, 1024), 1024*1024)

	for {
		fmt.Fprintf(a.config.Stdout, "%s> ", a.registry.ActivePreset)
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return err
			}
			fmt.Fprintln(a.config.Stdout)
			return a.save()
		}

		keepRunning, err := a.HandleLine(ctx, scanner.Text())
		if err != nil {
			fmt.Fprintf(a.config.Stderr, "Error: %v\n", err)
			continue
		}
		if !keepRunning {
			return nil
		}
	}
}

func (a *App) HandleLine(ctx context.Context, line string) (bool, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true, nil
	}
	if !strings.HasPrefix(trimmed, "/") {
		return true, a.sendToActivePreset(ctx, trimmed)
	}

	fields := strings.Fields(strings.TrimPrefix(trimmed, "/"))
	if len(fields) == 0 {
		return true, nil
	}

	switch fields[0] {
	case "agents":
		a.printAgents()
		return true, nil
	case "use":
		if len(fields) != 2 {
			return true, fmt.Errorf("usage: /use <preset>")
		}
		return true, a.usePreset(fields[1])
	case "show":
		a.printShow()
		return true, nil
	case "reset":
		return true, a.resetActivePreset()
	case "exit":
		return false, a.save()
	case "help":
		fmt.Fprintln(a.config.Stdout, "Commands: /agents /use <preset> /show /reset /exit")
		return true, nil
	default:
		return true, fmt.Errorf("unknown command %q", fields[0])
	}
}

func (a *App) ActivePreset() string {
	return a.registry.ActivePreset
}

func (a *App) sendToActivePreset(ctx context.Context, input string) error {
	preset, ok := a.presetIndex[a.registry.ActivePreset]
	if !ok {
		return fmt.Errorf("switch-chat: active preset %q is not defined", a.registry.ActivePreset)
	}

	switch preset.Mode {
	case RuntimeModeChat:
		return a.sendChat(ctx, preset, input)
	case RuntimeModeReflection:
		return a.sendReflection(ctx, preset, input)
	default:
		return fmt.Errorf("switch-chat: unsupported preset mode %q", preset.Mode)
	}
}

func (a *App) sendChat(ctx context.Context, preset PresetSpec, input string) error {
	modelRef, err := a.currentModelRef()
	if err != nil {
		return err
	}

	session := a.ensureSession(preset)
	snapshot := cloneSnapshotPtr(session.ChatSnapshot)
	if snapshot == nil {
		snapshot = &core.AgentSnapshot{
			SessionID: newSessionID(),
		}
	}
	if strings.TrimSpace(snapshot.SessionID) == "" {
		snapshot.SessionID = newSessionID()
	}
	snapshot.SystemPrompt = preset.SystemPrompt
	snapshot.Model = cloneModelRef(modelRef)

	definition := core.AgentDefinition{
		SystemPrompt: preset.SystemPrompt,
		DefaultModel: cloneModelRef(modelRef),
		Model:        a.config.ChatModel,
	}
	agent, err := core.NewAgent(definition, core.WithSnapshot(*snapshot))
	if err != nil {
		return err
	}

	var printedDelta bool
	unsubscribe := agent.Subscribe(func(event core.AgentEvent) {
		if event.Type != core.EventMessageUpdate {
			return
		}
		if event.Message == nil || event.Message.Role != core.RoleAssistant {
			return
		}
		if event.AssistantEvent != nil && event.AssistantEvent.Type != core.AssistantEventTextDelta {
			return
		}
		if event.Delta == "" {
			return
		}
		printedDelta = true
		_, _ = io.WriteString(a.config.Stdout, event.Delta)
	})
	defer unsubscribe()

	if err := agent.PromptText(ctx, input); err != nil {
		return err
	}

	finalSnapshot := agent.Snapshot()
	if printedDelta {
		fmt.Fprintln(a.config.Stdout)
	} else {
		text := latestAssistantText(finalSnapshot.Messages)
		if strings.TrimSpace(text) != "" {
			fmt.Fprintln(a.config.Stdout, text)
		}
	}

	session.ChatSnapshot = &finalSnapshot
	session.Transcript = cloneMessages(finalSnapshot.Messages)
	session.UpdatedAt = a.now().UTC()
	a.registry.Sessions[preset.Name] = session
	return a.save()
}

func (a *App) sendReflection(ctx context.Context, preset PresetSpec, input string) error {
	modelRef, err := a.currentModelRef()
	if err != nil {
		return err
	}

	model := a.config.ReflectionModel
	if model == nil {
		model = newTextPigoStreamModel(modelRef)
	}
	critic := a.config.ReflectionCritic
	if critic == nil {
		critic = model
	}

	agent, err := prebuilt.CreateReflectionAgent(prebuilt.ReflectionAgentConfig{
		Model:            model,
		ReflectionModel:  critic,
		MaxIterations:    a.config.ReflectionMaxTurns,
		SystemMessage:    preset.SystemPrompt,
		ReflectionPrompt: preset.ReflectionPrompt,
	})
	if err != nil {
		return err
	}

	result, err := agent.PromptText(ctx, input)
	if err != nil {
		return err
	}
	if strings.TrimSpace(result.Draft) != "" {
		fmt.Fprintln(a.config.Stdout, result.Draft)
	}

	session := a.ensureSession(preset)
	session.ChatSnapshot = nil
	session.Transcript = append(session.Transcript,
		core.NewUserTextMessage(input),
		core.NewTextMessage(core.RoleAssistant, result.Draft),
	)
	session.UpdatedAt = a.now().UTC()
	a.registry.Sessions[preset.Name] = session
	return a.save()
}

func (a *App) printAgents() {
	for _, preset := range a.presets {
		session := a.ensureSession(preset)
		prefix := " "
		if preset.Name == a.registry.ActivePreset {
			prefix = "*"
		}
		fmt.Fprintf(
			a.config.Stdout,
			"%s %s mode=%s messages=%d updated=%s\n",
			prefix,
			preset.Name,
			preset.Mode,
			len(session.Transcript),
			formatUpdatedAt(session.UpdatedAt),
		)
	}
}

func (a *App) printShow() {
	preset := a.presetIndex[a.registry.ActivePreset]
	session := a.ensureSession(preset)
	fmt.Fprintf(a.config.Stdout, "preset: %s\n", preset.Name)
	fmt.Fprintf(a.config.Stdout, "mode: %s\n", preset.Mode)
	fmt.Fprintf(a.config.Stdout, "provider: %s\n", a.config.Provider)
	fmt.Fprintf(a.config.Stdout, "model: %s\n", a.config.Model)
	fmt.Fprintf(a.config.Stdout, "messages: %d\n", len(session.Transcript))
	if session.ChatSnapshot != nil && strings.TrimSpace(session.ChatSnapshot.SessionID) != "" {
		fmt.Fprintf(a.config.Stdout, "session_id: %s\n", session.ChatSnapshot.SessionID)
	}
	fmt.Fprintf(a.config.Stdout, "updated: %s\n", formatUpdatedAt(session.UpdatedAt))
	if preset.Mode == RuntimeModeReflection {
		fmt.Fprintln(a.config.Stdout, "note: reflection mode is intentionally single-run and does not reuse transcript as live model context.")
	}
}

func (a *App) usePreset(name string) error {
	if _, ok := a.presetIndex[name]; !ok {
		return fmt.Errorf("switch-chat: unknown preset %q", name)
	}
	a.registry.ActivePreset = name
	if err := a.save(); err != nil {
		return err
	}
	fmt.Fprintf(a.config.Stdout, "active preset: %s\n", name)
	return nil
}

func (a *App) resetActivePreset() error {
	preset := a.presetIndex[a.registry.ActivePreset]
	session := SessionRecord{
		PresetName: preset.Name,
		Mode:       preset.Mode,
		UpdatedAt:  a.now().UTC(),
	}
	if preset.Mode == RuntimeModeChat {
		session.ChatSnapshot = &core.AgentSnapshot{
			SessionID: newSessionID(),
			Model: core.ModelRef{
				Provider: a.config.Provider,
				Model:    a.config.Model,
			},
			SystemPrompt: preset.SystemPrompt,
		}
	}
	a.registry.Sessions[preset.Name] = session
	if err := a.save(); err != nil {
		return err
	}
	fmt.Fprintf(a.config.Stdout, "reset preset: %s\n", preset.Name)
	return nil
}

func (a *App) ensureSession(preset PresetSpec) SessionRecord {
	session, ok := a.registry.Sessions[preset.Name]
	if !ok {
		session = SessionRecord{
			PresetName: preset.Name,
			Mode:       preset.Mode,
		}
	}
	if session.PresetName == "" {
		session.PresetName = preset.Name
	}
	if session.Mode == "" {
		session.Mode = preset.Mode
	}
	return session
}

func (a *App) currentModelRef() (core.ModelRef, error) {
	return resolveModelRef(a.config.Provider, a.config.Model, a.config.AuthRoot)
}

func (a *App) save() error {
	return saveRegistry(a.config.DataDir, a.registry)
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
		return "", fmt.Errorf("switch-chat: provider %q has no registered models", provider)
	}
	return models[0].ID, nil
}

func formatUpdatedAt(timestamp time.Time) string {
	if timestamp.IsZero() {
		return "never"
	}
	return timestamp.Format(time.RFC3339)
}

func latestAssistantText(messages []core.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == core.RoleAssistant {
			return messageText(messages[i])
		}
	}
	return ""
}

func messageText(message core.Message) string {
	var builder strings.Builder
	for _, part := range message.Parts {
		if part.Type == core.PartTypeText || part.Type == core.PartTypeThinking {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}

func newSessionID() string {
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err == nil {
		return "switch-chat-" + hex.EncodeToString(bytes[:])
	}
	return "switch-chat-" + time.Now().UTC().Format("20060102150405.000000000")
}

func cloneMessages(messages []core.Message) []core.Message {
	if len(messages) == 0 {
		return nil
	}
	cloned := make([]core.Message, len(messages))
	for i, message := range messages {
		cloned[i] = cloneMessage(message)
	}
	return cloned
}

func cloneMessage(message core.Message) core.Message {
	cloned := message
	cloned.Parts = cloneParts(message.Parts)
	cloned.ToolCalls = cloneToolCalls(message.ToolCalls)
	cloned.ToolResult = cloneToolResultPayload(message.ToolResult)
	cloned.Metadata = cloneStringAnyMap(message.Metadata)
	cloned.Payload = cloneStringAnyMap(message.Payload)
	return cloned
}

func cloneParts(parts []core.Part) []core.Part {
	if len(parts) == 0 {
		return nil
	}
	cloned := make([]core.Part, len(parts))
	copy(cloned, parts)
	return cloned
}

func cloneToolCalls(calls []core.ToolCall) []core.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	cloned := make([]core.ToolCall, len(calls))
	for i, call := range calls {
		cloned[i] = call
		if len(call.Arguments) > 0 {
			cloned[i].Arguments = append([]byte(nil), call.Arguments...)
		}
		cloned[i].ParsedArgs = cloneStringAnyMap(call.ParsedArgs)
	}
	return cloned
}

func cloneToolResultPayload(payload *core.ToolResultPayload) *core.ToolResultPayload {
	if payload == nil {
		return nil
	}
	cloned := *payload
	cloned.Content = cloneParts(payload.Content)
	return &cloned
}

func cloneSnapshotPtr(snapshot *core.AgentSnapshot) *core.AgentSnapshot {
	if snapshot == nil {
		return nil
	}
	cloned := *snapshot
	cloned.Model = cloneModelRef(snapshot.Model)
	cloned.Messages = cloneMessages(snapshot.Messages)
	cloned.PendingToolCalls = clonePendingToolCalls(snapshot.PendingToolCalls)
	cloned.Metadata = cloneStringAnyMap(snapshot.Metadata)
	return &cloned
}

func cloneModelRef(ref core.ModelRef) core.ModelRef {
	return core.ModelRef{
		Provider:       ref.Provider,
		Model:          ref.Model,
		ProviderConfig: cloneProviderConfig(ref.ProviderConfig),
		Metadata:       cloneStringAnyMap(ref.Metadata),
	}
}

func cloneProviderConfig(config core.ProviderConfig) core.ProviderConfig {
	return core.ProviderConfig{
		BaseURL: config.BaseURL,
		APIKey:  config.APIKey,
		Headers: cloneStringMap(config.Headers),
		Auth:    cloneProviderAuthConfig(config.Auth),
	}
}

func cloneProviderAuthConfig(config *core.ProviderAuthConfig) *core.ProviderAuthConfig {
	if config == nil {
		return nil
	}
	cloned := *config
	if config.OAuth != nil {
		oauth := *config.OAuth
		cloned.OAuth = &oauth
	}
	return &cloned
}

func clonePendingToolCalls(calls []core.PendingToolCall) []core.PendingToolCall {
	if len(calls) == 0 {
		return nil
	}
	cloned := make([]core.PendingToolCall, len(calls))
	copy(cloned, calls)
	return cloned
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneStringAnyMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = cloneAny(value)
	}
	return cloned
}

func cloneAny(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneStringAnyMap(typed)
	case []any:
		cloned := make([]any, len(typed))
		for i, item := range typed {
			cloned[i] = cloneAny(item)
		}
		return cloned
	default:
		return typed
	}
}
