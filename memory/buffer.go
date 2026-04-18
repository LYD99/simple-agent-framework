package memory

import (
	"context"

	"github.com/LYD99/simple-agent-framework/model"
)

// BufferMemory is a sliding-window semantic Memory. It does not hold any
// messages directly — all reads and writes are delegated to the underlying
// MessageStore data engine (in-memory / Redis / SQL / custom), so the same
// windowing policy works across any storage backend.
//
// When the number of stored messages exceeds maxSize, the oldest messages
// are trimmed from the head. Leading system messages are preserved in the
// Replace path of the in-memory engine for backward compatibility; engines
// that cannot preserve a "keep system" invariant may drop them — system
// prompts should generally be added by the prompt builder per-turn rather
// than stored in memory.
type BufferMemory struct {
	store   MessageStore
	maxSize int
}

// NewBuffer constructs a BufferMemory backed by the given MessageStore.
// If maxSize <= 0, no trimming is performed.
func NewBuffer(store MessageStore, maxSize int) *BufferMemory {
	if store == nil {
		store = NewInMemoryMessageStore()
	}
	return &BufferMemory{store: store, maxSize: maxSize}
}

var _ Memory = (*BufferMemory)(nil)

// Store returns the underlying MessageStore (useful for adapters).
func (m *BufferMemory) Store() MessageStore { return m.store }

func (m *BufferMemory) Messages(ctx context.Context) ([]model.ChatMessage, error) {
	return m.store.List(ctx)
}

func (m *BufferMemory) Add(ctx context.Context, msgs ...model.ChatMessage) error {
	if len(msgs) == 0 {
		return nil
	}
	if err := m.store.Append(ctx, msgs...); err != nil {
		return err
	}
	if m.maxSize <= 0 {
		return nil
	}
	n, err := m.store.Len(ctx)
	if err != nil {
		return err
	}
	if n <= m.maxSize {
		return nil
	}
	return m.trimPreservingSystem(ctx, n-m.maxSize)
}

func (m *BufferMemory) Clear(ctx context.Context) error {
	return m.store.Clear(ctx)
}

// trimPreservingSystem removes `drop` messages from the head while trying to
// keep any leading system messages intact. If the engine supports only
// blind TrimHead, we fall back to a list-rewrite (Replace) path.
func (m *BufferMemory) trimPreservingSystem(ctx context.Context, drop int) error {
	if drop <= 0 {
		return nil
	}
	msgs, err := m.store.List(ctx)
	if err != nil {
		return err
	}
	// Count leading system messages.
	sysHead := 0
	for sysHead < len(msgs) && msgs[sysHead].Role == model.RoleSystem {
		sysHead++
	}
	// Fast path: no leading system messages to preserve.
	if sysHead == 0 {
		return m.store.TrimHead(ctx, drop)
	}
	// Keep the system head, drop `drop` messages from the non-system tail.
	bodyStart := sysHead
	if drop >= len(msgs)-bodyStart {
		// Dropping everything non-system.
		return m.store.Replace(ctx, append([]model.ChatMessage(nil), msgs[:bodyStart]...))
	}
	kept := make([]model.ChatMessage, 0, len(msgs)-drop)
	kept = append(kept, msgs[:bodyStart]...)
	kept = append(kept, msgs[bodyStart+drop:]...)
	return m.store.Replace(ctx, kept)
}
