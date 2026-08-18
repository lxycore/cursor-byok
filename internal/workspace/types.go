package workspace

import "time"

// Op 表示一次编辑事件的操作类型。
type Op string

const (
	// OpCreate 表示新文件创建（before 为空、after 非空）。
	OpCreate Op = "create"
	// OpModify 表示已有文件修改（before 与 after 均非空且不同）。
	OpModify Op = "modify"
	// OpDelete 表示文件删除（before 非空、after 为空）。
	OpDelete Op = "delete"
	// OpJump 表示版本跳转写入（系统行为）。
	OpJump Op = "jump"
)

// EventSource 表示编辑事件的来源。
type EventSource string

const (
	// SourceAI 表示 AI 编辑。
	SourceAI EventSource = "ai"
	// SourceUser 保留（编辑事件来源字段，当前仅 AI 产生编辑事件）。
	SourceUser EventSource = "user"
	// SourceSystem 表示系统行为（跳转、恢复）。
	SourceSystem EventSource = "system"
)

// WriterRef 标识一次写入的来源（对话 + 轮次）。
type WriterRef struct {
	ConversationID string `json:"conversationId,omitempty"`
	TurnSeq        int64  `json:"turnSeq,omitempty"`
	RequestID      string `json:"requestId,omitempty"`
	Source         string `json:"source,omitempty"`
}

