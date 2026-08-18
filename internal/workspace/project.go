package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Project 聚合单个项目的全部工作区能力：编辑日志、内容存储、事件流与写协调器。
type Project struct {
	Root  string // 规范化项目根（磁盘路径）
	Hash  string // 稳定 hash（目录命名）
	Dir   string // data/workspace/projects/<hash>/
	Name  string // 展示名（最后一级目录名）

	Blobs       *BlobStore
	Log         *EditLog
	Events      *ProjectEvents
	Coordinator *Coordinator
}

// Manager 管理所有项目与"对话 → 项目"绑定，是 workspace 的入口。
type Manager struct {
	root string // data/workspace/

	mu       sync.RWMutex
	projects map[string]*Project          // hash → Project
	bindings map[string]string            // conversationID → hash
	onEvent  func(*Project, ProjectEvent) // 全局事件回调（Wails 推送）
}

// NewManager 创建 workspace 管理器，root 为数据根目录（如 data/workspace）。
// 创建时扫描磁盘上的既有项目（应用重启后恢复版本历史）。
func NewManager(root string) *Manager {
	manager := &Manager{
		root:     strings.TrimSpace(root),
		projects: make(map[string]*Project),
		bindings: make(map[string]string),
	}
	_ = manager.loadProjects()
	return manager
}

// loadProjects 扫描磁盘 projects/ 目录，恢复已存在的项目到内存。
// 项目根从 project.json 读取（旧版本数据回退读 index.json 兼容迁移）。
func (manager *Manager) loadProjects() error {
	if manager == nil || strings.TrimSpace(manager.root) == "" {
		return nil
	}
	projectsDir := filepath.Join(manager.root, "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		hash := strings.TrimSpace(entry.Name())
		if hash == "" || len(hash) != 16 {
			continue
		}
		projectRoot := readProjectRoot(filepath.Join(projectsDir, hash))
		if projectRoot == "" {
			continue
		}
		if _, err := manager.ensureProject(projectRoot); err != nil {
			continue
		}
	}
	return nil
}

// readProjectRoot 从项目目录反查项目根：优先 project.json（新格式），
// 缺失时回退 index.json（旧版本数据，含 projectPath 字段）。
func readProjectRoot(projectDir string) string {
	data, err := os.ReadFile(filepath.Join(projectDir, "project.json"))
	if err == nil {
		var meta struct {
			ProjectRoot string `json:"projectRoot"`
		}
		if json.Unmarshal(data, &meta) == nil && strings.TrimSpace(meta.ProjectRoot) != "" {
			return NormalizePath(strings.TrimSpace(meta.ProjectRoot))
		}
	}
	data, err = os.ReadFile(filepath.Join(projectDir, "index.json"))
	if err == nil {
		var idx struct {
			ProjectPath string `json:"projectPath"`
		}
		if json.Unmarshal(data, &idx) == nil {
			return NormalizePath(strings.TrimSpace(idx.ProjectPath))
		}
	}
	return ""
}

// SetEventListener 设置全局事件监听（用于 Wails 推送，透传给各项目）。
func (manager *Manager) SetEventListener(listener func(*Project, ProjectEvent)) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.onEvent = listener
}

// BindConversation 把对话绑定到项目根，返回对应 Project（幂等）。
// projectRoot 为空时不建立绑定，返回 nil。
func (manager *Manager) BindConversation(conversationID string, projectRoot string) *Project {
	conversationID = strings.TrimSpace(conversationID)
	projectRoot = NormalizePath(strings.TrimSpace(projectRoot))
	if conversationID == "" || projectRoot == "" {
		return nil
	}

	manager.mu.RLock()
	if hash, ok := manager.bindings[conversationID]; ok {
		if project, ok2 := manager.projects[hash]; ok2 {
			manager.mu.RUnlock()
			return project
		}
	}
	manager.mu.RUnlock()

	project, err := manager.ensureProject(projectRoot)
	if err != nil {
		return nil
	}
	manager.mu.Lock()
	manager.bindings[conversationID] = project.Hash
	manager.mu.Unlock()
	return project
}

