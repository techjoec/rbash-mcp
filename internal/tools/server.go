package tools

import (
	"crypto/rand"
	"sync"
	"time"
)

// State manages global application state for the tools package, including
// file access tracking and background shell processes. Access to State is
// synchronized via its embedded RWMutex to support concurrent read/write
// operations from multiple tool handlers.
type State struct {
	Mu sync.RWMutex

	// ReadFiles tracks the modification times of files that have been read,
	// used to detect when file content may have changed between operations.
	ReadFiles map[string]time.Time

	// BackgroundShells maps task IDs to their corresponding BackgroundShell
	// structs, allowing callers to monitor running processes and retrieve output.
	BackgroundShells map[string]*BackgroundShell

	// OnTaskExit is invoked when a backgrounded shell exits. Wired by the
	// main entrypoint to emit `notifications/claude/channel` push events.
	// Nil is safe; the monitoring goroutine checks before calling.
	OnTaskExit func(shell *BackgroundShell)
}

var globalState *State

func init() {
	globalState = NewState()
}

func NewState() *State {
	return &State{
		ReadFiles:        make(map[string]time.Time),
		BackgroundShells: make(map[string]*BackgroundShell),
	}
}

// GetState returns the global State singleton for the tools package.
func GetState() *State {
	return globalState
}

// generateTaskID returns a new `b` + 8 random alphanumeric-lowercase task
// identifier, matching Claude Code's native `bXXXXXXXX` background-task ID
// format so the model's training priors apply uniformly.
func generateTaskID() string {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	out := make([]byte, 0, 9)
	out = append(out, 'b')
	for _, b := range buf {
		out = append(out, alphabet[int(b)%len(alphabet)])
	}
	return string(out)
}
