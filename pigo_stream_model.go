package piagentgo

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	pigo "github.com/Icatme/pi-go/pkg/pigo"
)

var defaultProviderStreamModel StreamModel = pigoStreamModel{}

type pigoStreamModel struct{}

type pigoAssistantStream struct {
	events chan AssistantEvent
	result chan pigoStreamResult
}

type pigoStreamResult struct {
	message Message
	err     error
}

func (pigoStreamModel) Stream(ctx context.Context, request ModelRequest) (AssistantStream, error) {
	provider := strings.TrimSpace(request.Model.Provider)
	modelID := strings.TrimSpace(request.Model.Model)
	if provider == "" || modelID == "" {
		return nil, ErrModelNotConfigured
	}

	model := pigo.GetModel(pigo.Provider(provider), modelID)
	if model == nil {
		return nil, fmt.Errorf("piagentgo: unsupported provider/model %q/%q", provider, modelID)
	}

	resolvedModel := *model
	if request.Model.ProviderConfig.BaseURL != "" {
		resolvedModel.BaseURL = request.Model.ProviderConfig.BaseURL
	}

	stream := pigo.StreamSimple(
		resolvedModel,
		buildPigoContext(request.SystemPrompt, request.Messages, request.Tools),
		buildPigoStreamOptions(ctx, request, resolvedModel.Provider),
	)
	return newPigoAssistantStream(stream), nil
}

func newPigoAssistantStream(stream *pigo.AssistantMessageEventStream) AssistantStream {
	wrapped := &pigoAssistantStream{
		events: make(chan AssistantEvent, 1024),
		result: make(chan pigoStreamResult, 1),
	}

	go func() {
		defer close(wrapped.events)
		for event := range stream.Events() {
			wrapped.events <- convertPigoAssistantEvent(event)
		}

		final := stream.Result()
		wrapped.result <- pigoStreamResult{
			message: convertPigoAssistantMessage(final),
			err:     nil,
		}
		close(wrapped.result)
	}()

	return wrapped
}

func (s *pigoAssistantStream) Events() <-chan AssistantEvent {
	return s.events
}

func (s *pigoAssistantStream) Wait() (Message, error) {
	result, ok := <-s.result
	if !ok {
		return Message{}, fmt.Errorf("piagentgo: stream result unavailable")
	}
	return result.message, result.err
}

func buildPigoContext(systemPrompt string, messages []Message, tools []ToolDefinition) pigo.Context {
	return pigo.Context{
		SystemPrompt: systemPrompt,
		Messages:     convertMessagesToPigo(messages),
		Tools:        convertToolsToPigo(tools),
	}
}

func buildPigoStreamOptions(ctx context.Context, request ModelRequest, provider pigo.Provider) pigo.SimpleStreamOptions {
	apiKey := strings.TrimSpace(request.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(request.Model.ProviderConfig.APIKey)
	}
	if apiKey == "" {
		apiKey = pigo.GetEnvAPIKey(provider)
	}

	return pigo.SimpleStreamOptions{
		APIKey:          apiKey,
		Auth:            toPigoAuthConfigs(provider, request.Model.ProviderConfig.Auth),
		Headers:         cloneStringMap(request.Model.ProviderConfig.Headers),
		Transport:       toPigoTransport(request.Transport),
		SessionID:       request.SessionID,
		MaxRetryDelay:   request.MaxRetryDelayMs,
		RequestContext:  ctx,
		Reasoning:       toPigoThinkingLevel(request.ThinkingLevel),
		ThinkingBudgets: toPigoThinkingBudgets(request.ThinkingBudgets),
	}
}

func convertMessagesToPigo(messages []Message) []pigo.Message {
	if len(messages) == 0 {
		return nil
	}

	converted := make([]pigo.Message, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case RoleUser:
			if user, ok := convertUserMessageToPigo(message); ok {
				converted = append(converted, user)
			}
		case RoleAssistant:
			converted = append(converted, convertAssistantMessageToPigo(message))
		case RoleTool:
			if toolResult, ok := convertToolResultMessageToPigo(message); ok {
				converted = append(converted, toolResult)
			}
		}
	}
	return converted
}

