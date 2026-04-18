package agent

import (
	"context"
	stderrors "errors"
	"fmt"
	"sync"
	"time"

	agenterrs "github.com/LYD99/simple-agent-framework/errors"
	"github.com/LYD99/simple-agent-framework/evaluator"
	"github.com/LYD99/simple-agent-framework/executor"
	"github.com/LYD99/simple-agent-framework/hook"
	"github.com/LYD99/simple-agent-framework/hook/outputhook"
	"github.com/LYD99/simple-agent-framework/interrupter"
	"github.com/LYD99/simple-agent-framework/memory"
	"github.com/LYD99/simple-agent-framework/model"
	"github.com/LYD99/simple-agent-framework/planner"
	"github.com/LYD99/simple-agent-framework/prompt"
	"github.com/LYD99/simple-agent-framework/rule"
	"github.com/LYD99/simple-agent-framework/skill"
	"github.com/LYD99/simple-agent-framework/tool"
	"github.com/LYD99/simple-agent-framework/tool/builtin"
)

const DefaultModelMaxTokens = 128000

// Agent is the shared, application-level singleton.
// All per-session mutable state (memory, loop detection, content store, compressor)
// is held by Session; Agent only stores shared config and components.
type Agent struct {
	mu sync.RWMutex

	// --- shared components (application-level, read-only after init) ---
	name      string
	model     model.ChatModel
	planner   planner.Planner
	executor  executor.Executor
	evaluator evaluator.Evaluator

	reactPlanner        planner.Planner
	planAndSolvePlanner *planner.PlanAndSolvePlanner

	toolRegistry  *tool.ToolRegistry
	interrupter   interrupter.Interrupter
	hookManager   *hook.HookManager
	ruleRegistry  *rule.RuleRegistry
	skillRegistry *skill.SkillRegistry

	promptBuilder *prompt.PromptBuilder
	systemPrompt  string

	currentMode   planner.ExecutionMode
	evalEnabled   bool
	maxIterations int
	timeout       time.Duration
	streamEnabled bool

	outputSchema any
	autoRetry    int

	builtinRuleViewRegistered        bool
	builtinSkillCallRegistered       bool
	builtinFetchFullResultRegistered bool

	// --- shared config (determines Session defaults) ---
	loopDetectionThreshold int
	toolResultMaxLen       int
	recentToolResultTokens int
	compressConfig         *CompressAgentConfig
	maxContextRatio        float64
	modelMaxTokens         int

	// --- session-level resource factories ---
	memoryFactory       MemoryFactory
	contentStoreFactory ContentStoreFactory

	// --- session management ---
	sessions sync.Map // map[string]*Session
}

type AgentResult struct {
	SessionID string
	Answer    string
	Messages  []model.ChatMessage
	Usage     model.Usage
	Error     error
	Duration  time.Duration
}

func New(opts ...AgentOption) *Agent {
	a := &Agent{
		toolRegistry:  tool.NewToolRegistry(),
		ruleRegistry:  rule.NewRegistry(),
		skillRegistry: skill.NewRegistry(),
		hookManager:   hook.NewHookManager(),
		maxIterations: 10,
		timeout:       5 * time.Minute,
		currentMode:   planner.ModeReAct,

		loopDetectionThreshold: DefaultLoopThreshold,
		toolResultMaxLen:       memory.DefaultToolResultMaxLen,
		recentToolResultTokens: memory.DefaultRecentToolResultTokens,
		maxContextRatio:        0.9,
		modelMaxTokens:         DefaultModelMaxTokens,
	}
	for _, opt := range opts {
		opt(a)
	}
	a.init()
	return a
}

