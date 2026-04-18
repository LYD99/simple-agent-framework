package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LYD99/simple-agent-framework/model"
)

type ActionPlan struct {
	Steps       []PlanStep
	CurrentStep int
}

type PlanStep struct {
	StepID      int
	Description string
	Action      Action
	Status      StepStatus
	Result      string
}

type StepStatus int

const (
	StepPending StepStatus = iota
	StepRunning
	StepDone
	StepFailed
	StepSkipped
)

const (
	defaultPlanPrompt  = `You break down the user task into an ordered list of steps. Each step either calls one tool by exact name from the provided list, asks the user a question, or states a final answer. Respond with ONE JSON object only, no markdown fences. Schema: {"steps":[{"step_id":number,"description":string,"type":"tool_call"|"final_answer"|"ask_human","tool_name":string,"tool_input":object,"answer":string}]}. Use type tool_call with tool_name and tool_input; type final_answer with answer; type ask_human with answer as the question. Omit unused fields.`
	defaultSolvePrompt = `Execute steps in order. tool_input must be a JSON object matching the tool schema.`
)

type PlanAndSolvePlanner struct {
	model       model.ChatModel
	planPrompt  string
	solvePrompt string
	plan        *ActionPlan
}

type PlanAndSolveOption func(*PlanAndSolvePlanner)

func WithPlanPrompt(p string) PlanAndSolveOption {
	return func(x *PlanAndSolvePlanner) { x.planPrompt = p }
}

func WithSolvePrompt(p string) PlanAndSolveOption {
	return func(x *PlanAndSolvePlanner) { x.solvePrompt = p }
}

func NewPlanAndSolve(m model.ChatModel, opts ...PlanAndSolveOption) *PlanAndSolvePlanner {
	p := &PlanAndSolvePlanner{model: m}
	for _, o := range opts {
		o(p)
	}
	return p
}

func (p *PlanAndSolvePlanner) Plan(ctx context.Context, state *PlanState) (*PlanResult, error) {
	if p.model == nil {
		return nil, fmt.Errorf("plan-and-solve: model is nil")
	}
	if p.plan == nil {
		if err := p.generatePlan(ctx, state); err != nil {
			return nil, err
		}
	}
	if rs := p.runningStep(); rs != nil {
		return nil, fmt.Errorf("plan-and-solve: step %d still running; call MarkStepDone or MarkStepFailed first", rs.StepID)
	}
	step := p.nextStep()
	if step == nil {
		return &PlanResult{
			Action:    Action{Type: ActionFinalAnswer, Answer: p.summarizeResults()},
			Reasoning: "",
		}, nil
	}
	step.Status = StepRunning
	for i := range p.plan.Steps {
		if &p.plan.Steps[i] == step {
			p.plan.CurrentStep = i
			break
		}
	}
	return &PlanResult{Action: step.Action, Reasoning: step.Description}, nil
}

func (p *PlanAndSolvePlanner) generatePlan(ctx context.Context, state *PlanState) error {
	return p.generatePlanWithNotes(ctx, state, "")
}

func (p *PlanAndSolvePlanner) generatePlanWithNotes(ctx context.Context, state *PlanState, completedNotes string) error {
	pp := strings.TrimSpace(p.planPrompt)
	if pp == "" {
		pp = defaultPlanPrompt
	}
	sp := strings.TrimSpace(p.solvePrompt)
	if sp == "" {
		sp = defaultSolvePrompt
	}
	sys := pp + "\n\n" + sp
	if completedNotes != "" {
		sys += "\n\nCompleted work and results (respect and build on this):\n" + completedNotes
	}
	toolBlock := formatToolsForPlan(state.Tools)
	user := toolBlock
	if len(state.Messages) > 0 {
		var b strings.Builder
		for _, m := range state.Messages {
			b.WriteString(string(m.Role))
			b.WriteString(": ")
			b.WriteString(m.Content)
			b.WriteByte('\n')
		}
		user = b.String() + "\n" + toolBlock
	}
	msgs := []model.ChatMessage{
		{Role: model.RoleSystem, Content: sys},
		{Role: model.RoleUser, Content: user},
	}
	resp, err := p.model.Generate(ctx, msgs)
	if err != nil {
		return err
	}
	if resp == nil {
		return fmt.Errorf("plan-and-solve: empty response")
	}
	ap, err := parseActionPlan(resp.Message.Content)
	if err != nil {
		return err
	}
	p.plan = ap
	return nil
}