func convertUserMessageToPigo(message Message) (pigo.Message, bool) {
	if len(message.Parts) == 0 {
		return nil, false
	}

	if len(message.Parts) == 1 && message.Parts[0].Type == PartTypeText {
		text := strings.TrimSpace(message.Parts[0].Text)
		if text == "" {
			return nil, false
		}
		return pigo.UserMessage{
			Content:   text,
			Timestamp: message.Timestamp,
		}, true
	}

	content := convertPartsToPigoBlocks(message.Parts)
	if len(content) == 0 {
		return nil, false
	}

	return pigo.UserMessage{
		Content:   content,
		Timestamp: message.Timestamp,
	}, true
}

func convertAssistantMessageToPigo(message Message) pigo.Message {
	content := convertPartsToPigoBlocks(message.Parts)
	for _, call := range message.ToolCalls {
		content = append(content, pigo.ToolCall{
			ID:               providerToolCallID(call),
			Name:             call.Name,
			Arguments:        toolCallArgumentsMap(call),
			ThoughtSignature: call.ThoughtSignature,
		})
	}

	return pigo.AssistantMessage{
		Content:      content,
		API:          pigo.API(message.API),
		Provider:     pigo.Provider(message.Provider),
		Model:        message.Model,
		ResponseID:   message.ResponseID,
		StopReason:   toPigoStopReason(message.StopReason),
		ErrorMessage: message.ErrorMessage,
		Timestamp:    message.Timestamp,
	}
}

func convertToolResultMessageToPigo(message Message) (pigo.Message, bool) {
	if message.ToolResult == nil {
		return nil, false
	}

	toolCallID := strings.TrimSpace(message.ToolResult.OriginalToolCallID)
	if toolCallID == "" {
		toolCallID = strings.TrimSpace(message.ToolResult.ToolCallID)
	}
	if toolCallID == "" {
		return nil, false
	}

	content := convertPartsToPigoBlocks(message.ToolResult.Content)
	return pigo.ToolResultMessage{
		ToolCallID: toolCallID,
		ToolName:   message.ToolResult.ToolName,
		Content:    content,
		IsError:    message.ToolResult.IsError,
		Timestamp:  message.Timestamp,
	}, true
}

func convertPartsToPigoBlocks(parts []Part) []pigo.ContentBlock {
	if len(parts) == 0 {
		return nil
	}

	blocks := make([]pigo.ContentBlock, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case PartTypeText:
			if strings.TrimSpace(part.Text) == "" {
				continue
			}
			blocks = append(blocks, pigo.TextContent{
				Text:          part.Text,
				TextSignature: part.Signature,
			})
		case PartTypeThinking:
			if strings.TrimSpace(part.Text) == "" && strings.TrimSpace(part.Signature) == "" && !part.Redacted {
				continue
			}
			blocks = append(blocks, pigo.ThinkingContent{
				Thinking:          part.Text,
				ThinkingSignature: part.Signature,
				Redacted:          part.Redacted,
			})
		case PartTypeImage:
			data, mimeType, ok := extractInlineImageData(part)
			if !ok {
				continue
			}
			blocks = append(blocks, pigo.ImageContent{
				Data:     data,
				MIMEType: mimeType,
			})
		}
	}
	return blocks
}

func convertToolsToPigo(tools []ToolDefinition) []pigo.Tool {
	if len(tools) == 0 {
		return nil
	}

	converted := make([]pigo.Tool, 0, len(tools))
	for _, tool := range tools {
		converted = append(converted, pigo.Tool{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  cloneStringAnyMap(tool.Parameters),
		})
	}
	return converted
}

