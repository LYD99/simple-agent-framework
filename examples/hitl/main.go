package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/LYD99/simple-agent-framework/agent"
	"github.com/LYD99/simple-agent-framework/hook"
	"github.com/LYD99/simple-agent-framework/interrupter"
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
	registry.Register("write_file", "Write content to a file.", struct {
		Path    string `json:"path"    description:"File path"    required:"true"`
		Content string `json:"content" description:"File content" required:"true"`
	}{}, func(input map[string]interface{}) (string, error) {
		fmt.Printf("[simulated] wrote file %s\n", input["path"])
		return "file written successfully", nil
	})
	registry.Register("read_file", "Read the content of a file.", struct {
		Path string `json:"path" description:"File path" required:"true"`
	}{}, func(input map[string]interface{}) (string, error) {
		return "file content: Hello World", nil
	})

	// HITL: only write-like tools require human approval.
	hitl := interrupter.NewHITL(
		func(event interrupter.InterruptEvent) (*interrupter.HumanResponse, error) {
			fmt.Printf("\n[!] Tool call requires approval: %s\n", event.Action.ToolName)
			fmt.Printf("    input: %v\n", event.Action.ToolInput)
			fmt.Print("    allow? (y/n): ")

			reader := bufio.NewReader(os.Stdin)
			answer, _ := reader.ReadString('\n')
			approved := strings.TrimSpace(strings.ToLower(answer)) == "y"

			return &interrupter.HumanResponse{Approved: approved, Message: answer}, nil
		},
		interrupter.WithRequireApproval("write_file"),
		interrupter.WithAutoApproveRead(true),
	)

	a := agent.New(
		agent.WithModel(openai.New("gpt-4o", apiKey)),
		agent.WithToolRegistry(registry),
		agent.WithHITL(hitl),
		agent.WithHook(hook.NewLogger(os.Stdout)),
		agent.WithMaxIterations(5),
	)

	result, err := a.Run(context.Background(), "First read config.txt, then write its content into output.txt.")
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}
	fmt.Printf("answer: %s\n", result.Answer)
}
