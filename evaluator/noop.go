package evaluator

import "context"

type NoopEvaluator struct{}

func (n *NoopEvaluator) Evaluate(ctx context.Context, state *EvalState) (*EvalResult, error) {
	_ = ctx
	_ = state
	return &EvalResult{Decision: DecisionContinue}, nil
}
