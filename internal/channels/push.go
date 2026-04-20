package channels

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// NotificationMethod is the JSON-RPC method name Claude Code expects for
// claude/channel pushes.
const NotificationMethod = "notifications/claude/channel"

// metaKeyPattern enforces Claude Code's meta-key rule: only
// `[A-Za-z0-9_]+`. Keys with other characters are silently dropped by
// Claude Code — we drop them here with a stderr log so developer bugs
// surface during the research preview rather than vanishing.
var metaKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// Pusher owns a reference to the transport so background goroutines
// (e.g. the bash Cmd.Wait goroutine) can publish events without holding
// a request context.
type Pusher struct {
	mu        sync.Mutex
	transport *Transport
}

// NewPusher binds a Pusher to a Transport.
func NewPusher(t *Transport) *Pusher { return &Pusher{transport: t} }

// Push emits a claude/channel notification. Silently no-ops if no
// connection has been captured yet.
func (p *Pusher) Push(ctx context.Context, content string, meta map[string]any) error {
	p.mu.Lock()
	t := p.transport
	p.mu.Unlock()
	if t == nil {
		return nil
	}
	c := t.Conn()
	if c == nil {
		slog.Warn("channel push dropped: no connection captured")
		return nil
	}
	safe := sanitizeMeta(meta)
	params := map[string]any{"content": content}
	if len(safe) > 0 {
		params["meta"] = safe
	}
	return c.SendNotification(ctx, NotificationMethod, params)
}

// PushExit is the typed wrapper for the v1 exit event. Fires when a
// backgrounded shell completes or is killed.
func (p *Pusher) PushExit(ctx context.Context, taskID string, exitCode int, state string, durationMs int64, command string) error {
	content := fmt.Sprintf(
		"Background shell %s %s. Exit %d. duration %dms.",
		taskID, state, exitCode, durationMs,
	)
	if command != "" {
		if len(command) > 120 {
			command = command[:120]
		}
		content += "\ncommand: " + command
	}
	meta := map[string]any{
		"event":       "exit",
		"task_id":     taskID,
		"bash_id":     taskID, // compat name matching the native convention
		"state":       state,
		"exit_code":   exitCode,
		"duration_ms": durationMs,
	}
	return p.Push(ctx, content, meta)
}

// sanitizeMeta coerces all values to strings and drops keys that don't
// match `[A-Za-z0-9_]+`. Claude Code's protocol requires this.
func sanitizeMeta(meta map[string]any) map[string]string {
	if len(meta) == 0 {
		return nil
	}
	clean := make(map[string]string, len(meta))
	for k, v := range meta {
		if !metaKeyPattern.MatchString(k) {
			slog.Warn("channel push: dropping invalid meta key", "key", k, "allowed", "[A-Za-z0-9_]+")
			continue
		}
		clean[k] = stringify(v)
	}
	return clean
}

func stringify(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case time.Duration:
		return strconv.FormatInt(x.Milliseconds(), 10)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}
