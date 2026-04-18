package memory

import (
	"context"
	"slices"
	"sync"

	"github.com/LYD99/simple-agent-framework/model"
)

type BufferMemory struct {
	mu       sync.RWMutex
	messages []model.ChatMessage
	maxSize  int
}

func NewBuffer(maxSize int) *BufferMemory {
	return &BufferMemory{maxSize: maxSize}
}

func (m *BufferMemory) Messages(ctx context.Context) ([]model.ChatMessage, error) {
	_ = ctx
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := slices.Clone(m.messages)
	return out, nil
}

func (m *BufferMemory) Add(ctx context.Context, msgs ...model.ChatMessage) error {
	_ = ctx
	if len(msgs) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, msgs...)
	m.trimLocked()
	return nil
}

func (m *BufferMemory) Clear(ctx context.Context) error {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = nil
	return nil
}

func (m *BufferMemory) trimLocked() {
	if m.maxSize <= 0 || len(m.messages) <= m.maxSize {
		return
	}
	for len(m.messages) > m.maxSize {
		if len(m.messages) > 0 && m.messages[0].Role == model.RoleSystem {
			idx := -1
			for i := 1; i < len(m.messages); i++ {
				if m.messages[i].Role != model.RoleSystem {
					idx = i
					break
				}
			}
			if idx >= 0 {
				m.messages = append(m.messages[:idx], m.messages[idx+1:]...)
			} else if len(m.messages) > 1 {
				m.messages = append(m.messages[:1], m.messages[2:]...)
			} else {
				m.messages = m.messages[:0]
			}
		} else {
			m.messages = m.messages[1:]
		}
	}
}
