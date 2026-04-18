package agent

import (
	"time"

	"github.com/LYD99/simple-agent-framework/evaluator"
	"github.com/LYD99/simple-agent-framework/hook"
	"github.com/LYD99/simple-agent-framework/interrupter"
	"github.com/LYD99/simple-agent-framework/memory"
	"github.com/LYD99/simple-agent-framework/model"
	"github.com/LYD99/simple-agent-framework/planner"
	"github.com/LYD99/simple-agent-framework/rule"
	"github.com/LYD99/simple-agent-framework/skill"
	"github.com/LYD99/simple-agent-framework/tool"
)

type AgentOption func(*Agent)

type MemoryFactory func(sessionID string) memory.Memory

type ContentStoreFactory func(sessionID string) memory.ContentStore

type SessionOption func(*Session)

func WithModel(m model.ChatModel) AgentOption {
	return func(a *Agent) {
		a.model = m
	}
}

func WithPlanner(p planner.Planner) AgentOption {
	return func(a *Agent) {
		a.reactPlanner = p
	}
}

func WithEvaluator(e evaluator.Evaluator) AgentOption {
	return func(a *Agent) {
		a.evaluator = e
		if e != nil {
			a.evalEnabled = true
		} else {
			a.evalEnabled = false
		}
	}
}

// WithMemory sets a fixed Memory for all sessions (shared).
// For multi-session isolation use WithMemoryFactory instead.
func WithMemory(m memory.Memory) AgentOption {
	return func(a *Agent) {
		a.memoryFactory = func(_ string) memory.Memory { return m }
	}
}

func WithMemoryFactory(f MemoryFactory) AgentOption {
	return func(a *Agent) {
		a.memoryFactory = f
	}
}

func WithContentStoreFactory(f ContentStoreFactory) AgentOption {
	return func(a *Agent) {
		a.contentStoreFactory = f
	}
}

func WithTools(tools ...tool.Tool) AgentOption {
	return func(a *Agent) {
		if a.toolRegistry == nil {
			a.toolRegistry = tool.NewToolRegistry()
		}
		for _, t := range tools {
			if t != nil {
				a.toolRegistry.AddTool(t)
			}
		}
	}
}

func WithToolRegistry(r *tool.ToolRegistry) AgentOption {
	return func(a *Agent) {
		if r != nil {
			a.toolRegistry = r
		}
	}
}

func WithRules(rules ...rule.Rule) AgentOption {
	return func(a *Agent) {
		a.ruleRegistry.Add(rules...)
	}
}

// WithRulePaths loads rules from given paths (file or directory).
// Each path can be a single .md file or a directory of .md files.
func WithRulePaths(paths ...string) AgentOption {
	return func(a *Agent) {
		for _, p := range paths {
			rules, err := rule.LoadPath(p)
			if err != nil {
				continue
			}
			a.ruleRegistry.Add(rules...)
		}
	}
}

func WithSkills(skills ...skill.Skill) AgentOption {
	return func(a *Agent) {
		a.skillRegistry.Add(skills...)
	}
}

// WithSkillPaths loads skills from given paths (file, skill dir, or parent dir).
func WithSkillPaths(paths ...string) AgentOption {
	return func(a *Agent) {
		for _, p := range paths {
			skills, err := skill.LoadPath(p)
			if err != nil {
				continue
			}
			a.skillRegistry.Add(skills...)
		}
	}
}

func WithHITL(h interrupter.Interrupter) AgentOption {
	return func(a *Agent) {
		a.interrupter = h
	}
}

func WithHook(h hook.Hook) AgentOption {
	return func(a *Agent) {
		a.hookManager.Add(h)
	}
}

func WithHooks(hooks ...hook.Hook) AgentOption {
	return func(a *Agent) {
		for _, h := range hooks {
			a.hookManager.Add(h)
		}
	}
}

func WithExecutionMode(mode planner.ExecutionMode) AgentOption {
	return func(a *Agent) {
		a.currentMode = mode
	}
}

func WithMaxIterations(n int) AgentOption {
	return func(a *Agent) {
		if n > 0 {
			a.maxIterations = n
		}
	}
}

func WithTimeout(d time.Duration) AgentOption {
	return func(a *Agent) {
		if d > 0 {
			a.timeout = d
		}
	}
}

func WithSystemPrompt(p string) AgentOption {
	return func(a *Agent) {
		a.systemPrompt = p
	}
}

func WithOutputSchema(schema any, retries int) AgentOption {
	return func(a *Agent) {
		a.outputSchema = schema
		if retries >= 0 {
			a.autoRetry = retries
		}
	}
}

func WithStreamEnabled(enabled bool) AgentOption {
	return func(a *Agent) {
		a.streamEnabled = enabled
	}
}

func WithName(name string) AgentOption {
	return func(a *Agent) {
		a.name = name
	}
}

type CompressAgentConfig struct {
	Model           model.ChatModel
	Prompt          string
	MaxContextRatio float64
}

func WithLoopDetectionThreshold(n int) AgentOption {
	return func(a *Agent) {
		if n > 0 {
			a.loopDetectionThreshold = n
		}
	}
}

func WithToolResultMaxLen(n int) AgentOption {
	return func(a *Agent) {
		if n > 0 {
			a.toolResultMaxLen = n
		}
	}
}

// WithContentStore sets a fixed ContentStore for all sessions (shared).
// For multi-session isolation use WithContentStoreFactory instead.
func WithContentStore(store memory.ContentStore) AgentOption {
	return func(a *Agent) {
		a.contentStoreFactory = func(_ string) memory.ContentStore { return store }
	}
}

func WithRecentToolResultTokens(n int) AgentOption {
	return func(a *Agent) {
		if n > 0 {
			a.recentToolResultTokens = n
		}
	}
}

func WithMaxContextRatio(ratio float64) AgentOption {
	return func(a *Agent) {
		if ratio > 0 {
			a.maxContextRatio = ratio
		}
	}
}

func WithModelMaxTokens(n int) AgentOption {
	return func(a *Agent) {
		if n > 0 {
			a.modelMaxTokens = n
		}
	}
}

func WithCompressAgent(config CompressAgentConfig) AgentOption {
	return func(a *Agent) {
		c := config
		a.compressConfig = &c
	}
}
