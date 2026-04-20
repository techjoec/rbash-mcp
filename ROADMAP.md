# rbash-mcp — Roadmap (parked)

Everything beyond v1. Grouped by category. No commitments, priorities only suggested.

## Execution model

### PTY option for `Bash`
- **What**: Optional `tty: bool` parameter (or auto-detect) that runs the command attached to a pseudo-terminal instead of plain pipes.
- **Why**: Many daemons and REPLs change behavior without a TTY (buffering, prompts, diagnostic output, progress bars). Foreground-daemon-watching use case wants this.
- **Notes**: Keep pipes as default (native parity). PTY is additive.

### `bash_stdin` — write stdin to a backgrounded shell
- **What**: New tool: `bash_stdin(task_id, data)` writes to the child process's stdin.
- **Why**: Enables interactive conversations with a daemon started in background without spawning a new shell.
- **Notes**: Native Claude Code has no equivalent. Genuinely net-new capability.

### Signal selection in `KillShell`
- **What**: Optional `signal: string` parameter (SIGTERM, SIGINT, SIGHUP, SIGKILL). Default: SIGTERM with configurable grace before SIGKILL escalation.
- **Why**: Cleaner shutdown of daemons that trap signals (systemd-style services, python apps with cleanup handlers).
- **Notes**: Native uses immediate SIGKILL via `tree-kill`. We mirror native in v1; this is additive.

### Channels — extra event types beyond `exit`
- **What**: v1 ships exit-event pushes via Claude Code's `claude/channel` capability. This item extends the event vocabulary:
  - `match` — regex-on-stdout / regex-on-stderr matches, per-shell trigger list registered at bash-call time
  - `stall` — 45 s no-growth + interactive-prompt-regex tail detection → notify
  - `running` — periodic heartbeat (elapsed, bytes-since-last-tick) for very long jobs
- **Why**: Poll-free awareness of specific moments (a pattern appeared, a job stalled) without the model having to guess when to check.
- **Notes**:
  - Push machinery is already in place from v1 (transport wrapper + `push()` helper)
  - Real work is the **budget/flood control**: per-job event caps, `skip_if_unchanged` on heartbeat, regex-match deduplication. The `CHANNELS_INTEGRATION.md` package in `refs/channels/` calls out the issue-#11716 flood failure mode as the primary design constraint.
  - Each event type gets its own `push_*` typed wrapper in `internal/channels/push.go`
  - Trigger specs passed on the `Bash` tool call: `triggers: [{event: "match", pattern: "ERROR", stream: "stdout"}]`

### Auto-background at assistant-set threshold
- **What**: Mirror native's 15-second assistant-mode auto-background behavior for long blocking commands.
- **Notes**: This is client-side UX (Claude decides). MCP server doesn't need it unless we want to surface `assistantAutoBackgrounded: true` for parity — low value.

## Safety / hardening

### 750 MB absolute output size watchdog
- **What**: Per-shell poller every 5s; if accumulated output exceeds 750 MB, SIGKILL the process.
- **Why**: Prevents runaway `yes` or bad `cat` from filling the guest disk.
- **Notes**: Native has this. Relies on filesystem caps in v1; add when guest disk management matters.

### Stall watchdog (45s no-growth + interactive-prompt regex → notify)
- **What**: Every 5s, check if stdout hasn't grown in 45s AND tail matches patterns like `(y/n)`, `Press Enter`, `Password:`. If so, emit a stall notice.
- **Why**: Catches commands stuck at interactive prompts.
- **Notes**: Purely informational in native. Folds naturally into the channels extensions above — emit as a `stall` channel event.

### Permission/allowlist scaffolding
- **What**: Optional command allowlist/denylist with glob or regex rules applied at the daemon. Inside the guest it's largely moot (trust boundary is the guest), but could help as a soft guardrail.
- **Notes**: Explicitly out of scope for v1 since the guest is the trust boundary.

## UX / ergonomics

