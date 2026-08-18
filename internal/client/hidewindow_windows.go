//go:build windows

package client

import (
	"os/exec"
	"syscall"
)

// hideWindowSysProcAttr 在 Windows 上隐藏子进程控制台窗口，避免测试时弹出黑色终端窗口。
func hideWindowSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true}
}

var _ = exec.Command
