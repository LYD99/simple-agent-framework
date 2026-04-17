package builtin

import (
	"context"
	"errors"
	"os"

	"simple-agent-framework/tool"
)

type ReadTool struct{}

var _ tool.Tool = (*ReadTool)(nil)

func (t *ReadTool) Name() string {
	return "read_file"
}

func (t *ReadTool) Description() string {
	return "读取指定路径的文件内容"
}

func (t *ReadTool) Schema() *tool.SchemaProperty {
	return &tool.SchemaProperty{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"path": {Type: "string", Description: "文件路径"},
		},
		Required: []string{"path"},
	}
}

func (t *ReadTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	path, _ := input["path"].(string)
	if path == "" {
		return "", errors.New("path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
