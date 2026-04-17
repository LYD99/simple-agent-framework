package memory

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"sync"
)

type ContentStore interface {
	Store(ctx context.Context, key string, content string) error
	Load(ctx context.Context, key string) (string, error)
}

type InMemoryContentStore struct {
	m sync.Map
}

func NewInMemoryContentStore() *InMemoryContentStore {
	return &InMemoryContentStore{}
}

func (s *InMemoryContentStore) Store(ctx context.Context, key string, content string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.m.Store(key, content)
	return nil
}

func (s *InMemoryContentStore) Load(ctx context.Context, key string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	v, ok := s.m.Load(key)
	if !ok {
		return "", os.ErrNotExist
	}
	return v.(string), nil
}

type FileContentStore struct {
	dir string
}

func NewFileContentStore(dir string) (*FileContentStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	return &FileContentStore{dir: abs}, nil
}

func (f *FileContentStore) pathForKey(key string) string {
	name := base64.RawURLEncoding.EncodeToString([]byte(key)) + ".txt"
	return filepath.Join(f.dir, name)
}

func (f *FileContentStore) Store(ctx context.Context, key string, content string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path := f.pathForKey(key)
	return os.WriteFile(path, []byte(content), 0o644)
}

func (f *FileContentStore) Load(ctx context.Context, key string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	path := f.pathForKey(key)
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
