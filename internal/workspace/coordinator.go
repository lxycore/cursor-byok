// coordinator.go 实现服务端写盘协调：
//   - per-file 互斥锁（跨对话/跨 goroutine）
//   - 错误完整透传（绝不静默失败）
//   - AI 写入前的三方合并协调（读磁盘 → merge3 → 按策略处理冲突）
//   - 冲突/合并/写失败事件
package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// fileLockEntry 是单个文件的进程内互斥锁。
type fileLockEntry struct {
	mu   sync.Mutex
	refs int
}

// Coordinator 协调项目内所有服务端写盘操作。
type Coordinator struct {
	project *Project

	mu        sync.Mutex
	fileLocks map[string]*fileLockEntry
}

// NewCoordinator 创建写协调器。
func NewCoordinator(project *Project) *Coordinator {
	return &Coordinator{
		project:   project,
		fileLocks: make(map[string]*fileLockEntry),
	}
}

// lockFile 获取指定文件的互斥锁。
func (coordinator *Coordinator) lockFile(path string) func() {
	path = NormalizePath(path)
	coordinator.mu.Lock()
	entry, ok := coordinator.fileLocks[path]
	if !ok {
		entry = &fileLockEntry{}
		coordinator.fileLocks[path] = entry
	}
	entry.refs++
	coordinator.mu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		coordinator.mu.Lock()
		entry.refs--
		if entry.refs <= 0 {
			delete(coordinator.fileLocks, path)
		}
		coordinator.mu.Unlock()
	}
}

// WriteFile 直接写盘（服务端行为：版本跳转），带 per-file 锁与完整错误透传。
// 返回最终写入内容的 hash。
func (coordinator *Coordinator) WriteFile(path string, content string, owner *WriterRef) (string, error) {
	if coordinator == nil || coordinator.project == nil {
		return "", errors.New("coordinator is not initialized")
	}
	path = NormalizePath(path)
	if path == "" {
		return "", errors.New("write path is empty")
	}
	release := coordinator.lockFile(path)
	defer release()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		coordinator.emitWriteError(path, err)
		return "", err
	}
	return HashText(content), nil
}

// WriteFileFromBlob 按内容 hash 写盘（避免重复读 blob）。
func (coordinator *Coordinator) WriteFileFromBlob(path string, contentHash string, owner *WriterRef) (string, error) {
	content, ok := coordinator.project.Blobs.Get(contentHash)
	if !ok {
		return "", ErrBlobMissing
	}
	return coordinator.WriteFile(path, string(content), owner)
}

// RemoveFile 删除磁盘文件（服务端行为：跳转到早期轮次时删除本对话后来创建的文件）。
// 返回 nil 表示删除成功或文件本就不存在。
func (coordinator *Coordinator) RemoveFile(path string, owner *WriterRef) error {
	if coordinator == nil || coordinator.project == nil {
		return errors.New("coordinator is not initialized")
	}
	path = NormalizePath(path)
	if path == "" {
		return errors.New("remove path is empty")
	}
	release := coordinator.lockFile(path)
	defer release()

	err := os.Remove(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// 文件本就不存在：视同成功
			return nil
		}
		coordinator.emitWriteError(path, err)
		return err
	}
	return nil
}

// AIWritePlan 描述一次 AI 写入前的协调结果。
type AIWritePlan struct {
	// Content 是最终应写入的内容（merged 或 theirs）。
	Content string
	// Result 是协调结果描述。
	Result WriteResult
}

