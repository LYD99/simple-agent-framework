package main

import (
	"context"
	"fmt"
	"os"

	"github.com/LYD99/simple-agent-framework/agent"
	"github.com/LYD99/simple-agent-framework/evaluator"
	"github.com/LYD99/simple-agent-framework/hook"
	"github.com/LYD99/simple-agent-framework/model/provider/openai"
	"github.com/LYD99/simple-agent-framework/rule"
	"github.com/LYD99/simple-agent-framework/tool"
)

func main() {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Println("Please set the OPENAI_API_KEY environment variable.")
		return
	}

	m := openai.New("gpt-4o", apiKey)

	registry := tool.NewToolRegistry()
	registry.Register("search", "Search for information.", struct {
		Query string `json:"query" description:"Search keywords" required:"true"`
	}{}, func(input map[string]interface{}) (string, error) {
		return fmt.Sprintf("Search results for %v: ...", input["query"]), nil
	})
	registry.Register("calculator", "Evaluate a math expression.", struct {
		Expression string `json:"expression" description:"Math expression" required:"true"`
	}{}, func(input map[string]interface{}) (string, error) {
		_ = input
		return "42", nil
	})

	safetyRule := rule.NewFileRule(
		"safety",
		"Safety guardrails",
		"Do not execute dangerous operations such as rm -rf, format, or destructive sudo commands.",
		true,
	)

	ruleEval := evaluator.NewRuleBased(func(state *evaluator.EvalState) (*evaluator.EvalResult, error) {
		if state.Iteration >= 3 {
			return &evaluator.EvalResult{Decision: evaluator.DecisionComplete, Feedback: "Enough iterations reached."}, nil
		}
		return &evaluator.EvalResult{Decision: evaluator.DecisionContinue}, nil
	})

	a := agent.New(
		agent.WithModel(m),
		agent.WithToolRegistry(registry),
		agent.WithEvaluator(ruleEval),
		agent.WithRules(safetyRule),
		agent.WithHook(hook.NewLogger(os.Stdout)),
		agent.WithMaxIterations(10),
	)

	result, err := a.Run(context.Background(), "Search for the latest Go 1.24 features and summarize them.")
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}
	fmt.Printf("answer: %s\n", result.Answer)
}
