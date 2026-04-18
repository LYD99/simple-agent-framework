package memory

import (
	"context"
	"slices"
	"sync"

	"simple-agent-framework/model"
)

// InMemoryMessageStore is the default zero-dependency MessageStore backed by
// a mutex-guarded slice. Messages are lost on process exit; use only for
// local/test scenarios or as a fallback when no persistent engine is
// configured. For production, construct a Redis/SQL/custom MessageStore
// inside your MemoryFactory closure and pass it to memory.NewBuffer /
// memory.NewSummary.
type InMemoryMessageStore struct {
	mu       sync.Mutex
	messages []model.ChatMessage
}

// NewInMemoryMessageStore returns an empty in-process MessageStore.
func NewInMemoryMessageStore() *InMemoryMessageStore {
	return &InMemoryMessageStore{}
}

var _ MessageStore = (*InMemoryMessageStore)(nil)

func (s *InMemoryMessageStore) Append(ctx context.Context, msgs ...model.ChatMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(msgs) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, msgs...)
	return nil
}

func (s *InMemoryMessageStore) List(ctx context.Context) ([]model.ChatMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.messages), nil
}

func (s *InMemoryMessageStore) Replace(ctx context.Context, msgs []model.ChatMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if msgs == nil {
		s.messages = nil
		return nil
	}
	s.messages = slices.Clone(msgs)
	return nil
}

func (s *InMemoryMessageStore) Len(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.messages), nil
}

func (s *InMemoryMessageStore) TrimHead(ctx context.Context, n int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if n <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if n >= len(s.messages) {
		s.messages = s.messages[:0]
		return nil
	}
	s.messages = append(s.messages[:0:0], s.messages[n:]...)
	return nil
}

func (s *InMemoryMessageStore) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = nil
	return nil
}

func (s *InMemoryMessageStore) Close() error { return nil }
