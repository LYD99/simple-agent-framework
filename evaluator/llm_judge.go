package evaluator

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"simple-agent-framework/model"
)

const defaultLLMJudgePrompt = `You evaluate agent execution progress. Judge whether the task is on track.

Respond with a single JSON object only (no markdown, no extra text), with exactly these keys:
{"decision":"continue|complete|retry|escalate|replan","feedback":"brief rationale"}

Meanings:
- continue: keep iterating with the current plan
- complete: goal satisfied, stop successfully
- retry: last step should be retried or corrected
- escalate: human or operator help needed
- replan: change strategy or plan before continuing

`

type LLMJudgeEvaluator struct {
	model  model.ChatModel
	prompt string
}

func NewLLMJudge(m model.ChatModel) *LLMJudgeEvaluator {
	return &LLMJudgeEvaluator{model: m, prompt: defaultLLMJudgePrompt}
}

func NewLLMJudgeWithPrompt(m model.ChatModel, prompt string) *LLMJudgeEvaluator {
	return &LLMJudgeEvaluator{model: m, prompt: prompt}
}

func (e *LLMJudgeEvaluator) Evaluate(ctx context.Context, state *EvalState) (*EvalResult, error) {
	if e.model == nil {
		return nil, fmt.Errorf("llm judge: model is nil")
	}
	userPayload, err := buildEvalStatePayload(state)
	if err != nil {
		return nil, err
	}
	msgs := []model.ChatMessage{
		{Role: model.RoleSystem, Content: e.prompt},
		{Role: model.RoleUser, Content: userPayload},
	}
	resp, err := e.model.Generate(ctx, msgs)
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(resp.Message.Content)
	decision, feedback := parseJudgeResponse(raw)
	return &EvalResult{Decision: decision, Feedback: feedback}, nil
}

func buildEvalStatePayload(state *EvalState) (string, error) {
	type stepPayload struct {
		ToolName string         `json:"tool_name"`
		Input    map[string]any `json:"input,omitempty"`
		Output   string         `json:"output,omitempty"`
		Err      string         `json:"error,omitempty"`
	}
	type payload struct {
		Iteration   int                 `json:"iteration"`
		Messages    []model.ChatMessage `json:"messages,omitempty"`
		StepResults []stepPayload       `json:"step_results,omitempty"`
	}
	p := payload{Iteration: 0}
	if state != nil {
		p.Iteration = state.Iteration
		p.Messages = state.Messages
		for _, sr := range state.StepResults {
			sp := stepPayload{ToolName: sr.ToolName, Input: sr.Input, Output: sr.Output}
			if sr.Error != nil {
				sp.Err = sr.Error.Error()
			}
			p.StepResults = append(p.StepResults, sp)
		}
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return "", fmt.Errorf("llm judge: marshal state: %w", err)
	}
	return fmt.Sprintf("Evaluation context (JSON):\n%s", string(b)), nil
}

type judgeJSON struct {
	Decision string `json:"decision"`
	Feedback string `json:"feedback"`
}

var reJSONFence = regexp.MustCompile("(?is)```(?:json)?\\s*([\\s\\S]*?)```")

func parseJudgeResponse(raw string) (Decision, string) {
	candidates := []string{strings.TrimSpace(raw)}
	if m := reJSONFence.FindStringSubmatch(raw); len(m) > 1 {
		candidates = append([]string{strings.TrimSpace(m[1])}, candidates...)
	}
	if i := strings.Index(raw, "{"); i >= 0 {
		if j := strings.LastIndex(raw, "}"); j > i {
			candidates = append(candidates, strings.TrimSpace(raw[i:j+1]))
		}
	}

	for _, c := range candidates {
		var j judgeJSON
		if err := json.Unmarshal([]byte(c), &j); err == nil {
			return mapDecisionString(j.Decision), strings.TrimSpace(j.Feedback)
		}
	}
	return DecisionContinue, strings.TrimSpace(raw)
}

func mapDecisionString(s string) Decision {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "complete", "done", "success", "finished":
		return DecisionComplete
	case "retry", "again":
		return DecisionRetry
	case "escalate", "human", "help":
		return DecisionEscalate
	case "replan", "re-plan":
		return DecisionReplan
	case "continue", "cont", "proceed":
		return DecisionContinue
	default:
		return DecisionContinue
	}
}
