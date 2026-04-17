package builtin

import (
	"context"
	"errors"

	"simple-agent-framework/tool"
)

type SkillCallTool struct {
	executeFn func(ctx context.Context, name, input string) (string, error)
}

var _ tool.Tool = (*SkillCallTool)(nil)

func NewSkillCallTool(executeFn func(ctx context.Context, name, input string) (string, error)) *SkillCallTool {
	return &SkillCallTool{executeFn: executeFn}
}

func (t *SkillCallTool) Name() string {
	return "skill_call"
}

func (t *SkillCallTool) Description() string {
	return "按名称调用 Skill 并传入输入文本"
}

func (t *SkillCallTool) Schema() *tool.SchemaProperty {
	return &tool.SchemaProperty{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"name":  {Type: "string", Description: "Skill 名称"},
			"input": {Type: "string", Description: "传给 Skill 的输入"},
		},
		Required: []string{"name", "input"},
	}
}

func (t *SkillCallTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	name, _ := input["name"].(string)
	if name == "" {
		return "", errors.New("name is required")
	}
	in, ok := input["input"].(string)
	if !ok {
		return "", errors.New("input is required")
	}
	if t.executeFn == nil {
		return "", errors.New("skill execution is not configured")
	}
	return t.executeFn(ctx, name, in)
}
