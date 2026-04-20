package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultTimeout = 120_000 // 2 minutes, matches Claude Code native
	maxTimeout     = 600_000 // 10 minutes, matches Claude Code native
)

// BackgroundShell represents a long-running command executing asynchronously.
// LastStdoutReadAt / LastStderrReadAt track byte positions so repeated
// BashOutput calls return only new output.
type BackgroundShell struct {
	ID               string
	Command          string
	Description      string
	Cmd              *exec.Cmd
	Stdout           *SyncBuffer
	Stderr           *SyncBuffer
	StartTime        time.Time
	EndTime          time.Time
	Done             chan struct{}
	Err              error
	ExitCode         int
	Killed           bool
	LastStdoutReadAt int
	LastStderrReadAt int
}

// DurationMillis returns elapsed wall-clock milliseconds.
func (b *BackgroundShell) DurationMillis() int64 {
	end := b.EndTime
	if end.IsZero() {
		end = time.Now()
	}
	return end.Sub(b.StartTime).Milliseconds()
}

// StateName returns "completed" or "killed" depending on how the shell exited.
func (b *BackgroundShell) StateName() string {
	if b.Killed {
		return "killed"
	}
	return "completed"
}

// standardBashEnv returns the base environment handed to spawned shells,
// matching Claude Code native injections (CLAUDECODE=1, SHELL=/bin/bash,
// GIT_EDITOR=true, plus the inherited env).
func standardBashEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+3)
	skip := map[string]bool{"CLAUDECODE": true, "SHELL": true, "GIT_EDITOR": true}
	for _, kv := range env {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			out = append(out, kv)
			continue
		}
		if skip[kv[:i]] {
			continue
		}
		out = append(out, kv)
	}
	out = append(out,
		"CLAUDECODE=1",
		"SHELL=/bin/bash",
		"GIT_EDITOR=true",
	)
	return out
}

func (s *State) executeBashCommand(ctx context.Context, command, description string, timeout int64, runInBackground bool) (*BashResult, error) {
	if command == "" {
		return nil, fmt.Errorf("Command cannot be empty.")
	}

	timeoutMs := defaultTimeout
	if timeout > 0 {
		if timeout > maxTimeout {
			return nil, fmt.Errorf("Timeout cannot exceed %d milliseconds (10 minutes).", maxTimeout)
		}
		timeoutMs = int(timeout)
	}

	var cmd *exec.Cmd
	if runInBackground {
		cmd = exec.Command("bash", "-c", command)
	} else {
		cmdCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
		defer cancel()
		cmd = exec.CommandContext(cmdCtx, "bash", "-c", command)
	}

	cmd.Env = standardBashEnv()
	if wd, err := os.Getwd(); err == nil {
		cmd.Dir = wd
	}

	if runInBackground {
		return s.executeBackground(cmd, command, description)
	}
	return s.executeForeground(ctx, cmd, command)
}

func (s *State) executeForeground(ctx context.Context, cmd *exec.Cmd, command string) (*BashResult, error) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	runErr := cmd.Run()

	stdoutStr := stdout.String()
	stderrStr := stderr.String()

	// Spill to disk if combined output exceeds the inline cap.
	var persistedPath string
	var persistedSize int64
	total := int64(len(stdoutStr) + len(stderrStr))
	if total > int64(inlineOutputCap()) {
		taskID := generateTaskID()
		combined := stdoutStr
		if stderrStr != "" {
			if combined != "" {
				combined += "\n"
			}
			combined += "--- stderr ---\n" + stderrStr
		}
		inline, path, size, err := spillIfNeeded(taskID, combined)
		if err != nil {
			return nil, err
		}
		stdoutStr = inline
		stderrStr = ""
		persistedPath = path
		persistedSize = size
	}

	interrupted := false
	returnCodeInterp := ""

	if runErr != nil {
		if strings.Contains(runErr.Error(), "context deadline exceeded") {
			interrupted = true
			returnCodeInterp = "Command timed out. Consider increasing the timeout parameter or running in background."
		} else if exitErr, ok := runErr.(*exec.ExitError); ok {
			code := exitErr.ExitCode()
			if code == -1 && strings.Contains(runErr.Error(), "signal: killed") {
				interrupted = true
				returnCodeInterp = "Command timed out. Consider increasing the timeout parameter or running in background."
			}
		}
	}

	return &BashResult{
		Stdout:                   stdoutStr,
		Stderr:                   stderrStr,
		Interrupted:              interrupted,
		PersistedOutputPath:      persistedPath,
		PersistedOutputSize:      persistedSize,
		ReturnCodeInterpretation: returnCodeInterp,
	}, nil
}

