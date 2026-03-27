package piagentgo

import "errors"

var (
	// ErrModelNotConfigured indicates that no model was configured for a run.
	ErrModelNotConfigured = errors.New("piagentgo: model not configured")
	// ErrAlreadyRunning indicates that an agent is already processing a request.
	ErrAlreadyRunning = errors.New("piagentgo: agent is already running")
	// ErrNoPromptMessages indicates that prompt was called without messages.
	ErrNoPromptMessages = errors.New("piagentgo: no prompt messages provided")
	// ErrNoMessagesToContinue indicates that continue was called without history.
	ErrNoMessagesToContinue = errors.New("piagentgo: no messages to continue from")
	// ErrCannotContinueFromAssistant indicates that continue was called from an assistant tail message.
	ErrCannotContinueFromAssistant = errors.New("piagentgo: cannot continue from assistant message")
	// ErrMaxTurnsExceeded indicates that a run exceeded its configured turn budget.
	ErrMaxTurnsExceeded = errors.New("piagentgo: maximum turns exceeded")
)
