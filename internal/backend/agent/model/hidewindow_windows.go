//go:build windows

package modeladapter

import (
	"os/exec"
	"syscall"
)

// applyHideWindow 在 Windows 上隐藏子进程控制台窗口，
// 避免识图转换时弹出黑色终端窗口。
func applyHideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
