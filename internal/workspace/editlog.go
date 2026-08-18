package workspace

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// EditLog 是单个项目的编辑事件日志。
// 持久化：<projectDir>/editlog/<conversationID>.jsonl（append-only，每行一个事件）。
// 内存维护 对话→事件 与 文件→事件 两级索引，启动时从磁盘加载。
type EditLog struct {
	projectDir string
	blobs      *BlobStore

	mu          sync.RWMutex
	byConv      map[string][]EditEvent
	byFile      map[string][]EditEvent
	seq         int64
	projectRoot string
}

// NewEditLog 创建编辑日志。projectDir 为项目 workspace 目录（含 editlog/ 子目录）。
func NewEditLog(projectDir string, projectRoot string, blobs *BlobStore) (*EditLog, error) {
	log := &EditLog{
		projectDir: projectDir,
		blobs:      blobs,
		byConv:     make(map[string][]EditEvent),
		byFile:     make(map[string][]EditEvent),
		projectRoot: projectRoot,
	}
	if err := log.load(); err != nil {
		return nil, err
	}
	return log, nil
}

func (log *EditLog) editlogDir() string {
	return filepath.Join(log.projectDir, "editlog")
}

func (log *EditLog) convPath(conversationID string) string {
	return filepath.Join(log.editlogDir(), conversationID+".jsonl")
}

func (log *EditLog) load() error {
	entries, err := os.ReadDir(log.editlogDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		conversationID := strings.TrimSuffix(entry.Name(), ".jsonl")
		events, err := log.loadConversationFile(filepath.Join(log.editlogDir(), entry.Name()))
		if err != nil {
			return fmt.Errorf("load editlog %s: %w", entry.Name(), err)
		}
		for _, event := range events {
			event.ConversationID = conversationID
			log.byConv[conversationID] = append(log.byConv[conversationID], event)
			normalized := NormalizePath(event.FilePath)
			event.FilePath = normalized
			log.byFile[normalized] = append(log.byFile[normalized], event)
			if event.Seq > log.seq {
				log.seq = event.Seq
			}
		}
	}
	return nil
}

func (log *EditLog) loadConversationFile(path string) ([]EditEvent, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var events []EditEvent
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event EditEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		events = append(events, event)
	}
	return events, scanner.Err()
}

