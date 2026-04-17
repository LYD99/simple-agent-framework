package hook

import (
	"context"
	"time"
)

type EventType int

const (
	EventPlanStart EventType = iota
	EventPlanDone
	EventToolCallStart
	EventToolCallDone
	EventEvalStart
	EventEvalDone
	EventLoopComplete
	EventError
	EventStreamChunk
	EventSkillContextLog
	EventRuleView      // 渐进式披露：模型按需加载 rule 完整内容
	EventSkillCallStart // 模型调用 skill_call 开始
	EventSkillCallDone  // skill_call 执行完成
)

type Event struct {
	Type      EventType
	Payload   any
	Timestamp time.Time
}

type Hook interface {
	OnEvent(ctx context.Context, event Event) error
}

type HookManager struct {
	hooks []Hook
}

func NewHookManager() *HookManager {
	return &HookManager{hooks: make([]Hook, 0)}
}

func (m *HookManager) Add(h Hook) {
	if h == nil {
		return
	}
	m.hooks = append(m.hooks, h)
}

func (m *HookManager) Emit(ctx context.Context, event Event) error {
	for _, h := range m.hooks {
		if err := h.OnEvent(ctx, event); err != nil {
			return err
		}
	}
	return nil
}