### Installer with CLAUDE.md + `/rbash` slash command
- **What**: `install.sh` that:
  - Installs guest binary + systemd unit
  - Configures incus proxy device
  - Appends an HTML-marked block to `~/.claude/CLAUDE.md` describing the tool
  - Installs a `/rbash` slash command with usage guide
- **Why**: Zero-friction onboarding.
- **Notes**: Pattern from `remote-toolkit` / `claude-remote-shell`. Good polish.

### cwd-persistence tempfile across calls
- **What**: Native uses a tempfile to round-trip the shell's pwd between calls so `cd /foo` in one call persists to the next.
- **Status**: In v1 scope (included in PLAN.md for native parity). Mentioned here only as a cross-reference.

### Path translation (host ↔ guest)
- **What**: Rewrite host absolute paths in commands → guest paths before exec; rewrite guest paths in output → host paths before return.
- **Why**: Seamless mixing of Claude's native host tools with rbash's guest tools.
- **Notes**: Overkill if user stays on guest paths explicitly. Revisit if the mixed flow becomes awkward in practice.

### ANSI stripping option
- **What**: Optional strip of ANSI escape codes from output (some tools emit color even without TTY).
- **Why**: Cleaner output for the model to parse.
- **Notes**: Native doesn't strip. Low priority.

## Architecture extensions

### Profile system for multiple guests
- **What**: Single host-side rbash shim able to target multiple incus guests via named profiles (`--profile=gpu1`).
- **Why**: One Claude → many guests.
- **Notes**: v1 punts by letting users configure N MCP entries in Claude. Revisit if we find users juggling many guests.

### Session concept (inherit cwd/env from named group)
- **What**: Optional `session_id` on `Bash` that pools shared cwd/env across calls within the session.
- **Why**: Native has this implicitly via tempfile cwd tracking. Generalizing allows multiple concurrent independent session contexts.
- **Notes**: Only matters if concurrent-independent-context use cases emerge.

### Multi-client MCP over the socket
- **What**: Let several shim connections share the daemon, each with its own MCP session but sharing the global shell registry.
- **Notes**: Partly in v1 depending on answer to Open Question #2 in PLAN.md.

### Alternative transports
- **What**: Non-stdio transports — WebSocket, direct unix socket (bypass stdio shim), TCP for remote-to-remote.
- **Notes**: Not needed for the incus use case. Document only if someone asks.

### Support non-incus guests
- **What**: Generic unix-socket-or-stdio back-end — docker, podman, firecracker, SSH. The daemon doesn't care; the only incus-specific bit is the proxy device.
- **Notes**: Cheap generalization. Write the deploy docs for each as demand arises.

## Observability

### Structured logging from the daemon
- **What**: JSON logs with task lifecycle events to a log file (or journald when on systemd).
- **Why**: Debug flaky commands, post-hoc analysis.

### Metrics / introspection tool
- **What**: An `Info` or `Stats` tool returning registry size, total spawns, memory/disk used by persisted outputs.
- **Why**: Quick sanity check from inside Claude.

### Per-task timing data
- **What**: Expose `startTime`, `endTime`, `wallClockDuration`, `cpuTime` in `BashOutput` results.
- **Notes**: Native doesn't surface these. Minor.

## Integrations / polish

### Claude plugin wrapper
- **What**: Ship an installable Claude plugin that bundles rbash-mcp config, the `/rbash` slash command, and a SessionStart hook for bootstrap.
- **Notes**: Pairs with the installer item above.

### Incus snapshot/restore helpers
- **What**: MCP tools like `GuestSnapshot(name)`, `GuestRestore(name)`, `GuestReset()` that call `incus snapshot` from the host side (shim-adjacent, not inside guest).
- **Why**: Support iterative research where you want to reset the guest between runs.
- **Notes**: Needs a separate privileged path outside the guest daemon.

### Pre-submission directory listing in Claude's MCP Directory
- **What**: Run the Anthropic MCP pre-submission checklist, submit to the directory.
- **Notes**: Low priority; mainly relevant if we ever open-source and distribute broadly.
