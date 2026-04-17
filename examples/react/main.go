package main

import (
	"context"
	"fmt"
	"os"

	"simple-agent-framework/agent"
	"simple-agent-framework/evaluator"
	"simple-agent-framework/hook"
	"simple-agent-framework/model/provider/openai"
	"simple-agent-framework/rule"
	"simple-agent-framework/tool"
)

func main() {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Println("请设置 OPENAI_API_KEY 环境变量")
		return
	}

	m := openai.New("gpt-4o", apiKey)

	registry := tool.NewToolRegistry()
	registry.Register("search", "搜索信息", struct {
		Query string `json:"query" description:"搜索关键词" required:"true"`
	}{}, func(input map[string]interface{}) (string, error) {
		return fmt.Sprintf("搜索结果: %v 相关信息...", input["query"]), nil
	})
	registry.Register("calculator", "数学计算", struct {
		Expression string `json:"expression" description:"数学表达式" required:"true"`
	}{}, func(input map[string]interface{}) (string, error) {
		_ = input
		return "42", nil
	})

	safetyRule := rule.NewFileRule("safety", "安全约束规则", "禁止执行危险操作", true)

	ruleEval := evaluator.NewRuleBased(func(state *evaluator.EvalState) (*evaluator.EvalResult, error) {
		if state.Iteration >= 3 {
			return &evaluator.EvalResult{Decision: evaluator.DecisionComplete, Feedback: "已达到足够迭代"}, nil
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

	result, err := a.Run(context.Background(), "帮我搜索最新的 Go 1.24 新特性，并做个总结")
	if err != nil {
		fmt.Printf("错误: %v\n", err)
		return
	}
	fmt.Printf("回答: %s\n", result.Answer)
}
