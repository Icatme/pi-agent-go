package piagentgo

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAgentSnapshotJSONRoundTripPreservesExpandedRuntimeFields(t *testing.T) {
	t.Parallel()

	original := AgentSnapshot{
		SessionID:    "session-123",
		SystemPrompt: "be precise",
		Model: ModelRef{
			Provider: "openai-codex",
			Model:    "gpt-5.4",
			ProviderConfig: ProviderConfig{
				BaseURL: "https://example.test",
				APIKey:  "api-key",
				Headers: map[string]string{
					"x-trace-id": "trace-1",
				},
				Auth: &ProviderAuthConfig{
					Type:   ProviderAuthTypeOAuth,
					APIKey: "auth-key",
					OAuth: &OAuthCredentials{
						AccessToken:  "access",
						RefreshToken: "refresh",
						ExpiresUnix:  12345,
					},
				},
			},
			Metadata: map[string]any{
				"env": "test",
			},
		},
		Messages: []Message{
			{
				ID:         "assistant-1",
				Role:       RoleAssistant,
				Kind:       "final",
				Provider:   "openai-codex",
				API:        "openai-codex-responses",
				Model:      "gpt-5.4",
				ResponseID: "resp-1",
				Parts: []Part{
					{Type: PartTypeText, Text: "done"},
					{Type: PartTypeThinking, Text: "reasoning", Signature: "sig-1"},
				},
				ToolCalls: []ToolCall{{
					ID:               "tool-normalized-1",
					OriginalID:       "tool-raw-1",
					Name:             "lookup",
					Arguments:        json.RawMessage(`{"value":42}`),
					ParsedArgs:       map[string]any{"value": "42"},
					ThoughtSignature: "sig-1",
				}},
				ToolResult: &ToolResultPayload{
					ToolCallID:         "tool-normalized-1",
					OriginalToolCallID: "tool-raw-1",
					ToolName:           "lookup",
					Content: []Part{
						{Type: PartTypeText, Text: "42"},
						NewImagePart("image-payload", "image/png"),
					},
					Details: map[string]any{"status": "ok"},
					IsError: false,
				},
				Timestamp:    time.Unix(1700000000, 0).UTC(),
				Metadata:     map[string]any{"stage": "prod"},
				Payload:      map[string]any{"opaque": "value"},
				StopReason:   StopReasonToolUse,
				ErrorMessage: "",
			},
		},
		PendingToolCalls: []PendingToolCall{{
			ToolCallID:         "tool-normalized-1",
			OriginalToolCallID: "tool-raw-1",
			ToolName:           "lookup",
		}},
		Error: "transient",
		Metadata: map[string]any{
			"tenant": "acme",
		},
	}

	payload, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}

	var decoded AgentSnapshot
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}

	if decoded.SessionID != original.SessionID || decoded.SystemPrompt != original.SystemPrompt || decoded.Error != original.Error {
		t.Fatalf("expected snapshot headers to round-trip, got %+v", decoded)
	}
	if decoded.Model.Provider != original.Model.Provider || decoded.Model.Model != original.Model.Model {
		t.Fatalf("expected model ref to round-trip, got %+v", decoded.Model)
	}
	if decoded.Model.ProviderConfig.BaseURL != original.Model.ProviderConfig.BaseURL || decoded.Model.ProviderConfig.APIKey != original.Model.ProviderConfig.APIKey {
		t.Fatalf("expected provider config base/api key to round-trip, got %+v", decoded.Model.ProviderConfig)
	}
	if decoded.Model.ProviderConfig.Headers["x-trace-id"] != "trace-1" {
		t.Fatalf("expected provider headers to round-trip, got %+v", decoded.Model.ProviderConfig.Headers)
	}
	if decoded.Model.ProviderConfig.Auth == nil || decoded.Model.ProviderConfig.Auth.Type != ProviderAuthTypeOAuth {
		t.Fatalf("expected auth config to round-trip, got %+v", decoded.Model.ProviderConfig.Auth)
	}
	if decoded.Model.ProviderConfig.Auth.OAuth == nil || decoded.Model.ProviderConfig.Auth.OAuth.AccessToken != "access" {
		t.Fatalf("expected oauth credentials to round-trip, got %+v", decoded.Model.ProviderConfig.Auth)
	}
	if len(decoded.Messages) != 1 {
		t.Fatalf("expected one message after round-trip, got %+v", decoded.Messages)
	}
	if decoded.Messages[0].ResponseID != "resp-1" || decoded.Messages[0].Provider != "openai-codex" || decoded.Messages[0].API != "openai-codex-responses" || decoded.Messages[0].Model != "gpt-5.4" {
		t.Fatalf("expected provider fields on message to round-trip, got %+v", decoded.Messages[0])
	}
	if len(decoded.Messages[0].ToolCalls) != 1 || decoded.Messages[0].ToolCalls[0].OriginalID != "tool-raw-1" {
		t.Fatalf("expected tool call raw id to round-trip, got %+v", decoded.Messages[0].ToolCalls)
	}
	if decoded.Messages[0].ToolResult == nil || decoded.Messages[0].ToolResult.OriginalToolCallID != "tool-raw-1" {
		t.Fatalf("expected tool result raw id to round-trip, got %+v", decoded.Messages[0].ToolResult)
	}
	if len(decoded.Messages[0].ToolResult.Content) != 2 || decoded.Messages[0].ToolResult.Content[1].Data != "image-payload" || decoded.Messages[0].ToolResult.Content[1].MIMEType != "image/png" {
		t.Fatalf("expected tool result image content to round-trip, got %+v", decoded.Messages[0].ToolResult.Content)
	}
	if len(decoded.PendingToolCalls) != 1 || decoded.PendingToolCalls[0].OriginalToolCallID != "tool-raw-1" {
		t.Fatalf("expected pending tool call raw id to round-trip, got %+v", decoded.PendingToolCalls)
	}
}