func (s *State) executeBackground(cmd *exec.Cmd, command, description string) (*BashResult, error) {
	stdout := &SyncBuffer{}
	stderr := &SyncBuffer{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	// Put the child in its own process group so KillShell's process-group
	// SIGKILL reaches grandchildren too (equivalent to native's tree-kill).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("Failed to start background command: %s", err)
	}

	s.Mu.Lock()
	taskID := generateTaskID()
	shell := &BackgroundShell{
		ID:          taskID,
		Command:     command,
		Description: description,
		Cmd:         cmd,
		Stdout:      stdout,
		Stderr:      stderr,
		StartTime:   time.Now(),
		Done:        make(chan struct{}),
	}
	s.BackgroundShells[taskID] = shell
	onExit := s.OnTaskExit
	s.Mu.Unlock()

	go func() {
		err := cmd.Wait()
		s.Mu.Lock()
		shell.Err = err
		shell.EndTime = time.Now()
		if cmd.ProcessState != nil {
			shell.ExitCode = cmd.ProcessState.ExitCode()
		}
		close(shell.Done)
		s.Mu.Unlock()

		if onExit != nil {
			onExit(shell)
		}
	}()

	return &BashResult{BackgroundTaskID: taskID}, nil
}

// SyncBuffer wraps bytes.Buffer with a mutex so the subprocess can write
// while BashOutput reads.
type SyncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (sb *SyncBuffer) Write(p []byte) (int, error) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.Write(p)
}

func (sb *SyncBuffer) String() string {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.String()
}

var (
	_ io.Writer = (*SyncBuffer)(nil)

	// BashTool mirrors Claude Code's native Bash tool. Description is the
	// native prose with sandbox and git-commit/PR sections stripped (don't
	// apply) and a guest-scope lead prepended so the model picks rbash's
	// Bash for guest work without ambiguity vs. the host's built-in Bash.
	BashTool = sdk.Tool{
		Name:        "Bash",
		Description: bashToolDescription,
	}
)

const bashToolDescription = `Executes a bash command inside the incus guest VM. Use this for all shell work on the guest; filesystem and network calls happen in the guest, not on the host.

The working directory persists between commands, but shell state does not. The shell environment is initialized from the user's profile (bash).

IMPORTANT: Avoid using this tool to run ` + "`find`" + `, ` + "`grep`" + `, ` + "`cat`" + `, ` + "`head`" + `, ` + "`tail`" + `, ` + "`sed`" + `, ` + "`awk`" + `, or ` + "`echo`" + ` commands, unless explicitly instructed or after you have verified that a dedicated tool cannot accomplish your task. Instead, use the appropriate dedicated tool as this will provide a much better experience for the user:

- File search: Use Glob (NOT find or ls)
- Content search: Use Grep (NOT grep or rg)
- Read files: Use Read (NOT cat/head/tail)
- Edit files: Use Edit (NOT sed/awk)
- Write files: Use Write (NOT echo >/cat <<EOF)
- Communication: Output text directly (NOT echo/printf)

While the Bash tool can do similar things, it's better to use the built-in tools as they provide a better user experience and make it easier to review tool calls and give permission.

# Instructions

- If your command will create new directories or files, first use this tool to run ` + "`ls`" + ` to verify the parent directory exists and is the correct location.
- Always quote file paths that contain spaces with double quotes in your command (e.g., cd "path with spaces/file.txt")
- Try to maintain your current working directory throughout the session by using absolute paths and avoiding usage of ` + "`cd`" + `. You may use ` + "`cd`" + ` if the User explicitly requests it.
- You may specify an optional timeout in milliseconds (up to 600000ms / 10 minutes). By default, your command will timeout after 120000ms (2 minutes).
- You can use the ` + "`run_in_background`" + ` parameter to run the command in the background. Only use this if you don't need the result immediately and are OK being notified when the command completes later. You do not need to check the output right away - you'll be notified when it finishes. You do not need to use '&' at the end of the command when using this parameter.
- When issuing multiple commands:
  - If the commands are independent and can run in parallel, make multiple Bash tool calls in a single message. Example: if you need to run "git status" and "git diff", send a single message with two Bash tool calls in parallel.
  - If the commands depend on each other and must run sequentially, use a single Bash call with '&&' to chain them together.
  - Use ';' only when you need to run commands sequentially but don't care if earlier commands fail.
  - DO NOT use newlines to separate commands (newlines are ok in quoted strings).
- Avoid unnecessary ` + "`sleep`" + ` commands:
  - Do not sleep between commands that can run immediately — just run them.
  - If your command is long running and you would like to be notified when it finishes — use ` + "`run_in_background`" + `. No sleep needed.
  - Do not retry failing commands in a sleep loop — diagnose the root cause.
  - If waiting for a background task you started with ` + "`run_in_background`" + `, call BashOutput with block=true to await completion.
`

