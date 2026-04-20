package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Inline output cap. Matches Claude Code native (`BASH_MAX_OUTPUT_LENGTH`):
// 30 000 bytes by default, capped at 150 000 via env. Past the cap, the full
// output is written to disk and a trailer line points at the file.
const (
	defaultInlineOutputCap = 30_000
	maxInlineOutputCap     = 150_000
)

// persistedOutputDir is where spilled outputs live inside the guest. The
// directory is created on first use.
const persistedOutputDir = "/var/lib/rbash/outputs"

func inlineOutputCap() int {
	if v := os.Getenv("BASH_MAX_OUTPUT_LENGTH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > maxInlineOutputCap {
				return maxInlineOutputCap
			}
			return n
		}
	}
	return defaultInlineOutputCap
}

// spillIfNeeded returns the output to return to the caller plus, when the
// full output exceeded the inline cap, a persisted-file path and the total
// byte size. It always writes the trailer inline when spilling.
//
// Matches the native `\nOutput truncated (NNN kB total). Full output saved to: /path\n` shape.
func spillIfNeeded(taskID, output string) (inline string, persistedPath string, totalSize int64, err error) {
	cap := inlineOutputCap()
	size := int64(len(output))
	if size <= int64(cap) {
		return output, "", size, nil
	}

	if err := os.MkdirAll(persistedOutputDir, 0o755); err != nil {
		return output, "", size, fmt.Errorf("create persisted output dir: %w", err)
	}
	path := filepath.Join(persistedOutputDir, taskID+".log")
	if err := os.WriteFile(path, []byte(output), 0o644); err != nil {
		return output, "", size, fmt.Errorf("write persisted output: %w", err)
	}

	// Keep the last N bytes inline so the caller still has recent context,
	// plus the trailer. Native surfaces the *last 5 lines* on spill; we
	// approximate by keeping the last `cap/2` bytes to stay predictable.
	keep := cap / 2
	tail := output
	if int64(keep) < size {
		tail = output[size-int64(keep):]
	}
	kB := (size + 512) / 1024
	trailer := fmt.Sprintf("\nOutput truncated (%d kB total). Full output saved to: %s\n", kB, path)
	// Strip any partial leading line on the kept tail so we don't start mid-line.
	if i := strings.IndexByte(tail, '\n'); i >= 0 && int64(keep) < size {
		tail = tail[i+1:]
	}
	return tail + trailer, path, size, nil
}
