package langgraphgo

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Icatme/pi-agent-go"
	"github.com/smallnest/langgraphgo/graph"
)

const (
	// SupervisorNodeName is the orchestration node name used by the runtime supervisor helper.
	SupervisorNodeName = "supervisor"
	supervisorFinish   = "FINISH"
)

// RegisteredSupervisorMember is a pre-registered worker that can be selected
// into one supervisor run.
type RegisteredSupervisorMember[S any] struct {
	Name        string
	Description string
	Runnable    *graph.StateRunnable[S]
}

// SupervisorRegistry stores reusable supervisor workers. The active subset is
// selected per run before compiling the graph.
type SupervisorRegistry[S any] struct {
	members map[string]RegisteredSupervisorMember[S]
}

// SupervisorSelector chooses which registered members should participate in one run.
type SupervisorSelector[S any] func(context.Context, S, []RegisteredSupervisorMember[S]) ([]string, error)

// SupervisorConfig describes how supervisor routing projects into graph state.
type SupervisorConfig[S any] struct {
	Router       piagentgo.AgentDefinition
	GetMessages  func(S) []piagentgo.Message
	GetNext      func(S) string
	SetNext      func(S, string) S
	RouterPrompt string
}

// NewSupervisorRegistry creates a registry from pre-registered members.
func NewSupervisorRegistry[S any](members ...RegisteredSupervisorMember[S]) (*SupervisorRegistry[S], error) {
	registry := &SupervisorRegistry[S]{
		members: make(map[string]RegisteredSupervisorMember[S], len(members)),
	}
	for _, member := range members {
		if err := registry.Register(member); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

// Register adds one reusable member to the registry.
func (r *SupervisorRegistry[S]) Register(member RegisteredSupervisorMember[S]) error {
	if r == nil {
		return fmt.Errorf("supervisor registry is nil")
	}
	name := strings.TrimSpace(member.Name)
	if name == "" {
		return fmt.Errorf("supervisor member name is required")
	}
	if strings.EqualFold(name, supervisorFinish) {
		return fmt.Errorf("%q is reserved", supervisorFinish)
	}
	if member.Runnable == nil {
		return fmt.Errorf("supervisor member %q runnable is required", name)
	}
	if _, exists := r.members[name]; exists {
		return fmt.Errorf("supervisor member %q already registered", name)
	}

	member.Name = name
	r.members[name] = member
	return nil
}

// Members returns the registered members in stable name order.
func (r *SupervisorRegistry[S]) Members() []RegisteredSupervisorMember[S] {
	if r == nil || len(r.members) == 0 {
		return nil
	}

	names := make([]string, 0, len(r.members))
	for name := range r.members {
		names = append(names, name)
	}
	sort.Strings(names)

	members := make([]RegisteredSupervisorMember[S], 0, len(names))
	for _, name := range names {
		members = append(members, r.members[name])
	}
	return members
}

// CompileSupervisor builds a supervisor runnable for one active subset of members.
//
// Passing no member names activates every registered member.
func (r *SupervisorRegistry[S]) CompileSupervisor(config SupervisorConfig[S], memberNames ...string) (*graph.StateRunnable[S], error) {
	selected, err := r.resolveMembers(memberNames)
	if err != nil {
		return nil, err
	}
	return compileSupervisor(selected, config)
}

// CompileSupervisorForState chooses an active subset for one run and builds a
// supervisor runnable for that subset.
func (r *SupervisorRegistry[S]) CompileSupervisorForState(ctx context.Context, state S, selector SupervisorSelector[S], config SupervisorConfig[S]) (*graph.StateRunnable[S], error) {
	if selector == nil {
		return r.CompileSupervisor(config)
	}

	selectedNames, err := selector(ctx, state, r.Members())
	if err != nil {
		return nil, err
	}
	return r.CompileSupervisor(config, selectedNames...)
}

func (r *SupervisorRegistry[S]) resolveMembers(memberNames []string) ([]RegisteredSupervisorMember[S], error) {
	if r == nil {
		return nil, fmt.Errorf("supervisor registry is nil")
	}
	if len(memberNames) == 0 {
		return r.Members(), nil
	}

	seen := make(map[string]struct{}, len(memberNames))
	selected := make([]RegisteredSupervisorMember[S], 0, len(memberNames))
	for _, rawName := range memberNames {
		name := strings.TrimSpace(rawName)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		member, ok := r.members[name]
		if !ok {
			return nil, fmt.Errorf("supervisor member %q is not registered", name)
		}
		seen[name] = struct{}{}
		selected = append(selected, member)
	}

	sort.Slice(selected, func(i, j int) bool {
		return selected[i].Name < selected[j].Name
	})
	return selected, nil
}

func compileSupervisor[S any](members []RegisteredSupervisorMember[S], config SupervisorConfig[S]) (*graph.StateRunnable[S], error) {
	routerDefinition, err := config.Router.Validate()
	if err != nil {
		return nil, err
	}
	if config.GetMessages == nil {
		return nil, fmt.Errorf("supervisor GetMessages is required")
	}
	if config.GetNext == nil {
		return nil, fmt.Errorf("supervisor GetNext is required")
	}
	if config.SetNext == nil {
		return nil, fmt.Errorf("supervisor SetNext is required")
	}

	workflow := graph.NewStateGraph[S]()
	workflow.AddNode(SupervisorNodeName, "Supervisor orchestration node", func(ctx context.Context, state S) (S, error) {
		next, err := routeSupervisorNext(ctx, routerDefinition, config.GetMessages(state), members, config.RouterPrompt, threadIDFromContext(ctx))
		if err != nil {
			return state, err
		}
		return config.SetNext(state, next), nil
	})

	for _, member := range members {
		memberName := member.Name
		memberRunnable := member.Runnable
		workflow.AddNode(memberName, "Agent: "+memberName, func(ctx context.Context, state S) (S, error) {
			return memberRunnable.Invoke(ctx, state)
		})
		workflow.AddEdge(memberName, SupervisorNodeName)
	}

	workflow.SetEntryPoint(SupervisorNodeName)
	workflow.AddConditionalEdge(SupervisorNodeName, func(ctx context.Context, state S) string {
		next := strings.TrimSpace(config.GetNext(state))
		if next == "" || strings.EqualFold(next, supervisorFinish) {
			return graph.END
		}
		return next
	})

	return workflow.Compile()
}

func routeSupervisorNext[S any](ctx context.Context, definition piagentgo.AgentDefinition, messages []piagentgo.Message, members []RegisteredSupervisorMember[S], routerPrompt string, threadID string) (string, error) {
	snapshot := normalizeSnapshotSessionID(piagentgo.AgentSnapshot{}, threadID)
	model, modelRef, err := definition.ResolveModel(ctx, snapshot)
	if err != nil {
		return "", err
	}

	transformed, err := definition.TransformContext(ctx, messages)
	if err != nil {
		return "", err
	}
	converted, err := definition.ConvertToLLM(ctx, transformed)
	if err != nil {
		return "", err
	}

	stream, err := model.Stream(ctx, piagentgo.ModelRequest{
		Model:           modelRef,
		SystemPrompt:    buildSupervisorPrompt(definition.SystemPrompt, routerPrompt, members),
		Messages:        converted,
		Tools:           []piagentgo.ToolDefinition{buildSupervisorRouteTool(members)},
		ThinkingLevel:   definition.ThinkingLevel,
		SessionID:       snapshot.SessionID,
		Transport:       definition.Transport,
		MaxRetryDelayMs: definition.MaxRetryDelayMs,
		ThinkingBudgets: definition.ThinkingBudgets,
	})
	if err != nil {
		return "", err
	}

	finalMessage, err := stream.Wait()
	if err != nil {
		return "", err
	}
	return parseSupervisorRoute(finalMessage, supervisorOptions(members))
}

func buildSupervisorPrompt[S any](basePrompt string, routerPrompt string, members []RegisteredSupervisorMember[S]) string {
	lines := make([]string, 0, len(members)+3)
	lines = append(lines, "You are a supervisor tasked with selecting the next worker.")
	if len(members) == 0 {
		lines = append(lines, "No workers are currently active. Select FINISH.")
	} else {
		lines = append(lines, "Available workers:")
		for _, member := range members {
			description := strings.TrimSpace(member.Description)
			if description == "" {
				lines = append(lines, "- "+member.Name)
				continue
			}
			lines = append(lines, fmt.Sprintf("- %s: %s", member.Name, description))
		}
	}
	lines = append(lines, "Call the 'route' tool and set next to one of the active worker names or FINISH.")

	instructions := strings.Join(lines, "\n")
	custom := strings.TrimSpace(routerPrompt)
	if custom != "" {
		return custom + "\n\n" + instructions
	}

	base := strings.TrimSpace(basePrompt)
	if base == "" {
		return instructions
	}
	return base + "\n\n" + instructions
}

func buildSupervisorRouteTool[S any](members []RegisteredSupervisorMember[S]) piagentgo.ToolDefinition {
	options := supervisorOptions(members)
	return piagentgo.ToolDefinition{
		Name:        "route",
		Description: "Select the next worker to act, or FINISH to stop.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"next": map[string]any{
					"type": "string",
					"enum": options,
				},
			},
			"required": []string{"next"},
		},
	}
}

