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
		fmt.Println("Please set the OPENAI_API_KEY environment variable.")
		return
	}

	registry := tool.NewToolRegistry()
	registry.Register("get_weather", "Get the weather for a given city.", struct {
		City string `json:"city" description:"City name" required:"true"`
	}{}, func(input map[string]interface{}) (string, error) {
		city := input["city"].(string)
		return fmt.Sprintf("%s: sunny, 25°C", city), nil
	})

	a := agent.New(
		agent.WithModel(openai.New("gpt-4o", apiKey)),
		agent.WithToolRegistry(registry),
		agent.WithMaxIterations(5),
		agent.WithSystemPrompt("You are a helpful assistant."),
	)

	result, err := a.Run(context.Background(), "What is the weather in Shanghai today?")
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}
	fmt.Printf("answer: %s\n", result.Answer)
}