func formatToolsForPlan(tools []ToolInfo) string {
	if len(tools) == 0 {
		return "Available tools: none."
	}
	var b strings.Builder
	b.WriteString("Available tools (JSON lines):\n")
	for _, t := range tools {
		line := map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"parameters":  t.Schema,
		}
		raw, _ := json.Marshal(line)
		b.Write(raw)
		b.WriteByte('\n')
	}
	return b.String()
}

func (p *PlanAndSolvePlanner) nextStep() *PlanStep {
	if p.plan == nil {
		return nil
	}
	for i := range p.plan.Steps {
		s := &p.plan.Steps[i]
		switch s.Status {
		case StepDone, StepSkipped:
			continue
		case StepFailed:
			return nil
		case StepRunning:
			return nil
		case StepPending:
			return s
		}
	}
	return nil
}

func (p *PlanAndSolvePlanner) runningStep() *PlanStep {
	if p.plan == nil {
		return nil
	}
	for i := range p.plan.Steps {
		if p.plan.Steps[i].Status == StepRunning {
			return &p.plan.Steps[i]
		}
	}
	return nil
}

func (p *PlanAndSolvePlanner) summarizeResults() string {
	if p.plan == nil {
		return ""
	}
	var b strings.Builder
	for _, s := range p.plan.Steps {
		if s.Status == StepDone && strings.TrimSpace(s.Result) != "" {
			fmt.Fprintf(&b, "Step %d (%s): %s\n", s.StepID, s.Description, strings.TrimSpace(s.Result))
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "All planned steps finished."
	}
	return out
}

func (p *PlanAndSolvePlanner) MarkStepDone(stepID int, result string) {
	if p.plan == nil {
		return
	}
	for i := range p.plan.Steps {
		if p.plan.Steps[i].StepID == stepID {
			p.plan.Steps[i].Status = StepDone
			p.plan.Steps[i].Result = result
			return
		}
	}
}

func (p *PlanAndSolvePlanner) MarkStepFailed(stepID int, err error) {
	if p.plan == nil {
		return
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	for i := range p.plan.Steps {
		if p.plan.Steps[i].StepID == stepID {
			p.plan.Steps[i].Status = StepFailed
			p.plan.Steps[i].Result = msg
			return
		}
	}
}

func (p *PlanAndSolvePlanner) Replan(ctx context.Context, state *PlanState) error {
	var notes strings.Builder
	if p.plan != nil {
		for _, s := range p.plan.Steps {
			if s.Status == StepDone {
				fmt.Fprintf(&notes, "step %d (%s) done: %s\n", s.StepID, s.Description, s.Result)
			}
			if s.Status == StepFailed {
				fmt.Fprintf(&notes, "step %d (%s) failed: %s\n", s.StepID, s.Description, s.Result)
			}
		}
	}
	for _, h := range state.History {
		fmt.Fprintf(&notes, "history action=%v output=%s err=%v\n", h.Action.Type, h.Output, h.Error)
	}
	p.plan = nil
	return p.generatePlanWithNotes(ctx, state, strings.TrimSpace(notes.String()))
}

func (p *PlanAndSolvePlanner) CurrentPlan() *ActionPlan { return p.plan }

type planFile struct {
	Steps []rawPlanStep `json:"steps"`
}

type rawPlanStep struct {
	StepID       int             `json:"step_id"`
	Description  string          `json:"description"`
	Type         string          `json:"type"`
	ToolName     string          `json:"tool_name"`
	ToolInput    json.RawMessage `json:"tool_input"`
	Answer       string          `json:"answer"`
	NestedAction json.RawMessage `json:"action"`
}

func parseActionPlan(content string) (*ActionPlan, error) {
	raw := extractJSONObject(strings.TrimSpace(content))
	if raw == "" {
		return nil, fmt.Errorf("plan-and-solve: no JSON object in model output")
	}
	var pf planFile
	if err := json.Unmarshal([]byte(raw), &pf); err != nil {
		return nil, fmt.Errorf("plan-and-solve: parse plan json: %w", err)
	}
	if len(pf.Steps) == 0 {
		return nil, fmt.Errorf("plan-and-solve: plan has no steps")
	}
	out := &ActionPlan{Steps: make([]PlanStep, 0, len(pf.Steps))}
	for i, rs := range pf.Steps {
		step := PlanStep{
			StepID:      rs.StepID,
			Description: strings.TrimSpace(rs.Description),
			Status:      StepPending,
		}
		if step.StepID == 0 {
			step.StepID = i + 1
		}
		act, err := rawStepToAction(rs)
		if err != nil {
			return nil, err
		}
		step.Action = act
		out.Steps = append(out.Steps, step)
	}
	return out, nil
}

func rawStepToAction(rs rawPlanStep) (Action, error) {
	if len(rs.NestedAction) > 0 {
		var inner struct {
			Type      string          `json:"type"`
			ToolName  string          `json:"tool_name"`
			ToolInput json.RawMessage `json:"tool_input"`
			Answer    string          `json:"answer"`
		}
		if err := json.Unmarshal(rs.NestedAction, &inner); err == nil && (inner.Type != "" || inner.ToolName != "" || inner.Answer != "") {
			rs.Type = inner.Type
			rs.ToolName = inner.ToolName
			rs.ToolInput = inner.ToolInput
			rs.Answer = inner.Answer
		}
	}
	typ := strings.ToLower(strings.TrimSpace(rs.Type))
	switch typ {
	case "tool_call", "tool", "call", "":
		if strings.TrimSpace(rs.ToolName) != "" || len(rs.ToolInput) > 0 {
			in, err := parseToolInputRaw(rs.ToolInput)
			if err != nil {
				return Action{}, err
			}
			return Action{Type: ActionToolCall, ToolName: strings.TrimSpace(rs.ToolName), ToolInput: in}, nil
		}
	case "final_answer", "answer", "final":
		return Action{Type: ActionFinalAnswer, Answer: strings.TrimSpace(rs.Answer)}, nil
	case "ask_human", "human", "clarify":
		return Action{Type: ActionAskHuman, Answer: strings.TrimSpace(rs.Answer)}, nil
	}
	if strings.TrimSpace(rs.Answer) != "" && rs.ToolName == "" && len(rs.ToolInput) == 0 {
		return Action{Type: ActionFinalAnswer, Answer: strings.TrimSpace(rs.Answer)}, nil
	}
	if strings.TrimSpace(rs.ToolName) != "" {
		in, err := parseToolInputRaw(rs.ToolInput)
		if err != nil {
			return Action{}, err
		}
		return Action{Type: ActionToolCall, ToolName: strings.TrimSpace(rs.ToolName), ToolInput: in}, nil
	}
	return Action{}, fmt.Errorf("plan-and-solve: cannot map step to action (type=%q)", rs.Type)
}

func parseToolInputRaw(raw json.RawMessage) (map[string]any, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err == nil {
		return m, nil
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		str = strings.TrimSpace(str)
		if str == "" {
			return map[string]any{}, nil
		}
		if err := json.Unmarshal([]byte(str), &m); err == nil {
			return m, nil
		}
		return map[string]any{"input": str}, nil
	}
	return map[string]any{"_raw": string(raw)}, nil
}

func extractJSONObject(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		if nl := strings.Index(rest, "\n"); nl >= 0 {
			rest = rest[nl+1:]
		}
		if j := strings.Index(rest, "```"); j >= 0 {
			s = strings.TrimSpace(rest[:j])
		}
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return ""
	}
	return s[start : end+1]
}
