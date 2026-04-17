package evaluator

import (
	"context"

	"simple-agent-framework/model"
)

type Evaluator interface {
	Evaluate(ctx context.Context, state *EvalState) (*EvalResult, error)
}

type EvalState struct {
	Messages    []model.ChatMessage
	StepResults []StepResult
	Iteration   int
}

type StepResult struct {
	ToolName string
	Input    map[string]any
	Output   string
	Error    error
}

type EvalResult struct {
	Decision Decision
	Feedback string
}

type Decision int

const (
	DecisionContinue Decision = iota
	DecisionComplete
	DecisionRetry
	DecisionEscalate
	DecisionReplan
)