func (a *Agent) init() {
	if a.memoryFactory == nil {
		a.memoryFactory = func(_ string) memory.Memory {
			return memory.NewBuffer(100)
		}
	}
	if a.contentStoreFactory == nil {
		a.contentStoreFactory = func(_ string) memory.ContentStore {
			return memory.NewInMemoryContentStore()
		}
	}

	if a.model != nil {
		if a.reactPlanner == nil {
			a.reactPlanner = planner.NewReAct(a.model, planner.WithSystemPrompt(a.systemPrompt))
		}
		if a.planAndSolvePlanner == nil {
			a.planAndSolvePlanner = planner.NewPlanAndSolve(a.model)
		}
	}
	if a.executor == nil {
		a.executor = executor.NewParallel(a.toolRegistry, 8)
	}
	if a.evaluator != nil {
		a.evalEnabled = true
	}
	a.rebuildPromptBuilder()
	a.ensureBuiltinToolsUnlocked()
	a.syncPlannerRef()
}

func (a *Agent) syncPlannerRef() {
	if a.currentMode == planner.ModePlanAndSolve {
		a.planner = a.planAndSolvePlanner
	} else {
		a.planner = a.reactPlanner
	}
}

func (a *Agent) rebuildPromptBuilder() {
	base := a.systemPrompt
	if base == "" {
		base = a.defaultSystemPrompt()
	}
	ruleSums, skillSums := buildPromptSummaries(a)
	a.promptBuilder = prompt.NewBuilder(base).WithRules(ruleSums).WithSkills(skillSums)
}

// defaultSystemPrompt returns the mode-appropriate default when no custom prompt is set.
func (a *Agent) defaultSystemPrompt() string {
	if a.currentMode == planner.ModePlanAndSolve {
		return prompt.DefaultPlanAndSolveSystemPrompt
	}
	return prompt.DefaultReActSystemPrompt
}

func (a *Agent) ensureBuiltinTools() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ensureBuiltinToolsUnlocked()
}

func (a *Agent) emit(ctx context.Context, typ hook.EventType, payload any) error {
	return a.hookManager.Emit(ctx, hook.Event{
		Type:      typ,
		Payload:   payload,
		Timestamp: time.Now(),
	})
}

func toolInfos(reg *tool.ToolRegistry) []planner.ToolInfo {
	ts := reg.Tools()
	out := make([]planner.ToolInfo, 0, len(ts))
	for _, t := range ts {
		var sch any
		if t.Schema() != nil {
			sch = t.Schema()
		} else {
			sch = map[string]any{}
		}
		out = append(out, planner.ToolInfo{
			Name:        t.Name(),
			Description: t.Description(),
			Schema:      sch,
		})
	}
	return out
}

// Run executes the agent loop. If sessionID is provided, the session is reused
// (preserving conversation history); otherwise a new session is created.
// The returned AgentResult.SessionID always contains the (possibly generated) session ID.
func (a *Agent) Run(ctx context.Context, input string, sessionID ...string) (*AgentResult, error) {
	return a.resolveSession(sessionID...).Run(ctx, input)
}

// RunStream is the streaming variant of Run with the same session semantics.
func (a *Agent) RunStream(ctx context.Context, input string, sessionID ...string) (*AgentResult, error) {
	return a.resolveSession(sessionID...).RunStream(ctx, input)
}

func (a *Agent) resolveSession(sessionID ...string) *Session {
	if len(sessionID) > 0 && sessionID[0] != "" {
		return a.Session(sessionID[0])
	}
	return a.NewSession()
}

// ShowSystemPrompt returns the fully assembled system prompt, including
// the base prompt plus any injected rule and skill sections/catalogs.
func (a *Agent) ShowSystemPrompt(ctx context.Context) string {
	a.mu.RLock()
	pb := a.promptBuilder
	a.mu.RUnlock()

	if pb == nil {
		a.mu.Lock()
		a.rebuildPromptBuilder()
		pb = a.promptBuilder
		a.mu.Unlock()
	}
	if pb == nil {
		return ""
	}
	return pb.Build(ctx)
}

func (a *Agent) SetExecutionMode(mode planner.ExecutionMode) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.currentMode = mode
	a.syncPlannerRef()
	if a.systemPrompt == "" {
		a.rebuildPromptBuilder()
	}
}

func (a *Agent) EnableEvaluator(ev evaluator.Evaluator) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.evaluator = ev
	a.evalEnabled = ev != nil
}