func supervisorOptions[S any](members []RegisteredSupervisorMember[S]) []string {
	options := make([]string, 0, len(members)+1)
	for _, member := range members {
		options = append(options, member.Name)
	}
	options = append(options, supervisorFinish)
	return options
}

func parseSupervisorRoute(message piagentgo.Message, options []string) (string, error) {
	allowed := make(map[string]struct{}, len(options))
	for _, option := range options {
		allowed[option] = struct{}{}
	}

	for _, call := range message.ToolCalls {
		if call.Name != "" && call.Name != "route" {
			continue
		}
		next, err := parseSupervisorToolArguments(call.Arguments)
		if err != nil {
			return "", err
		}
		if _, ok := allowed[next]; ok {
			return next, nil
		}
		return "", fmt.Errorf("supervisor selected unknown member %q", next)
	}

	text := strings.TrimSpace(messageText(message))
	for _, option := range options {
		if strings.EqualFold(text, option) {
			return option, nil
		}
	}
	return "", fmt.Errorf("supervisor did not select a valid next step")
}

func parseSupervisorToolArguments(arguments []byte) (string, error) {
	var payload struct {
		Next string `json:"next"`
	}
	if err := json.Unmarshal(arguments, &payload); err != nil {
		return "", fmt.Errorf("failed to parse supervisor route arguments: %w", err)
	}
	next := strings.TrimSpace(payload.Next)
	if next == "" {
		return "", fmt.Errorf("supervisor route did not include next")
	}
	return next, nil
}

func messageText(message piagentgo.Message) string {
	if len(message.Parts) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, part := range message.Parts {
		if part.Type != piagentgo.PartTypeText {
			continue
		}
		builder.WriteString(part.Text)
	}
	return builder.String()
}
