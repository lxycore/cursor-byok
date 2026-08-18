package workspace

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// EventListener 订阅项目事件的回调。
type EventListener func(ProjectEvent)

// ProjectEvents 是项目级协调事件流（append-only jsonl + 内存尾部缓存）。
type ProjectEvents struct {
	path string

	mu       sync.RWMutex
	seq      int64
	recent   []ProjectEvent // 内存缓存最近事件（上限 recentLimit）
	listener EventListener  // 供 Wails 事件桥推送
}

const recentLimit = 500

// NewProjectEvents 创建事件流。
func NewProjectEvents(dir string) (*ProjectEvents, error) {
	events := &ProjectEvents{
		path: filepath.Join(dir, "events.jsonl"),
	}
	if err := events.load(); err != nil {
		return nil, err
	}
	return events, nil
}

func (events *ProjectEvents) load() error {
	file, err := os.Open(events.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	var lastSeq int64
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event ProjectEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if event.Seq > lastSeq {
			lastSeq = event.Seq
		}
		if len(events.recent) >= recentLimit {
			events.recent = events.recent[1:]
		}
		events.recent = append(events.recent, event)
	}
	events.seq = lastSeq
	return scanner.Err()
}

// Emit 写入并广播一条事件，返回带 seq 的事件。
func (events *ProjectEvents) Emit(event ProjectEvent) (ProjectEvent, error) {
	if events == nil {
		return ProjectEvent{}, errors.New("project events nil")
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	events.mu.Lock()
	events.seq++
	event.Seq = events.seq
	line, err := json.Marshal(event)
	if err != nil {
		events.mu.Unlock()
		return ProjectEvent{}, err
	}
	if err := os.MkdirAll(filepath.Dir(events.path), 0o755); err != nil {
		events.mu.Unlock()
		return ProjectEvent{}, err
	}
	file, err := os.OpenFile(events.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		events.mu.Unlock()
		return ProjectEvent{}, err
	}
	_, writeErr := file.Write(append(line, '\n'))
	closeErr := file.Close()
	if writeErr != nil {
		events.mu.Unlock()
		return ProjectEvent{}, writeErr
	}
	if closeErr != nil {
		events.mu.Unlock()
		return ProjectEvent{}, closeErr
	}
	if len(events.recent) >= recentLimit {
		events.recent = events.recent[1:]
	}
	events.recent = append(events.recent, event)
	listener := events.listener
	events.mu.Unlock()

	if listener != nil {
		listener(event)
	}
	return event, nil
}

// SetListener 设置事件监听器（Wails 推送用）。
func (events *ProjectEvents) SetListener(listener EventListener) {
	events.mu.Lock()
	defer events.mu.Unlock()
	events.listener = listener
}

// Recent 返回最近事件（按 seq 升序）。
func (events *ProjectEvents) Recent(limit int) []ProjectEvent {
	events.mu.RLock()
	defer events.mu.RUnlock()
	if limit <= 0 || limit > len(events.recent) {
		limit = len(events.recent)
	}
	if limit >= len(events.recent) {
		return append([]ProjectEvent(nil), events.recent...)
	}
	return append([]ProjectEvent(nil), events.recent[len(events.recent)-limit:]...)
}

// Since 返回 seq 之后的事件（供前端增量拉取）。
func (events *ProjectEvents) Since(seq int64) []ProjectEvent {
	events.mu.RLock()
	defer events.mu.RUnlock()
	var result []ProjectEvent
	for _, event := range events.recent {
		if event.Seq > seq {
			result = append(result, event)
		}
	}
	return result
}