func (a *Agent) DisableEvaluator() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.evaluator = nil
	a.evalEnabled = false
}

func (a *Agent) AddRule(r rule.Rule) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ruleRegistry.Add(r)
	a.rebuildPromptBuilder()
	a.ensureBuiltinToolsUnlocked()
}

func (a *Agent) RemoveRule(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ruleRegistry.Remove(name)
	a.rebuildPromptBuilder()
}

func (a *Agent) AddSkill(s skill.Skill) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.skillRegistry.Add(s)
	a.rebuildPromptBuilder()
	a.ensureBuiltinToolsUnlocked()
}

func (a *Agent) RemoveSkill(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.skillRegistry.Remove(name)
	a.rebuildPromptBuilder()
}

func (a *Agent) ensureBuiltinToolsUnlocked() {
	if len(a.ruleRegistry.List()) > 0 && !a.builtinRuleViewRegistered {
		a.toolRegistry.AddTool(builtin.NewRuleViewTool(func(name string) (string, error) {
			r, ok := a.ruleRegistry.Get(name)
			if !ok {
				return "", fmt.Errorf("rule not found: %s", name)
			}
			_ = a.hookManager.Emit(context.Background(), hook.Event{
				Type:      hook.EventRuleView,
				Payload:   name,
				Timestamp: time.Now(),
			})
			return r.Content(), nil
		}))
		a.builtinRuleViewRegistered = true
	}
	if len(a.skillRegistry.List()) > 0 && !a.builtinSkillCallRegistered {
		a.toolRegistry.AddTool(builtin.NewSkillCallTool(func(ctx context.Context, name, userInput string) (string, error) {
			s, ok := a.skillRegistry.Get(name)
			if !ok {
				return "", fmt.Errorf("skill not found: %s", name)
			}
			_ = a.hookManager.Emit(ctx, hook.Event{
				Type:      hook.EventSkillCallStart,
				Payload:   map[string]string{"skill": name, "input": userInput},
				Timestamp: time.Now(),
			})
			sc := skill.NewContext(s, userInput, s.Instruction())
			out, log := sc.Run(ctx, a.model)
			_ = a.hookManager.Emit(ctx, hook.Event{
				Type:      hook.EventSkillContextLog,
				Payload:   log,
				Timestamp: time.Now(),
			})
			_ = a.hookManager.Emit(ctx, hook.Event{
				Type:      hook.EventSkillCallDone,
				Payload:   map[string]string{"skill": name, "output": out},
				Timestamp: time.Now(),
			})
			return out, nil
		}))
		a.builtinSkillCallRegistered = true
	}
	if !a.builtinFetchFullResultRegistered {
		a.toolRegistry.AddTool(builtin.NewFetchFullResultTool(func(ctx context.Context, id string) (string, error) {
			cs := contentStoreFromContext(ctx)
			if cs == nil {
				return "", fmt.Errorf("no content store in context")
			}
			return cs.Load(ctx, id)
		}))
		a.builtinFetchFullResultRegistered = true
	}
}

func (a *Agent) InjectRules(dirPath string) error {
	rules, err := rule.LoadDir(dirPath)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ruleRegistry.Add(rules...)
	a.rebuildPromptBuilder()
	a.ensureBuiltinToolsUnlocked()
	return nil
}

func (a *Agent) InjectSkills(dirPath string) error {
	skills, err := skill.LoadDir(dirPath)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.skillRegistry.Add(skills...)
	a.rebuildPromptBuilder()
	a.ensureBuiltinToolsUnlocked()
	return nil
}

func (a *Agent) validateFinalOutput(answer string) (string, error) {
	if a.outputSchema == nil {
		return "", nil
	}
	oc := outputhook.NewOutputController(a.outputSchema, a.autoRetry)
	_, err := oc.ValidateOutput(answer)
	if err != nil {
		var sve *agenterrs.StructuredValidationError
		if stderrors.As(err, &sve) {
			formatted, fmtErr := sve.FormatForModel()
			if fmtErr == nil {
				return formatted, err
			}
		}
		return err.Error(), err
	}
	return "", nil
}
