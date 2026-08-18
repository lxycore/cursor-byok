// view.go 提供版本展示的聚合逻辑：轮次 diff（该轮做了什么）、磁盘匹配判定。
// bridge 层只做 DTO 转换，不在桥接层做文件 IO。
package workspace

import (
	"errors"
	"sort"
	"strings"
	"time"
)

// TurnDiff 计算一轮对话的差异视图（"该轮做了什么"：该轮内每个文件的变更）。
// conversationID 无编辑记录时返回 error。
func (project *Project) TurnDiff(conversationID string, targetTurnSeq int64) (*TurnDiff, error) {
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
	maxTurn := project.Log.MaxTurnSeq(conversationID)
	if maxTurn == 0 {
		return nil, errors.New("no file edits found for this conversation")
	}
	if targetTurnSeq > maxTurn {
		return nil, errors.New("target turn exceeds conversation max turn")
	}

	result := &TurnDiff{
		ConversationID: conversationID,
		TurnSeq:        targetTurnSeq,
		EditedAt:       project.turnEditedAt(conversationID, targetTurnSeq),
		UserMessage:    "",
	}

	result.Files = project.turnChangeDiff(conversationID, targetTurnSeq)
	// 无实际差异是正常结果（如磁盘内容与该轮一致，仅行尾不同），
	// 由调用方决定如何展示，不视为错误。
	return result, nil
}

// turnEditedAt 取该轮最早编辑事件时间（轮次时间）。
func (project *Project) turnEditedAt(conversationID string, turnSeq int64) time.Time {
	events := project.Log.EventsByConversation(conversationID)
	var earliest time.Time
	for _, event := range events {
		if event.TurnSeq != turnSeq {
			continue
		}
		if earliest.IsZero() || event.CreatedAt.Before(earliest) {
			earliest = event.CreatedAt
		}
	}
	return earliest
}

// turnChangeDiff 计算"该轮做了什么"：同一轮内同一文件可能多次编辑
// （如多次 PatchEdit），取该轮**首个事件的 before** 与 **最后一个事件的 after**
// 做累积 diff，避免把一轮操作拆散成多段。
func (project *Project) turnChangeDiff(conversationID string, targetTurnSeq int64) []DiffDetail {
	events := project.Log.EventsByConversation(conversationID)
	firstByPath := make(map[string]EditEvent)
	lastByPath := make(map[string]EditEvent)
	for _, event := range events {
		if event.TurnSeq != targetTurnSeq {
			continue
		}
		if _, ok := firstByPath[event.FilePath]; !ok {
			firstByPath[event.FilePath] = event
		}
		lastByPath[event.FilePath] = event
	}
	var details []DiffDetail
	for path := range lastByPath {
		first := firstByPath[path]
		last := lastByPath[path]
		before := project.Blobs.GetText(first.BeforeHash)
		after := project.Blobs.GetText(last.AfterHash)
		status := "modified"
		switch {
		case first.BeforeHash == "" && last.AfterHash != "":
			status = "added" // 整轮净效果：新增
		case first.BeforeHash != "" && last.AfterHash == "":
			status = "deleted" // 整轮净效果：删除
		}
		detail := DiffDetail{
			FilePath:    path,
			Status:      status,
			TurnContent: before,
			DiskContent: after,
		}
		diff := DiffContent(before, after)
		detail.DiffString = diff.DiffString
		detail.LinesAdded = int(diff.LinesAdded)
		detail.LinesRemoved = int(diff.LinesRemoved)
		details = append(details, detail)
	}
	sortDetails(details)
	return details
}

// MatchesDisk 判断该对话的目标轮次状态是否与磁盘完全一致（active 判定）。
// 规则：磁盘内容与该轮记录完全一致才返回 true（全文件判定，无独占/共享概念）。
// 多对话共享文件被合并后，磁盘是混合状态，不等于任何对话的纯记录 → 不匹配，
// 如实反映真实状态。
func (project *Project) MatchesDisk(conversationID string, targetTurnSeq int64) bool {
	if project == nil {
		return false
	}
	state, err := project.Log.TurnFileStateAt(conversationID, targetTurnSeq)
	if err != nil || len(state) == 0 {
		return false
	}
	allFiles := project.Log.ConversationFiles(conversationID)
	for path := range allFiles {
		content, existedAtTurn := state[path]
		disk, diskExists := readDiskContent(path)
		if !existedAtTurn {
			// 该轮时不存在：磁盘必须也不存在（跳转删除语义）
			if diskExists {
				return false
			}
			continue
		}
		// 行尾差异（CRLF vs LF）不算修改
		if !diskExists || normalizeLF(disk) != normalizeLF(content) {
			return false
		}
	}
	return true
}

func sortDetails(details []DiffDetail) {
	sort.Slice(details, func(i, j int) bool {
		return details[i].FilePath < details[j].FilePath
	})
}
