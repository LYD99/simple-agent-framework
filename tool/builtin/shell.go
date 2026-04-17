package builtin

import (
	"context"
	"errors"
	"os/exec"
	"strings"

	"simple-agent-framework/tool"
)

type ShellTool struct{}

var _ tool.Tool = (*ShellTool)(nil)

func (t *ShellTool) Name() string {
	return "shell"
}

func (t *ShellTool) Description() string {
	return "在可选工作目录下执行 Shell 命令，返回标准输出与标准错误合并后的文本"
}

func (t *ShellTool) Schema() *tool.SchemaProperty {
	return &tool.SchemaProperty{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"command": {
				Type:        "string",
				Description: "要执行的命令名或可执行文件路径",
			},
			"args": {
				Type:        "array",
				Description: "命令参数列表",
				Items:       &tool.SchemaProperty{Type: "string"},
			},
			"work_dir": {
				Type:        "string",
				Description: "工作目录，可选",
			},
		},
		Required: []string{"command"},
	}
}

func parseStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, a := range x {
			if s, ok := a.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func (t *ShellTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	command, _ := input["command"].(string)
	command = strings.TrimSpace(command)
	if command == "" {
		return "", errors.New("command is required")
	}
	args := parseStringSlice(input["args"])
	cmd := exec.CommandContext(ctx, command, args...)
	if wd, ok := input["work_dir"].(string); ok && wd != "" {
		cmd.Dir = wd
	}
	out, err := cmd.CombinedOutput()
	s := string(out)
	if err != nil {
		if s != "" {
			return s, err
		}
		return "", err
	}
	return s, nil
}
