package tmux

import (
	"context"
	"fmt"
	"strings"
)

// Launch ensures a tmux window exists for session/window in dir, then sends
// the agent command (argv) into the new window's pane. argv is the already-
// resolved agent invocation (e.g. ["claude","--resume","<id>"]); each token
// is POSIX-single-quoted and the tokens are space-joined into one literal so
// tmux send-keys -l cannot reinterpret spaces, ';', '$', quotes, etc. Returns
// the pane ID from Connect.
func (o Tmux) Launch(ctx context.Context, session, window, dir string, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", fmt.Errorf("tmux Launch: argv must not be empty")
	}

	paneID, err := o.Connect(ctx, session, window, dir)
	if err != nil {
		return "", fmt.Errorf("tmux Launch: connect: %w", err)
	}

	// Quote each token independently so shell metacharacters (spaces, ';', '$',
	// quotes) cannot be reinterpreted by the shell receiving the input.
	tokens := make([]string, len(argv))
	for i, tok := range argv {
		tokens[i] = shellQuote(tok)
	}
	literal := strings.Join(tokens, " ")

	// send-keys into a freshly-spawned shell relies on the pty buffering input
	// before the shell finishes init (robust in practice; this is the established tmux pattern).
	if err := o.SendKeys(ctx, paneID, literal); err != nil {
		return "", fmt.Errorf("tmux Launch: send-keys: %w", err)
	}

	return paneID, nil
}
