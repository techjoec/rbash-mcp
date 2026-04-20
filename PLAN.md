# rbash-mcp — v1 Build Plan

## Purpose

An MCP server that mimics Claude Code's native `Bash` + `BashOutput` + `KillShell` tools but executes inside an incus guest VM. Arguments travel as JSON (no shell parsing on the client side), dodging the quoting/escaping pain of the built-in Bash tool for commands targeting the guest.

Secondary surface (free, from the Go base): `Read`, `Write`, `Edit`, `Glob`, `Grep` — all scoped to the guest filesystem.

Plus a **channels push** surface: when a backgrounded shell exits, the daemon emits a `notifications/claude/channel` JSON-RPC notification that Claude Code surfaces in the next turn as a `<channel source="rbash" event="exit" …>` tag. Poll-based `BashOutput` still works; channels add the "fire, forget, get pinged when done" flow.

## Architecture

```
┌──────────────────────┐          ┌──────────────────────────┐
│ Host                 │          │ Incus guest              │
│                      │          │                          │
│ Claude Code          │          │ rbash-mcp daemon         │
│   │                  │          │   - MCP JSON-RPC over    │
│   │ stdio            │          │     unix socket          │
│   ▼                  │          │   - per-call bash -c     │
│ host-side shim       │◄────────►│   - shell registry       │
│ (stdio ↔ socket)     │  unix    │   - file tool set        │
└──────────────────────┘  socket  └──────────────────────────┘
                          via incus proxy device
```

- **Host-side shim**: minimal stdio↔socket bridge invoked by Claude Code as the MCP command. Forwards JSON-RPC frames to the guest unix socket.
- **Guest daemon**: persistent MCP server, holds shell registry, spawns `bash -c <cmd>` per call, manages background jobs, serves file tools.
- **Transport**: incus proxy device exposes the in-guest unix socket onto the host filesystem. From Claude's perspective this is a plain stdio MCP.

## v1 Scope

### Tool surface (mirrors Claude Code native, over MCP namespace)

Surfaced to Claude as `mcp__rbash__*`. All names, parameters, and descriptions mirror native where applicable so the model's training priors activate.

#### `Bash`

- **Input**
  - `command` (string, required)
  - `timeout` (number, optional, default 120000, max 600000) — milliseconds
  - `description` (string, optional) — 5–10 word active-voice description
  - `run_in_background` (bool, optional, default false)
- **Output**
  - `stdout` (string)
  - `stderr` (string)
  - `interrupted` (bool)
  - `backgroundTaskId` (string, conditional) — `bXXXXXXXX` format
  - `persistedOutputPath` (string, conditional) — set when output exceeds inline limit
  - `persistedOutputSize` (number, conditional)
  - `returnCodeInterpretation` (string, conditional)
  - `noOutputExpected` (bool, conditional)
- **Description**: native verbatim, minus sandbox and git-commit/PR sections, plus a one-line lead: *"Executes a bash command inside the incus guest VM. Use this for all shell work on the guest; filesystem and network calls happen in the guest, not on the host."*
- **Semantics**
  - Spawn: `exec.Command("bash", "-c", command)` — command string travels as a single argv, zero client-side shell parsing
  - Env injection: `CLAUDECODE=1`, `SHELL=/bin/bash`, `GIT_EDITOR=true`
  - cwd: tracked per-session via tempfile (matches native behavior)
  - stdio: `[pipe, fileFd, fileFd]` for foreground; same for background (output spills to disk via O_APPEND)
  - Foreground: context-timeout, blocks, returns full output
  - Background: `cmd.Start()`, returns `backgroundTaskId` immediately, goroutine does `cmd.Wait()` and reaps
  - Output inline cap: 30 KB (env-overridable to 150 KB). Past cap → trailer `\nOutput truncated (NNN kB total). Full output saved to: /path\n`, with full output persisted to file

#### `BashOutput`

- **Input**
  - `task_id` (string, required) — accepts backgroundTaskId from Bash
  - `block` (bool, optional, default true)
  - `timeout` (number, optional, default 30000, max 600000) — only meaningful with `block: true`