func convertPigoAssistantEvent(event pigo.AssistantMessageEvent) AssistantEvent {
	converted := AssistantEvent{
		Type:         fromPigoAssistantEventType(event.Type),
		Delta:        event.Delta,
		ContentIndex: event.ContentIndex,
		Reason:       fromPigoStopReason(event.Reason),
	}

	switch event.Type {
	case pigo.AssistantMessageEventDone:
		converted.Message = convertPigoAssistantMessage(event.Message)
	case pigo.AssistantMessageEventError:
		converted.Message = convertPigoAssistantMessage(event.Error)
	default:
		converted.Message = convertPigoAssistantMessage(event.Partial)
	}

	if event.ToolCall.Name != "" || event.ToolCall.ID != "" {
		toolCall := convertPigoToolCall(event.ToolCall)
		converted.ToolCall = &toolCall
	}

	return converted
}

func convertPigoAssistantMessage(message pigo.AssistantMessage) Message {
	parts := make([]Part, 0, len(message.Content))
	toolCalls := make([]ToolCall, 0, len(message.Content))
	for _, block := range message.Content {
		switch typed := block.(type) {
		case pigo.TextContent:
			parts = append(parts, Part{
				Type:      PartTypeText,
				Text:      typed.Text,
				Signature: typed.TextSignature,
			})
		case pigo.ThinkingContent:
			parts = append(parts, Part{
				Type:      PartTypeThinking,
				Text:      typed.Thinking,
				Signature: typed.ThinkingSignature,
				Redacted:  typed.Redacted,
			})
		case pigo.ToolCall:
			toolCalls = append(toolCalls, convertPigoToolCall(typed))
		}
	}

	return Message{
		Role:         RoleAssistant,
		Parts:        parts,
		ToolCalls:    toolCalls,
		Timestamp:    message.Timestamp,
		API:          string(message.API),
		Provider:     string(message.Provider),
		Model:        message.Model,
		ResponseID:   message.ResponseID,
		StopReason:   fromPigoStopReason(message.StopReason),
		ErrorMessage: message.ErrorMessage,
	}
}

func convertPigoToolCall(call pigo.ToolCall) ToolCall {
	return ToolCall{
		ID:               call.ID,
		OriginalID:       call.ID,
		Name:             call.Name,
		Arguments:        marshalRawJSON(call.Arguments),
		ParsedArgs:       cloneStringAnyMap(call.Arguments),
		ThoughtSignature: call.ThoughtSignature,
	}
}

func providerToolCallID(call ToolCall) string {
	if strings.TrimSpace(call.OriginalID) != "" {
		return call.OriginalID
	}
	return call.ID
}

func toolCallArgumentsMap(call ToolCall) map[string]any {
	if len(call.ParsedArgs) > 0 {
		return cloneStringAnyMap(call.ParsedArgs)
	}
	if len(call.Arguments) == 0 {
		return nil
	}

	var parsed map[string]any
	if err := json.Unmarshal(call.Arguments, &parsed); err != nil {
		return nil
	}
	return parsed
}

func extractInlineImageData(part Part) (string, string, bool) {
	if strings.TrimSpace(part.ImageURL) == "" {
		return "", "", false
	}
	if !strings.HasPrefix(part.ImageURL, "data:") {
		return "", "", false
	}

	payload := strings.TrimPrefix(part.ImageURL, "data:")
	mimeType, data, found := strings.Cut(payload, ",")
	if !found {
		return "", "", false
	}

	mimeType = strings.TrimSuffix(mimeType, ";base64")
	if mimeType == "" {
		mimeType = part.MIMEType
	}
	if mimeType == "" {
		return "", "", false
	}
	return data, mimeType, true
}

func toPigoAuthConfigs(provider pigo.Provider, config *ProviderAuthConfig) map[pigo.Provider]pigo.AuthConfig {
	if config == nil {
		return nil
	}

	authType := pigo.AuthType(config.Type)
	if authType == "" {
		return nil
	}

	return map[pigo.Provider]pigo.AuthConfig{
		provider: {
			Type:   authType,
			APIKey: config.APIKey,
			OAuth:  toPigoOAuthCredentials(config.OAuth),
		},
	}
}