// ProjectForConversation 返回对话绑定的项目；未绑定时（如应用重启后
// 的历史对话）扫描所有项目，按 editlog 中是否存在该对话的事件定位。
func (manager *Manager) ProjectForConversation(conversationID string) *Project {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil
	}
	manager.mu.RLock()
	hash, ok := manager.bindings[conversationID]
	manager.mu.RUnlock()
	if ok {
		manager.mu.RLock()
		project := manager.projects[hash]
		manager.mu.RUnlock()
		if project != nil {
			return project
		}
	}
	// fallback：扫描所有项目，按 editlog 事件定位（历史对话无绑定）
	manager.mu.RLock()
	projects := make([]*Project, 0, len(manager.projects))
	for _, project := range manager.projects {
		projects = append(projects, project)
	}
	manager.mu.RUnlock()
	for _, project := range projects {
		if project.Log == nil {
			continue
		}
		if len(project.Log.EventsByConversation(conversationID)) > 0 {
			// 补上绑定，后续查询走快路径
			manager.mu.Lock()
			manager.bindings[conversationID] = project.Hash
			manager.mu.Unlock()
			return project
		}
	}
	return nil
}

// GetProject 按项目根返回项目（不存在则创建）。
func (manager *Manager) GetProject(projectRoot string) *Project {
	projectRoot = NormalizePath(strings.TrimSpace(projectRoot))
	if projectRoot == "" {
		return nil
	}
	project, err := manager.ensureProject(projectRoot)
	if err != nil {
		return nil
	}
	return project
}

// ensureProject 创建或加载项目。
func (manager *Manager) ensureProject(projectRoot string) (*Project, error) {
	hash := ProjectHash(projectRoot)

	manager.mu.RLock()
	project, ok := manager.projects[hash]
	manager.mu.RUnlock()
	if ok {
		return project, nil
	}

	manager.mu.Lock()
	defer manager.mu.Unlock()
	// 双重检查
	if project, ok = manager.projects[hash]; ok {
		return project, nil
	}

	dir := filepath.Join(manager.root, "projects", hash)
	blobsRoot := filepath.Join(manager.root, "blobs")
	blobs := NewBlobStore(blobsRoot)
	log, err := NewEditLog(dir, projectRoot, blobs)
	if err != nil {
		return nil, err
	}
	events, err := NewProjectEvents(dir)
	if err != nil {
		return nil, err
	}
	project = &Project{
		Root:  projectRoot,
		Hash:  hash,
		Dir:   dir,
		Name:  ProjectDisplayName(projectRoot),
		Blobs: blobs,
		Log:   log,
		Events: events,
	}
	project.Coordinator = NewCoordinator(project)
	// 事件监听：全局回调
	events.SetListener(func(event ProjectEvent) {
		if manager.onEvent != nil {
			manager.onEvent(project, event)
		}
	})
	// 持久化项目根（loadProjects 反查用；老数据已由 index.json 回退迁移）
	meta := map[string]string{"projectRoot": projectRoot}
	if data, err := json.Marshal(meta); err == nil {
		_ = os.WriteFile(filepath.Join(dir, "project.json"), data, 0o644)
	}
	manager.projects[hash] = project
	return project, nil
}

// Projects 返回全部项目（按名称排序）。
func (manager *Manager) Projects() []*Project {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	result := make([]*Project, 0, len(manager.projects))
	for _, project := range manager.projects {
		result = append(result, project)
	}
	// 稳定顺序
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if result[j].Name < result[i].Name {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	return result
}

// Root 返回 workspace 数据根目录。
func (manager *Manager) Root() string {
	return manager.root
}

// EnsureRoot 确保数据根目录存在。
func (manager *Manager) EnsureRoot() error {
	if manager == nil {
		return fmt.Errorf("workspace manager is nil")
	}
	return os.MkdirAll(manager.root, 0o755)
}
