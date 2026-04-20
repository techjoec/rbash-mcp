# rbash-mcp — TODO

Active next-step tracker. Complements `PLAN.md` (v1 scope, shipped) and `ROADMAP.md` (parked long-term items).

Read order for a fresh session: `CLAUDE.md` → `TODO.md` → whatever's next.

---

## Paused here (2026-04-19)

v1 committed + pushed to `origin/main`. Binaries build clean, tests pass, lint clean. **User is recabling their lab** before any further work that touches the incus guest.

---

## Immediate (prep work — can be done before lab is ready)

- [ ] **`deploy/smoke-test.sh`** — stand-alone script run *inside the guest* that hits `/run/rbash.sock` directly via `socat`/`nc`, exercises all 7 PLAN.md scenarios (foreground exec, background exec, BashOutput block, KillShell, output spill, file tools, 5-call concurrency). Validates the daemon end-to-end *before* Claude Code is in the loop. Fails loud on any mismatch.
- [ ] **`deploy/mcp-config-example.json`** — ready-to-paste `.mcp.json` entry with comments for host-side Claude Code config (both the minimal form and the path-arg form).
- [ ] **`deploy/DEBUGGING.md`** — "where to look when X fails": journalctl commands, socket permission checks, incus proxy-device verification, channel-flag gotcha, MCP handshake failure symptoms.

## Smoke testing (once lab is ready)

Order of operations, guest side:

- [ ] Build host shim: `go build -o bin/rbash-shim ./cmd/rbash-shim`
- [ ] Build daemon: `go build -o bin/rbash-mcp ./cmd/rbash-mcp`
- [ ] Create incus profile from `deploy/incus/profile.yaml.example` and attach to the guest
- [ ] Push daemon + unit into guest, run `deploy/install-guest.sh`
- [ ] Verify daemon up: `incus exec <guest> -- systemctl status rbash-mcp`
- [ ] Verify host-side socket reachable: `socat - UNIX-CONNECT:/run/rbash.sock` → type raw JSON-RPC
- [ ] Run `deploy/smoke-test.sh` inside the guest — all green

Claude Code side:

- [ ] Add rbash to `.mcp.json`
- [ ] Launch with `claude --dangerously-load-development-channels server:rbash`
- [ ] `/mcp` shows `rbash: connected`
- [ ] Call `Bash(command="echo hi")` → returns "hi"
- [ ] Call `Bash(command="sleep 5 && echo done", run_in_background=true)` → returns `backgroundTaskId`
- [ ] Wait, send any message → next turn shows `<channel source="rbash" event="exit" …>` tag
- [ ] Call `BashOutput(task_id=<id>, block=true)` on an in-flight task → returns when task completes
- [ ] Call `KillShell(task_id=<id>)` on a running task → terminates
- [ ] Call `Read`/`Write`/`Edit`/`Glob`/`Grep` against guest paths

## Fix paper cuts

- [ ] Whatever breaks on first real run — expect at least one round. Log issues here as they surface.

## Post-smoke: ROADMAP pulls (priority order for the research use case)

- [ ] **PTY option on `Bash`** — needed for daemon-watching (many daemons change behavior without a TTY). Probably the first real pull.
- [ ] **`bash_stdin` tool** — write stdin to a backgrounded shell so you can drive a daemon that's already running.
- [ ] **Regex-match channel events** — the "wake me when X logs" flow. Builds on the existing transport + pusher; main new work is per-shell trigger registry + flood-control budgets.

Each of these gets its own planning cycle (read the ROADMAP entry, scope v1 of the feature, add to TODO, execute).

## Housekeeping (low priority)

- [ ] Consider packaging as a Claude plugin (ROADMAP item — installer + `/rbash` slash command + SessionStart hook for config bootstrap).
- [ ] `NOTICE` file or expanded README attribution line if ever distributing publicly (currently solo, skip).

## Done

- [x] v1 scope decided, PLAN.md + ROADMAP.md written
- [x] GitHub fork `techjoec/rbash-mcp` from `mathematic-inc/claude-tools-mcp`
- [x] Repo restructured, upstream CI/release scaffolding removed
- [x] Module renamed to `github.com/techjoec/rbash-mcp`
- [x] Bash tool family rewritten for native parity (schemas, descriptions, env injection, output spill, `bXXXXXXXX` IDs, process-group kill, block-mode BashOutput)
- [x] File tools augmented with guest-scope description lead
- [x] `internal/channels` transport wrapper + pusher for `notifications/claude/channel`
- [x] Host shim (`cmd/rbash-shim`) and daemon (`cmd/rbash-mcp`) written
- [x] `deploy/` systemd unit, incus profile example, install script
- [x] `.golangci.yml`, structured logging via `log/slog`
- [x] `CLAUDE.md` for future session context
- [x] Committed + pushed; `main` at `93bc593`; stale branches cleaned up
