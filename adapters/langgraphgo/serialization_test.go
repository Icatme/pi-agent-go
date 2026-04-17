package langgraphgo

import (
	"encoding/json"
	"testing"
	"time"

	piagentgo "github.com/Icatme/pi-agent-go"
)

func TestSessionStateJSONRoundTripPreservesExpandedRuntimeFields(t *testing.T) {
	t.Parallel()

	original := SessionState{
		Snapshot: piagentgo.AgentSnapshot{
			SessionID:    "thread-123",
			SystemPrompt: "be precise",
			Model: piagentgo.ModelRef{
				Provider: "openai-codex",
				Model:    "gpt-5.4",
				ProviderConfig: piagentgo.ProviderConfig{
					BaseURL: "https://example.test",
					APIKey:  "api-key",
					Headers: map[string]string{"x-trace-id": "trace-1"},
				},
			},
			Messages: []piagentgo.Message{
				piagentgo.NewTextMessage(piagentgo.RoleUser, "hello"),
				{
					ID:         "assistant-1",
					Role:       piagentgo.RoleAssistant,
					Provider:   "openai-codex",
					API:        "openai-codex-responses",
					Model:      "gpt-5.4",
					ResponseID: "resp-1",
					Parts: []piagentgo.Part{
						{Type: piagentgo.PartTypeText, Text: "done"},
						{Type: piagentgo.PartTypeThinking, Text: "reason", Signature: "sig-1"},
					},
					ToolCalls: []piagentgo.ToolCall{{
						ID:         "tool-normalized-1",
						OriginalID: "tool-raw-1",
						Name:       "lookup",
						Arguments:  json.RawMessage(`{"value":42}`),
					}},
					Timestamp:  time.Unix(1700000100, 0).UTC(),
					StopReason: piagentgo.StopReasonToolUse,
				},
				piagentgo.NewToolResultMessage(piagentgo.ToolCall{
					ID:         "tool-normalized-1",
					OriginalID: "tool-raw-1",
					Name:       "lookup",
				}, piagentgo.ToolResult{
					Content: []piagentgo.Part{
						{Type: piagentgo.PartTypeText, Text: "42"},
						piagentgo.NewImagePart("img-base64", "image/png"),
					},
				}, false),
			},
			PendingToolCalls: []piagentgo.PendingToolCall{{
				ToolCallID:         "tool-normalized-1",
				OriginalToolCallID: "tool-raw-1",
				ToolName:           "lookup",
			}},
			Error: "transient",
		},
		Prompts: []piagentgo.Message{
			piagentgo.NewTextMessage(piagentgo.RoleUser, "queued prompt"),
		},
		Steering: []piagentgo.Message{
			piagentgo.NewTextMessage(piagentgo.RoleUser, "queued steering"),
		},
		FollowUps: []piagentgo.Message{
			piagentgo.NewTextMessage(piagentgo.RoleUser, "queued follow-up"),
		},
		Mode: RunModeContinue,
	}

	payload, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal session state: %v", err)
	}

	var decoded SessionState
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal session state: %v", err)
	}

	if decoded.Snapshot.SessionID != "thread-123" || decoded.Snapshot.Model.Provider != "openai-codex" || decoded.Snapshot.Model.ProviderConfig.APIKey != "api-key" {
		t.Fatalf("expected snapshot model fields to round-trip, got %+v", decoded.Snapshot)
	}
	if len(decoded.Snapshot.Messages) != 3 {
		t.Fatalf("expected snapshot messages to round-trip, got %+v", decoded.Snapshot.Messages)
	}
	if decoded.Snapshot.Messages[1].ResponseID != "resp-1" || decoded.Snapshot.Messages[1].ToolCalls[0].OriginalID != "tool-raw-1" {
		t.Fatalf("expected assistant runtime fields to round-trip, got %+v", decoded.Snapshot.Messages[1])
	}
	if decoded.Snapshot.Messages[2].ToolResult == nil || decoded.Snapshot.Messages[2].ToolResult.OriginalToolCallID != "tool-raw-1" {
		t.Fatalf("expected tool result raw id to round-trip, got %+v", decoded.Snapshot.Messages[2].ToolResult)
	}
	if len(decoded.Snapshot.PendingToolCalls) != 1 || decoded.Snapshot.PendingToolCalls[0].OriginalToolCallID != "tool-raw-1" {
		t.Fatalf("expected pending tool calls to round-trip, got %+v", decoded.Snapshot.PendingToolCalls)
	}
	if len(decoded.Prompts) != 1 || len(decoded.Steering) != 1 || len(decoded.FollowUps) != 1 || decoded.Mode != RunModeContinue {
		t.Fatalf("expected queued session state to round-trip, got %+v", decoded)
	}
}

