package piagentgo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDefaultPigoStreamModelReplaysRawIDsAndPreservesProviderFields(t *testing.T) {
	var requestBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("expected /v1/messages path, got %s", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "kimi-test-key" {
			t.Fatalf("expected kimi api key header, got %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("expected valid request json: %v", err)
		}

		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(buildAnthropicSSE(
			map[string]any{
				"type": "message_start",
				"message": map[string]any{
					"id": "msg_resp_1",
					"usage": map[string]any{
						"input_tokens":                12,
						"output_tokens":               0,
						"cache_read_input_tokens":     0,
						"cache_creation_input_tokens": 0,
					},
				},
			},
			map[string]any{
				"type":  "content_block_start",
				"index": 0,
				"content_block": map[string]any{
					"type": "thinking",
				},
			},
			map[string]any{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]any{
					"type":     "thinking_delta",
					"thinking": "reason first",
				},
			},
			map[string]any{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]any{
					"type":      "signature_delta",
					"signature": "sig_live",
				},
			},
			map[string]any{
				"type":  "content_block_stop",
				"index": 0,
			},
			map[string]any{
				"type":  "content_block_start",
				"index": 1,
				"content_block": map[string]any{
					"type": "tool_use",
					"id":   "toolu_live_1",
					"name": "lookup",
				},
			},
			map[string]any{
				"type":  "content_block_delta",
				"index": 1,
				"delta": map[string]any{
					"type":         "input_json_delta",
					"partial_json": "{\"value\":42}",
				},
			},
			map[string]any{
				"type":  "content_block_stop",
				"index": 1,
			},
			map[string]any{
				"type": "message_delta",
				"delta": map[string]any{
					"stop_reason": "tool_use",
				},
				"usage": map[string]any{
					"input_tokens":  12,
					"output_tokens": 4,
				},
			},
			map[string]any{
				"type": "message_stop",
			},
		)))
	}))
	defer server.Close()

	definition := AgentDefinition{
		DefaultModel: ModelRef{
			Provider: "kimi-coding",
			Model:    "kimi-k2-thinking",
			ProviderConfig: ProviderConfig{
				BaseURL: server.URL,
				APIKey:  "kimi-test-key",
			},
		},
	}

	model, ref, err := definition.ResolveModel(context.Background(), AgentSnapshot{})
	if err != nil {
		t.Fatalf("expected default provider model, got error: %v", err)
	}
	if model == nil {
		t.Fatal("expected resolved model")
	}

	stream, err := model.Stream(context.Background(), ModelRequest{
		Model:        ref,
		SystemPrompt: "Be concise.",
		Messages: []Message{
			NewTextMessage(RoleUser, "Use the previous context."),
			{
				Role:     RoleAssistant,
				Provider: "kimi-coding",
				API:      "anthropic-messages",
				Model:    "kimi-k2-thinking",
				Parts: []Part{
					{Type: PartTypeThinking, Text: "I should inspect the tool.", Signature: "sig_hist"},
				},
				ToolCalls: []ToolCall{{
					ID:         "tool_normalized_1",
					OriginalID: "tool_raw_1",
					Name:       "lookup",
					ParsedArgs: map[string]any{"value": 21},
				}},
				StopReason: StopReasonToolUse,
			},
			{
				Role: RoleTool,
				ToolResult: &ToolResultPayload{
					ToolCallID:         "tool_normalized_1",
					OriginalToolCallID: "tool_raw_1",
					ToolName:           "lookup",
					Content:            []Part{{Type: PartTypeText, Text: "42"}},
				},
			},
			NewTextMessage(RoleUser, "Continue."),
		},
		ThinkingLevel: ThinkingHigh,
	})
	if err != nil {
		t.Fatalf("expected stream, got error: %v", err)
	}

	var events []AssistantEvent
	for event := range stream.Events() {
		events = append(events, event)
	}

	final, err := stream.Wait()
	if err != nil {
		t.Fatalf("expected final message, got error: %v", err)
	}

	if final.ResponseID != "msg_resp_1" {
		t.Fatalf("expected response id msg_resp_1, got %q", final.ResponseID)
	}
	if final.Provider != "kimi-coding" || final.API != "anthropic-messages" || final.Model != "kimi-k2-thinking" {
		t.Fatalf("expected provider/api/model to be preserved, got %+v", final)
	}
	if final.StopReason != StopReasonToolUse {
		t.Fatalf("expected tool_use stop reason, got %q", final.StopReason)
	}
	if len(final.Parts) != 1 || final.Parts[0].Type != PartTypeThinking {
		t.Fatalf("expected one thinking part, got %+v", final.Parts)
	}
	if final.Parts[0].Text != "reason first" || final.Parts[0].Signature != "sig_live" {
		t.Fatalf("expected thinking text/signature to be preserved, got %+v", final.Parts[0])
	}
	if len(final.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %+v", final.ToolCalls)
	}
	if final.ToolCalls[0].ID != "toolu_live_1" || final.ToolCalls[0].OriginalID != "toolu_live_1" {
		t.Fatalf("expected tool call ids to be preserved, got %+v", final.ToolCalls[0])
	}

	var sawThinkingDelta bool
	var sawToolDelta bool
	for _, event := range events {
		if event.Type == AssistantEventThinkingDelta && event.Delta == "reason first" {
			sawThinkingDelta = true
		}
		if event.Type == AssistantEventToolCallDelta && strings.Contains(event.Delta, "\"value\":42") {
			sawToolDelta = true
		}
	}
	if !sawThinkingDelta {
		t.Fatal("expected thinking delta event")
	}
	if !sawToolDelta {
		t.Fatal("expected tool call delta event")
	}

	messages, ok := requestBody["messages"].([]any)
	if !ok || len(messages) != 4 {
		t.Fatalf("expected four outgoing messages, got %#v", requestBody["messages"])
	}

	assistantMessage, ok := messages[1].(map[string]any)
	if !ok {
		t.Fatalf("expected assistant replay message, got %#v", messages[1])
	}
	assistantContent, ok := assistantMessage["content"].([]any)
	if !ok || len(assistantContent) != 2 {
		t.Fatalf("expected assistant replay content, got %#v", assistantMessage["content"])
	}
	thinkingBlock := assistantContent[0].(map[string]any)
	if thinkingBlock["type"] != "thinking" || thinkingBlock["signature"] != "sig_hist" {
		t.Fatalf("expected replayed thinking signature, got %#v", thinkingBlock)
	}
	toolUseBlock := assistantContent[1].(map[string]any)
	if toolUseBlock["type"] != "tool_use" || toolUseBlock["id"] != "tool_raw_1" {
		t.Fatalf("expected replayed raw tool id, got %#v", toolUseBlock)
	}

	toolResultMessage, ok := messages[2].(map[string]any)
	if !ok {
		t.Fatalf("expected tool result replay message, got %#v", messages[2])
	}
	toolResultBlocks, ok := toolResultMessage["content"].([]any)
	if !ok || len(toolResultBlocks) != 1 {
		t.Fatalf("expected tool result blocks, got %#v", toolResultMessage["content"])
	}
	toolResultBlock := toolResultBlocks[0].(map[string]any)
	if toolResultBlock["type"] != "tool_result" || toolResultBlock["tool_use_id"] != "tool_raw_1" {
		t.Fatalf("expected tool result to keep raw tool id, got %#v", toolResultBlock)
	}
}