// Record 记录一次编辑事件：
//   - 把 before/after 全文写入 blob（内容寻址去重）
//   - 分配全项目递增 seq
//   - 追加到对话 jsonl（原子写：先写临时文件再 rename 追加不可行——
//     采用 O_APPEND 单写者追加，由进程内锁保证顺序）
//   - 更新内存索引
//
// 返回带 seq 的事件副本。
func (log *EditLog) Record(event EditEvent) (EditEvent, error) {
	if log == nil {
		return EditEvent{}, errors.New("edit log is nil")
	}
	event.FilePath = NormalizePath(event.FilePath)
	if event.FilePath == "" {
		return EditEvent{}, errors.New("edit event file path is empty")
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	// before/after 全文由调用方经 SetBlob 传入；此处仅校验引用存在。
	if event.BeforeHash != "" && !log.blobs.Exists(event.BeforeHash) {
		return EditEvent{}, fmt.Errorf("before blob missing for %s", event.FilePath)
	}
	if event.AfterHash != "" && !log.blobs.Exists(event.AfterHash) {
		return EditEvent{}, fmt.Errorf("after blob missing for %s", event.FilePath)
	}

	log.mu.Lock()
	defer log.mu.Unlock()

	log.seq++
	event.Seq = log.seq

	line, err := json.Marshal(event)
	if err != nil {
		return EditEvent{}, err
	}
	if err := os.MkdirAll(log.editlogDir(), 0o755); err != nil {
		return EditEvent{}, err
	}
	file, err := os.OpenFile(log.convPath(event.ConversationID), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return EditEvent{}, err
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		file.Close()
		return EditEvent{}, err
	}
	if err := file.Close(); err != nil {
		return EditEvent{}, err
	}

	log.byConv[event.ConversationID] = append(log.byConv[event.ConversationID], event)
	log.byFile[event.FilePath] = append(log.byFile[event.FilePath], event)
	return event, nil
}

// SetBlob 写入内容到 blob 并返回哈希（供 Record 前构造事件使用）。
func (log *EditLog) SetBlob(content []byte) (string, error) {
	if log == nil || log.blobs == nil {
		return "", errors.New("edit log blobs unavailable")
	}
	return log.blobs.Put(content)
}

// EventsByConversation 返回指定对话的全部事件（按 seq 升序）。
func (log *EditLog) EventsByConversation(conversationID string) []EditEvent {
	log.mu.RLock()
	defer log.mu.RUnlock()
	return append([]EditEvent(nil), log.byConv[conversationID]...)
}

// EventsByFile 返回指定文件（规范化路径）的全部事件（按 seq 升序）。
func (log *EditLog) EventsByFile(path string) []EditEvent {
	log.mu.RLock()
	defer log.mu.RUnlock()
	return append([]EditEvent(nil), log.byFile[NormalizePath(path)]...)
}

// AllEvents 返回全部事件（按 seq 升序）。
func (log *EditLog) AllEvents() []EditEvent {
	log.mu.RLock()
	defer log.mu.RUnlock()
	var all []EditEvent
	for _, events := range log.byConv {
		all = append(all, events...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Seq < all[j].Seq })
	return all
}

// TurnFileStateAt 计算指定对话在指定轮次结束时的文件状态：
// 重放该对话 TurnSeq <= target 的事件，每个文件取最后一次的 AfterHash。
// 返回 map[规范化路径]content（磁盘上的最终文本）。
// 若某文件在目标轮次时不存在（尚未创建或已被删除），不包含在结果中。
func (log *EditLog) TurnFileStateAt(conversationID string, targetTurnSeq int64) (map[string]string, error) {
	events := log.EventsByConversation(conversationID)
	// path → 最后的 after hash
	state := make(map[string]string)
	exists := make(map[string]bool)
	for _, event := range events {
		if event.TurnSeq > targetTurnSeq {
			continue
		}
		path := event.FilePath
		if event.Op == OpDelete {
			delete(state, path)
			exists[path] = false
			continue
		}
		if event.AfterHash == "" {
			continue
		}
		content := log.blobs.GetText(event.AfterHash)
		state[path] = content
		exists[path] = true
	}
	return state, nil
}

// TurnFileStates 返回该对话在所有轮次中编辑过的文件集合（规范化路径）。
func (log *EditLog) ConversationFiles(conversationID string) map[string]bool {
	events := log.EventsByConversation(conversationID)
	files := make(map[string]bool, len(events))
	for _, event := range events {
		files[event.FilePath] = true
	}
	return files
}

// TurnSummaries 聚合各对话各轮次的编辑摘要（按 (conversation, turn) 分组），
// 返回按编辑时间倒序的列表。
func (log *EditLog) TurnSummaries() []TurnSummary {
	events := log.AllEvents()
	type turnKey struct {
		conversationID string
		turnSeq        int64
	}
	order := make([]turnKey, 0, 32)
	group := make(map[turnKey]*TurnSummary)
	for _, event := range events {
		if event.Source == SourceUser {
			continue // 用户手改事件不构成 AI 轮次变更
		}
		key := turnKey{event.ConversationID, event.TurnSeq}
		summary, ok := group[key]
		if !ok {
			summary = &TurnSummary{
				ConversationID: event.ConversationID,
				TurnSeq:        event.TurnSeq,
				EditedAt:       event.CreatedAt,
			}
			group[key] = summary
			order = append(order, key)
		}
		if event.CreatedAt.Before(summary.EditedAt) {
			summary.EditedAt = event.CreatedAt
		}
		if summary.ModelName == "" && event.ModelName != "" {
			summary.ModelName = event.ModelName
			summary.ModelID = event.ModelID
		}
		summary.FileChanges = append(summary.FileChanges, TurnFileChange{
			FilePath: event.FilePath,
			Op:       event.Op,
		})
	}
	// 同轮内去重（同一文件多次编辑保留最终 op 与首次出现位置）
	for key := range group {
		summary := group[key]
		seen := make(map[string]int) // path → index in FileChanges
		deduped := make([]TurnFileChange, 0, len(summary.FileChanges))
		for _, change := range summary.FileChanges {
			if idx, ok := seen[change.FilePath]; ok {
				deduped[idx] = change
				continue
			}
			seen[change.FilePath] = len(deduped)
			deduped = append(deduped, change)
		}
		summary.FileChanges = deduped
	}
	sort.Slice(order, func(i, j int) bool {
		left := group[order[i]]
		right := group[order[j]]
		if !left.EditedAt.Equal(right.EditedAt) {
			return left.EditedAt.After(right.EditedAt)
		}
		return left.ConversationID < right.ConversationID
	})
	summaries := make([]TurnSummary, 0, len(order))
	for _, key := range order {
		summaries = append(summaries, *group[key])
	}
	return summaries
}

// MaxTurnSeq 返回指定对话的最大轮次号。
func (log *EditLog) MaxTurnSeq(conversationID string) int64 {
	events := log.EventsByConversation(conversationID)
	var max int64
	for _, event := range events {
		if event.TurnSeq > max {
			max = event.TurnSeq
		}
	}
	return max
}

// ProjectRoot 返回项目根路径。
func (log *EditLog) ProjectRoot() string {
	return log.projectRoot
}
