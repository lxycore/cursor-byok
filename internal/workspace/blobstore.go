package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// BlobStore 是内容寻址存储：以内容 sha256 为文件名的只读 blob 仓库。
// 相同内容只落盘一份；空内容（清空文件）也是合法 blob。
//
// 目录布局：
//
//	<root>/<sha256[0:2]>/<sha256>
//
// blob 文件内容即原始字节（无包装），文件名即内容哈希，天然防篡改。
type BlobStore struct {
	root string
	mu   sync.RWMutex
}

// NewBlobStore 创建内容寻址存储，root 为存储根目录。
func NewBlobStore(root string) *BlobStore {
	return &BlobStore{root: strings.TrimSpace(root)}
}

// blobPath 返回内容对应的存储路径。
func (store *BlobStore) blobPath(hash string) string {
	return filepath.Join(store.root, hash[:2], hash)
}

// Put 写入内容并返回其 sha256 哈希；已存在时直接返回哈希（幂等）。
func (store *BlobStore) Put(content []byte) (string, error) {
	hash := ContentHash(content)
	path := store.blobPath(hash)

	store.mu.Lock()
	defer store.mu.Unlock()

	if _, err := os.Stat(path); err == nil {
		return hash, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	// 原子写入：临时文件 + rename
	temp := path + ".tmp"
	if err := os.WriteFile(temp, content, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return "", err
	}
	return hash, nil
}

// Get 读取内容；不存在时返回 (nil, false)。
func (store *BlobStore) Get(hash string) ([]byte, bool) {
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return nil, false
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	data, err := os.ReadFile(store.blobPath(hash))
	if err != nil {
		return nil, false
	}
	return data, true
}

// GetText 读取内容为文本；不存在或为空时返回空串。
func (store *BlobStore) GetText(hash string) string {
	data, ok := store.Get(hash)
	if !ok {
		return ""
	}
	return string(data)
}

// Exists 判断指定哈希的 blob 是否存在。
func (store *BlobStore) Exists(hash string) bool {
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return false
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	info, err := os.Stat(store.blobPath(hash))
	return err == nil && info != nil && !info.IsDir()
}

// Remove 删除 blob（供 GC 使用，需由调用方确保不再被引用）。
func (store *BlobStore) Remove(hash string) error {
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return os.Remove(store.blobPath(hash))
}

// Root 返回存储根目录。
func (store *BlobStore) Root() string {
	return store.root
}

// ErrBlobMissing 表示引用的内容缺失。
var ErrBlobMissing = errors.New("blob content missing")