func TestMessageJSONRoundTripPreservesThinkingAndProviderFields(t *testing.T) {
	t.Parallel()

	original := Message{
		ID:         "assistant-2",
		Role:       RoleAssistant,
		Provider:   "kimi-coding",
		API:        "anthropic-messages",
		Model:      "k2p5",
		ResponseID: "msg-2",
		Parts: []Part{
			{Type: PartTypeText, Text: "observed"},
			{Type: PartTypeThinking, Text: "need inspect", Signature: "sig-2", Redacted: true},
		},
		Timestamp:    time.Unix(1700000001, 0).UTC(),
		StopReason:   StopReasonStop,
		ErrorMessage: "none",
	}

	payload, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}

	var decoded Message
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}

	if decoded.ID != original.ID || decoded.Provider != original.Provider || decoded.API != original.API || decoded.Model != original.Model || decoded.ResponseID != original.ResponseID {
		t.Fatalf("expected provider envelope to round-trip, got %+v", decoded)
	}
	if len(decoded.Parts) != 2 || decoded.Parts[1].Signature != "sig-2" || !decoded.Parts[1].Redacted {
		t.Fatalf("expected thinking part to round-trip, got %+v", decoded.Parts)
	}
	if !decoded.Timestamp.Equal(original.Timestamp) {
		t.Fatalf("expected timestamp to round-trip, got %s", decoded.Timestamp)
	}
}

func TestToolCallAndToolResultPayloadJSONRoundTripPreservesRawIDs(t *testing.T) {
	t.Parallel()

	call := ToolCall{
		ID:               "tool-normalized-9",
		OriginalID:       "tool-raw-9",
		Name:             "search",
		Arguments:        json.RawMessage(`{"query":"status"}`),
		ParsedArgs:       map[string]any{"query": "status"},
		ThoughtSignature: "sig-9",
	}
	payload := ToolResultPayload{
		ToolCallID:         "tool-normalized-9",
		OriginalToolCallID: "tool-raw-9",
		ToolName:           "search",
		Content:            []Part{{Type: PartTypeText, Text: "ok"}},
		Details:            map[string]any{"code": "200"},
		IsError:            true,
	}

	callBody, err := json.Marshal(call)
	if err != nil {
		t.Fatalf("marshal tool call: %v", err)
	}
	resultBody, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal tool result payload: %v", err)
	}

	var decodedCall ToolCall
	if err := json.Unmarshal(callBody, &decodedCall); err != nil {
		t.Fatalf("unmarshal tool call: %v", err)
	}
	var decodedPayload ToolResultPayload
	if err := json.Unmarshal(resultBody, &decodedPayload); err != nil {
		t.Fatalf("unmarshal tool result payload: %v", err)
	}

	if decodedCall.OriginalID != "tool-raw-9" || string(decodedCall.Arguments) != `{"query":"status"}` || decodedCall.ThoughtSignature != "sig-9" {
		t.Fatalf("expected tool call raw fields to round-trip, got %+v", decodedCall)
	}
	if decodedPayload.OriginalToolCallID != "tool-raw-9" || decodedPayload.ToolCallID != "tool-normalized-9" || !decodedPayload.IsError {
		t.Fatalf("expected tool result raw fields to round-trip, got %+v", decodedPayload)
	}
}
