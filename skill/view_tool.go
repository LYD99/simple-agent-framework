package skill

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/LYD99/simple-agent-framework/tool"
)

type SkillViewTool struct {
	basePath string
}

var _ tool.Tool = (*SkillViewTool)(nil)

func NewSkillViewTool(basePath string) *SkillViewTool {
	return &SkillViewTool{basePath: filepath.Clean(basePath)}
}

func (t *SkillViewTool) Name() string        { return "skill_view" }
func (t *SkillViewTool) Description() string { return "读取当前 Skill 目录下的文件" }

func (t *SkillViewTool) Schema() *tool.SchemaProperty {
	return &tool.SchemaProperty{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"path": {
				Type:        "string",
				Description: "相对于 Skill 根目录的文件路径",
			},
		},
		Required: []string{"path"},
	}
}

func (t *SkillViewTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	_ = ctx
	raw, ok := input["path"]
	if !ok {
		return "", fmt.Errorf("missing path")
	}
	relPath, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("path must be a string")
	}
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("path must be relative")
	}
	base := filepath.Clean(t.basePath)
	fullPath := filepath.Clean(filepath.Join(base, relPath))
	rel, err := filepath.Rel(base, fullPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path escapes skill directory")
	}
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", relPath, err)
	}
	return string(content), nil
}
