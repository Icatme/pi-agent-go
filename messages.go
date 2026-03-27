package piagentgo

import "time"

// NewTextPart creates a text content part.
func NewTextPart(text string) Part {
	return Part{
		Type: PartTypeText,
		Text: text,
	}
}

// NewImagePart creates an image content part.
func NewImagePart(imageURL, mimeType string) Part {
	return Part{
		Type:     PartTypeImage,
		ImageURL: imageURL,
		MIMEType: mimeType,
	}
}

// NewUserMessage creates a user message from content parts.
func NewUserMessage(parts ...Part) Message {
	return Message{
		Role:      RoleUser,
		Parts:     cloneParts(parts),
		Timestamp: time.Now().UTC(),
	}
}

// NewUserTextMessage creates a user text message with optional image parts appended.
func NewUserTextMessage(text string, images ...Part) Message {
	parts := []Part{NewTextPart(text)}
	parts = append(parts, cloneParts(images)...)
	return NewUserMessage(parts...)
}

// NewCustomMessage creates a custom message that can be transformed before model use.
func NewCustomMessage(kind string, payload map[string]any, parts ...Part) Message {
	return Message{
		Role:      RoleCustom,
		Kind:      kind,
		Parts:     cloneParts(parts),
		Payload:   cloneStringAnyMap(payload),
		Timestamp: time.Now().UTC(),
	}
}
