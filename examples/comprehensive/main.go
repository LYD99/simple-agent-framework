// Package main demonstrates the full feature set of simple-agent-framework.
//
// Features covered:
//   - Model: DeepSeek provider (deepseek-chat)
//   - Tools: ToolRegistry.Register with typed schema structs
//   - Rules: alwaysApply=true (inline + safety.md) + alwaysApply=false (code_style.md)
//   - Skills: deploy skill loaded from directory (alwaysApply=false)
//   - Evaluator: RuleBasedEvaluator, CompositeEvaluator; Enable/Disable at runtime
//   - Hook: LoggerHook + custom EventCounter tracking all event types
//   - HITL: auto-approve interrupter with per-tool approval list
//   - Memory: BufferMemory per-session factory; SummaryMemory via separate agent
//   - ContentStore: InMemoryContentStore (default); FileContentStore factory
//   - Session: reuse via AgentResult.SessionID + explicit Session API
//   - Execution modes: ReAct (default) → PlanAndSolve → back
//   - OutputSchema: structured output validation with auto-retry
//   - Dynamic management: AddRule/RemoveRule, AddSkill/RemoveSkill, InjectRules/InjectSkills
//   - CheckpointStore: MemoryStore save/load agent snapshots
//   - Loop detection threshold, ToolResultMaxLen, timeout, maxIterations
//
// Usage:
//
//	export DEEPSEEK_API_KEY=sk-...
//	cd simple-agent-framework
//	go run ./examples/comprehensive/
package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"simple-agent-framework/agent"
	"simple-agent-framework/evaluator"
	"simple-agent-framework/hook"
	"simple-agent-framework/interrupter"
	"simple-agent-framework/memory"
	"simple-agent-framework/model"
	"simple-agent-framework/model/provider/anthropic"
	"simple-agent-framework/model/provider/deepseek"
	"simple-agent-framework/model/provider/openai"
	"simple-agent-framework/planner"
	"simple-agent-framework/retriever"
	"simple-agent-framework/rule"
	"simple-agent-framework/runtime"
	"simple-agent-framework/skill"
	"simple-agent-framework/tool"
	"simple-agent-framework/tool/builtin"
	"simple-agent-framework/tool/mcp"
)

// ── Output schema for Demo 7 ─────────────────────────────────────────────────

// WeatherReport is the structured output schema for the OutputSchema demo.
type WeatherReport struct {
	City        string  `json:"city"        validate:"required"`
	Temperature float64 `json:"temperature" validate:"required"`
	Condition   string  `json:"condition"   validate:"required"`
}

