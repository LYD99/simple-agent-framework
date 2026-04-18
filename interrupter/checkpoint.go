package interrupter

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/LYD99/simple-agent-framework/model"
	"github.com/LYD99/simple-agent-framework/planner"
)

// AgentSnapshot Agent 运行快照
type AgentSnapshot struct {
	RunID         string               `json:"run_id"`
	Iteration     int                  `json:"iteration"`
	Messages      []model.ChatMessage  `json:"messages"`
	PendingAction *planner.Action      `json:"pending_action,omitempty"`
	TokensUsed    int                  `json:"tokens_used"`
	StepResults   []planner.StepResult `json:"step_results"`
}

// Serialize 序列化快照为 JSON
func (s *AgentSnapshot) Serialize() ([]byte, error) {
	return json.Marshal(s)
}

// Deserialize 从 JSON 恢复快照
func Deserialize(data []byte) (*AgentSnapshot, error) {
	var s AgentSnapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// CheckpointStore 检查点存储接口
type CheckpointStore interface {
	Save(ctx context.Context, runID string, snapshot *AgentSnapshot) error
	Load(ctx context.Context, runID string) (*AgentSnapshot, error)
	Delete(ctx context.Context, runID string) error
}

// MemoryStore 内存检查点存储（开发/测试用）
type MemoryStore struct {
	data map[string][]byte
	mu   sync.RWMutex
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		data: make(map[string][]byte),
	}
}

func (s *MemoryStore) Save(ctx context.Context, runID string, snapshot *AgentSnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshot == nil {
		return nil
	}
	b, err := snapshot.Serialize()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[runID] = b
	return nil
}

func (s *MemoryStore) Load(ctx context.Context, runID string) (*AgentSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	b, ok := s.data[runID]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("checkpoint: not found for run_id %q", runID)
	}
	return Deserialize(b)
}

func (s *MemoryStore) Delete(ctx context.Context, runID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, runID)
	return nil
}
