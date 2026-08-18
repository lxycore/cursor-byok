//go:build !windows

package modeladapter

import "os/exec"

// applyHideWindow 在非 Windows 平台为空操作。
func applyHideWindow(cmd *exec.Cmd) {
	_ = cmd
}
