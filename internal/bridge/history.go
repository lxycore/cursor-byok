// history.go 提供对话编辑历史查询、版本跳转与 checkpoint 的 Wails 桥接服务。
// 数据源为 workspace 版本系统（编辑事件日志 + 内容寻址存储 + 快照），
// 对话 history 仅用于补充用户消息文本。
package bridge

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"

	"cursor/gen/agentv1"
	"cursor/internal/backend/forwarder"
	"cursor/internal/workspace"
)

// ConversationTurnSummary 描述一个有文件编辑操作的对话轮次摘要。
type ConversationTurnSummary struct {
	ConversationID    string           `json:"conversationId"`
	TurnSeq           int64            `json:"turnSeq"`
	UserMessage       string           `json:"userMessage"`
	FilePaths         []string         `json:"filePaths"`
	FileChanges       []FileChangeBrief `json:"fileChanges"`
	EditedAt          string           `json:"editedAt"`
	IsActive          bool             `json:"isActive"`
	ProjectName       string           `json:"projectName"`
	ProjectRoot       string           `json:"projectRoot"`
	ModelName         string           `json:"modelName"`
	ModelID           string           `json:"modelId"`
	ConversationLabel string           `json:"conversationLabel"`
	JumpedAt          string           `json:"jumpedAt"` // 最近一次跳转到该轮的时间（RFC3339）
}

// FileChangeBrief 描述单文件的变更摘要。
type FileChangeBrief struct {
	FilePath string `json:"filePath"`
	Status   string `json:"status"` // "added", "modified", "deleted"
}

// JumpToTurnResult 描述一次版本跳跃操作的结果。
type JumpToTurnResult struct {
	TargetTurnSeq int64    `json:"targetTurnSeq"`
	UpdatedFiles  []string `json:"updatedFiles"`
	MaxTurnSeq    int64    `json:"maxTurnSeq"` // 对话中已知的最大轮次
	SkippedFiles  []string `json:"skippedFiles,omitempty"` // 因被其他对话编辑而跳过的共享文件
}

// FileDiffDetail 桥接层 DTO，描述一个文件在当前磁盘与目标轮次之间的差异。
type FileDiffDetail struct {
	FilePath     string `json:"filePath"`
	Status       string `json:"status"`       // "added", "modified", "deleted", "unchanged"
	TurnContent  string `json:"turnContent"`  // 该轮次时的文件内容
	DiskContent  string `json:"diskContent"`  // 当前磁盘上的文件内容
	DiffString   string `json:"diffString"`   // unified diff (patch format)
	LinesAdded   int    `json:"linesAdded"`
	LinesRemoved int    `json:"linesRemoved"`
}

// TurnDiffResult 桥接层 DTO，描述一轮对话的完整差异结果。
type TurnDiffResult struct {
	ConversationID string           `json:"conversationId"`
	TurnSeq        int64            `json:"turnSeq"`
	ModelName      string           `json:"modelName"`
	ModelID        string           `json:"modelId"`
	EditedAt       string           `json:"editedAt"`
	UserMessage    string           `json:"userMessage"`
	Files          []FileDiffDetail `json:"files"`
}

// HistoryService 提供对话历史的查询和版本跳跃操作，通过 Wails 暴露给前端。
type HistoryService struct {
	store *forwarder.ConversationFileStore
	ws    *workspace.Manager
}

// NewHistoryService 创建 HistoryService。
// historyRoot 为对话历史文件根目录；ws 为 workspace 版本系统管理器。
func NewHistoryService(historyRoot string, ws *workspace.Manager) *HistoryService {
	return &HistoryService{
		store: forwarder.NewConversationFileStore(historyRoot),
		ws:    ws,
	}
}

// ListRecentTurns 返回最近有文件编辑操作的对话轮次列表，按时间降序排列。
func (s *HistoryService) ListRecentTurns(limit int32) ([]ConversationTurnSummary, error) {
	if s.ws == nil {
		return []ConversationTurnSummary{}, nil
	}
	var allTurns []ConversationTurnSummary
	projects := s.ws.Projects()
	for _, project := range projects {
		allTurns = append(allTurns, s.collectProjectTurns(project)...)
	}
	sort.Slice(allTurns, func(i, j int) bool {
		return allTurns[i].EditedAt > allTurns[j].EditedAt
	})
	if limit > 0 && int32(len(allTurns)) > limit {
		allTurns = allTurns[:limit]
	}
	if allTurns == nil {
		return []ConversationTurnSummary{}, nil
	}
	return allTurns, nil
}

