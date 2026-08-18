package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// NormalizePath 规范化文件路径，作为所有 map key 与存储路径的唯一形式：
//   - 统一为绝对路径（不做解析，仅规范化格式）
//   - 统一分隔符为反斜杠不变、正斜杠转反斜杠（Windows）或保持（其余平台）
//   - Windows 驱动器字母统一小写（C:\Foo -> c:\foo）
//   - 清理冗余的 . / .. 段与末尾分隔符
//
// 无法规范化时返回原样字符串（调用方应自行跳过）。
func NormalizePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	// 统一分隔符
	path = filepath.Clean(path)
	// Windows 驱动器字母小写：c:/foo 或 c:\foo
	if len(path) >= 2 && path[1] == ':' {
		drive := strings.ToLower(path[:1])
		path = drive + path[1:]
	}
	// 确保返回的是绝对路径形式（接受 \\server\share 或 x:\ 形式）
	return path
}

// IsAbsolutePath 判断路径是否为绝对路径（跨平台：Windows 盘符、UNC、
// Unix 风格正斜杠前缀——Cursor 在 Windows 上也可能传入正斜杠路径）。
func IsAbsolutePath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	if filepath.IsAbs(path) {
		return true
	}
	// Unix 风格绝对路径（在 Windows 运行时 filepath.IsAbs 不识别）
	if strings.HasPrefix(path, "/") {
		return true
	}
	// Windows 风格盘符
	if len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/') {
		return true
	}
	// UNC
	if strings.HasPrefix(path, "\\\\") || strings.HasPrefix(path, "//") {
		return true
	}
	// 当前盘根（\foo）
	if strings.HasPrefix(path, "\\") {
		return true
	}
	return false
}

// ProjectHash 根据项目根路径生成稳定 hash，用于 workspace 目录命名。
func ProjectHash(projectRoot string) string {
	normalized := strings.ToLower(NormalizePath(projectRoot))
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])[:16]
}

// ProjectDisplayName 取项目根路径的最后一级目录名作为展示名。
func ProjectDisplayName(projectRoot string) string {
	root := strings.TrimRight(NormalizePath(projectRoot), `/\`)
	if root == "" {
		return ""
	}
	idx := strings.LastIndexAny(root, `/\`)
	if idx >= 0 && idx < len(root)-1 {
		return root[idx+1:]
	}
	return root
}

// ContentHash 计算内容的 sha256 十六进制摘要。
func ContentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// HashText 计算文本内容的 sha256 摘要。
func HashText(content string) string {
	return ContentHash([]byte(content))
}

// JoinProjectPath 把项目根与规范化后的路径拼成绝对路径（主要给跳转/恢复使用）。
func JoinProjectPath(projectRoot string, relative string) string {
	return NormalizePath(filepath.Join(projectRoot, relative))
}

// readDiskContent 读取磁盘文件内容；文件不存在（或读取失败）返回 (empty, false)。
// 供 AI 写入协调（PlanAIWritePath）与版本跳转判断使用。
func readDiskContent(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}
