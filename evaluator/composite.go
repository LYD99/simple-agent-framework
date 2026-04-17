package evaluator

import (
	"context"
	"strings"
)

type CompositeEvaluator struct {
	evaluators []Evaluator
}

func NewComposite(evaluators ...Evaluator) *CompositeEvaluator {
	return &CompositeEvaluator{evaluators: append([]Evaluator(nil), evaluators...)}
}

func decisionPriority(d Decision) int {
	switch d {
	case DecisionComplete:
		return 5
	case DecisionEscalate:
		return 4
	case DecisionRetry:
		return 3
	case DecisionReplan:
		return 2
	case DecisionContinue:
		return 0
	default:
		return 0
	}
}

func (e *CompositeEvaluator) Evaluate(ctx context.Context, state *EvalState) (*EvalResult, error) {
	var parts []string
	best := DecisionContinue
	bestP := 0

	for _, ev := range e.evaluators {
		res, err := ev.Evaluate(ctx, state)
		if err != nil {
			return nil, err
		}
		if res.Feedback != "" {
			parts = append(parts, res.Feedback)
		}
		p := decisionPriority(res.Decision)
		if p > bestP {
			bestP = p
			best = res.Decision
		}
	}

	feedback := strings.Join(parts, "\n")
	return &EvalResult{Decision: best, Feedback: feedback}, nil
}