// collectProjectTurns 收集一个项目的全部轮次摘要（AI 编辑轮次）。
func (s *HistoryService) collectProjectTurns(project *workspace.Project) []ConversationTurnSummary {
	var turns []ConversationTurnSummary
	summaries := project.Log.TurnSummaries()
	for _, summary := range summaries {
		convLabel := s.conversationLabel(summary.ConversationID)
		userMsg := s.turnUserMessage(summary.ConversationID, summary.TurnSeq)
		if userMsg == "" {
			userMsg = fmt.Sprintf("(对话轮次 %d)", summary.TurnSeq)
		}
		filePaths := make([]string, 0, len(summary.FileChanges))
		fileChanges := make([]FileChangeBrief, 0, len(summary.FileChanges))
		for _, change := range summary.FileChanges {
			filePaths = append(filePaths, change.FilePath)
			fileChanges = append(fileChanges, FileChangeBrief{
				FilePath: change.FilePath,
				Status:   briefStatus(change.Op),
			})
		}
		turns = append(turns, ConversationTurnSummary{
			ConversationID:    summary.ConversationID,
			TurnSeq:           summary.TurnSeq,
			UserMessage:       userMsg,
			FilePaths:         filePaths,
			FileChanges:       fileChanges,
			EditedAt:          summary.EditedAt.Format("2006-01-02T15:04:05Z07:00"),
			IsActive:          project.MatchesDisk(summary.ConversationID, summary.TurnSeq),
			ProjectName:       project.Name,
			ProjectRoot:       project.Root,
			ModelName:         summary.ModelName,
			ModelID:           summary.ModelID,
			ConversationLabel: convLabel,
		})
	}
	return turns
}

// briefStatus 把编辑 op 映射为展示状态。
func briefStatus(op workspace.Op) string {
	switch op {
	case workspace.OpCreate:
		return "added"
	case workspace.OpDelete:
		return "deleted"
	default:
		return "modified"
	}
}

// GetTurnDiff 返回目标轮次的差异视图（该轮做了什么）。
func (s *HistoryService) GetTurnDiff(conversationID string, targetTurnSeq int64) (TurnDiffResult, error) {
	convID := strings.TrimSpace(conversationID)
	if convID == "" {
		return TurnDiffResult{}, fmt.Errorf("conversationID is required")
	}
	if targetTurnSeq <= 0 {
		return TurnDiffResult{}, fmt.Errorf("targetTurnSeq must be positive")
	}
	project := s.projectForConversation(convID)
	if project == nil {
		return TurnDiffResult{}, fmt.Errorf("conversation %q has no workspace project", convID)
	}
	diff, err := project.TurnDiff(convID, targetTurnSeq)
	if err != nil {
		return TurnDiffResult{}, err
	}
	return s.turnDiffToDTO(project, diff), nil
}

func (s *HistoryService) turnDiffToDTO(project *workspace.Project, diff *workspace.TurnDiff) TurnDiffResult {
	result := TurnDiffResult{
		ConversationID: diff.ConversationID,
		TurnSeq:        diff.TurnSeq,
		EditedAt:       diff.EditedAt.Format("2006-01-02T15:04:05Z07:00"),
		UserMessage:    diff.UserMessage,
	}
	if result.UserMessage == "" {
		result.UserMessage = s.turnUserMessage(diff.ConversationID, diff.TurnSeq)
	}
	if result.EditedAt == "" || strings.HasPrefix(result.EditedAt, "0001") {
		if conv, err := s.store.LoadConversation(diff.ConversationID); err == nil && conv != nil {
			result.EditedAt = conv.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")
		}
	}
	for _, fd := range diff.Files {
		result.Files = append(result.Files, FileDiffDetail{
			FilePath:     fd.FilePath,
			Status:       fd.Status,
			TurnContent:  fd.TurnContent,
			DiskContent:  fd.DiskContent,
			DiffString:   fd.DiffString,
			LinesAdded:   fd.LinesAdded,
			LinesRemoved: fd.LinesRemoved,
		})
	}
	// 模型信息：优先从 editlog 事件取（可能为空则 fallback history）
	if diff.TurnSeq > 0 {
		events := project.Log.EventsByConversation(diff.ConversationID)
		for _, event := range events {
			if event.TurnSeq == diff.TurnSeq && event.ModelName != "" {
				result.ModelName = event.ModelName
				result.ModelID = event.ModelID
				break
			}
		}
	}
	if result.ModelName == "" {
		modelName, modelID := forwarder.ExtractModelNameFromEntries(s.entriesOf(diff.ConversationID), diff.TurnSeq)
		result.ModelName = modelName
		result.ModelID = modelID
	}
	return result
}

