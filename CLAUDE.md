# CLAUDE.md — rbash-mcp

Context for Claude Code sessions on this repo.

## What this is

An MCP server that mirrors Claude Code's native `Bash` / `BashOutput` / `KillShell` tools (plus `Read` / `Write` / `Edit` / `Glob` / `Grep`) but runs inside an **incus guest VM**. Tool arguments travel as JSON over MCP, sidestepping the quoting/escaping pain of the built-in Bash tool for commands targeting the guest.

Also emits `claude/channel` exit-event pushes so Claude learns about backgrounded task completions between turns.

## Architecture in one line

Host `rbash-shim` ↔ unix socket (incus proxy device) ↔ in-guest `rbash-mcp` daemon. Claude sees a normal stdio MCP.

## Layout

- `cmd/rbash-mcp/` — in-guest daemon
- `cmd/rbash-shim/` — host-side stdio↔socket bridge
- `internal/tools/` — single Go package with all MCP tool handlers (bash family + file tools)
- `internal/channels/` — Transport/Connection wrapper + Pusher for `notifications/claude/channel`
- `deploy/` — systemd unit, incus profile example, install script
- `PLAN.md` — v1 scope, decisions, build steps
- `ROADMAP.md` — parked items (PTY, bash_stdin, regex/match/stall channel events, etc.)
- `refs/` — local-only reference material, gitignored (extracted Claude Code source snippets + channels integration package)

## Build

```bash
go build -o bin/rbash-mcp ./cmd/rbash-mcp
go build -o bin/rbash-shim ./cmd/rbash-shim
go test ./...
```

## Upstream lineage

This repo is a GitHub fork of [`mathematic-inc/claude-tools-mcp`](https://github.com/mathematic-inc/claude-tools-mcp). The `upstream` git remote points there for future cherry-picks (e.g. file-tool bug fixes). Apache-2.0.

## Launch flag

For `claude/channel` exit pushes to land in Claude's context, the user must launch Claude Code with:

```
claude --dangerously-load-development-channels server:rbash
```

Without the flag, tools work; pushes are silently dropped by Claude Code.

## Key decisions locked in

- Daemon runs as **root** inside the guest (guest is the trust boundary)
- **Single-client** MCP — one Claude session at a time; second connection rejected
- Socket path resolution: CLI arg → `$RBASH_SOCKET` → `$XDG_RUNTIME_DIR/rbash.sock` → `/run/rbash.sock`
- Tool names capitalized to match Claude Code native (`Bash`, `BashOutput`, `KillShell`, `Read`, …) so model instincts transfer
- Shell IDs: `bXXXXXXXX` (8 random alphanumeric), native format
- Env injected into every spawn: `CLAUDECODE=1`, `SHELL=/bin/bash`, `GIT_EDITOR=true`
- Output inline cap: 30 000 bytes (overridable via `BASH_MAX_OUTPUT_LENGTH` up to 150 000); past cap spills to `/var/lib/rbash/outputs/<task_id>.log`
- Kill semantics: process-group SIGKILL (sets `Setpgid: true` on spawn)

## Don't

- Don't split `internal/tools/` into sub-packages. It's one Go package by design.
- Don't import from `refs/` — it's research reference, not part of the build.
- Don't push without explicit user confirmation.
