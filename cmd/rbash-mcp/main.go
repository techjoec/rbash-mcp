// rbash-mcp runs inside the incus guest. It serves MCP tools (Bash,
// BashOutput, KillShell, ListShells, Read, Write, Edit, Glob, Grep) to
// a single Claude Code client over a unix socket, and emits
// claude/channel exit-event pushes so Claude learns about backgrounded
// task completions between turns.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/techjoec/rbash-mcp/internal/channels"
	"github.com/techjoec/rbash-mcp/internal/tools"
)

const (
	name         = "rbash"
	version      = "0.1.0"
	instructions = `Tools here run inside the incus guest VM. Bash executes commands on the guest; Read/Write/Edit/Glob/Grep operate on the guest filesystem.

Events from this server arrive as <channel source="rbash" ...> tags in your next turn. meta.event is "exit" when a backgrounded task completes. meta.task_id / meta.bash_id identify the task. meta.state is "completed" or "killed". meta.exit_code carries the return code. Call BashOutput(task_id=...) to drain any remaining output if useful before acting.`
)

func main() {
	socketPath := flag.String("socket", "", "Unix socket path to listen on (overrides $RBASH_SOCKET and defaults)")
	flag.Parse()

	path := resolveSocketPath(*socketPath)
	if err := run(path); err != nil {
		fmt.Fprintln(os.Stderr, "rbash-mcp:", err)
		os.Exit(1)
	}
}

// resolveSocketPath follows the documented priority order:
//
//  1. --socket flag
//  2. $RBASH_SOCKET env var
//  3. $XDG_RUNTIME_DIR/rbash.sock
//  4. /run/rbash.sock
func resolveSocketPath(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv("RBASH_SOCKET"); v != "" {
		return v
	}
	if v := os.Getenv("XDG_RUNTIME_DIR"); v != "" {
		return filepath.Join(v, "rbash.sock")
	}
	return "/run/rbash.sock"
}

func run(socketPath string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Clean up any stale socket from a prior run.
	_ = os.Remove(socketPath)
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(socketPath), err)
	}

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", socketPath, err)
	}
	defer ln.Close()
	if err := os.Chmod(socketPath, 0o600); err != nil {
		return fmt.Errorf("chmod %s: %w", socketPath, err)
	}
	slog.Info("rbash-mcp listening", "socket", socketPath)

	// Single-client semantics: accept one connection at a time, serve it,
	// then loop to accept the next. The BackgroundShell registry lives in
	// process memory so it survives across reconnects.
	var sessionMu sync.Mutex

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.Error("accept failed", "err", err)
			continue
		}
		// Reject concurrent clients — single-session guarantee.
		if !sessionMu.TryLock() {
			slog.Warn("rejecting second client (single-session)")
			_ = c.Close()
			continue
		}
		go func(conn net.Conn) {
			defer sessionMu.Unlock()
			defer conn.Close()
			serveClient(ctx, conn)
		}(c)
	}
}

func serveClient(ctx context.Context, conn net.Conn) {
	// Build MCP server per-session so each client gets a fresh handshake.
	caps := &mcp.ServerCapabilities{
		Experimental: map[string]any{"claude/channel": map[string]any{}},
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: name, Version: version}, &mcp.ServerOptions{
		Instructions: instructions,
		Capabilities: caps,
	})

	mcp.AddTool(srv, &tools.BashTool, tools.Bash)
	mcp.AddTool(srv, &tools.BashOutputTool, tools.BashOutput)
	mcp.AddTool(srv, &tools.KillShellTool, tools.KillShell)
	mcp.AddTool(srv, &tools.ListShellsTool, tools.ListShells)
	mcp.AddTool(srv, &tools.ReadTool, tools.Read)
	mcp.AddTool(srv, &tools.WriteTool, tools.Write)
	mcp.AddTool(srv, &tools.EditTool, tools.Edit)
	mcp.AddTool(srv, &tools.GlobTool, tools.Glob)
	mcp.AddTool(srv, &tools.GrepTool, tools.Grep)

	// Wrap the IO transport so we can emit claude/channel pushes.
	inner := &mcp.IOTransport{Reader: conn, Writer: conn}
	transport := &channels.Transport{Inner: inner}
	pusher := channels.NewPusher(transport)

	// Wire the bash task-exit callback into the channels pusher. We
	// reset on every new connection so a stale closure doesn't hang
	// around when a prior session dies.
	state := tools.GetState()
	state.Mu.Lock()
	state.OnTaskExit = func(shell *tools.BackgroundShell) {
		_ = pusher.PushExit(
			context.Background(),
			shell.ID,
			shell.ExitCode,
			shell.StateName(),
			shell.DurationMillis(),
			shell.Command,
		)
	}
	state.Mu.Unlock()
	defer func() {
		state.Mu.Lock()
		state.OnTaskExit = nil
		state.Mu.Unlock()
	}()

	if err := srv.Run(ctx, transport); err != nil {
		slog.Error("server run exited", "err", err)
	}
}
