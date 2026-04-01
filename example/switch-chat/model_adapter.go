package main

import (
	"context"
	"fmt"
	"strings"

	core "github.com/Icatme/pi-agent-go"
	pigo "github.com/Icatme/pi-go/pkg/pigo"
)

type textPigoStreamModel struct {
	modelRef core.ModelRef
}

func newTextPigoStreamModel(modelRef core.ModelRef) core.StreamModel {
	return textPigoStreamModel{modelRef: cloneModelRef(modelRef)}
}

func (m textPigoStreamModel) Stream(ctx context.Context, request core.ModelRequest) (core.AssistantStream, error) {
	ref := m.modelRef
	if strings.TrimSpace(ref.Provider) == "" {
		ref.Provider = request.Model.Provider
	}
	if strings.TrimSpace(ref.Model) == "" {
		ref.Model = request.Model.Model
	}
	if strings.TrimSpace(ref.Provider) == "" || strings.TrimSpace(ref.Model) == "" {
		return nil, fmt.Errorf("switch-chat: reflection model is not configured")
	}

	model := pigo.GetModel(pigo.Provider(ref.Provider), ref.Model)
	if model == nil {
		return nil, fmt.Errorf("switch-chat: unsupported provider/model %q/%q", ref.Provider, ref.Model)
	}

	resolvedModel := *model
	if strings.TrimSpace(ref.ProviderConfig.BaseURL) != "" {
		resolvedModel.BaseURL = ref.ProviderConfig.BaseURL
	}

	stream := pigo.StreamSimple(
		resolvedModel,
		pigo.Context{
			SystemPrompt: request.SystemPrompt,
			Messages:     convertTextMessagesToPigo(request.Messages),
		},
		pigo.SimpleStreamOptions{
			APIKey:          strings.TrimSpace(ref.ProviderConfig.APIKey),
			Auth:            toPigoAuth(ref.Provider, ref.ProviderConfig.Auth),
			Headers:         cloneStringMap(ref.ProviderConfig.Headers),
			RequestContext:  ctx,
			SessionID:       request.SessionID,
			Reasoning:       toPigoThinkingLevel(request.ThinkingLevel),
			ThinkingBudgets: toPigoThinkingBudgets(request.ThinkingBudgets),
			MaxRetryDelay:   request.MaxRetryDelayMs,
			Transport:       toPigoTransport(request.Transport),
		},
	)
	return newPigoAssistantStream(stream), nil
}

type pigoAssistantStream struct {
	events chan core.AssistantEvent
	result chan pigoStreamResult
}

type pigoStreamResult struct {
	message core.Message
	err     error
}

func newPigoAssistantStream(stream *pigo.AssistantMessageEventStream) core.AssistantStream {
	wrapped := &pigoAssistantStream{
		events: make(chan core.AssistantEvent, 1024),
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
		}
		close(wrapped.result)
	}()

	return wrapped
}

func (s *pigoAssistantStream) Events() <-chan core.AssistantEvent {
	return s.events
}

func (s *pigoAssistantStream) Wait() (core.Message, error) {
	result, ok := <-s.result
	if !ok {
		return core.Message{}, fmt.Errorf("switch-chat: stream result unavailable")
	}
	return result.message, result.err
}

func convertTextMessagesToPigo(messages []core.Message) []pigo.Message {
	if len(messages) == 0 {
		return nil
	}

	converted := make([]pigo.Message, 0, len(messages))
	for _, message := range messages {
		text := messageText(message)
		if strings.TrimSpace(text) == "" {
			continue
		}
		switch message.Role {
		case core.RoleUser:
			converted = append(converted, pigo.UserMessage{
				Content:   text,
				Timestamp: message.Timestamp,
			})
		case core.RoleAssistant:
			converted = append(converted, pigo.AssistantMessage{
				Content: []pigo.ContentBlock{
					pigo.TextContent{Text: text},
				},
				Timestamp: message.Timestamp,
			})
		}
	}
	return converted
}

