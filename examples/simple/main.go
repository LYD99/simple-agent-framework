package main

import (
	"context"
	"fmt"
	"os"

	"github.com/LYD99/simple-agent-framework/agent"
	"github.com/LYD99/simple-agent-framework/model/provider/openai"
	"github.com/LYD99/simple-agent-framework/tool"
)

func main() {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Println("请设置 OPENAI_API_KEY 环境变量")
		return
	}

	registry := tool.NewToolRegistry()
	registry.Register("get_weather", "获取城市天气", struct {
		City string `json:"city" description:"城市名称" required:"true"`
	}{}, func(input map[string]interface{}) (string, error) {
		city := input["city"].(string)
		return fmt.Sprintf("%s: 晴天, 25°C", city), nil
	})

	a := agent.New(
		agent.WithModel(openai.New("gpt-4o", apiKey)),
		agent.WithToolRegistry(registry),
		agent.WithMaxIterations(5),
		agent.WithSystemPrompt("你是一个有用的助手。"),
	)

	result, err := a.Run(context.Background(), "上海今天天气怎么样？")
	if err != nil {
		fmt.Printf("错误: %v\n", err)
		return
	}
	fmt.Printf("回答: %s\n", result.Answer)
}
