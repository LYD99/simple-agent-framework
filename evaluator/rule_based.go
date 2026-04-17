package evaluator

import (
	"context"
)

// RuleFunc is a single evaluation rule. Return DecisionContinue to defer to the next rule.
type RuleFunc func(state *EvalState) (*EvalResult, error)

type RuleBasedEvaluator struct {
	rules []RuleFunc
}

func NewRuleBased(rules ...RuleFunc) *RuleBasedEvaluator {
	return &RuleBasedEvaluator{rules: append([]RuleFunc(nil), rules...)}
}

func (e *RuleBasedEvaluator) Evaluate(ctx context.Context, state *EvalState) (*EvalResult, error) {
	_ = ctx
	for _, rule := range e.rules {
		res, err := rule(state)
		if err != nil {
			return nil, err
		}
		if res == nil {
			continue
		}
		if res.Decision != DecisionContinue {
			return res, nil
		}
	}
	return &EvalResult{Decision: DecisionContinue}, nil
}