func convertPigoAssistantEvent(event pigo.AssistantMessageEvent) core.AssistantEvent {
	converted := core.AssistantEvent{
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
	return converted
}

func convertPigoAssistantMessage(message pigo.AssistantMessage) core.Message {
	parts := make([]core.Part, 0, len(message.Content))
	for _, block := range message.Content {
		switch typed := block.(type) {
		case pigo.TextContent:
			parts = append(parts, core.Part{
				Type:      core.PartTypeText,
				Text:      typed.Text,
				Signature: typed.TextSignature,
			})
		case pigo.ThinkingContent:
			parts = append(parts, core.Part{
				Type:      core.PartTypeThinking,
				Text:      typed.Thinking,
				Signature: typed.ThinkingSignature,
				Redacted:  typed.Redacted,
			})
		}
	}

	return core.Message{
		Role:         core.RoleAssistant,
		Parts:        parts,
		Timestamp:    message.Timestamp,
		API:          string(message.API),
		Provider:     string(message.Provider),
		Model:        message.Model,
		ResponseID:   message.ResponseID,
		StopReason:   fromPigoStopReason(message.StopReason),
		ErrorMessage: message.ErrorMessage,
	}
}

func fromPigoAssistantEventType(eventType pigo.AssistantMessageEventType) core.AssistantEventType {
	switch eventType {
	case pigo.AssistantMessageEventStart:
		return core.AssistantEventStart
	case pigo.AssistantMessageEventTextStart:
		return core.AssistantEventTextStart
	case pigo.AssistantMessageEventTextDelta:
		return core.AssistantEventTextDelta
	case pigo.AssistantMessageEventTextEnd:
		return core.AssistantEventTextEnd
	case pigo.AssistantMessageEventThinkingStart:
		return core.AssistantEventThinkingStart
	case pigo.AssistantMessageEventThinkingDelta:
		return core.AssistantEventThinkingDelta
	case pigo.AssistantMessageEventThinkingEnd:
		return core.AssistantEventThinkingEnd
	case pigo.AssistantMessageEventDone:
		return core.AssistantEventDone
	case pigo.AssistantMessageEventError:
		return core.AssistantEventError
	default:
		return core.AssistantEventError
	}
}

func fromPigoStopReason(reason pigo.StopReason) core.StopReason {
	switch reason {
	case pigo.StopReasonStop:
		return core.StopReasonStop
	case pigo.StopReasonLength:
		return core.StopReasonLength
	case pigo.StopReasonToolUse:
		return core.StopReasonToolUse
	case pigo.StopReasonError:
		return core.StopReasonError
	case pigo.StopReasonAborted:
		return core.StopReasonAborted
	default:
		return ""
	}
}

func toPigoThinkingLevel(level core.ThinkingLevel) pigo.ThinkingLevel {
	switch level {
	case core.ThinkingMinimal:
		return pigo.ThinkingLevelMinimal
	case core.ThinkingLow:
		return pigo.ThinkingLevelLow
	case core.ThinkingMedium:
		return pigo.ThinkingLevelMedium
	case core.ThinkingHigh:
		return pigo.ThinkingLevelHigh
	case core.ThinkingXHigh:
		return pigo.ThinkingLevelXHigh
	default:
		return ""
	}
}

func toPigoThinkingBudgets(budgets core.ThinkingBudgets) pigo.ThinkingBudgets {
	return pigo.ThinkingBudgets{
		Minimal: budgets[core.ThinkingMinimal],
		Low:     budgets[core.ThinkingLow],
		Medium:  budgets[core.ThinkingMedium],
		High:    budgets[core.ThinkingHigh],
	}
}

func toPigoTransport(transport core.Transport) pigo.Transport {
	switch transport {
	case core.TransportWebSocket:
		return pigo.TransportWebSocket
	case core.TransportAuto:
		return pigo.TransportAuto
	default:
		return pigo.TransportSSE
	}
}

func toPigoAuth(provider string, config *core.ProviderAuthConfig) map[pigo.Provider]pigo.AuthConfig {
	if config == nil {
		return nil
	}

	switch config.Type {
	case core.ProviderAuthTypeOAuth:
		if config.OAuth == nil {
			return nil
		}
		return map[pigo.Provider]pigo.AuthConfig{
			pigo.Provider(provider): {
				Type: pigo.AuthTypeOAuth,
				OAuth: &pigo.OAuthCredentials{
					AccessToken:  config.OAuth.AccessToken,
					RefreshToken: config.OAuth.RefreshToken,
					ExpiresUnix:  config.OAuth.ExpiresUnix,
				},
			},
		}
	case core.ProviderAuthTypeAPIKey:
		return map[pigo.Provider]pigo.AuthConfig{
			pigo.Provider(provider): {
				Type:   pigo.AuthTypeAPIKey,
				APIKey: config.APIKey,
			},
		}
	default:
		return nil
	}
}
