package prebuilt

import (
	"context"
	"testing"

	core "github.com/Icatme/pi-agent-go"
)

func TestCreateReflectionAgentRequiresModel(t *testing.T) {
	if _, err := CreateReflectionAgent(ReflectionAgentConfig{}); err == nil {
		t.Fatal("expected error when model is missing")
	}
}

func TestReflectionAgentStopsWhenReflectionIsSatisfactory(t *testing.T) {
	model := &chatScriptedModel{
		responses: []chatScriptedResponse{
			{
				events: []core.AssistantEvent{{Type: core.AssistantEventStart, Message: core.Message{Role: core.RoleAssistant}}},
				final:  core.Message{Role: core.RoleAssistant, Parts: []core.Part{{Type: core.PartTypeText, Text: "Initial response"}}, StopReason: core.StopReasonStop},
			},
			{
				events: []core.AssistantEvent{{Type: core.AssistantEventStart, Message: core.Message{Role: core.RoleAssistant}}},
				final:  core.Message{Role: core.RoleAssistant, Parts: []core.Part{{Type: core.PartTypeText, Text: "Excellent. No major issues."}}, StopReason: core.StopReasonStop},
			},
		},
	}

	agent, err := CreateReflectionAgent(ReflectionAgentConfig{
		Model:         model,
		MaxIterations: 3,
	})
	if err != nil {
		t.Fatalf("CreateReflectionAgent returned error: %v", err)
	}

	result, err := agent.PromptText(context.Background(), "Test")
	if err != nil {
		t.Fatalf("PromptText returned error: %v", err)
	}

	if result.Iteration != 1 {
		t.Fatalf("expected one generation iteration, got %d", result.Iteration)
	}
	if result.Draft != "Initial response" {
		t.Fatalf("unexpected draft: %q", result.Draft)
	}
	if result.Reflection != "Excellent. No major issues." {
		t.Fatalf("unexpected reflection: %q", result.Reflection)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("expected initial user message plus final draft, got %d messages", len(result.Messages))
	}
	if len(model.requests) != 2 {
		t.Fatalf("expected one generation and one reflection request, got %d", len(model.requests))
	}
}

func TestReflectionAgentUsesSeparateReflectionModel(t *testing.T) {
	generator := &chatScriptedModel{
		responses: []chatScriptedResponse{
			{
				events: []core.AssistantEvent{{Type: core.AssistantEventStart, Message: core.Message{Role: core.RoleAssistant}}},
				final:  core.Message{Role: core.RoleAssistant, Parts: []core.Part{{Type: core.PartTypeText, Text: "Draft one"}}, StopReason: core.StopReasonStop},
			},
			{
				events: []core.AssistantEvent{{Type: core.AssistantEventStart, Message: core.Message{Role: core.RoleAssistant}}},
				final:  core.Message{Role: core.RoleAssistant, Parts: []core.Part{{Type: core.PartTypeText, Text: "Draft two"}}, StopReason: core.StopReasonStop},
			},
		},
	}
	reflector := &chatScriptedModel{
		responses: []chatScriptedResponse{
			{
				events: []core.AssistantEvent{{Type: core.AssistantEventStart, Message: core.Message{Role: core.RoleAssistant}}},
				final:  core.Message{Role: core.RoleAssistant, Parts: []core.Part{{Type: core.PartTypeText, Text: "Needs more work."}}, StopReason: core.StopReasonStop},
			},
		},
	}

	agent, err := CreateReflectionAgent(ReflectionAgentConfig{
		Model:           generator,
		ReflectionModel: reflector,
		MaxIterations:   2,
	})
	if err != nil {
		t.Fatalf("CreateReflectionAgent returned error: %v", err)
	}

	result, err := agent.PromptText(context.Background(), "Test")
	if err != nil {
		t.Fatalf("PromptText returned error: %v", err)
	}

	if len(generator.requests) != 2 {
		t.Fatalf("expected two generation requests, got %d", len(generator.requests))
	}
	if len(reflector.requests) != 1 {
		t.Fatalf("expected one reflection request, got %d", len(reflector.requests))
	}
	if result.Iteration != 2 {
		t.Fatalf("expected two iterations, got %d", result.Iteration)
	}
	if result.Draft != "Draft two" {
		t.Fatalf("expected revised draft, got %q", result.Draft)
	}
	if result.Reflection != "Needs more work." {
		t.Fatalf("expected latest reflection to be retained, got %q", result.Reflection)
	}
	if len(result.Messages) != 3 {
		t.Fatalf("expected original message plus two drafts, got %d messages", len(result.Messages))
	}
}