- **Output**
  - `retrieval_status` ("success" | "timeout" | "not_ready")
  - `task` (object | null)
    - `task_id` (string)
    - `task_type` (string) — always `"local_bash"` in v1
    - `status` (string) — running | completed | killed | error
    - `description` (string)
    - `output` (string) — new bytes since last poll (cursor-based)
    - `exitCode` (number | null, conditional)
    - `error` (string, conditional)
- **Description**: native verbatim minus the "DEPRECATED" preamble
- **Semantics**
  - Non-block mode: returns current state immediately
  - Block mode: polls every 100ms until the task completes or `timeout` elapses
  - Output uses per-shell byte cursor so repeated polls return only new data

#### `KillShell`

- **Input**
  - `task_id` (string, optional) — preferred
  - `shell_id` (string, optional, deprecated alias)
  - At least one required (validation enforced)
- **Output**
  - `message` (string)
  - `task_id` (string)
  - `task_type` (string) — `"local_bash"`
  - `command` (string, conditional)
- **Description**: native verbatim
- **Semantics**
  - `tree-kill`-equivalent SIGKILL to the process group (matches native; immediate, no grace)
  - Shell ID format `bXXXXXXXX` (8 random alphanumeric) — matches native
  - Task stays in registry briefly post-kill so late BashOutput polls can see final state

#### File tools (kept from Go base, scoped to guest FS)

`Read`, `Write`, `Edit`, `Glob`, `Grep` — use as-shipped in the Go base. Descriptions augmented with a one-line lead noting "operates on the incus guest filesystem, not the host" to keep Claude's tool choice unambiguous.

#### Channels push (exit-event only in v1)

- **Capability declared** at init: `ServerCapabilities.Experimental = {"claude/channel": {}}`
- **Session instructions** teach Claude the tag schema:
  > *"Events from this server arrive as `<channel source="rbash" …>` tags. `meta.event` is one of: `exit`. `meta.bash_id` identifies the background job. Call `BashOutput(bash_id)` to drain remaining output on exit."*
- **Emitted on**: a backgrounded shell's `cmd.Wait()` returning (completed or killed)
- **Payload** — content (string) + meta (string→string), meta keys `[A-Za-z0-9_]+`:
  ```json
  {
    "content": "Background shell b3a7f2k9 completed. Exit 0. duration 4713ms.",
    "meta": {
      "event": "exit",
      "bash_id": "b3a7f2k9",
      "state": "completed",
      "exit_code": "0",
      "duration_ms": "4713"
    }
  }
  ```
- **Launch requirement** documented: `claude --dangerously-load-development-channels server:rbash` during research preview
- **Transport wrapper** (~30 LoC) exposes `SendNotification(ctx, method, params)` using public SDK surface only (`mcp.Transport`, `mcp.Connection`, `jsonrpc.Request`) — no SDK internals
- **Meta sanitizer** (~10 LoC) enforces key regex and stringifies values; invalid keys logged to stderr (Claude Code captures stderr to `~/.claude/debug/<session-id>.txt`)
- **Not in v1**: regex-on-output match events, stall events, heartbeat/running events, per-job event budgets, skip-if-unchanged throttling — all parked in ROADMAP
- **Graceful degradation**: if Claude Code is launched without the channels flag, pushes still go on the wire but Claude drops them; tools keep working normally

### Infrastructure v1

- **Language**: Go (matches base repo, official MCP Go SDK)
- **Transport**: stdio for the MCP server binary inside the guest (swap `mcp.NewStreamableHTTPHandler` for the stdio transport)
- **Host-side shim**: stdio ↔ unix socket bridge; candidate implementations:
  - Option 1: Tiny Go binary that `net.Dial("unix", path)` and plumbs stdin/stdout through
  - Option 2: `socat - UNIX-CONNECT:/path/to/socket.sock` (dependency on socat on host)
  - **Default**: Option 1, shipped with the project.
