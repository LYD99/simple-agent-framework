package builtin

import (
	"context"
	"errors"

	"github.com/LYD99/simple-agent-framework/tool"
)

type RuleViewTool struct {
	lookupFn func(name string) (string, error)
}

var _ tool.Tool = (*RuleViewTool)(nil)

func NewRuleViewTool(lookupFn func(name string) (string, error)) *RuleViewTool {
	return &RuleViewTool{lookupFn: lookupFn}
}

func (t *RuleViewTool) Name() string {
	return "rule_view"
}

func (t *RuleViewTool) Description() string {
	return "按名称查看规则的完整内容"
}

func (t *RuleViewTool) Schema() *tool.SchemaProperty {
	return &tool.SchemaProperty{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"name": {Type: "string", Description: "规则名称"},
		},
		Required: []string{"name"},
	}
}

func (t *RuleViewTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	name, _ := input["name"].(string)
	if name == "" {
		return "", errors.New("name is required")
	}
	if t.lookupFn == nil {
		return "", errors.New("rule lookup is not configured")
	}
	return t.lookupFn(name)
}
