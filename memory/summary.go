package memory

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/LYD99/simple-agent-framework/model"
)

// SummaryCallback is invoked whenever SummaryMemory compresses old messages
// into a summary. It receives the number of messages that existed before
// summarization, the number kept in the tail after, and the freshly produced
// summary text. Useful for observability/logging.
type SummaryCallback func(before, after int, summary string)

// summaryPromptTemplate is the default English prompt used by SummaryMemory.
const summaryPromptTemplate = "Please compress the following conversation into a concise context description, preserving user goals, key findings, completed steps, and outstanding questions:\n\n%s"

// SummaryMemory is a semantic Memory that summarizes the head of the history
// once a message-count threshold is crossed. It delegates all message storage
// to an underlying MessageStore data engine — the summary itself is kept in
// an in-process field (summaries are small and regenerated as needed).
type SummaryMemory struct {
	mu        sync.Mutex
	store     MessageStore
	summary   string
	model     model.ChatModel
	threshold int
	onSummary SummaryCallback
}

type SummaryOption func(*SummaryMemory)

func WithSummaryCallback(fn SummaryCallback) SummaryOption {
	return func(sm *SummaryMemory) { sm.onSummary = fn }
}

// NewSummary constructs a SummaryMemory backed by the given MessageStore.
// When the stored message count exceeds `threshold`, the oldest half is
// summarized via `m` and the store is rewritten (Replace) to keep only the
// recent tail; the summary is prepended as a system message on reads.
func NewSummary(store MessageStore, m model.ChatModel, threshold int, opts ...SummaryOption) *SummaryMemory {
	if store == nil {
		store = NewInMemoryMessageStore()
	}
	sm := &SummaryMemory{store: store, model: m, threshold: threshold}
	for _, opt := range opts {
		opt(sm)
	}
	return sm
}

var _ Memory = (*SummaryMemory)(nil)

// Store returns the underlying MessageStore.
func (sm *SummaryMemory) Store() MessageStore { return sm.store }

func (sm *SummaryMemory) Messages(ctx context.Context) ([]model.ChatMessage, error) {
	raw, err := sm.store.List(ctx)
	if err != nil {
		return nil, err
	}
	sm.mu.Lock()
	summary := sm.summary
	sm.mu.Unlock()
	if summary == "" {
		return raw, nil
	}
	out := make([]model.ChatMessage, 0, 1+len(raw))
	out = append(out, model.ChatMessage{
		Role:    model.RoleSystem,
		Content: summary,
	})
	out = append(out, raw...)
	return out, nil
}

func (sm *SummaryMemory) Add(ctx context.Context, msgs ...model.ChatMessage) error {
	if len(msgs) == 0 {
		return nil
	}
	if err := sm.store.Append(ctx, msgs...); err != nil {
		return err
	}
	for sm.threshold > 0 {
		n, err := sm.store.Len(ctx)
		if err != nil {
			return err
		}
		if n <= sm.threshold {
			return nil
		}
		if err := sm.summarize(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (sm *SummaryMemory) Clear(ctx context.Context) error {
	sm.mu.Lock()
	sm.summary = ""
	sm.mu.Unlock()
	return sm.store.Clear(ctx)
}

func (sm *SummaryMemory) summarize(ctx context.Context) error {
	cb, before, after, summary, err := sm.summarizeAndSnapshot(ctx)
	if err != nil {
		return err
	}
	if cb != nil {
		cb(before, after, summary)
	}
	return nil
}

// summarizeAndSnapshot performs the summarization, persists the new tail
// via the underlying store, and returns the callback + snapshot info so the
// caller can invoke the callback *after* the lock is released.
func (sm *SummaryMemory) summarizeAndSnapshot(ctx context.Context) (SummaryCallback, int, int, string, error) {
	msgs, err := sm.store.List(ctx)
	if err != nil {
		return nil, 0, 0, "", err
	}
	if len(msgs) <= sm.threshold {
		return nil, 0, 0, "", nil
	}
	keepN := sm.threshold / 2
	if keepN < 1 {
		keepN = 1
	}
	if len(msgs) <= keepN {
		return nil, 0, 0, "", nil
	}

	before := len(msgs)
	head := msgs[:before-keepN]
	tail := msgs[before-keepN:]

	sm.mu.Lock()
	existing := sm.summary
	sm.mu.Unlock()

	var body strings.Builder
	if existing != "" {
		fmt.Fprintf(&body, "Existing summary:\n%s\n\n", existing)
	}
	for _, msg := range head {
		fmt.Fprintf(&body, "[%s]: %s\n", msg.Role, msg.Content)
	}
	prompt := fmt.Sprintf(summaryPromptTemplate, body.String())
	resp, err := sm.model.Generate(ctx, []model.ChatMessage{
		{Role: model.RoleUser, Content: prompt},
	})
	if err != nil {
		return nil, 0, 0, "", err
	}

	newSummary := resp.Message.Content
	sm.mu.Lock()
	sm.summary = newSummary
	cb := sm.onSummary
	sm.mu.Unlock()

	if err := sm.store.Replace(ctx, tail); err != nil {
		return nil, 0, 0, "", err
	}
	return cb, before, len(tail), newSummary, nil
}