// JumpToTurn 将文件状态跳转到目标轮次完成后的状态。
func (s *HistoryService) JumpToTurn(conversationID string, targetTurnSeq int64) (JumpToTurnResult, error) {
	convID := strings.TrimSpace(conversationID)
	if convID == "" {
		return JumpToTurnResult{}, fmt.Errorf("conversationID is required")
	}
	if targetTurnSeq <= 0 {
		return JumpToTurnResult{}, fmt.Errorf("targetTurnSeq must be positive")
	}
	project := s.projectForConversation(convID)
	if project == nil {
		return JumpToTurnResult{}, fmt.Errorf("conversation %q has no workspace project", convID)
	}
	result, err := project.JumpToTurn(convID, targetTurnSeq)
	if err != nil {
		return JumpToTurnResult{}, err
	}
	return JumpToTurnResult{
		TargetTurnSeq: result.TargetTurnSeq,
		UpdatedFiles:  result.UpdatedFiles,
		MaxTurnSeq:    result.MaxTurnSeq,
		SkippedFiles:  result.SkippedFiles,
	}, nil
}

// ProjectEvents 返回指定项目最近的事件（前端通知区）。
func (s *HistoryService) ProjectEvents(projectRoot string, limit int32, sinceSeq int64) ([]workspace.ProjectEvent, error) {
	if s.ws == nil {
		return []workspace.ProjectEvent{}, nil
	}
	project := s.ws.GetProject(projectRoot)
	if project == nil {
		return []workspace.ProjectEvent{}, nil
	}
	if sinceSeq > 0 {
		return project.Events.Since(sinceSeq), nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return project.Events.Recent(int(limit)), nil
}

// projectForConversation 返回对话绑定的项目。
func (s *HistoryService) projectForConversation(conversationID string) *workspace.Project {
	if s.ws == nil {
		return nil
	}
	return s.ws.ProjectForConversation(conversationID)
}

// turnUserMessage 从对话 history 提取指定轮次的用户消息。
func (s *HistoryService) turnUserMessage(conversationID string, turnSeq int64) string {
	conv, err := s.store.LoadConversation(conversationID)
	if err != nil || conv == nil {
		return ""
	}
	for _, entry := range conv.Entries {
		if entry.TurnSeq != turnSeq || entry.Kind != "user_message" || len(entry.Payload) == 0 {
			continue
		}
		if msg := extractUserMessageText(entry.Payload); msg != "" {
			return msg
		}
	}
	return ""
}

// conversationLabel 提取对话中第一条用户消息作为对话标签。
func (s *HistoryService) conversationLabel(conversationID string) string {
	conv, err := s.store.LoadConversation(conversationID)
	if err != nil || conv == nil {
		return ""
	}
	for _, entry := range conv.Entries {
		if entry.Kind == "user_message" && entry.Role == "user" && len(entry.Payload) > 0 {
			if msg := extractUserMessageText(entry.Payload); msg != "" {
				return msg
			}
		}
	}
	return ""
}

// entriesOf 加载对话条目（供模型名 fallback）。
func (s *HistoryService) entriesOf(conversationID string) []forwarder.HistoryEntry {
	conv, err := s.store.LoadConversation(conversationID)
	if err != nil || conv == nil {
		return nil
	}
	return conv.Entries
}

// extractUserMessageText 从 user_message 条目的 Payload 中提取用户文本。
func extractUserMessageText(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	userMsg := &agentv1.UserMessage{}
	if err := protojson.Unmarshal(payload, userMsg); err != nil {
		return ""
	}
	text := strings.TrimSpace(userMsg.GetText())
	if text == "" {
		text = strings.TrimSpace(userMsg.GetRichText())
	}
	if text == "" {
		return ""
	}
	if idx := strings.Index(text, "\n"); idx > 0 && idx < 100 {
		text = text[:idx]
	}
	runeLen := 0
	for range text {
		runeLen++
		if runeLen > 80 {
			text = string([]rune(text)[:80]) + "..."
			break
		}
	}
	return text
}