func toPigoOAuthCredentials(credentials *OAuthCredentials) *pigo.OAuthCredentials {
	if credentials == nil {
		return nil
	}

	return &pigo.OAuthCredentials{
		AccessToken:  credentials.AccessToken,
		RefreshToken: credentials.RefreshToken,
		ExpiresUnix:  credentials.ExpiresUnix,
	}
}

func toPigoTransport(transport Transport) pigo.Transport {
	switch transport {
	case TransportWebSocket:
		return pigo.TransportWebSocket
	case TransportAuto:
		return pigo.TransportAuto
	default:
		return pigo.TransportSSE
	}
}

func toPigoThinkingLevel(level ThinkingLevel) pigo.ThinkingLevel {
	switch level {
	case ThinkingMinimal:
		return pigo.ThinkingLevelMinimal
	case ThinkingLow:
		return pigo.ThinkingLevelLow
	case ThinkingMedium:
		return pigo.ThinkingLevelMedium
	case ThinkingHigh:
		return pigo.ThinkingLevelHigh
	case ThinkingXHigh:
		return pigo.ThinkingLevelXHigh
	default:
		return ""
	}
}

func toPigoThinkingBudgets(budgets ThinkingBudgets) pigo.ThinkingBudgets {
	return pigo.ThinkingBudgets{
		Minimal: budgets[ThinkingMinimal],
		Low:     budgets[ThinkingLow],
		Medium:  budgets[ThinkingMedium],
		High:    budgets[ThinkingHigh],
	}
}

func toPigoStopReason(reason StopReason) pigo.StopReason {
	switch reason {
	case StopReasonStop:
		return pigo.StopReasonStop
	case StopReasonLength:
		return pigo.StopReasonLength
	case StopReasonToolUse:
		return pigo.StopReasonToolUse
	case StopReasonError:
		return pigo.StopReasonError
	case StopReasonAborted:
		return pigo.StopReasonAborted
	default:
		return ""
	}
}

func fromPigoStopReason(reason pigo.StopReason) StopReason {
	switch reason {
	case pigo.StopReasonStop:
		return StopReasonStop
	case pigo.StopReasonLength:
		return StopReasonLength
	case pigo.StopReasonToolUse:
		return StopReasonToolUse
	case pigo.StopReasonError:
		return StopReasonError
	case pigo.StopReasonAborted:
		return StopReasonAborted
	default:
		return ""
	}
}

func fromPigoAssistantEventType(eventType pigo.AssistantMessageEventType) AssistantEventType {
	switch eventType {
	case pigo.AssistantMessageEventStart:
		return AssistantEventStart
	case pigo.AssistantMessageEventTextStart:
		return AssistantEventTextStart
	case pigo.AssistantMessageEventTextDelta:
		return AssistantEventTextDelta
	case pigo.AssistantMessageEventTextEnd:
		return AssistantEventTextEnd
	case pigo.AssistantMessageEventThinkingStart:
		return AssistantEventThinkingStart
	case pigo.AssistantMessageEventThinkingDelta:
		return AssistantEventThinkingDelta
	case pigo.AssistantMessageEventThinkingEnd:
		return AssistantEventThinkingEnd
	case pigo.AssistantMessageEventToolCallStart:
		return AssistantEventToolCallStart
	case pigo.AssistantMessageEventToolCallDelta:
		return AssistantEventToolCallDelta
	case pigo.AssistantMessageEventToolCallEnd:
		return AssistantEventToolCallEnd
	case pigo.AssistantMessageEventDone:
		return AssistantEventDone
	case pigo.AssistantMessageEventError:
		return AssistantEventError
	default:
		return AssistantEventError
	}
}

func marshalRawJSON(value any) json.RawMessage {
	if value == nil {
		return nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return payload
}
