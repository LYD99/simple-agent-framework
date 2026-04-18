package builtin

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/LYD99/simple-agent-framework/tool"
)

type WriteTool struct{}

var _ tool.Tool = (*WriteTool)(nil)

func (t *WriteTool) Name() string {
	return "write_file"
}

func (t *WriteTool) Description() string {
	return "写入文件内容，若父目录不存在则自动创建"
}

func (t *WriteTool) Schema() *tool.SchemaProperty {
	return &tool.SchemaProperty{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"path":    {Type: "string", Description: "文件路径"},
			"content": {Type: "string", Description: "文件内容"},
		},
		Required: []string{"path", "content"},
	}
}

func (t *WriteTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	path, _ := input["path"].(string)
	if path == "" {
		return "", errors.New("path is required")
	}
	content, ok := input["content"].(string)
	if !ok {
		return "", errors.New("content is required")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return "", nil
}
