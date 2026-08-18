//go:build !windows

package client

import (
	"os/exec"
	"syscall"
)

// hideWindowSysProcAttr 在非 Windows 平台返回 nil（无窗口隐藏需求）。
func hideWindowSysProcAttr() *syscall.SysProcAttr {
	return nil
}

var _ = exec.Command