func main() {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "Set DEEPSEEK_API_KEY to run this demo")
		os.Exit(1)
	}
	demoDir := mustDemoDir()
	rulesDir := filepath.Join(demoDir, "rules")
	skillsDir := filepath.Join(demoDir, "skills")

	// ── Model ────────────────────────────────────────────────────────────────
	m := deepseek.New("deepseek-chat", apiKey)

	// ── Tools ────────────────────────────────────────────────────────────────
	reg := buildToolRegistry()

	// ── Rules ────────────────────────────────────────────────────────────────
	// inlineRule: alwaysApply=true → injected directly into system prompt.
	inlineRule := rule.NewFileRule(
		"response_format",
		"Respond in English with bullet points; keep answers under 200 words.",
		"Use bullet points for lists. Keep answers under 200 words when possible.",
		true,
	)

	// ── Evaluator ────────────────────────────────────────────────────────────
	// RuleFunc signature: func(*EvalState) (*EvalResult, error)
	primaryEv := evaluator.NewRuleBased(
		func(state *evaluator.EvalState) (*evaluator.EvalResult, error) {
			// Complete once at least one tool step has run.
			if state.Iteration >= 1 && len(state.StepResults) >= 1 {
				return &evaluator.EvalResult{Decision: evaluator.DecisionComplete}, nil
			}
			return &evaluator.EvalResult{Decision: evaluator.DecisionContinue}, nil
		},
	)

	// ── Hooks ────────────────────────────────────────────────────────────────
	logger := hook.NewLoggerWithPrefix(os.Stdout, "[demo]")
	counter := &eventCounter{}

	// ── HITL ─────────────────────────────────────────────────────────────────
	hitl := interrupter.NewHITL(
		func(event interrupter.InterruptEvent) (*interrupter.HumanResponse, error) {
			fmt.Printf("  [HITL] Auto-approving tool: %s\n", event.Action.ToolName)
			return &interrupter.HumanResponse{Approved: true, Message: "auto-approved"}, nil
		},
		interrupter.WithRequireApproval("deploy_app"),
		interrupter.WithAutoApproveRead(true),
	)

	// ── Main Agent ───────────────────────────────────────────────────────────
	a := agent.New(
		agent.WithName("comprehensive-demo"),
		agent.WithModel(m),
		agent.WithToolRegistry(reg),

		// Rules: inline (alwaysApply=true) + file-based safety (alwaysApply=true)
		// and code_style (alwaysApply=false → catalog-only, loaded on demand via rule_view).
		agent.WithRules(inlineRule),
		agent.WithRulePaths(rulesDir),

		// Skills: deploy skill (alwaysApply=false → catalog + skill_call builtin tool).
		agent.WithSkillPaths(skillsDir),

		agent.WithHook(logger),
		agent.WithHook(counter),
		agent.WithHITL(hitl),

		agent.WithMaxIterations(15),
		agent.WithTimeout(2*time.Minute),
		agent.WithLoopDetectionThreshold(3),
		agent.WithToolResultMaxLen(5000),
		agent.WithRecentToolResultTokens(2000),

		// Per-session BufferMemory: each session gets its own isolated history.
		agent.WithMemoryFactory(func(sid string) memory.Memory {
			fmt.Printf("  [factory] Creating BufferMemory for session %s\n", sid[:8])
			return memory.NewBuffer(50)
		}),

		// Per-session InMemoryContentStore for large tool result persistence.
		agent.WithContentStoreFactory(func(sid string) memory.ContentStore {
			return memory.NewInMemoryContentStore()
		}),
	)
	ctx := context.Background()

	fmt.Printf("System prompt: %s\n", a.ShowSystemPrompt(ctx))
	// ═══════════════════════════════════════════════════════════════════════════
	// Demo 1: ReAct mode — multi-tool weather query
	// Covers: ReAct planner, tool calls, rules (alwaysApply=true injected),
	//         alwaysApply=false rule catalog, evaluator, hooks.
	// ═══════════════════════════════════════════════════════════════════════════
	section("Demo 1: ReAct Mode — Weather Query")
	fmt.Println("System prompt: DefaultReActSystemPrompt (auto-selected for ReAct mode)")
	fmt.Println("Rules: response_format + safety (alwaysApply=true inline) | code_style (catalog)")
	fmt.Println()

	r1, err := runWeatherDemo(a, ctx)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	printResult(r1)

	// ═══════════════════════════════════════════════════════════════════════════
	// Demo 2: Session reuse via SessionID
	// Covers: persistent conversation context, memory carry-over.
	// ═══════════════════════════════════════════════════════════════════════════
	section("Demo 2: Session Reuse via SessionID")
	fmt.Printf("Reusing session: %s\n\n", r1.SessionID)

	r2, err := a.Run(ctx, "Based on the weather you just found, which city is better for an outdoor picnic?", r1.SessionID)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Same session preserved: %v\n", r2.SessionID == r1.SessionID)
	printResult(r2)

	// ═══════════════════════════════════════════════════════════════════════════
	// Demo 3: PlanAndSolve mode — multi-step task
	// Covers: SetExecutionMode, DefaultPlanAndSolveSystemPrompt auto-selection,
	//         plan generation, step execution, plan tracking.
	// ═══════════════════════════════════════════════════════════════════════════
	section("Demo 3: PlanAndSolve Mode — Multi-step Task")
	a.SetExecutionMode(planner.ModePlanAndSolve)
	fmt.Println("Switched to PlanAndSolve (DefaultPlanAndSolveSystemPrompt auto-selected)")
	fmt.Println()

	r3, err := a.Run(ctx, "Search for the latest Go release, then calculate 1.22 * 100.")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	printResult(r3)

	a.SetExecutionMode(planner.ModeReAct)

	// ═══════════════════════════════════════════════════════════════════════════
	// Demo 4a: HITL — deploy_app auto-approved
	// Covers: InterruptBeforeToolCall, RequireApproval list, AutoApproveRead,
	//         WaitForHuman callback.
	// ═══════════════════════════════════════════════════════════════════════════
	section("Demo 4a: HITL — Deploy with Auto-Approval")
	fmt.Println("deploy_app is in RequireApproval list → HITL callback fires (auto-approved here)")
	fmt.Println("get/check/search tools are auto-approved via AutoApproveRead=true")
	fmt.Println()

	r4a, err := a.Run(ctx, "Check the current version, build v2.1.0, and deploy it.")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	printResult(r4a)

	// ═══════════════════════════════════════════════════════════════════════════
	// Demo 4b: HITL — real human approval via stdin
	// Covers: interactive Approved/Rejected flow, ModifiedInput override,
	//         WaitForHuman blocking until human responds.
	// ═══════════════════════════════════════════════════════════════════════════
	section("Demo 4b: HITL — Interactive Human Approval")
	fmt.Println("deploy_app requires YOUR approval via stdin.")
	fmt.Println("When prompted: y = approve, n = reject, or type a version to approve with override.")
	fmt.Println()

	stdinReader := bufio.NewReader(os.Stdin)
	reviewHITL := interrupter.NewHITL(
		func(event interrupter.InterruptEvent) (*interrupter.HumanResponse, error) {
			args := event.Action.ToolInput
			fmt.Printf("\n  ┌─── HITL: Approval Required ───────────────────────\n")
			fmt.Printf("  │ Tool:  %s\n", event.Action.ToolName)
			fmt.Printf("  │ Args:  %v\n", args)
			fmt.Printf("  │\n")
			fmt.Printf("  │ [y] approve  [n] reject  [version string] approve with override\n")
			fmt.Printf("  └──────────────────────────────────────────────────\n")
			fmt.Print("  Your decision: ")

			line, _ := stdinReader.ReadString('\n')
			input := strings.TrimSpace(line)

			switch strings.ToLower(input) {
			case "y", "yes":
				fmt.Println("  → Approved")
				return &interrupter.HumanResponse{Approved: true, Message: "human approved"}, nil
			case "n", "no":
				fmt.Println("  → Rejected")
				return &interrupter.HumanResponse{Approved: false, Message: "human rejected the action"}, nil
			default:
				fmt.Printf("  → Approved with version override: %s\n", input)
				return &interrupter.HumanResponse{
					Approved:      true,
					Message:       fmt.Sprintf("approved with version override to %s", input),
					ModifiedInput: map[string]any{"version": input},
				}, nil
			}
		},
		interrupter.WithRequireApproval("deploy_app"),
		interrupter.WithAutoApproveRead(true),
	)

	reviewAgent := agent.New(
		agent.WithName("hitl-review-demo"),
		agent.WithModel(m),
		agent.WithToolRegistry(reg),
		agent.WithHITL(reviewHITL),
		agent.WithHook(logger),
		agent.WithMaxIterations(15),
		agent.WithTimeout(5*time.Minute),
	)

	r4b, err := reviewAgent.Run(ctx, "Build v2.1.0 and deploy it.")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	printResult(r4b)

	// ═══════════════════════════════════════════════════════════════════════════
	// Demo 5: Explicit Session management — multi-turn conversation
	// Covers: agent.Session(id), Session.Run, Session.ID, Session.Messages,
	//         deterministic session ID, multi-turn context persistence.
	// ═══════════════════════════════════════════════════════════════════════════
	section("Demo 5: Explicit Session — Multi-turn Conversation")
	sess := a.Session("demo-session-001")
	fmt.Printf("Session ID: %s\n\n", sess.ID())

	r5a, err := sess.Run(ctx, "Search for Go modules best practices.")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Turn 1:\n%s\n\n", truncate(r5a.Answer, 150))

	r5b, err := sess.Run(ctx, "Summarize the above in one sentence.")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Turn 2 (follow-up):\n%s\n", r5b.Answer)
	fmt.Printf("Session preserved: %v\n", r5b.SessionID == "demo-session-001")

	// ═══════════════════════════════════════════════════════════════════════════
	// Demo 6: Dynamic Rule/Skill Management
	// Covers: AddRule, RemoveRule, AddSkill, RemoveSkill, InjectRules, InjectSkills.
	// Rule/skill catalogs in the system prompt are rebuilt on every change.
	// ═══════════════════════════════════════════════════════════════════════════
	section("Demo 6: Dynamic Rule and Skill Management")

	// AddRule at runtime (alwaysApply=false → added to catalog, model uses rule_view).
	runtimeRule := rule.NewFileRule(
		"perf_hints",
		"Performance optimization hints for Go — profiling, allocation, goroutine use.",
		"1. Profile before optimizing (pprof). 2. Avoid unnecessary allocations. 3. Use sync.Pool for hot paths.",
		false,
	)
	a.AddRule(runtimeRule)
	fmt.Println("Added runtime rule 'perf_hints' (alwaysApply=false) → catalog entry injected")

	// InjectRules loads rules from a directory at runtime.
	_ = a.InjectRules(rulesDir) // re-injects (idempotent via name)
	fmt.Printf("InjectRules(%q) re-loaded safely (registry dedupes by name)\n", rulesDir)

	// AddSkill at runtime (programmatic DirSkill).
	rollbackSkill := skill.NewDirSkill(
		"rollback",
		"Emergency rollback SOP — verify, revert, confirm.",
		filepath.Join(skillsDir, "rollback"),
		"1. Call check_version. 2. Call deploy_app with previous version. 3. Confirm with check_version.",
		skill.WithMaxIterations(5),
		skill.WithAlwaysApply(false),
	)
	a.AddSkill(rollbackSkill)
	fmt.Println("Added runtime skill 'rollback' (programmatic DirSkill)")

	// InjectSkills from directory.
	_ = a.InjectSkills(skillsDir)
	fmt.Printf("InjectSkills(%q) re-loaded safely\n", skillsDir)

	promptAfterInject := a.ShowSystemPrompt(ctx)
	if err := requirePromptContains(promptAfterInject,
		`<rule name="perf_hints">`,
		`<rule name="code_style">`,
		`<skill name="rollback">`,
		`<skill name="deploy">`,
	); err != nil {
		fmt.Printf("dynamic management validation error: %v\n", err)
		return
	}
	fmt.Println("Validated prompt catalogs contain perf_hints, code_style, rollback, and deploy")

	// RemoveRule — removes from catalog and rebuilds prompt.
	a.RemoveRule("perf_hints")
	fmt.Println("Removed 'perf_hints' rule — catalog rebuilt")

	// RemoveSkill — removes from catalog and rebuilds prompt.
	a.RemoveSkill("rollback")
	fmt.Println("Removed 'rollback' skill — catalog rebuilt")

	promptAfterRemove := a.ShowSystemPrompt(ctx)
	if err := requirePromptContains(promptAfterRemove, `<rule name="code_style">`, `<skill name="deploy">`); err != nil {
		fmt.Printf("post-remove validation error: %v\n", err)
		return
	}
	if err := requirePromptNotContains(promptAfterRemove, `<rule name="perf_hints">`, `<skill name="rollback">`); err != nil {
		fmt.Printf("post-remove validation error: %v\n", err)
		return
	}
	fmt.Println("Validated prompt catalogs removed perf_hints and rollback while keeping code_style and deploy")

	// ── Trigger rule_view builtin: ask LLM to consult the code_style rule ───────
	fmt.Println("\n--- Triggering rule_view builtin tool ---")
	r6rule, err := a.Run(ctx,
		"Look up the 'code_style' rule using the rule_view tool, then briefly summarize what it says in one sentence.")
	if err != nil {
		fmt.Printf("rule_view demo error: %v\n", err)
	} else {
		fmt.Printf("rule_view answer: %s\n", truncate(r6rule.Answer, 150))
	}

	// ── Trigger skill_call builtin: ask LLM to invoke the deploy skill ──────────
	fmt.Println("\n--- Triggering skill_call builtin tool ---")
	r6skill, err := a.Run(ctx,
		"Use the skill_call tool to invoke the 'deploy' skill with input 'deploy version v3.0.0 to production'. "+
			"Then report the skill's final output.")
	if err != nil {
		fmt.Printf("skill_call demo error: %v\n", err)
	} else {
		fmt.Printf("skill_call answer: %s\n", truncate(r6skill.Answer, 200))
	}

	// ═══════════════════════════════════════════════════════════════════════════
	// Demo 7: OutputSchema — structured output validation with auto-retry
	// Covers: WithOutputSchema, WithSystemPrompt, OutputController, auto-retry loop.
	// ═══════════════════════════════════════════════════════════════════════════
	section("Demo 7: OutputSchema — Structured Output Validation")
	fmt.Println("Schema: WeatherReport{city, temperature, condition}")
	fmt.Println("Auto-retry: 2 attempts on validation failure")
	fmt.Println()

	schemaAgent := agent.New(
		agent.WithModel(m),
		agent.WithToolRegistry(reg),
		agent.WithSystemPrompt(
			"You are a weather assistant. When asked about weather, respond ONLY with a valid JSON object "+
				"matching this schema exactly: {\"city\":\"string\",\"temperature\":number,\"condition\":\"string\"}. "+
				"No markdown, no extra text — raw JSON only.",
		),
		agent.WithOutputSchema(WeatherReport{}, 2),
		agent.WithMaxIterations(5),
		agent.WithTimeout(60*time.Second),
	)

	r7, err := schemaAgent.Run(ctx, "What's the weather in Beijing?")
	if err != nil {
		fmt.Printf("Output validation error (schema enforced): %v\n", err)
	} else {
		fmt.Printf("Structured output received:\n%s\n", r7.Answer)
		fmt.Printf("Duration: %s\n", r7.Duration.Round(time.Millisecond))
	}

	// ═══════════════════════════════════════════════════════════════════════════
	// Demo 8: CompositeEvaluator + EnableEvaluator / DisableEvaluator
	// Covers: NewComposite, multiple evaluators composed with priority merge,
	//         runtime Enable/Disable control.
	// ═══════════════════════════════════════════════════════════════════════════
	section("Demo 8: CompositeEvaluator + Runtime Enable/Disable")

	// guardrailEv: never exceed 8 iterations, or escalate.
	guardrailEv := evaluator.NewRuleBased(
		func(state *evaluator.EvalState) (*evaluator.EvalResult, error) {
			if state.Iteration > 8 {
				return &evaluator.EvalResult{
					Decision: evaluator.DecisionEscalate,
					Feedback: "guardrail: iteration limit exceeded",
				}, nil
			}
			return &evaluator.EvalResult{Decision: evaluator.DecisionContinue}, nil
		},
	)
	// LLMJudgeEvaluator: uses the LLM itself to judge execution progress.
	llmJudge := evaluator.NewLLMJudge(m)
	fmt.Println("evaluator.NewLLMJudge(model) — LLM-as-judge evaluator constructed")

	// 8a: LLMJudge standalone — let the LLM decide when the task is complete.
	fmt.Println("\n--- 8a: LLMJudgeEvaluator standalone ---")
	a.EnableEvaluator(llmJudge)
	fmt.Println("EnableEvaluator(LLMJudgeEvaluator)")

	r8a, err := a.Run(ctx, "Get the weather in Tokyo.")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Printf("With LLMJudge → %s\n", truncate(r8a.Answer, 120))
	}

	// 8b: CompositeEvaluator — guardrail + LLMJudge composed together.
	fmt.Println("\n--- 8b: CompositeEvaluator (guardrail + LLMJudge) ---")
	composite := evaluator.NewComposite(guardrailEv, llmJudge)
	a.EnableEvaluator(composite)
	fmt.Println("EnableEvaluator(CompositeEvaluator{guardrail + LLMJudge})")

	r8b, err := a.Run(ctx, "Get the weather in Shanghai.")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Printf("With Composite(guardrail+LLMJudge) → %s\n", truncate(r8b.Answer, 120))
	}

	// 8c: DisableEvaluator — agent runs without evaluation loop.
	fmt.Println("\n--- 8c: DisableEvaluator ---")
	a.DisableEvaluator()
	fmt.Println("DisableEvaluator() — agent runs without evaluation")

	r8c, err := a.Run(ctx, "Get the weather in Beijing.")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Printf("Without evaluator → %s\n", truncate(r8c.Answer, 120))
	}

	// Restore original evaluator.
	a.EnableEvaluator(primaryEv)
	fmt.Println("\nRestored primary RuleBasedEvaluator")

	// ═══════════════════════════════════════════════════════════════════════════
	// Demo 9: SummaryMemory — auto-summarization when history exceeds threshold
	// Covers: memory.NewSummary, threshold-triggered LLM summarization,
	//         WithMemoryFactory for a separate agent configuration.
	// ═══════════════════════════════════════════════════════════════════════════
	section("Demo 9: SummaryMemory — Auto-summarization")
	fmt.Println("SummaryMemory with threshold=8: messages > 8 trigger LLM summarization.")
	fmt.Println("A completion evaluator caps tool calls per turn so we don't spam compression.")
	fmt.Println()

	compressions := 0
	lastSummary := ""
	summaryCB := func(before, after int, summary string) {
		compressions++
		if summary == lastSummary {
			fmt.Printf("  [SummaryMemory] compression #%d: %d → %d (summary unchanged)\n",
				compressions, before, after)
			return
		}
		lastSummary = summary
		fmt.Printf("  [SummaryMemory] compression #%d: %d → %d messages (tail kept)\n",
			compressions, before, after)
		fmt.Printf("  [SummaryMemory] new summary: %s\n", truncate(summary, 140))
	}

	// Simple evaluator: complete after the first tool call so the LLM doesn't loop.
	summaryEv := evaluator.NewRuleBased(
		func(state *evaluator.EvalState) (*evaluator.EvalResult, error) {
			if len(state.StepResults) >= 1 {
				return &evaluator.EvalResult{Decision: evaluator.DecisionComplete}, nil
			}
			return &evaluator.EvalResult{Decision: evaluator.DecisionContinue}, nil
		},
	)

	summaryAgent := agent.New(
		agent.WithModel(m),
		agent.WithToolRegistry(reg),
		agent.WithMaxIterations(4),
		agent.WithTimeout(90*time.Second),
		agent.WithEvaluator(summaryEv),
		// SummaryMemory: threshold=8 — when session exceeds 8 messages,
		// the model compresses the oldest head into a summary injected as system context.
		// Callback fires each time compression runs so you can observe it.
		agent.WithMemoryFactory(func(sid string) memory.Memory {
			return memory.NewSummary(m, 8, memory.WithSummaryCallback(summaryCB))
		}),
	)

	summSess := summaryAgent.Session("summary-demo")
	for i, q := range []string{
		"Search for Go 1.24 release notes.",
		"What are the main features of Go 1.24?",
		"How does Go 1.24 improve performance?",
	} {
		fmt.Printf("Turn %d Q: %s\n", i+1, q)
		r, err := summSess.Run(ctx, q)
		if err != nil {
			fmt.Printf("Turn %d error (err): %v\n", i+1, err)
			break
		}
		if r.Error != nil {
			fmt.Printf("Turn %d error (r.Error): %v  (answer=%q msgs=%d)\n",
				i+1, r.Error, r.Answer, len(r.Messages))
			continue
		}
		answer := strings.TrimSpace(r.Answer)
		if answer == "" {
			answer = fmt.Sprintf("<empty answer — msgs=%d, last role=%s>",
				len(r.Messages), lastMsgRole(r.Messages))
		}
		memMsgs, _ := summSess.Messages(ctx)
		fmt.Printf("Turn %d A: %s\n", i+1, truncate(answer, 200))
		fmt.Printf("Turn %d memory size: %d messages (incl. summary if present)\n\n",
			i+1, len(memMsgs))
	}
	fmt.Printf("Total compressions triggered: %d\n", compressions)

	// ═══════════════════════════════════════════════════════════════════════════
	// Demo 10: FileContentStore + ToolResultMaxLen truncation
	// Covers: ContentStoreFactory, FileContentStore, TruncateToolResult,
	//         fetch_full_result builtin tool, large result handling.
	// ═══════════════════════════════════════════════════════════════════════════
	section("Demo 10: FileContentStore + Large Result Truncation")
	fmt.Println("ToolResultMaxLen=80: results >80 chars stored in FileContentStore")
	fmt.Println("fetch_full_result builtin tool auto-registered to retrieve by ID")
	fmt.Println()

	storeDir := filepath.Join(os.TempDir(), "agent-demo-content")
	truncReg := buildToolRegistry()
	truncReg.Register("get_report", "Generate a large report", struct{}{},
		func(_ map[string]any) (string, error) {
			return strings.Repeat("Report line data. ", 30), nil // ~540 chars > 80
		})

	truncAgent := agent.New(
		agent.WithModel(m),
		agent.WithToolRegistry(truncReg),
		agent.WithMaxIterations(8),
		agent.WithTimeout(90*time.Second),
		agent.WithToolResultMaxLen(80),
		agent.WithContentStoreFactory(func(sid string) memory.ContentStore {
			dir := filepath.Join(storeDir, sid[:8])
			store, err := memory.NewFileContentStore(dir)
			if err != nil {
				return memory.NewInMemoryContentStore()
			}
			fmt.Printf("  [FileContentStore] created at %s\n", dir)
			return store
		}),
	)

	r10, err := truncAgent.Run(ctx, "Generate a report and summarize the key points.")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Printf("Answer: %s\n", truncate(r10.Answer, 200))
		fmt.Printf("Duration: %s\n", r10.Duration.Round(time.Millisecond))
	}

	// ═══════════════════════════════════════════════════════════════════════════
	// Demo 11: CheckpointStore — agent snapshot persistence
	// Covers: interrupter.MemoryStore, AgentSnapshot, Serialize/Deserialize.
	// ═══════════════════════════════════════════════════════════════════════════
	section("Demo 11: CheckpointStore — Agent Snapshot Persistence")

	store := interrupter.NewMemoryStore()
	snapshot := &interrupter.AgentSnapshot{
		RunID:     "run-demo-001",
		Iteration: 3,
		Messages: []model.ChatMessage{
			{Role: model.RoleUser, Content: "Deploy v2.1.0 to production"},
			{Role: model.RoleAssistant, Content: "Checking current version first..."},
		},
	}

	if err := store.Save(ctx, snapshot.RunID, snapshot); err != nil {
		fmt.Printf("Save error: %v\n", err)
	} else {
		fmt.Printf("Snapshot saved: run_id=%s iteration=%d messages=%d\n",
			snapshot.RunID, snapshot.Iteration, len(snapshot.Messages))
	}

	loaded, err := store.Load(ctx, snapshot.RunID)
	if err != nil {
		fmt.Printf("Load error: %v\n", err)
	} else {
		fmt.Printf("Snapshot loaded: run_id=%s iteration=%d messages=%d\n",
			loaded.RunID, loaded.Iteration, len(loaded.Messages))
		fmt.Printf("Serialize → JSON: %d bytes\n", func() int {
			b, _ := loaded.Serialize()
			return len(b)
		}())
	}

	if err := store.Delete(ctx, snapshot.RunID); err != nil {
		fmt.Printf("Delete error: %v\n", err)
	} else {
		fmt.Println("Snapshot deleted from store")
		_, err := store.Load(ctx, snapshot.RunID)
		fmt.Printf("Load after delete (expected error): %v\n", err)
	}

	// ═══════════════════════════════════════════════════════════════════════════
	// Demo 12: Streaming — RunStream + WithStreamEnabled + EventStreamChunk
	// Covers: WithStreamEnabled option, RunStream method, stream chunk hook.
	// Note: RunStream delegates to Run; EventStreamChunk fires per chunk when
	//       the model backend supports streaming.
	// ═══════════════════════════════════════════════════════════════════════════
	section("Demo 12: Streaming — RunStream + EventStreamChunk")
	fmt.Println("WithStreamEnabled(true) opts in to streaming; EventStreamChunk hook fires per delta chunk")
	fmt.Println()

	sp := &streamPrinter{}
	streamAgent := agent.New(
		agent.WithName("stream-demo"),
		agent.WithModel(m),
		agent.WithStreamEnabled(true),
		agent.WithHooks(sp, counter),
		agent.WithMaxIterations(5),
		agent.WithTimeout(60*time.Second),
	)
	r12, err := streamAgent.RunStream(ctx, "Explain in one short sentence why 3 + 4 = 7.")
	if err != nil {
		fmt.Printf("\nStream error: %v\n", err)
	} else {
		fmt.Printf("\nAnswer: %s\n", truncate(r12.Answer, 120))
		fmt.Printf("StreamChunk events captured: %d\n", sp.chunks)
		if sp.chunks == 0 {
			fmt.Println("Stream demo error: no text stream chunks were observed")
			return
		}
	}

	// ═══════════════════════════════════════════════════════════════════════════
	// Demo 13: Context Compression — WithCompressAgent + WithMaxContextRatio
	// Covers: CompressAgentConfig (model, prompt, ratio), WithMaxContextRatio,
	//         ContextCompressor auto-triggered when context exceeds ratio limit.
	// ═══════════════════════════════════════════════════════════════════════════
	section("Demo 13: Context Compression — WithCompressAgent + WithMaxContextRatio")

	compressAgent := agent.New(
		agent.WithName("compress-demo"),
		agent.WithModel(m),
		agent.WithToolRegistry(reg),
		agent.WithMaxIterations(8),
		agent.WithTimeout(90*time.Second),
		// WithMaxContextRatio: compress when messages exceed 85% of model token limit.
		agent.WithMaxContextRatio(0.85),
		// WithCompressAgent: dedicated model + custom prompt for compression.
		// When context ratio is exceeded, ContextCompressor runs this model to
		// summarize old messages into a structured JSON summary injected as system context.
		agent.WithCompressAgent(agent.CompressAgentConfig{
			Model:           m,
			Prompt:          "Summarize the conversation. Keep: goals, findings, completed steps, key identifiers.",
			MaxContextRatio: 0.80,
		}),
		agent.WithMemoryFactory(func(sid string) memory.Memory {
			return memory.NewBuffer(200)
		}),
	)
	fmt.Println("CompressAgentConfig.Model:           <same model as main agent>")
	fmt.Println("CompressAgentConfig.MaxContextRatio: 0.80 — compress when 80% full")
	fmt.Println("WithMaxContextRatio:                 0.85 — session-level fallback threshold")
	fmt.Println("memory.DefaultCompressPrompt:        full structured summarization template available")

	r13, err := compressAgent.Run(ctx, "Search for Go best practices and summarize.")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Printf("Answer: %s\n", truncate(r13.Answer, 120))
	}

	// ═══════════════════════════════════════════════════════════════════════════
	// Demo 14: Retriever / RAG — KeywordRetriever, SemanticRetriever,
	//          HybridRetriever, RRFMerger, RAGTool, WithFormatter
	// Covers: all retriever constructors, search options, RAGTool as agent tool.
	// ═══════════════════════════════════════════════════════════════════════════
	section("Demo 14: Retriever / RAG — Keyword, Semantic, Hybrid, RAGTool")

	// ── Knowledge base documents ─────────────────────────────────────────────
	kbDocs := []retriever.Document{
		{ID: "doc-1", Content: "Go 1.24 adds weak pointers and improved finalizers.", Score: 0, Source: "go-blog"},
		{ID: "doc-2", Content: "Go modules enable reproducible builds via go.mod and go.sum.", Score: 0, Source: "go-docs"},
		{ID: "doc-3", Content: "Go generics were introduced in Go 1.18 with type parameters.", Score: 0, Source: "go-spec"},
		{ID: "doc-4", Content: "The Go standard library includes net/http for HTTP servers.", Score: 0, Source: "go-pkg"},
	}

	// KeywordRetriever: BM25-style full-text search backed by KeywordIndex.
	kwIdx := &mockKeyIdx{corpus: kbDocs}
	kwRetriever := retriever.NewKeywordRetriever(kwIdx)
	kwDocs, _ := kwRetriever.Retrieve(ctx, "go modules", retriever.WithTopK(2))
	fmt.Printf("KeywordRetriever.Retrieve('go modules', TopK=2): %d docs\n", len(kwDocs))
	for _, d := range kwDocs {
		fmt.Printf("  [%s] %s\n", d.Source, truncate(d.Content, 60))
	}

	// SemanticRetriever: embedding + vector search.
	semRetriever := retriever.NewSemanticRetriever(&mockEmbedder{}, &mockVecStore{docs: kbDocs})
	semDocs, _ := semRetriever.Retrieve(ctx, "generics type parameters",
		retriever.WithTopK(2), retriever.WithMinScore(0.5))
	fmt.Printf("\nSemanticRetriever.Retrieve('generics type parameters', TopK=2, MinScore=0.5): %d docs\n", len(semDocs))

	// HybridRetriever: parallel semantic+keyword with RRF merge.
	merger := &retriever.RRFMerger{K: 60, SemanticWeight: 1.0, KeywordWeight: 1.0}
	hybridRetriever := retriever.NewHybridRetriever(semRetriever, kwRetriever, merger)
	hybridDocs, _ := hybridRetriever.Retrieve(ctx, "go generics", retriever.WithTopK(3))
	fmt.Printf("\nHybridRetriever.Retrieve('go generics', TopK=3, RRF K=60): %d docs\n", len(hybridDocs))

	// RAGTool: wraps Retriever as an agent tool; WithFormatter customizes output.
	ragTool := retriever.NewRAGTool(
		"search_knowledge_base",
		"Search the internal Go knowledge base for relevant documentation.",
		hybridRetriever,
		retriever.WithFormatter(retriever.DefaultFormatter{}),
	)
	fmt.Printf("\nRAGTool '%s' schema: %s | required: %v\n",
		ragTool.Name(), ragTool.Schema().Type, ragTool.Schema().Required)

	// Register RAGTool and run an agent query.
	ragReg := buildToolRegistry()
	ragReg.AddTool(ragTool)
	ragAgent := agent.New(
		agent.WithName("rag-demo"),
		agent.WithModel(m),
		agent.WithToolRegistry(ragReg),
		agent.WithMaxIterations(6),
		agent.WithTimeout(90*time.Second),
	)
	r14, err := ragAgent.Run(ctx, "What do you know about Go modules from the knowledge base?")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Printf("RAG answer: %s\n", truncate(r14.Answer, 150))
	}

	// ═══════════════════════════════════════════════════════════════════════════
	// Demo 15: LocalRuntime — runtime.NewLocal as a sandboxed execution tool
	// Covers: runtime.Runtime interface, NewLocal, Exec, ExecOutput fields,
	//         wrapping runtime as an agent tool.
	// ═══════════════════════════════════════════════════════════════════════════
	section("Demo 15: LocalRuntime — Shell Command Execution")

	rt := runtime.NewLocal(os.TempDir())
	out15, err := rt.Exec(ctx, "echo", "hello from LocalRuntime")
	if err != nil {
		fmt.Printf("Exec error: %v\n", err)
	} else {
		fmt.Printf("LocalRuntime.Exec stdout: %q  exit=%d\n",
			strings.TrimSpace(out15.Stdout), out15.ExitCode)
	}
	_ = rt.Close()

	// Wrap LocalRuntime as a custom agent tool.
	rt2 := runtime.NewLocal(os.TempDir())
	rtReg := buildToolRegistry()
	rtReg.Register("run_shell", "Execute a shell command in the sandbox", struct {
		Cmd string `json:"cmd" description:"Shell command to run" required:"true"`
	}{}, func(input map[string]any) (string, error) {
		cmd, _ := input["cmd"].(string)
		res, err := rt2.Exec(ctx, "sh", "-c", cmd)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("exit=%d\n%s%s", res.ExitCode, res.Stdout, res.Stderr), nil
	})

	rtAgent := agent.New(
		agent.WithName("runtime-demo"),
		agent.WithModel(m),
		agent.WithToolRegistry(rtReg),
		agent.WithMaxIterations(5),
		agent.WithTimeout(60*time.Second),
	)
	r15, err := rtAgent.Run(ctx, "Run 'date' command and tell me the result.")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Printf("Runtime agent answer: %s\n", truncate(r15.Answer, 120))
	}

	// ═══════════════════════════════════════════════════════════════════════════
	// Demo 16: Custom Model — model.WrapFunc (stub model for testing/prototyping)
	// Covers: WrapFunc, GenerateFunc signature, ChatResponse, Usage fields.
	// The stub model returns deterministic answers without real API calls.
	// ═══════════════════════════════════════════════════════════════════════════
	section("Demo 16: Custom Model — model.WrapFunc Stub")
	fmt.Println("WrapFunc wraps a plain Go function as a ChatModel — ideal for unit tests")
	fmt.Println()

	stubModel := model.WrapFunc(func(_ context.Context, messages []model.ChatMessage, _ ...model.Option) (*model.ChatResponse, error) {
		last := messages[len(messages)-1]
		return &model.ChatResponse{
			Message: model.ChatMessage{
				Role:    model.RoleAssistant,
				Content: fmt.Sprintf("Stub reply to: %q", truncate(last.Content, 40)),
			},
			Usage: model.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		}, nil
	})

	stubAgent := agent.New(
		agent.WithName("stub-model-demo"),
		agent.WithModel(stubModel),
		agent.WithMaxIterations(1),
		agent.WithTimeout(10*time.Second),
	)
	r16, err := stubAgent.Run(ctx, "Hello, stub model!")
	if err != nil {
		fmt.Printf("Stub model error: %v\n", err)
	} else {
		fmt.Printf("Stub model reply: %s\n", r16.Answer)
		fmt.Printf("Usage (from stub): prompt=%d completion=%d total=%d\n",
			r16.Usage.PromptTokens, r16.Usage.CompletionTokens, r16.Usage.TotalTokens)
	}

	// ═══════════════════════════════════════════════════════════════════════════
	// Demo 17: MCP Tool — tool/mcp.NewTool construction and registration
	// Covers: mcp.NewTool, mcp.WithDescription, mcp.WithSchema, AddTool.
	// MCPTool proxies calls to a remote MCP-compatible HTTP server.
	// ═══════════════════════════════════════════════════════════════════════════
	section("Demo 17: MCP Tool — tool/mcp.NewTool")

	mcpTool := mcp.NewTool(
		"http://localhost:8080",
		"web_search",
		mcp.WithDescription("Search the web via a remote MCP server"),
		mcp.WithSchema(&tool.SchemaProperty{
			Type: "object",
			Properties: map[string]*tool.SchemaProperty{
				"query": {Type: "string", Description: "Search query string"},
			},
			Required: []string{"query"},
		}),
	)
	mcpReg := tool.NewToolRegistry()
	mcpReg.AddTool(mcpTool)
	fmt.Printf("MCPTool '%s' registered → server: http://localhost:8080\n", mcpTool.Name())
	fmt.Printf("  Description: %s\n", mcpTool.Description())
	fmt.Printf("  Schema.Type: %s  Required: %v\n", mcpTool.Schema().Type, mcpTool.Schema().Required)
	fmt.Println("  (Execution requires a running MCP server at the configured URL)")

	// ═══════════════════════════════════════════════════════════════════════════
	// Demo 18: Memory Utilities — PruneConsecutiveFailures, CompactStaleToolResults,
	//          TruncateToolResult, InMemoryContentStore
	// Covers: all low-level memory helpers used internally by the agent loop.
	// ═══════════════════════════════════════════════════════════════════════════
	section("Demo 18: Memory Utilities")

	// PruneConsecutiveFailures: collapses repeated (assistant tool-call → tool error) pairs
	// to their last occurrence, preventing the model from seeing redundant failure loops.
	failMsgs := []model.ChatMessage{
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "c1", Name: "search", Arguments: map[string]any{"query": "go"}}}},
		{Role: model.RoleTool, Content: "error: rate limit exceeded", ToolCallID: "c1", Name: "search"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "c2", Name: "search", Arguments: map[string]any{"query": "go"}}}},
		{Role: model.RoleTool, Content: "error: rate limit exceeded", ToolCallID: "c2", Name: "search"},
		{Role: model.RoleAssistant, Content: "I encountered errors. Let me try differently."},
	}
	pruned := memory.PruneConsecutiveFailures(failMsgs)
	fmt.Printf("PruneConsecutiveFailures: %d → %d messages (2 repeated failures → 1)\n",
		len(failMsgs), len(pruned))

	// CompactStaleToolResults: clears old tool message content beyond a token budget,
	// keeping only the most recent results in full to control context size.
	toolMsgs := []model.ChatMessage{
		{Role: model.RoleTool, Content: strings.Repeat("old result data. ", 50), Name: "tool_a"},
		{Role: model.RoleTool, Content: strings.Repeat("old result data. ", 50), Name: "tool_b"},
		{Role: model.RoleTool, Content: "recent short result", Name: "tool_c"},
	}
	compacted := memory.CompactStaleToolResults(toolMsgs, 100)
	cleared := 0
	for _, msg := range compacted {
		if msg.Content == "[Old tool result content cleared]" {
			cleared++
		}
	}
	fmt.Printf("CompactStaleToolResults(budget=100 tokens): %d messages, %d stale results cleared\n",
		len(compacted), cleared)

	// TruncateToolResult: if a tool result exceeds maxLen, stores the full content in
	// ContentStore (keyed by call ID) and returns a truncated version with a retrieval hint.
	csDemo := memory.NewInMemoryContentStore()
	largeResult := strings.Repeat("line of data. ", 100) // ~1400 chars
	truncatedResult := memory.TruncateToolResult(ctx, largeResult, 200, csDemo, "call-xyz-001")
	fmt.Printf("TruncateToolResult(maxLen=200): original=%d chars, truncated=%d chars\n",
		len(largeResult), len(truncatedResult))
	fullContent, err := csDemo.Load(ctx, "call-xyz-001")
	if err != nil {
		fmt.Printf("  ContentStore.Load error: %v\n", err)
	} else {
		fmt.Printf("  ContentStore.Load('call-xyz-001'): %d chars retrieved ✓\n", len(fullContent))
	}

	// ═══════════════════════════════════════════════════════════════════════════
	// Demo 19: Multi-Provider — Anthropic + OpenAI with custom client options
	// Covers: anthropic.New, anthropic.WithAPIVersion, anthropic.WithHTTPClient,
	//         openai.WithBaseURL (Azure/Ollama compat), openai.WithHTTPClient,
	//         openai.WithTimeout.
	// ═══════════════════════════════════════════════════════════════════════════
	section("Demo 19: Multi-Provider — Anthropic + OpenAI custom options")

	// Anthropic provider — drop-in model.ChatModel implementation.
	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")
	if anthropicKey == "" {
		anthropicKey = "sk-ant-placeholder"
	}
	claudeModel := anthropic.New(
		"claude-3-5-sonnet-20241022",
		anthropicKey,
		anthropic.WithAPIVersion("2023-06-01"),
		anthropic.WithHTTPClient(&http.Client{Timeout: 30 * time.Second}),
	)
	claudeSet := os.Getenv("ANTHROPIC_API_KEY") != ""
	fmt.Printf("anthropic.New(claude-3-5-sonnet-20241022) — ANTHROPIC_API_KEY %s\n",
		map[bool]string{true: "set ✓", false: "not set (placeholder used)"}[claudeSet])
	fmt.Printf("  WithAPIVersion('2023-06-01'), WithHTTPClient(timeout=30s)\n")
	_ = claudeModel

	// OpenAI with custom base URL — compatible with Azure OpenAI, Ollama, LM Studio.
	customHTTP := &http.Client{Timeout: 45 * time.Second}
	azureModel := openai.New(
		"gpt-4o",
		"azure-key-placeholder",
		openai.WithBaseURL("https://my-resource.openai.azure.com/openai/deployments/gpt-4o"),
		openai.WithHTTPClient(customHTTP),
		openai.WithTimeout(45*time.Second),
	)
	fmt.Printf("openai.New (Azure endpoint): WithBaseURL, WithHTTPClient, WithTimeout\n")
	_ = azureModel

	// OpenAI pointing at a local Ollama server (no key needed).
	ollamaModel := openai.New(
		"llama3.2",
		"ollama",
		openai.WithBaseURL("http://localhost:11434/v1"),
	)
	fmt.Printf("openai.New (Ollama local): base=http://localhost:11434/v1, model=llama3.2\n")
	_ = ollamaModel

	// ═══════════════════════════════════════════════════════════════════════════
	// Demo 20: API Completeness — remaining constructor options + session variants
	// Covers: WithMemory (fixed shared), WithContentStore (fixed shared),
	//         WithTools (individual tool.Tool), WithHooks (plural), WithExecutionMode
	//         constructor option, NewSession (anonymous), Session.Messages,
	//         interrupter.WithWaitTimeout, HITL InterruptType coverage.
	// ═══════════════════════════════════════════════════════════════════════════
	section("Demo 20: API Completeness — Remaining Constructor Options")

	// WithMemory: single shared Memory instance (vs per-session factory).
	fixedMem := memory.NewBuffer(100)
	_ = agent.New(
		agent.WithModel(stubModel),
		agent.WithMemory(fixedMem),
		agent.WithMaxIterations(3),
	)
	fmt.Println("WithMemory(fixed BufferMemory) — all sessions share one Memory instance")

	// WithContentStore: single shared ContentStore (vs per-session factory).
	fixedCS := memory.NewInMemoryContentStore()
	_ = agent.New(
		agent.WithModel(stubModel),
		agent.WithContentStore(fixedCS),
		agent.WithMaxIterations(3),
	)
	fmt.Println("WithContentStore(fixed InMemoryContentStore) — all sessions share one store")

	// WithTools: add individual tool.Tool implementations (no registry required).
	shellTool := &builtin.ShellTool{}
	readTool := &builtin.ReadTool{}
	toolsAgent := agent.New(
		agent.WithModel(stubModel),
		agent.WithTools(shellTool, readTool),
		agent.WithMaxIterations(3),
	)
	fmt.Printf("WithTools: registered builtin.ShellTool('%s') + builtin.ReadTool('%s')\n",
		shellTool.Name(), readTool.Name())
	_ = toolsAgent

	// WithHooks: register multiple hooks in a single call.
	h1 := &eventCounter{}
	h2 := hook.NewLoggerWithPrefix(os.Stdout, "[h2-demo]")
	multiHookAgent := agent.New(
		agent.WithModel(stubModel),
		agent.WithHooks(h1, h2),
		agent.WithMaxIterations(3),
	)
	fmt.Printf("WithHooks: registered %T and %T simultaneously\n", h1, h2)
	_ = multiHookAgent

	// WithExecutionMode: set execution mode at construction time (constructor option,
	// not just via SetExecutionMode at runtime).
	psCtorAgent := agent.New(
		agent.WithName("plan-and-solve-ctor"),
		agent.WithModel(m),
		agent.WithToolRegistry(reg),
		agent.WithExecutionMode(planner.ModePlanAndSolve),
		agent.WithMaxIterations(8),
		agent.WithTimeout(60*time.Second),
	)
	fmt.Println("WithExecutionMode(ModePlanAndSolve) — set at construction time via option")
	_ = psCtorAgent

	// NewSession: create an anonymous session (auto-generated UUID-style ID).
	anonSess := a.NewSession()
	fmt.Printf("NewSession() anonymous ID: %s...\n", anonSess.ID()[:12])

	// Session.Messages: inspect conversation history programmatically.
	anonMsgs, _ := anonSess.Messages(ctx)
	fmt.Printf("Session.Messages() on empty session: %d messages\n", len(anonMsgs))

	// interrupter.WithWaitTimeout: HITL callback timeout configuration.
	timedHITL := interrupter.NewHITL(
		func(event interrupter.InterruptEvent) (*interrupter.HumanResponse, error) {
			return &interrupter.HumanResponse{Approved: true}, nil
		},
		interrupter.WithRequireApproval("deploy_app"),
		interrupter.WithAutoApproveRead(true),
		interrupter.WithWaitTimeout(30*time.Second),
	)
	fmt.Printf("interrupter.NewHITL: WithRequireApproval + WithAutoApproveRead + WithWaitTimeout(30s)\n")
	_ = timedHITL

	// Snapshot: show full AgentSnapshot fields including PendingAction + StepResults.
	fullSnap := &interrupter.AgentSnapshot{
		RunID:     "run-full-demo",
		Iteration: 2,
		Messages: []model.ChatMessage{
			{Role: model.RoleUser, Content: "Deploy v3.0.0"},
			{Role: model.RoleAssistant, Content: "Checking version first..."},
		},
		PendingAction: &planner.Action{
			Type:      planner.ActionToolCall,
			ToolName:  "check_version",
			ToolInput: map[string]any{},
		},
		TokensUsed: 420,
		StepResults: []planner.StepResult{
			{
				Action: planner.Action{
					Type:      planner.ActionToolCall,
					ToolName:  "check_version",
					ToolInput: map[string]any{},
				},
				Output: "v2.9.1",
				Error:  nil,
			},
		},
	}
	store20 := interrupter.NewMemoryStore()
	_ = store20.Save(ctx, fullSnap.RunID, fullSnap)
	loaded20, _ := store20.Load(ctx, fullSnap.RunID)
	b20, _ := loaded20.Serialize()
	fmt.Printf("AgentSnapshot full fields: run_id=%s iter=%d pending_action=%s tokens=%d steps=%d json=%d bytes\n",
		loaded20.RunID, loaded20.Iteration,
		loaded20.PendingAction.ToolName,
		loaded20.TokensUsed,
		len(loaded20.StepResults),
		len(b20))

	// ═══════════════════════════════════════════════════════════════════════════
	// Summary
	// ═══════════════════════════════════════════════════════════════════════════
	section("Summary — Hook Event Counter")
	fmt.Printf("Total events captured: %d\n", counter.total)
	fmt.Printf("  PlanStart:       %d\n", counter.planStart)
	fmt.Printf("  PlanDone:        %d\n", counter.planDone)
	fmt.Printf("  ToolCallStart:   %d\n", counter.toolStart)
	fmt.Printf("  ToolCallDone:    %d\n", counter.toolDone)
	fmt.Printf("  EvalStart:       %d\n", counter.evalStart)
	fmt.Printf("  EvalDone:        %d\n", counter.evalDone)
	fmt.Printf("  LoopComplete:    %d\n", counter.loopComplete)
	fmt.Printf("  RuleView:        %d\n", counter.ruleView)
	fmt.Printf("  SkillCallStart:  %d\n", counter.skillCallStart)
	fmt.Printf("  SkillCallDone:   %d\n", counter.skillCallDone)
	fmt.Printf("  SkillContextLog: %d\n", counter.skillContextLog)
	fmt.Printf("  StreamChunk:     %d\n", counter.streamChunk)
	fmt.Printf("  Errors:          %d\n", counter.errors)
}

