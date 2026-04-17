package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"simple-agent-framework/agent"
	"simple-agent-framework/hook"
	"simple-agent-framework/interrupter"
	"simple-agent-framework/model/provider/openai"
	"simple-agent-framework/tool"
)

func main() {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Println("请设置 OPENAI_API_KEY")
		return
	}

	registry := tool.NewToolRegistry()
	registry.Register("write_file", "写入文件", struct {
		Path    string `json:"path" description:"文件路径" required:"true"`
		Content string `json:"content" description:"文件内容" required:"true"`
	}{}, func(input map[string]interface{}) (string, error) {
		fmt.Printf("[模拟] 写入文件 %s\n", input["path"])
		return "文件写入成功", nil
	})
	registry.Register("read_file", "读取文件", struct {
		Path string `json:"path" description:"文件路径" required:"true"`
	}{}, func(input map[string]interface{}) (string, error) {
		return "文件内容: Hello World", nil
	})

	// HITL: 只对写入操作要求人工审批
	hitl := interrupter.NewHITL(
		func(event interrupter.InterruptEvent) (*interrupter.HumanResponse, error) {
			fmt.Printf("\n⚠️  工具调用需要审批: %s\n", event.Action.ToolName)
			fmt.Printf("   输入: %v\n", event.Action.ToolInput)
			fmt.Print("   是否允许? (y/n): ")

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

	result, err := a.Run(context.Background(), "先读取 config.txt，然后把内容写入 output.txt")
	if err != nil {
		fmt.Printf("错误: %v\n", err)
		return
	}
	fmt.Printf("回答: %s\n", result.Answer)
}
