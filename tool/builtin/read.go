package builtin

import (
	"context"
	"errors"
	"os"

	"github.com/LYD99/simple-agent-framework/tool"
)

type ReadTool struct{}

var _ tool.Tool = (*ReadTool)(nil)

func (t *ReadTool) Name() string {
	return "read_file"
}

func (t *ReadTool) Description() string {
	return "Read the content of a file at the given path."
}

func (t *ReadTool) Schema() *tool.SchemaProperty {
	return &tool.SchemaProperty{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"path": {Type: "string", Description: "File path to read."},
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