// ── Tool Registry Builder ─────────────────────────────────────────────────────

func buildToolRegistry() *tool.ToolRegistry {
	reg := tool.NewToolRegistry()

	reg.Register("get_weather", "Get current weather for a city", struct {
		City string `json:"city" description:"City name" required:"true"`
	}{}, func(input map[string]any) (string, error) {
		city, _ := input["city"].(string)
		weathers := map[string]string{
			"shanghai": "Shanghai: Sunny, 26°C, humidity 55%",
			"tokyo":    "Tokyo: Cloudy, 22°C, humidity 70%",
			"beijing":  "Beijing: Clear, 30°C, humidity 35%",
		}
		if w, ok := weathers[strings.ToLower(city)]; ok {
			return w, nil
		}
		return fmt.Sprintf("%s: Partly cloudy, 20°C, humidity 60%%", city), nil
	})

	reg.Register("search", "Search the web for information", struct {
		Query string `json:"query" description:"Search query" required:"true"`
	}{}, func(input map[string]any) (string, error) {
		query, _ := input["query"].(string)
		return fmt.Sprintf("Search results for '%s':\n1. Go 1.24 released Feb 2025 with improved performance\n2. Go modules are the standard dependency management\n3. Go 1.24 adds weak pointers and finalizer improvements", query), nil
	})

	reg.Register("calculate", "Evaluate a math expression", struct {
		Expression string `json:"expression" description:"Math expression" required:"true"`
	}{}, func(input map[string]any) (string, error) {
		expr, _ := input["expression"].(string)
		results := map[string]string{
			"26 - 22":    "4",
			"1.22 * 100": "122",
		}
		if r, ok := results[expr]; ok {
			return r, nil
		}
		return fmt.Sprintf("Result of '%s' = 42 (mock)", expr), nil
	})

	reg.Register("check_version", "Check current deployed version", struct{}{},
		func(_ map[string]any) (string, error) { return "v2.0.9", nil })

	reg.Register("run_build", "Build the application", struct {
		Version string `json:"version" description:"Target version" required:"true"`
	}{}, func(input map[string]any) (string, error) {
		v, _ := input["version"].(string)
		return fmt.Sprintf("Build %s succeeded (12s)", v), nil
	})

	reg.Register("deploy_app", "Deploy the application to production", struct {
		Version string `json:"version" description:"Version to deploy" required:"true"`
	}{}, func(input map[string]any) (string, error) {
		v, _ := input["version"].(string)
		return fmt.Sprintf("Deployed %s to production successfully", v), nil
	})

	return reg
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func section(title string) {
	bar := strings.Repeat("═", 64)
	fmt.Printf("\n%s\n  %s\n%s\n\n", bar, title, bar)
}

func printResult(r *agent.AgentResult) {
	fmt.Printf("SessionID: %s\n", r.SessionID)
	fmt.Printf("Duration:  %s\n", r.Duration.Round(time.Millisecond))
	if r.Error != nil {
		fmt.Printf("Error:     %v\n", r.Error)
	}
	fmt.Printf("Answer:\n%s\n", r.Answer)
}

func lastMsgRole(msgs []model.ChatMessage) string {
	if len(msgs) == 0 {
		return "<none>"
	}
	return string(msgs[len(msgs)-1].Role)
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func mustDemoDir() string {
	_, file, _, ok := goruntime.Caller(0)
	if !ok {
		panic("failed to resolve demo directory")
	}
	return filepath.Dir(file)
}

func runWeatherDemo(a *agent.Agent, ctx context.Context) (*agent.AgentResult, error) {
	prompt := "Use the get_weather tool for both Shanghai and Tokyo, then use the calculate tool to compute the temperature difference between them. After both tool calls complete, summarize which city is warmer."
	r, err := a.Run(ctx, prompt)
	if err != nil {
		return nil, err
	}
	if usedAllTools(r.Messages, "get_weather", "calculate") {
		return r, nil
	}

	fmt.Println("DeepSeek skipped the expected tool calls on the first try; retrying with a stricter instruction.")
	retryPrompt := "You must call get_weather for Shanghai and Tokyo and must call calculate for the temperature difference before answering. Do not ask follow-up questions and do not answer from memory."
	r, err = a.Run(ctx, retryPrompt, r.SessionID)
	if err != nil {
		return nil, err
	}
	if !usedAllTools(r.Messages, "get_weather", "calculate") {
		return nil, fmt.Errorf("weather demo did not trigger both get_weather and calculate")
	}
	return r, nil
}

func runToolForcedDemo(a *agent.Agent, ctx context.Context, toolName, prompt, retryPrompt string) (*agent.AgentResult, error) {
	prompts := []string{prompt, retryPrompt, retryPrompt}
	var r *agent.AgentResult
	var err error
	for i, p := range prompts {
		r, err = a.Run(ctx, p, sessionIDOrEmpty(r)...)
		if err != nil {
			return nil, err
		}
		if usedTool(r.Messages, toolName) {
			return r, nil
		}
		if i < len(prompts)-1 {
			fmt.Printf("%s was not called on attempt %d; retrying with a stricter instruction.\n", toolName, i+1)
		}
	}
	return nil, fmt.Errorf("%s was not called after %d attempts", toolName, len(prompts))
}

func sessionIDOrEmpty(r *agent.AgentResult) []string {
	if r == nil || r.SessionID == "" {
		return nil
	}
	return []string{r.SessionID}
}

func requirePromptContains(prompt string, needles ...string) error {
	for _, needle := range needles {
		if !strings.Contains(prompt, needle) {
			return fmt.Errorf("system prompt missing expected fragment %q", needle)
		}
	}
	return nil
}

func requirePromptNotContains(prompt string, needles ...string) error {
	for _, needle := range needles {
		if strings.Contains(prompt, needle) {
			return fmt.Errorf("system prompt still contains fragment %q", needle)
		}
	}
	return nil
}

func usedAllTools(msgs []model.ChatMessage, names ...string) bool {
	seen := make(map[string]bool, len(names))
	for _, msg := range msgs {
		for _, tc := range msg.ToolCalls {
			seen[tc.Name] = true
		}
		if msg.Role == model.RoleTool && msg.Name != "" {
			seen[msg.Name] = true
		}
	}
	for _, name := range names {
		if !seen[name] {
			return false
		}
	}
	return true
}

func usedTool(msgs []model.ChatMessage, name string) bool {
	return usedAllTools(msgs, name)
}

// ── eventCounter — custom Hook tracking all event types ───────────────────────

type eventCounter struct {
	total           int
	planStart       int
	planDone        int
	toolStart       int
	toolDone        int
	evalStart       int
	evalDone        int
	loopComplete    int
	ruleView        int
	skillCallStart  int
	skillCallDone   int
	skillContextLog int
	streamChunk     int
	errors          int
}

func (c *eventCounter) OnEvent(_ context.Context, e hook.Event) error {
	c.total++
	switch e.Type {
	case hook.EventPlanStart:
		c.planStart++
	case hook.EventPlanDone:
		c.planDone++
	case hook.EventToolCallStart:
		c.toolStart++
	case hook.EventToolCallDone:
		c.toolDone++
	case hook.EventEvalStart:
		c.evalStart++
	case hook.EventEvalDone:
		c.evalDone++
	case hook.EventLoopComplete:
		c.loopComplete++
	case hook.EventRuleView:
		c.ruleView++
	case hook.EventSkillCallStart:
		c.skillCallStart++
	case hook.EventSkillCallDone:
		c.skillCallDone++
	case hook.EventSkillContextLog:
		c.skillContextLog++
	case hook.EventStreamChunk:
		c.streamChunk++
	case hook.EventError:
		c.errors++
	}
	return nil
}

// ── streamPrinter — counts streaming chunks surfaced via Hook ────────────────

type streamPrinter struct {
	chunks int
}

func (p *streamPrinter) OnEvent(_ context.Context, e hook.Event) error {
	if e.Type == hook.EventStreamChunk {
		p.chunks++
	}
	return nil
}

// ── Retriever mocks for the RAG demo ─────────────────────────────────────────

type mockKeyIdx struct {
	corpus []retriever.Document
}

func (m *mockKeyIdx) Search(_ context.Context, query string, topK int) ([]retriever.Document, error) {
	q := strings.ToLower(query)
	var out []retriever.Document
	for _, doc := range m.corpus {
		score := 0.2
		content := strings.ToLower(doc.Content + " " + doc.Source)
		for _, token := range strings.Fields(q) {
			if strings.Contains(content, token) {
				score += 0.4
			}
		}
		if score <= 0.2 {
			continue
		}
		doc.Score = score
		out = append(out, doc)
	}
	if len(out) > topK {
		out = out[:topK]
	}
	return out, nil
}

func (m *mockKeyIdx) Index(_ context.Context, docs []retriever.Document) error {
	m.corpus = append(m.corpus, docs...)
	return nil
}

type mockEmbedder struct{}

func (m *mockEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	out := make([][]float64, 0, len(texts))
	for _, text := range texts {
		lower := strings.ToLower(text)
		out = append(out, []float64{
			float64(len(lower)),
			boolToFloat(strings.Contains(lower, "go")),
			boolToFloat(strings.Contains(lower, "module")),
			boolToFloat(strings.Contains(lower, "generic")),
		})
	}
	return out, nil
}

type mockVecStore struct {
	docs []retriever.Document
}

func (m *mockVecStore) Search(_ context.Context, vector []float64, topK int) ([]retriever.Document, error) {
	var out []retriever.Document
	for _, doc := range m.docs {
		content := strings.ToLower(doc.Content + " " + doc.Source)
		score := 0.3
		if len(vector) > 1 && vector[1] > 0 && strings.Contains(content, "go") {
			score += 0.3
		}
		if len(vector) > 2 && vector[2] > 0 && strings.Contains(content, "module") {
			score += 0.3
		}
		if len(vector) > 3 && vector[3] > 0 && strings.Contains(content, "generic") {
			score += 0.3
		}
		doc.Score = score
		out = append(out, doc)
	}
	if len(out) > topK {
		out = out[:topK]
	}
	return out, nil
}

func boolToFloat(v bool) float64 {
	if v {
		return 1
	}
	return 0
}
