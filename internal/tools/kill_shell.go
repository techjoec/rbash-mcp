package tools

import (
	"context"
	"fmt"
	"syscall"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// KillShell mirrors Claude Code's native KillShell (aka TaskStop) tool.
// Accepts `task_id` (primary) and `shell_id` (deprecated alias) for broadest
// compatibility with the model's training priors.

type KillShellInput struct {
	TaskID  string `json:"task_id,omitempty" jsonschema:"The ID of the background task to stop"`
	ShellID string `json:"shell_id,omitempty" jsonschema:"Deprecated: use task_id instead"`
}

type KillShellResult struct {
	Message  string `json:"message"`
	TaskID   string `json:"task_id"`
	TaskType string `json:"task_type"`
	Command  string `json:"command,omitempty"`
}

var KillShellTool = sdk.Tool{
	Name:        "KillShell",
	Description: killShellDescription,
}

const killShellDescription = `- Stops a running background shell on the incus guest VM by its task ID
- Takes a task_id parameter identifying the task to stop (shell_id is also accepted as a deprecated alias)
- Returns a success or failure status
- Use this tool when you need to terminate a long-running task`

func KillShell(ctx context.Context, req *sdk.CallToolRequest, args KillShellInput) (*sdk.CallToolResult, any, error) {
	taskID := args.TaskID
	if taskID == "" {
		taskID = args.ShellID
	}
	if taskID == "" {
		return nil, nil, fmt.Errorf("task_id is required.")
	}

	state := GetState()
	state.Mu.Lock()
	shell, ok := state.BackgroundShells[taskID]
	state.Mu.Unlock()
	if !ok {
		return nil, nil, fmt.Errorf("task_id '%s' not found.", taskID)
	}

	select {
	case <-shell.Done:
		return nil, nil, fmt.Errorf("task '%s' has already completed.", taskID)
	default:
	}

	// Match native semantics: immediate, tree-kill-equivalent SIGKILL to
	// the process group so grandchildren die too. We set Pgid via Setpgid
	// in exec (TODO if we ever move there); for now, a process-group kill
	// on cmd.Process.Pid reaches the whole group since bash -c forks as a
	// new session when started via Cmd.Start on Linux in the default case.
	if shell.Cmd.Process == nil {
		return nil, nil, fmt.Errorf("task '%s' has no live process.", taskID)
	}
	pid := shell.Cmd.Process.Pid
	// Try process-group kill first; fall back to plain kill on error.
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		if err := shell.Cmd.Process.Kill(); err != nil {
			return nil, nil, fmt.Errorf("failed to kill task '%s': %s", taskID, err)
		}
	}

	// Mark killed so channel push + status reflect it.
	state.Mu.Lock()
	shell.Killed = true
	state.Mu.Unlock()

	// Small delay to let cmd.Wait reap. The goroutine in executeBackground
	// will close shell.Done and invoke OnTaskExit.
	time.Sleep(100 * time.Millisecond)

	result := &KillShellResult{
		Message:  fmt.Sprintf("Killed task %s.", taskID),
		TaskID:   taskID,
		TaskType: "local_bash",
		Command:  shell.Command,
	}
	return &sdk.CallToolResult{
		Content:           []sdk.Content{&sdk.TextContent{Text: result.Message}},
		StructuredContent: result,
	}, result, nil
}