func TestCloneSessionStatePreservesExpandedRuntimeFields(t *testing.T) {
	t.Parallel()

	original := SessionState{
		Snapshot: piagentgo.AgentSnapshot{
			SessionID: "thread-456",
			Model: piagentgo.ModelRef{
				Provider: "kimi-coding",
				Model:    "k2p5",
				ProviderConfig: piagentgo.ProviderConfig{
					Headers: map[string]string{"x-trace-id": "trace-2"},
				},
			},
			Messages: []piagentgo.Message{{
				Role:       piagentgo.RoleAssistant,
				ResponseID: "resp-2",
				Parts:      []piagentgo.Part{{Type: piagentgo.PartTypeThinking, Text: "reason", Signature: "sig-2"}},
				ToolCalls: []piagentgo.ToolCall{{
					ID:         "tool-normalized-2",
					OriginalID: "tool-raw-2",
					Name:       "inspect",
				}},
				Timestamp: time.Unix(1700000200, 0).UTC(),
			}},
			PendingToolCalls: []piagentgo.PendingToolCall{{
				ToolCallID:         "tool-normalized-2",
				OriginalToolCallID: "tool-raw-2",
				ToolName:           "inspect",
			}},
		},
		Prompts: []piagentgo.Message{
			piagentgo.NewTextMessage(piagentgo.RoleUser, "queued"),
		},
	}

	cloned := cloneSessionState(original)

	original.Snapshot.Model.ProviderConfig.Headers["x-trace-id"] = "mutated"
	original.Snapshot.Messages[0].Parts[0].Text = "mutated"
	original.Snapshot.Messages[0].ToolCalls[0].OriginalID = "mutated"
	original.Snapshot.PendingToolCalls[0].OriginalToolCallID = "mutated"
	original.Prompts[0].Parts[0].Text = "mutated"

	if cloned.Snapshot.Model.ProviderConfig.Headers["x-trace-id"] != "trace-2" {
		t.Fatalf("expected provider config header clone to remain isolated, got %+v", cloned.Snapshot.Model.ProviderConfig.Headers)
	}
	if cloned.Snapshot.Messages[0].Parts[0].Text != "reason" || cloned.Snapshot.Messages[0].Parts[0].Signature != "sig-2" {
		t.Fatalf("expected cloned thinking part to be preserved, got %+v", cloned.Snapshot.Messages[0].Parts)
	}
	if cloned.Snapshot.Messages[0].ToolCalls[0].OriginalID != "tool-raw-2" {
		t.Fatalf("expected cloned tool raw id to be preserved, got %+v", cloned.Snapshot.Messages[0].ToolCalls)
	}
	if cloned.Snapshot.PendingToolCalls[0].OriginalToolCallID != "tool-raw-2" {
		t.Fatalf("expected cloned pending tool raw id to be preserved, got %+v", cloned.Snapshot.PendingToolCalls)
	}
	if cloned.Prompts[0].Parts[0].Text != "queued" {
		t.Fatalf("expected queued prompt clone to remain isolated, got %+v", cloned.Prompts)
	}
}
