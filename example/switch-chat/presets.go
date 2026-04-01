package main

func builtInPresets() []PresetSpec {
	return []PresetSpec{
		{
			Name:         "chat",
			Mode:         RuntimeModeChat,
			SystemPrompt: "You are a helpful general assistant. Answer directly, clearly, and concisely.",
		},
		{
			Name:         "coder",
			Mode:         RuntimeModeChat,
			SystemPrompt: "You are a pragmatic coding assistant. Focus on implementation details, debugging, and concrete engineering tradeoffs.",
		},
		{
			Name:             "reflect",
			Mode:             RuntimeModeReflection,
			SystemPrompt:     "You are a helpful assistant. Draft a strong answer to the user's request.",
			ReflectionPrompt: "You are a critical reviewer. Evaluate the draft, identify weaknesses, and say \"no major issues\" only when the answer is already strong enough.",
		},
	}
}

func defaultPresetName() string {
	return "chat"
}