- **Incus proxy device**: exposes guest-side unix socket at `/var/run/rbash.sock` onto the host side at `~/.rbash/<guest>.sock`. Configured via `incus config device add <guest> rbash-sock proxy listen=unix:/var/lib/incus/devices/<guest>/rbash-host.sock connect=unix:/var/run/rbash.sock`
- **Daemon lifecycle in guest**: systemd unit, restart on failure, starts on guest boot
- **Output persistence dir in guest**: `/var/lib/rbash/outputs/<task_id>` (O_APPEND targets)
- **Inline output cap env**: `BASH_MAX_OUTPUT_LENGTH` (default 30000, max 150000) — matches native
- **Channels transport wrapper**: custom `mcp.Transport` that wraps `mcp.StdioTransport` and exposes the `mcp.Connection` for out-of-band JSON-RPC notification writes
- **Version floor for channels**: Claude Code v2.1.80+; authenticated via claude.ai login (not API keys)

## Repository Layout (target)

```
rbash-mcp/
├── cmd/
│   ├── rbash-mcp/             # in-guest daemon
│   └── rbash-shim/            # host-side stdio<->socket bridge
├── internal/
│   ├── tools/                 # forked from claude-tools-mcp, trimmed + modified
│   │   ├── bash.go
│   │   ├── bash_output.go
│   │   ├── kill_shell.go
│   │   ├── list_shells.go    # keep (free from base)
│   │   ├── read.go
│   │   ├── write.go
│   │   ├── edit.go
│   │   ├── glob.go
│   │   ├── grep.go
│   │   ├── server.go
│   │   ├── constraints.go
│   │   └── common.go
│   ├── shell/
│   │   ├── registry.go        # shell_id generation, task map, cleanup TTL
│   │   ├── output.go          # inline/persisted output handling
│   │   └── cwd.go             # cwd-persistence tempfile
│   ├── channels/
│   │   ├── transport.go       # Transport+Connection wrapper, SendNotification
│   │   ├── push.go            # push(), push_exit(), meta sanitizer
│   │   └── push_test.go
│   └── transport/
│       ├── stdio.go           # MCP stdio transport (guest)
│       └── unix.go            # alt transport modes if needed
├── deploy/
│   ├── systemd/rbash-mcp.service
│   ├── incus/profile.yaml     # proxy device + socket path wiring
│   └── install-guest.sh
├── refs/                      # local-only references (gitignored)
├── PLAN.md
├── ROADMAP.md
└── README.md
```

## Build Steps (ordered)

1. **Scaffold repo** (done — repo is a GitHub fork of `mathematic-inc/claude-tools-mcp` at `techjoec/rbash-mcp` with `upstream` remote preserved for cherry-picks)
   - Module path `github.com/techjoec/rbash-mcp`
   - Upstream-specific CI/Docker/release files removed
   - `cmd/claude-tools-mcp` HTTP entrypoint removed; `cmd/rbash-mcp` + `cmd/rbash-shim` created

2. **Transport swap**
   - Replace `mcp.NewStreamableHTTPHandler` with the SDK's stdio server wiring
   - Daemon binds a unix socket and accepts one MCP client at a time (v1 single-client, matching stdio semantics)

3. **Native-parity edits to bash tool family**
   - Rename types/tools: `bash` / `bash_output` / `kill_shell` → `Bash` / `BashOutput` / `KillShell`; update tool names, add alias handling where native has aliases
   - Rewrite tool descriptions verbatim from native (strip sandbox/git sections, prepend guest-scope line)
   - Change shell_id format from `shell_N` to `bXXXXXXXX` (8 random alphanumeric); update the struct key to `TaskID`
   - Env injection on spawn (`CLAUDECODE`, `SHELL`, `GIT_EDITOR`)
   - Output: implement 30 KB inline cap with spill-to-file trailer; O_APPEND to `/var/lib/rbash/outputs/<id>`
   - Add `block: bool` + `timeout` handling to `BashOutput` (100ms poll loop until complete or timeout)
   - Return structured output with native field names (`backgroundTaskId`, `persistedOutputPath`, `persistedOutputSize`, `interrupted`, etc.)
   - Accept deprecated `shell_id` alias in `KillShell`; surface `task_type: "local_bash"`

