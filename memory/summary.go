package memory

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"simple-agent-framework/model"
)

// SummaryCallback is invoked whenever SummaryMemory compresses old messages
// into a summary. It receives the number of messages that existed before
// summarization, the number kept in the tail after, and the freshly produced
// summary text. Useful for observability/logging.
type SummaryCallback func(before, after int, summary string)

type SummaryMemory struct {
	mu        sync.RWMutex
	messages  []model.ChatMessage
	summary   string
	model     model.ChatModel
	threshold int
	onSummary SummaryCallback
}

type SummaryOption func(*SummaryMemory)

func WithSummaryCallback(fn SummaryCallback) SummaryOption {
	return func(sm *SummaryMemory) { sm.onSummary = fn }
}

func NewSummary(m model.ChatModel, threshold int, opts ...SummaryOption) *SummaryMemory {
	sm := &SummaryMemory{model: m, threshold: threshold}
	for _, opt := range opts {
		opt(sm)
	}
	return sm
}

func (sm *SummaryMemory) Messages(ctx context.Context) ([]model.ChatMessage, error) {
	_ = ctx
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if sm.summary == "" {
		return slicesClone(sm.messages), nil
	}
	out := make([]model.ChatMessage, 0, 1+len(sm.messages))
	out = append(out, model.ChatMessage{
		Role:    model.RoleSystem,
		Content: sm.summary,
	})
	out = append(out, slicesClone(sm.messages)...)
	return out, nil
}

func (sm *SummaryMemory) Add(ctx context.Context, msgs ...model.ChatMessage) error {
	if len(msgs) == 0 {
		return nil
	}
	sm.mu.Lock()
	sm.messages = append(sm.messages, msgs...)
	sm.mu.Unlock()
	for sm.threshold > 0 {
		sm.mu.RLock()
		overflow := len(sm.messages) > sm.threshold
		sm.mu.RUnlock()
		if !overflow {
			break
		}
		if err := sm.summarize(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (sm *SummaryMemory) Clear(ctx context.Context) error {
	_ = ctx
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.messages = nil
	sm.summary = ""
	return nil
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

// summarizeAndSnapshot performs the locked summarization and returns the
// callback + snapshot info so the caller can invoke the callback *after* the
// lock is released (to avoid deadlock and preserve print ordering).
func (sm *SummaryMemory) summarizeAndSnapshot(ctx context.Context) (SummaryCallback, int, int, string, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if len(sm.messages) <= sm.threshold {
		return nil, 0, 0, "", nil
	}
	keepN := sm.threshold / 2
	if keepN < 1 {
		keepN = 1
	}
	if len(sm.messages) <= keepN {
		return nil, 0, 0, "", nil
	}
	before := len(sm.messages)
	head := sm.messages[:before-keepN]
	tail := sm.messages[before-keepN:]
	var b strings.Builder
	if sm.summary != "" {
		fmt.Fprintf(&b, "已有摘要:\n%s\n\n", sm.summary)
	}
	for _, msg := range head {
		fmt.Fprintf(&b, "[%s]: %s\n", msg.Role, msg.Content)
	}
	prompt := "请将以下对话摘要为简洁的上下文描述:\n\n" + b.String()
	resp, err := sm.model.Generate(ctx, []model.ChatMessage{
		{Role: model.RoleUser, Content: prompt},
	})
	if err != nil {
		return nil, 0, 0, "", err
	}
	sm.summary = resp.Message.Content
	sm.messages = slicesClone(tail)
	return sm.onSummary, before, len(sm.messages), sm.summary, nil
}

func slicesClone(s []model.ChatMessage) []model.ChatMessage {
	if s == nil {
		return nil
	}
	return append([]model.ChatMessage(nil), s...)
}
