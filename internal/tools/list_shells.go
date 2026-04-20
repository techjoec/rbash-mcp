package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type listShellEntry struct {
	TaskID      string `json:"task_id"`
	TaskType    string `json:"task_type"`
	Description string `json:"description,omitempty"`
	Command     string `json:"command,omitempty"`
	Status      string `json:"status"`
	StartTime   int64  `json:"start_time_unix"`
}

type ListShellsResult struct {
	Tasks []listShellEntry `json:"tasks"`
	Count int              `json:"count"`
}

func (s *State) executeListShells(ctx context.Context) (string, *ListShellsResult, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()

	if len(s.BackgroundShells) == 0 {
		empty := &ListShellsResult{Tasks: []listShellEntry{}, Count: 0}
		return "No background shells are currently running.", empty, nil
	}

	entries := make([]listShellEntry, 0, len(s.BackgroundShells))
	for _, shell := range s.BackgroundShells {
		status := "running"
		select {
		case <-shell.Done:
			if shell.Killed {
				status = "killed"
			} else if shell.ExitCode != 0 {
				status = "failed"
			} else {
				status = "completed"
			}
		default:
		}
		entries = append(entries, listShellEntry{
			TaskID:      shell.ID,
			TaskType:    "local_bash",
			Description: shell.Description,
			Command:     shell.Command,
			Status:      status,
			StartTime:   shell.StartTime.Unix(),
		})
	}

	priority := map[string]int{"running": 0, "failed": 1, "killed": 2, "completed": 3}
	sort.Slice(entries, func(i, j int) bool {
		if priority[entries[i].Status] != priority[entries[j].Status] {
			return priority[entries[i].Status] < priority[entries[j].Status]
		}
		return entries[i].StartTime < entries[j].StartTime
	})

	out := &ListShellsResult{Tasks: entries, Count: len(entries)}
	jsonBytes, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", nil, fmt.Errorf("Failed to format shell list: %s", err)
	}
	return string(jsonBytes), out, nil
}

var ListShellsTool = sdk.Tool{
	Name:        "ListShells",
	Description: listShellsDescription,
}

const listShellsDescription = `- Lists all background shells on the incus guest VM with their current status
- Shows task_id, description, command, and status (running/completed/failed/killed)
- Use this tool to see what background shells are active and check their status
- Useful for tracking long-running operations before fetching output with BashOutput`

type ListShellsInput struct{}

func ListShells(ctx context.Context, req *sdk.CallToolRequest, args ListShellsInput) (*sdk.CallToolResult, any, error) {
	state := GetState()
	text, out, err := state.executeListShells(ctx)
	if err != nil {
		return nil, nil, err
	}
	return &sdk.CallToolResult{
		Content:           []sdk.Content{&sdk.TextContent{Text: text}},
		StructuredContent: out,
	}, out, nil
}