4. **File tools kept as-is**
   - Prepend one-line guest-scope lead to each description
   - Otherwise untouched — they inherit constraints (10 MB file cap, 1000-line grep cap) from base

5. **cwd persistence**
   - Tempfile-based pwd tracking per MCP client session, mirrors native

6. **Host-side shim (`cmd/rbash-shim`)**
   - Tiny Go binary: opens unix socket path from argv/env, plumbs stdin↔socket↔stdout
   - No MCP parsing; pure byte forwarding (but flushes on framing boundaries for stability)

7. **Incus deploy**
   - `deploy/install-guest.sh`: copies daemon binary into guest, installs systemd unit, creates `/var/lib/rbash/outputs/`
   - `deploy/incus/profile.yaml`: describes the proxy device (unix socket host↔guest) and necessary uid/gid mappings

8. **Claude MCP config + launch flag**
   - Documented `.mcp.json` snippet: `"rbash"` entry pointing to `rbash-shim` binary with the host-side socket path as arg
   - Documented launch command: `claude --dangerously-load-development-channels server:rbash` (required for exit-event pushes to surface)

9. **Channels wiring**
   - Transport wrapper in `internal/channels/transport.go`, exposing `SendNotification(ctx, method, params)` using public SDK surface
   - Capability + instructions declared at server construction
   - `push_exit` call hooked into the bash background goroutine right after `cmd.Wait()` returns

10. **Smoke tests**
   - `bash echo hello` → foreground returns immediately
   - `bash sleep 5 && echo done` with `run_in_background: true` → returns `backgroundTaskId`; `BashOutput block=true` returns after 5s
   - `bash yes` with `run_in_background: true` → `KillShell` terminates it; trailing `BashOutput` sees killed status
   - `Read` / `Write` / `Edit` / `Glob` / `Grep` against guest paths
   - Output spill: `bash head -c 100000 /dev/urandom | base64` → `persistedOutputPath` set, trailer correct
   - 5-call concurrency scenario (daemon-watch, client, ls, ps, ping) runs cleanly
   - Exit-event channel push: backgrounded command → next Claude turn shows `<channel source="rbash" event="exit" …>` tag
   - Meta-key sanitization: pushing `{"bad-key": "x", "good_key": "y"}` drops `bad-key`, logs to stderr

## Non-Goals (v1)

See `ROADMAP.md` for the full parked list. Most notable non-goals:
- No PTY support
- No stdin write to backgrounded shells
- No regex-match / stall / heartbeat channel events (exit events only in v1)
- No multi-guest / profile system
- No installer-based `~/.claude/CLAUDE.md` injection
- No path translation host↔guest
- No 750 MB hard size cap (relying on inline cap + filesystem alone in v1)

## Resolved Decisions

1. **Daemon runs as root** inside the guest. All Claude-driven work in the guest also runs as root — simplifies uid/gid handling everywhere, and the trust boundary is the guest itself.
2. **Single-client MCP**. Only one Claude session at a time. The daemon accepts one MCP connection; additional connect attempts are rejected with a clear error until the first disconnects. Shell registry lives inside the daemon and persists across shim reconnects.
3. **Socket-path discovery** (shim and daemon, in priority order):
   1. CLI arg — positional path or `--socket=<path>`
   2. `$RBASH_SOCKET` env var
   3. `$XDG_RUNTIME_DIR/rbash.sock` if set
   4. Fallback `/run/rbash.sock`

   Symmetric socket path on both host and guest (`/run/rbash.sock`) for mental simplicity. The incus proxy device forwards between them.
4. **Distribution**: single static Go binary for the guest daemon; single static Go binary for the host shim. systemd unit installed in the guest. `deploy/install-guest.sh` handles copy + systemd enablement.

## Host-mount context

The guest will have RW and RO bind mounts from the host filesystem. This means the rbash file tools (`Read`, `Write`, `Edit`, `Glob`, `Grep`) — even though they run inside the guest — can touch host files via those mount points. Useful pattern: bind-mount the research working directory into the guest so work done by Claude through rbash is visible on the host immediately, no sync needed.

No path translation required — Claude uses the guest-side mount path directly, and the mount makes the host-side file the same byte stream.
