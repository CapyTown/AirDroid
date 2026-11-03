//go:build !windows

package proc

import "os/exec"

func hide(cmd *exec.Cmd) {}