// PlanAIWritePath 在 AI 写盘前做并发协调（带路径版本）。
//   - base：AI 读取文件时的内容（为空表示 AI 认为文件不存在）
//   - theirs：AI 想写入的内容
//   - 读磁盘 ours → Merge3 → 按策略返回最终应写入的内容与结果描述。
func (coordinator *Coordinator) PlanAIWritePath(path string, base string, theirs string, owner *WriterRef) AIWritePlan {
	path = NormalizePath(path)
	release := coordinator.lockFile(path)
	defer release()

	ours, oursExists := readDiskContent(path)

	// 文件被删（AI 有 base，磁盘无）
	if !oursExists && strings.TrimSpace(base) != "" {
		return AIWritePlan{
			Content: theirs,
			Result: WriteResult{
				Outcome: OutcomeFileGone,
				Conflict: &LineConflict{
					FilePath:       path,
					ConflictType:   "file_deleted",
					BaseContent:    base,
					DiskContent:    "",
					AIAfterContent: theirs,
					DetectedAt:     nowRFC3339(),
				},
			},
		}
	}
	// 磁盘文件内容与 AI 读取一致（行尾差异不算修改）：直接采用 AI 意图
	if oursExists && normalizeLF(ours) == normalizeLF(base) {
		return AIWritePlan{Content: theirs, Result: WriteResult{Outcome: OutcomeApplied}}
	}
	// AI 新建文件（base 空），磁盘无文件或文件为空：直接采用 AI 意图
	if strings.TrimSpace(base) == "" && (!oursExists || strings.TrimSpace(ours) == "") {
		return AIWritePlan{Content: theirs, Result: WriteResult{Outcome: OutcomeApplied}}
	}
	// AI 新建文件（base 空），磁盘已有内容
	if strings.TrimSpace(base) == "" && oursExists && strings.TrimSpace(ours) != "" {
		conflict := &LineConflict{
			FilePath:       path,
			ConflictType:   "file_created",
			BaseContent:    base,
			DiskContent:    ours,
			AIAfterContent: theirs,
			DetectedAt:     nowRFC3339(),
		}
		return coordinator.conflictPlan(path, ours, conflict, theirs, owner)
	}

	// 三方合并
	result, err := Merge3(base, ours, theirs)
	if err != nil {
		return AIWritePlan{
			Content: theirs,
			Result: WriteResult{
				Outcome: OutcomeConflict,
				Conflict: &LineConflict{FilePath: path, ConflictType: "merge_error", BaseContent: base, DiskContent: ours, AIAfterContent: theirs, DetectedAt: nowRFC3339()},
			},
		}
	}
	if result.Conflict != nil {
		result.Conflict.FilePath = path
		return coordinator.conflictPlan(path, ours, result.Conflict, theirs, owner)
	}
	// 合并成功
	coordinator.emit(EventMergeApplied, path, "AI changes merged with concurrent disk changes", map[string]any{
		"mergedHash": HashText(result.Merged),
	})
	return AIWritePlan{
		Content: result.Merged,
		Result:  WriteResult{Outcome: OutcomeMerged, Merged: true, FinalHash: HashText(result.Merged)},
	}
}

// conflictPlan 处理冲突：直接按 AI 意图写入（磁盘上的其他改动丢弃），
// 并生成冲突事件作提示。
func (coordinator *Coordinator) conflictPlan(path string, ours string, conflict *LineConflict, theirs string, owner *WriterRef) AIWritePlan {
	coordinator.emit(EventConflict, path, "write conflict detected; disk content overwritten by AI", map[string]any{
		"conflictType": conflict.ConflictType,
		"overlapRange": conflict.OverlapRange,
		"diskHash":     HashText(ours),
		"aiHash":       HashText(theirs),
	})
	return AIWritePlan{
		Content: theirs,
		Result: WriteResult{
			Outcome:   OutcomeConflict,
			Conflict:  conflict,
			FinalHash: HashText(theirs),
		},
	}
}

// emit 生成项目事件。
func (coordinator *Coordinator) emit(eventType ProjectEventType, filePath string, message string, details map[string]any) {
	if coordinator == nil || coordinator.project == nil || coordinator.project.Events == nil {
		return
	}
	_, _ = coordinator.project.Events.Emit(ProjectEvent{
		Type:        eventType,
		ProjectPath: coordinator.project.Root,
		FilePath:    filePath,
		Message:     message,
		Details:     details,
	})
}

// emitWriteError 生成写失败事件。
func (coordinator *Coordinator) emitWriteError(path string, err error) {
	coordinator.emit(EventWriteError, path, "file write failed: "+err.Error(), map[string]any{
		"error": err.Error(),
	})
}