// EditEvent 描述一次完整的文件编辑事实。内容本体不内嵌，
// 通过 BeforeHash / AfterHash 引用内容寻址存储（blobstore）。
type EditEvent struct {
	Seq            int64      `json:"seq"`
	ConversationID string     `json:"conversationId"`
	TurnSeq        int64      `json:"turnSeq"`
	RequestID      string     `json:"requestId,omitempty"`
	ToolCallID     string     `json:"toolCallId,omitempty"`
	Op             Op         `json:"op"`
	FilePath       string     `json:"filePath"` // 规范化绝对路径
	BeforeHash     string     `json:"beforeHash,omitempty"`
	AfterHash      string     `json:"afterHash,omitempty"`
	BeforeSize     int64      `json:"beforeSize,omitempty"`
	AfterSize      int64      `json:"afterSize,omitempty"`
	ModelName      string     `json:"modelName,omitempty"`
	ModelID        string     `json:"modelId,omitempty"`
	Source         EventSource `json:"source,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
}

// DeriveOp 根据 before/after 是否存在推导操作类型。
func DeriveOp(beforeExists bool, beforeHash string, afterExists bool, afterHash string) Op {
	switch {
	case !beforeExists && afterExists:
		return OpCreate
	case beforeExists && !afterExists:
		return OpDelete
	case beforeExists && afterExists && beforeHash != afterHash:
		return OpModify
	default:
		return OpModify
	}
}

// FileState 已随工作区索引移除（不再跟踪磁盘状态）。

// Outcome 表示一次协调写入的结果类型。
type Outcome string

const (
	// OutcomeApplied 表示直接写入成功（无外部修改）。
	OutcomeApplied Outcome = "applied"
	// OutcomeMerged 表示三方合并后写入成功。
	OutcomeMerged Outcome = "merged"
	// OutcomeConflict 表示存在行级冲突，按 AI 意图覆盖写入。
	OutcomeConflict Outcome = "conflict"
	// OutcomeFileGone 表示目标文件已不存在（用户/其他对话删除），未写入。
	OutcomeFileGone Outcome = "file_gone"
	// OutcomeSkipped 表示策略决定不写入。
	OutcomeSkipped Outcome = "skipped"
)

// WriteResult 描述一次协调写入的结果。
type WriteResult struct {
	Outcome   Outcome       `json:"outcome"`
	Conflict  *LineConflict `json:"conflict,omitempty"`
	FinalHash string        `json:"finalHash,omitempty"`
	Merged    bool          `json:"merged,omitempty"`
}

// LineConflict 描述一次文件行级写入冲突。
type LineConflict struct {
	FilePath       string `json:"filePath"`
	ConflictType   string `json:"conflictType"` // "line_overlap" | "file_created" | "file_deleted"
	BaseContent    string `json:"baseContent"`  // AI 读取时的原始内容
	DiskContent    string `json:"diskContent"`  // 磁盘上的实际内容
	AIAfterContent string `json:"aiAfterContent"` // AI 想要写入的内容
	OverlapRange   string `json:"overlapRange"` // 重叠行范围，如 "L10-L15"
	DetectedAt     string `json:"detectedAt"`
}

// ProjectEventType 表示项目事件类型。
type ProjectEventType string

const (
	// EventConflict 表示写盘冲突（磁盘内容已被 AI 意图覆盖，事件仅作提示）。
	EventConflict ProjectEventType = "file_conflict"
	// EventMergeApplied 表示三方合并已应用。
	EventMergeApplied ProjectEventType = "merge_applied"
	// EventJumpPerformed 表示版本跳转已执行。
	EventJumpPerformed ProjectEventType = "jump_performed"
	// EventWriteError 表示写盘失败。
	EventWriteError ProjectEventType = "write_error"
)

// ProjectEvent 是项目级协调事件，供前端通知区展示。
type ProjectEvent struct {
	Seq         int64            `json:"seq"`
	Type        ProjectEventType `json:"type"`
	ProjectPath string           `json:"projectPath"`
	FilePath    string           `json:"filePath,omitempty"`
	Message     string           `json:"message"`
	Details     map[string]any   `json:"details,omitempty"`
	CreatedAt   time.Time        `json:"createdAt"`
}

// TurnFileChange 描述一轮对话中单个文件的变更摘要。
type TurnFileChange struct {
	FilePath string `json:"filePath"`
	Op       Op     `json:"op"`
}

// TurnSummary 描述一轮对话的版本历史摘要（编辑日志聚合结果）。
type TurnSummary struct {
	ConversationID string           `json:"conversationId"`
	TurnSeq        int64            `json:"turnSeq"`
	UserMessage    string           `json:"userMessage"`
	FileChanges    []TurnFileChange `json:"fileChanges"`
	ModelName      string           `json:"modelName"`
	ModelID        string           `json:"modelId"`
	EditedAt       time.Time        `json:"editedAt"`
	ProjectName    string           `json:"projectName"`
	ProjectRoot    string           `json:"projectRoot"`
}

// DiffDetail 描述一个文件在"目标轮次状态"与"当前磁盘"之间的差异。
type DiffDetail struct {
	FilePath     string `json:"filePath"`
	Status       string `json:"status"` // "added" | "modified" | "deleted" | "unchanged"
	TurnContent  string `json:"turnContent"`
	DiskContent  string `json:"diskContent"`
	DiffString   string `json:"diffString"`
	LinesAdded   int    `json:"linesAdded"`
	LinesRemoved int    `json:"linesRemoved"`
}

// TurnDiff 描述一轮对话的完整差异结果。
type TurnDiff struct {
	ConversationID string       `json:"conversationId"`
	TurnSeq        int64        `json:"turnSeq"`
	ModelName      string       `json:"modelName"`
	ModelID        string       `json:"modelId"`
	EditedAt       time.Time    `json:"editedAt"`
	UserMessage    string       `json:"userMessage"`
	Files          []DiffDetail `json:"files"`
}

// JumpResult 描述一次版本跳转的结果。
type JumpResult struct {
	TargetTurnSeq int64    `json:"targetTurnSeq"`
	UpdatedFiles  []string `json:"updatedFiles"`
	MaxTurnSeq    int64    `json:"maxTurnSeq"`
	// SkippedFiles 是跳转时因被其他对话编辑而跳过的共享文件
	// （合并冲突/目标轮不存在时保持磁盘原样，不覆盖其他对话的代码）。
	SkippedFiles []string `json:"skippedFiles,omitempty"`
}