func TestDefaultPigoStreamModelUsesCodexSessionAndPreservesResponseID(t *testing.T) {
	var (
		requestBody map[string]any
		headers     http.Header
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/responses" {
			t.Fatalf("expected /codex/responses path, got %s", r.URL.Path)
		}
		headers = r.Header.Clone()
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("expected valid request json: %v", err)
		}

		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(buildCodexSSE(
			map[string]any{
				"type": "response.created",
				"response": map[string]any{
					"id": "resp_codex_1",
				},
			},
			map[string]any{
				"type": "response.output_item.done",
				"item": map[string]any{
					"type": "message",
					"id":   "msg_codex_1",
					"content": []map[string]any{
						{"type": "output_text", "text": "codex ok"},
					},
				},
			},
			map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"id":     "resp_codex_1",
					"status": "completed",
					"usage": map[string]any{
						"input_tokens":  5,
						"output_tokens": 2,
						"total_tokens":  7,
						"input_tokens_details": map[string]any{
							"cached_tokens": 0,
						},
					},
				},
			},
		)))
	}))
	defer server.Close()

	definition := AgentDefinition{
		DefaultModel: ModelRef{
			Provider: "openai-codex",
			Model:    "gpt-5.4",
			ProviderConfig: ProviderConfig{
				BaseURL: server.URL,
				APIKey:  makeOpenAICodexToken("acc_test"),
			},
		},
	}

	model, ref, err := definition.ResolveModel(context.Background(), AgentSnapshot{})
	if err != nil {
		t.Fatalf("expected default provider model, got error: %v", err)
	}

	stream, err := model.Stream(context.Background(), ModelRequest{
		Model:         ref,
		SystemPrompt:  "Be precise.",
		Messages:      []Message{NewTextMessage(RoleUser, "hello")},
		SessionID:     "session-123",
		ThinkingLevel: ThinkingMinimal,
	})
	if err != nil {
		t.Fatalf("expected stream, got error: %v", err)
	}

	for range stream.Events() {
	}
	final, err := stream.Wait()
	if err != nil {
		t.Fatalf("expected final message, got error: %v", err)
	}

	if final.ResponseID != "resp_codex_1" {
		t.Fatalf("expected response id resp_codex_1, got %q", final.ResponseID)
	}
	if final.Provider != "openai-codex" || final.API != "openai-codex-responses" || final.Model != "gpt-5.4" {
		t.Fatalf("expected provider/api/model to be preserved, got %+v", final)
	}
	if len(final.Parts) != 1 || final.Parts[0].Text != "codex ok" {
		t.Fatalf("expected codex text output, got %+v", final.Parts)
	}

	if headers.Get("conversation_id") != "session-123" {
		t.Fatalf("expected conversation_id header, got %q", headers.Get("conversation_id"))
	}
	if headers.Get("session_id") != "session-123" {
		t.Fatalf("expected session_id header, got %q", headers.Get("session_id"))
	}
	if requestBody["prompt_cache_key"] != "session-123" {
		t.Fatalf("expected prompt_cache_key session-123, got %#v", requestBody["prompt_cache_key"])
	}

	reasoning, ok := requestBody["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("expected reasoning payload, got %#v", requestBody["reasoning"])
	}
	if reasoning["effort"] != "low" {
		t.Fatalf("expected clamped minimal->low reasoning effort, got %#v", reasoning["effort"])
	}
}

func buildAnthropicSSE(events ...map[string]any) string {
	lines := make([]string, 0, len(events)+1)
	for _, event := range events {
		payload, _ := json.Marshal(event)
		lines = append(lines, "data: "+string(payload))
	}
	lines = append(lines, "data: [DONE]")
	return strings.Join(lines, "\n\n") + "\n\n"
}

func buildCodexSSE(events ...map[string]any) string {
	lines := make([]string, 0, len(events)+1)
	for _, event := range events {
		payload, _ := json.Marshal(event)
		lines = append(lines, "data: "+string(payload))
	}
	lines = append(lines, "data: [DONE]")
	return strings.Join(lines, "\n\n") + "\n\n"
}

func makeOpenAICodexToken(accountID string) string {
	payloadBytes, _ := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
		},
	})
	return "aaa." + base64.RawURLEncoding.EncodeToString(payloadBytes) + ".bbb"
}
