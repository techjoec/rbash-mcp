package tools

import (
	"context"
	"fmt"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// BashOutput mirrors Claude Code's native BashOutput (aka TaskOutput) tool.
// Input: task_id (required), block (default true), timeout (default 30000, max 600000).
// Output: { retrieval_status, task }.

type BashOutputInput struct {
	TaskID  string `json:"task_id" jsonschema:"The task ID to get output from"`
	Block   *bool  `json:"block,omitempty" jsonschema:"Whether to wait for completion (default: true)"`
	Timeout int64  `json:"timeout,omitempty" jsonschema:"Max wait time in ms (default: 30000, max: 600000)"`
}

type BashOutputTask struct {
	TaskID      string `json:"task_id"`
	TaskType    string `json:"task_type"`
	Status      string `json:"status"`
	Description string `json:"description,omitempty"`
	Output      string `json:"output"`
	ExitCode    *int   `json:"exitCode,omitempty"`
	Error       string `json:"error,omitempty"`
}

type BashOutputResult struct {
	RetrievalStatus string          `json:"retrieval_status"`
	Task            *BashOutputTask `json:"task"`
}

var BashOutputTool = sdk.Tool{
	Name:        "BashOutput",
	Description: bashOutputDescription,
}

const bashOutputDescription = `- Retrieves output from a running or completed background shell on the incus guest VM
- Takes a task_id parameter identifying the task
- Returns the task output along with status information
- Use block=true (default) to wait for task completion
- Use block=false for non-blocking check of current status
- Works with any task_id returned by the Bash tool's backgroundTaskId field`

func BashOutput(ctx context.Context, req *sdk.CallToolRequest, args BashOutputInput) (*sdk.CallToolResult, any, error) {
	if args.TaskID == "" {
		return nil, nil, fmt.Errorf("task_id is required.")
	}

	block := true
	if args.Block != nil {
		block = *args.Block
	}
	timeoutMs := int64(30_000)
	if args.Timeout > 0 {
		timeoutMs = args.Timeout
	}
	if timeoutMs > 600_000 {
		timeoutMs = 600_000
	}

	state := GetState()
	state.Mu.RLock()
	shell, ok := state.BackgroundShells[args.TaskID]
	state.Mu.RUnlock()
	if !ok {
		return nil, nil, fmt.Errorf("task_id '%s' not found.", args.TaskID)
	}

	if block {
		deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
		for time.Now().Before(deadline) {
			select {
			case <-shell.Done:
				return buildBashOutputResult(shell, "success"), nil, nil
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
		// Timed out waiting for completion — still return current state.
		return buildBashOutputResult(shell, "timeout"), nil, nil
	}

	// Non-blocking: report whatever we have now.
	status := "success"
	select {
	case <-shell.Done:
	default:
		status = "not_ready"
	}
	return buildBashOutputResult(shell, status), nil, nil
}

func buildBashOutputResult(shell *BackgroundShell, retrieval string) *sdk.CallToolResult {
	state := GetState()
	state.Mu.Lock()
	stdoutFull := shell.Stdout.String()
	stderrFull := shell.Stderr.String()
	newStdout := stdoutFull[shell.LastStdoutReadAt:]
	newStderr := stderrFull[shell.LastStderrReadAt:]
	shell.LastStdoutReadAt = len(stdoutFull)
	shell.LastStderrReadAt = len(stderrFull)

	var exitCode *int
	statusStr := "running"
	select {
	case <-shell.Done:
		c := shell.ExitCode
		exitCode = &c
		statusStr = shell.StateName()
	default:
	}
	state.Mu.Unlock()

	combined := newStdout
	if newStderr != "" {
		if combined != "" {
			combined += "\n"
		}
		combined += "--- stderr ---\n" + newStderr
	}

	task := &BashOutputTask{
		TaskID:      shell.ID,
		TaskType:    "local_bash",
		Status:      statusStr,
		Description: shell.Description,
		Output:      combined,
		ExitCode:    exitCode,
	}
	if shell.Err != nil && exitCode != nil && *exitCode != 0 {
		task.Error = shell.Err.Error()
	}
	result := &BashOutputResult{
		RetrievalStatus: retrieval,
		Task:            task,
	}
	return &sdk.CallToolResult{
		Content:           []sdk.Content{&sdk.TextContent{Text: combined}},
		StructuredContent: result,
	}
}
