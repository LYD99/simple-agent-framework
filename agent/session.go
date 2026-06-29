package agent

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"

	"github.com/LYD99/simple-agent-framework/interrupter"
	"github.com/LYD99/simple-agent-framework/memory"
	"github.com/LYD99/simple-agent-framework/model"
	"github.com/LYD99/simple-agent-framework/planner"
	"github.com/LYD99/simple-agent-framework/prompt"
)

type Session struct {
	id           string
	agent        *Agent
	memory       memory.Memory
	loopDetector *LoopDetector
	contentStore memory.ContentStore
	compressor   *memory.ContextCompressor
	checkpoint   *sessionCheckpointConfig
}

func (s *Session) ID() string { return s.id }

func (s *Session) Messages(ctx context.Context) ([]model.ChatMessage, error) {
	return s.memory.Messages(ctx)
}

func (s *Session) Run(ctx context.Context, input string) (*AgentResult, error) {
	a := s.agent
	a.mu.RLock()
	to := a.timeout
	a.mu.RUnlock()
	if to > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, to)
		defer cancel()
	}

	if err := s.memory.Add(ctx, model.ChatMessage{Role: model.RoleUser, Content: input}); err != nil {
		return nil, err
	}
	result, err := s.runLoop(ctx)
	if result != nil {
		result.SessionID = s.id
	}
	return result, err
}

func (s *Session) RunStream(ctx context.Context, input string) (*AgentResult, error) {
	ctx = context.WithValue(ctx, runStreamCtxKey{}, true)
	return s.Run(ctx, input)
}

func (a *Agent) Session(sessionID string, opts ...SessionOption) *Session {
	if v, ok := a.sessions.Load(sessionID); ok {
		return v.(*Session)
	}
	s := a.newSessionInternal(sessionID, opts...)
	actual, loaded := a.sessions.LoadOrStore(sessionID, s)
	if loaded {
		return actual.(*Session)
	}
	return s
}

func (a *Agent) NewSession(opts ...SessionOption) *Session {
	return a.Session(generateSessionID(), opts...)
}

func (a *Agent) newSessionInternal(id string, opts ...SessionOption) *Session {
	a.mu.RLock()
	mf := a.memoryFactory
	csf := a.contentStoreFactory
	threshold := a.loopDetectionThreshold
	mainModel := a.model
	var compressModel model.ChatModel
	var compressPrompt string
	compressRatio := a.maxContextRatio
	if a.compressConfig != nil {
		compressModel = a.compressConfig.Model
		compressPrompt = a.compressConfig.Prompt
		if a.compressConfig.MaxContextRatio > 0 {
			compressRatio = a.compressConfig.MaxContextRatio
		}
	}
	a.mu.RUnlock()

	s := &Session{
		id:           id,
		agent:        a,
		memory:       mf(id),
		loopDetector: NewLoopDetector(threshold),
		contentStore: csf(id),
		compressor:   memory.NewContextCompressor(mainModel, compressModel, compressPrompt, compressRatio),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Session) buildPlanState(ctx context.Context, history []planner.StepResult) (*planner.PlanState, error) {
	a := s.agent
	a.mu.RLock()
	pb := a.promptBuilder
	a.mu.RUnlock()

	if pb == nil {
		a.mu.Lock()
		a.rebuildPromptBuilder()
		pb = a.promptBuilder
		a.mu.Unlock()
	}

	sys := pb.Build(ctx)
	mem, err := s.memory.Messages(ctx)
	if err != nil {
		return nil, err
	}
	msgs := make([]model.ChatMessage, 0, 1+len(mem))
	msgs = append(msgs, model.ChatMessage{Role: model.RoleSystem, Content: sys})
	msgs = append(msgs, mem...)

	a.mu.RLock()
	tools := toolInfos(a.toolRegistry)
	a.mu.RUnlock()

	return &planner.PlanState{
		Messages: msgs,
		Tools:    tools,
		History:  history,
	}, nil
}

func buildPromptSummaries(a *Agent) ([]prompt.RuleSummary, []prompt.SkillSummary) {
	rules := a.ruleRegistry.List()
	ruleSums := make([]prompt.RuleSummary, 0, len(rules))
	for _, r := range rules {
		ruleSums = append(ruleSums, prompt.RuleSummary{
			Name:        r.Name(),
			Description: r.Description(),
			AlwaysApply: r.AlwaysApply(),
			Content:     r.Content(),
		})
	}
	skills := a.skillRegistry.List()
	skillSums := make([]prompt.SkillSummary, 0, len(skills))
	for _, sk := range skills {
		skillSums = append(skillSums, prompt.SkillSummary{
			Name:        sk.Name(),
			Description: sk.Description(),
			AlwaysApply: sk.AlwaysApply(),
			Content:     sk.Instruction(),
		})
	}
	return ruleSums, skillSums
}

func generateSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

type contentStoreCtxKey struct{}
type runStreamCtxKey struct{}

func contentStoreFromContext(ctx context.Context) memory.ContentStore {
	if cs, ok := ctx.Value(contentStoreCtxKey{}).(memory.ContentStore); ok {
		return cs
	}
	return nil
}

func runStreamFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(runStreamCtxKey{}).(bool)
	return v
}

func (s *Session) injectSessionContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, contentStoreCtxKey{}, s.contentStore)
}

// --- Checkpoint support ---

type sessionCheckpointConfig struct {
	store            interrupter.CheckpointStore
	autoSaveInterval time.Duration
	runID            string
	lastSaveTime     time.Time
}

func (s *Session) isCheckpointEnabled() bool {
	return s.checkpoint != nil && s.checkpoint.store != nil
}

func (s *Session) getCheckpointStore() interrupter.CheckpointStore {
	if s.checkpoint == nil {
		return nil
	}
	return s.checkpoint.store
}

func (s *Session) getRunID() string {
	if s.checkpoint == nil {
		return ""
	}
	return s.checkpoint.runID
}

func (s *Session) SaveCheckpoint(ctx context.Context, snapshot *interrupter.AgentSnapshot) error {
	if !s.isCheckpointEnabled() {
		return nil
	}

	store := s.getCheckpointStore()
	if store == nil {
		return nil
	}

	if snapshot == nil {
		return nil
	}

	if snapshot.RunID == "" {
		snapshot.RunID = s.getRunID()
	}

	return store.Save(ctx, snapshot.RunID, snapshot)
}

func (s *Session) LoadCheckpoint(ctx context.Context, runID string) (*interrupter.AgentSnapshot, error) {
	if !s.isCheckpointEnabled() {
		return nil, nil
	}

	store := s.getCheckpointStore()
	if store == nil {
		return nil, nil
	}

	return store.Load(ctx, runID)
}

func (s *Session) Suspend(ctx context.Context, snapshot *interrupter.AgentSnapshot) error {
	return s.SaveCheckpoint(ctx, snapshot)
}

func (s *Session) Resume(ctx context.Context, runID string) (*interrupter.AgentSnapshot, error) {
	return s.LoadCheckpoint(ctx, runID)
}

func WithCheckpoint(store interrupter.CheckpointStore, autoSaveInterval time.Duration) SessionOption {
	return func(s *Session) {
		s.checkpoint = &sessionCheckpointConfig{
			store:            store,
			autoSaveInterval: autoSaveInterval,
			runID:            generateRunID(),
		}
	}
}

func generateRunID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("run-%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
