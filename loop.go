package piagentgo

import "context"

// AgentLoop runs the low-level agent loop with new prompt messages.
func AgentLoop(ctx context.Context, definition AgentDefinition, snapshot *AgentSnapshot, prompts []Message, emit EventSink) (*AgentSnapshot, error) {
	return NewEngine().Run(ctx, definition, snapshot, prompts, emit)
}

// AgentLoopContinue runs the low-level agent loop from existing snapshot state.
func AgentLoopContinue(ctx context.Context, definition AgentDefinition, snapshot *AgentSnapshot, emit EventSink) (*AgentSnapshot, error) {
	return NewEngine().Continue(ctx, definition, snapshot, emit)
}

// RunAgentLoop runs the low-level agent loop with new prompt messages.
func RunAgentLoop(ctx context.Context, definition AgentDefinition, snapshot *AgentSnapshot, prompts []Message, emit EventSink) (*AgentSnapshot, error) {
	return NewEngine().Run(ctx, definition, snapshot, prompts, emit)
}

// RunAgentLoopContinue runs the low-level agent loop from existing snapshot state.
func RunAgentLoopContinue(ctx context.Context, definition AgentDefinition, snapshot *AgentSnapshot, emit EventSink) (*AgentSnapshot, error) {
	return NewEngine().Continue(ctx, definition, snapshot, emit)
}
