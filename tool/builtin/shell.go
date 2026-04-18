package builtin

import (
	"context"
	"errors"
	"os/exec"
	"strings"

	"github.com/LYD99/simple-agent-framework/tool"
)

type ShellTool struct{}

var _ tool.Tool = (*ShellTool)(nil)

func (t *ShellTool) Name() string {
	return "shell"
}

func (t *ShellTool) Description() string {
	return "Execute a shell command (optionally in a given working directory). Returns combined stdout and stderr."
}

func (t *ShellTool) Schema() *tool.SchemaProperty {
	return &tool.SchemaProperty{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"command": {
				Type:        "string",
				Description: "Command name or absolute path of the executable to run.",
			},
			"args": {
				Type:        "array",
				Description: "Arguments to pass to the command.",
				Items:       &tool.SchemaProperty{Type: "string"},
			},
			"work_dir": {
				Type:        "string",
				Description: "Working directory for the command (optional).",
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
