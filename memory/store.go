package memory

import (
	"context"

	"simple-agent-framework/model"
)

// MessageStore is the low-level data-engine abstraction for a session's
// message sequence. Memory implementations (Buffer / Summary / custom) are
// thin semantic wrappers on top of a MessageStore and MUST NOT hold the
// messages themselves — every read/write goes through the store.
//
// MessageStore is purely an internal composition detail of concrete Memory
// implementations. The Agent does NOT know about MessageStore; users simply
// construct the appropriate store inside their MemoryFactory closure (e.g.
// memory.NewBuffer(memory.NewRedisMessageStore(...), 100)).
//
// Each MessageStore instance is logically bound to a single sessionID. A
// single connection pool (Redis / SQL) may back many sessions through
// sessionID-scoped keys / tables — that scoping is the caller's concern.
//
// Implementations must be safe for concurrent use within a single session
// goroutine (the agent loop serializes per-session access).
type MessageStore interface {
	// Append adds one or more messages, preserving insertion order.
	Append(ctx context.Context, msgs ...model.ChatMessage) error

	// List returns all messages for this session in insertion order.
	List(ctx context.Context) ([]model.ChatMessage, error)

	// Replace atomically replaces the full message list. Used by summarization
	// and other whole-history rewrites.
	Replace(ctx context.Context, msgs []model.ChatMessage) error

	// Len returns the current message count. Engines MAY implement this as
	// O(1) (e.g. Redis LLEN) — semantic layers call it before sliding-window
	// trimming to avoid pulling the full list.
	Len(ctx context.Context) (int, error)

	// TrimHead removes the first n messages (FIFO). Engines MAY optimize this
	// (e.g. Redis LTRIM). If n >= Len, the store becomes empty.
	TrimHead(ctx context.Context, n int) error

	// Clear removes all messages for this session.
	Clear(ctx context.Context) error

	// Close releases any engine-specific resources (connections, file handles).
	// InMemory implementations may no-op.
	Close() error
}
