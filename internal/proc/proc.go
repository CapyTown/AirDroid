// Package proc starts helper programs (adb, scrcpy, FFmpeg) the way a
// desktop app should: on Windows without a console window flashing up.
package proc

import (
	"context"
	"os/exec"
)

// Command is exec.CommandContext for a helper program that runs in the
// background.
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	hide(cmd)
	return cmd
}

// Hide keeps cmd's console out of sight, on Windows.
func Hide(cmd *exec.Cmd) { hide(cmd) }
