// snapshot.go 实现版本跳转：
//   - 跳转目标状态由该对话的 EditLog 重放得出，只影响该对话编辑过的文件
//   - 独占文件（只有本对话编辑过）直接写/删；共享文件（其他对话也编辑过）
//     内容级三方合并（本对话的部分回退、其他对话的改动保留），冲突时跳过
//   - 写盘走 Coordinator（带锁、错误透传）
package workspace

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// isSharedFile 判断文件是否被其他对话编辑过（共享文件）。
// 通过全局 byFile 索引查询：存在 ConversationID ≠ 本对话 的事件即为共享。
func (project *Project) isSharedFile(path string, conversationID string) bool {
	events := project.Log.EventsByFile(path)
	for _, event := range events {
		if event.ConversationID != conversationID {
			return true
		}
	}
	return false
}

// JumpToTurn 执行版本跳转：
//   - 目标状态：从 EditLog 重放该对话到 targetTurnSeq
//   - 独占文件（只有本对话编辑过）：写回目标状态 / 删除（目标轮不存在时）
//   - 共享文件（其他对话也编辑过）：内容级三方合并
//     Merge3(本对话最新轮内容, 磁盘, 目标状态) → 本对话的部分回退、
//     其他对话的改动保留；合并冲突/出错时跳过该文件（保持磁盘原样，
//     绝不丢失其他对话的代码）
//   - 写盘走 Coordinator（带锁、错误透传）
//
// 返回跳转结果（含写盘/删除的文件列表与跳过的共享文件）。
func (project *Project) JumpToTurn(conversationID string, targetTurnSeq int64) (*JumpResult, error) {
	if project == nil {
		return nil, errors.New("project is nil")
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil, errors.New("conversationID is required")
	}
	if targetTurnSeq <= 0 {
		return nil, errors.New("targetTurnSeq must be positive")
	}
	maxTurnSeq := project.Log.MaxTurnSeq(conversationID)
	if maxTurnSeq == 0 {
		return nil, fmt.Errorf("no file edits found for conversation %s", conversationID)
	}
	if targetTurnSeq > maxTurnSeq {
		return nil, fmt.Errorf("targetTurnSeq %d exceeds max turn %d", targetTurnSeq, maxTurnSeq)
	}

	// 目标状态：重放该对话到目标轮次；latestState：本对话最新轮（共享文件合并的 base）
	targetState, err := project.Log.TurnFileStateAt(conversationID, targetTurnSeq)
	if err != nil {
		return nil, err
	}
	latestState, err := project.Log.TurnFileStateAt(conversationID, maxTurnSeq)
	if err != nil {
		return nil, err
	}
	conversationFiles := project.Log.ConversationFiles(conversationID)

	var updated []string
	var skipped []string
	owner := &WriterRef{ConversationID: conversationID, TurnSeq: targetTurnSeq, Source: string(SourceSystem)}

	// 写入：目标状态中存在的文件 → 独占直接写；共享合并写（冲突跳过）
	paths := make([]string, 0, len(targetState))
	for path := range targetState {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		content := targetState[path]
		if project.isSharedFile(path, conversationID) {
			// 共享文件：磁盘已一致（或不存在）→ 直接写；否则内容级合并
			disk, diskExists := readDiskContent(path)
			if !diskExists || normalizeLF(disk) == normalizeLF(content) {
				if _, err := project.Coordinator.WriteFile(path, content, owner); err != nil {
					return nil, fmt.Errorf("write %s: %w", path, err)
				}
				updated = append(updated, path)
				continue
			}
			base := latestState[path] // 本对话最新轮该文件内容（共同祖先）
			merged, mergeErr := Merge3(base, disk, content)
			if mergeErr == nil && merged.Conflict == nil {
				if _, err := project.Coordinator.WriteFile(path, merged.Merged, owner); err != nil {
					return nil, fmt.Errorf("write %s: %w", path, err)
				}
				updated = append(updated, path)
			} else {
				// 合并冲突/出错：跳过该文件，保持磁盘原样（不丢其他对话的代码）
				skipped = append(skipped, path)
			}
			continue
		}
		// 独占文件：直接写目标状态
		if _, err := project.Coordinator.WriteFile(path, content, owner); err != nil {
			return nil, fmt.Errorf("write %s: %w", path, err)
		}
		updated = append(updated, path)
	}

	// 删除：该对话创建/编辑过、但目标轮次时不存在的文件。
	// 独占文件删除；共享文件跳过（其他对话的代码严格保留）。
	var toDelete []string
	for path := range conversationFiles {
		if _, exists := targetState[path]; !exists {
			toDelete = append(toDelete, path)
		}
	}
	sort.Strings(toDelete)
	for _, path := range toDelete {
		if project.isSharedFile(path, conversationID) {
			skipped = append(skipped, path)
			continue
		}
		if err := project.Coordinator.RemoveFile(path, owner); err != nil {
			return nil, fmt.Errorf("remove %s: %w", path, err)
		}
		updated = append(updated, path)
	}

	// 记录跳转事件
	details := map[string]any{
		"conversationId": conversationID,
		"targetTurnSeq":  targetTurnSeq,
		"updatedFiles":   updated,
	}
	if len(skipped) > 0 {
		details["skippedFiles"] = skipped
	}
	project.emit(EventJumpPerformed, "", "version jump performed", details)

	return &JumpResult{
		TargetTurnSeq: targetTurnSeq,
		UpdatedFiles:  updated,
		MaxTurnSeq:    maxTurnSeq,
		SkippedFiles:  skipped,
	}, nil
}

// emit 生成项目事件。
func (project *Project) emit(eventType ProjectEventType, filePath string, message string, details map[string]any) {
	if project == nil || project.Events == nil {
		return
	}
	_, _ = project.Events.Emit(ProjectEvent{
		Type:        eventType,
		ProjectPath: project.Root,
		FilePath:    filePath,
		Message:     message,
		Details:     details,
	})
}
