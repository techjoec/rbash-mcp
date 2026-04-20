// Package channels implements the `claude/channel` push-notification
// surface for Claude Code (experimental capability, v2.1.80+).
//
// The Go MCP SDK v1.5 does not expose a public typed API for emitting
// arbitrary notification methods like `notifications/claude/channel`, so
// we wrap the Transport/Connection pair and expose SendNotification on a
// capturable Connection reference. Uses public SDK surface only:
// mcp.Transport, mcp.Connection, jsonrpc.Request.
package channels

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Transport wraps another mcp.Transport and exposes the connected
// mcp.Connection for out-of-band JSON-RPC notification writes.
type Transport struct {
	Inner mcp.Transport

	mu   sync.Mutex
	conn *Conn
}

// Connect implements mcp.Transport. Records the returned connection so
// callers can fire notifications after the server is running.
func (t *Transport) Connect(ctx context.Context) (mcp.Connection, error) {
	inner, err := t.Inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	c := &Conn{Connection: inner}
	t.mu.Lock()
	t.conn = c
	t.mu.Unlock()
	return c, nil
}

// Conn returns the last captured connection, or nil if the server has
// not yet accepted a client.
func (t *Transport) Conn() *Conn {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.conn
}

// Conn wraps an mcp.Connection and adds SendNotification for arbitrary
// JSON-RPC method names.
type Conn struct {
	mcp.Connection
}

// SendNotification emits a JSON-RPC notification (request without an ID)
// with the given method and params. Safe for concurrent calls — the
// underlying mcp.Connection.Write contract allows concurrency.
func (c *Conn) SendNotification(ctx context.Context, method string, params any) error {
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		raw = b
	}
	// Zero-ID Request = JSON-RPC 2.0 Notification.
	return c.Write(ctx, &jsonrpc.Request{Method: method, Params: raw})
}