// BashInput mirrors Claude Code native Bash input schema. `dangerouslyDisableSandbox` is omitted — no sandbox on this server.
type BashInput struct {
	Command         string `json:"command" jsonschema:"The command to execute"`
	Description     string `json:"description,omitempty" jsonschema:"Clear, concise description of what this command does in active voice. Never use words like \"complex\" or \"risk\" in the description - just describe what it does.\n\nFor simple commands (git, npm, standard CLI tools), keep it brief (5-10 words):\n- ls → \"List files in current directory\"\n- git status → \"Show working tree status\"\n- npm install → \"Install package dependencies\"\n\nFor commands that are harder to parse at a glance (piped commands, obscure flags, etc.), add enough context to clarify what it does:\n- find . -name \"*.tmp\" -exec rm {} \\; → \"Find and delete all .tmp files recursively\"\n- git reset --hard origin/main → \"Discard all local changes and match remote main\"\n- curl -s url | jq '.data[]' → \"Fetch JSON from URL and extract data array elements\""`
	RunInBackground bool   `json:"run_in_background,omitempty" jsonschema:"Set to true to run this command in the background. Use BashOutput to read the output later."`
	Timeout         int64  `json:"timeout,omitempty" jsonschema:"Optional timeout in milliseconds (max 600000)"`
}

// BashResult mirrors Claude Code native Bash output schema.
type BashResult struct {
	Stdout                   string `json:"stdout,omitempty"`
	Stderr                   string `json:"stderr,omitempty"`
	Interrupted              bool   `json:"interrupted,omitempty"`
	BackgroundTaskID         string `json:"backgroundTaskId,omitempty"`
	PersistedOutputPath      string `json:"persistedOutputPath,omitempty"`
	PersistedOutputSize      int64  `json:"persistedOutputSize,omitempty"`
	ReturnCodeInterpretation string `json:"returnCodeInterpretation,omitempty"`
	NoOutputExpected         bool   `json:"noOutputExpected,omitempty"`
}

func Bash(ctx context.Context, req *sdk.CallToolRequest, args BashInput) (*sdk.CallToolResult, any, error) {
	server := GetState()
	result, err := server.executeBashCommand(ctx, args.Command, args.Description, args.Timeout, args.RunInBackground)
	if err != nil {
		return nil, nil, err
	}
	var text string
	switch {
	case result.BackgroundTaskID != "":
		text = fmt.Sprintf("Command running in background with ID: %s", result.BackgroundTaskID)
	case result.Stderr != "":
		text = result.Stdout + "\n" + result.Stderr
	default:
		text = result.Stdout
	}
	return &sdk.CallToolResult{
		Content:           []sdk.Content{&sdk.TextContent{Text: text}},
		StructuredContent: result,
	}, result, nil
}
