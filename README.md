# rbash-mcp

MCP server that mirrors Claude Code's native `Bash` / `BashOutput` / `KillShell` tools (plus `Read` / `Write` / `Edit` / `Glob` / `Grep`) but runs **inside an incus guest VM**. Commands travel as JSON argv over MCP, dodging the quoting/escaping pain of the built-in Bash tool for commands targeting the guest.

Emits `claude/channel` exit-event pushes so Claude learns about backgrounded task completions between turns.

## Architecture

```
┌──────────────────────┐          ┌──────────────────────────┐
│ Host                 │          │ Incus guest              │
│                      │          │                          │
│ Claude Code          │          │ rbash-mcp daemon         │
│   │                  │          │   - MCP JSON-RPC over    │
│   │ stdio            │          │     unix socket          │
│   ▼                  │          │   - per-call bash -c     │
│ rbash-shim           │◄────────►│   - shell registry       │
│ (stdio ↔ socket)     │  unix    │   - file tool set        │
└──────────────────────┘  socket  └──────────────────────────┘
                          via incus proxy device
```

## Build

```bash
go build -o bin/rbash-mcp ./cmd/rbash-mcp
go build -o bin/rbash-shim ./cmd/rbash-shim
```

## Install (guest)

1. Copy `bin/rbash-mcp` into the guest (e.g. `/usr/local/bin/rbash-mcp`).
2. Install the systemd unit from `deploy/systemd/rbash-mcp.service`.
3. `systemctl enable --now rbash-mcp`.

The daemon listens on `/run/rbash.sock` inside the guest by default.

## Install (host)

1. Copy `bin/rbash-shim` onto the host (e.g. `~/.local/bin/rbash-shim`).
2. Configure an incus proxy device that forwards the host-side socket to the guest-side socket:
   ```
   incus config device add <guest> rbash-sock proxy \
     listen=unix:/run/rbash.sock \
     connect=unix:/run/rbash.sock
   ```
3. Add rbash to Claude Code's MCP config:
   ```json
   {
     "mcpServers": {
       "rbash": {
         "command": "/home/you/.local/bin/rbash-shim"
       }
     }
   }
   ```
   Or pass a non-default socket path:
   ```json
   {
     "mcpServers": {
       "rbash": {
         "command": "/home/you/.local/bin/rbash-shim",
         "args": ["/run/rbash.sock"]
       }
     }
   }
   ```

## Launch

To receive `claude/channel` exit-event pushes, Claude Code must be launched with the development-channels flag during the research preview:

```bash
claude --dangerously-load-development-channels server:rbash
```

Without the flag, tools work normally but exit-event pushes are dropped by Claude Code.

## Tools

| Name | Purpose |
|---|---|
| `Bash` | Run a command on the guest. `run_in_background: true` returns a `backgroundTaskId`. |
| `BashOutput` | Retrieve output from a background task by `task_id`. `block: true` (default) waits for completion. |
| `KillShell` | Terminate a running background task. Accepts `task_id` or deprecated `shell_id`. |
| `ListShells` | List all background tasks with status. |
| `Read` / `Write` / `Edit` / `Glob` / `Grep` | File tools scoped to the **guest** filesystem. |

All tools surface to Claude as `mcp__rbash__<Name>`.

## Socket path resolution

Both `rbash-mcp` (daemon) and `rbash-shim` (host bridge) resolve the socket path in this order:

1. CLI arg (positional or `--socket=<path>`)
2. `$RBASH_SOCKET` env var
3. `$XDG_RUNTIME_DIR/rbash.sock`
4. `/run/rbash.sock`

## License

Apache-2.0. Based on [`mathematic-inc/claude-tools-mcp`](https://github.com/mathematic-inc/claude-tools-mcp); see `LICENSE`.
