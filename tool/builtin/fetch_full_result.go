package builtin

import (
	"context"
	"fmt"

	"github.com/LYD99/simple-agent-framework/tool"
)

type FetchFullResultTool struct {
	loadFn func(ctx context.Context, id string) (string, error)
}

var _ tool.Tool = (*FetchFullResultTool)(nil)

func NewFetchFullResultTool(loadFn func(ctx context.Context, id string) (string, error)) *FetchFullResultTool {
	return &FetchFullResultTool{loadFn: loadFn}
}

func (t *FetchFullResultTool) Name() string {
	return "fetch_full_result"
}

func (t *FetchFullResultTool) Description() string {
	return "Retrieve the full content of a previously truncated tool result by its persisted ID"
}

func (t *FetchFullResultTool) Schema() *tool.SchemaProperty {
	return &tool.SchemaProperty{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"id": {Type: "string", Description: "The persisted content ID from the truncation notice"},
		},
		Required: []string{"id"},
	}
}

func (t *FetchFullResultTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	id, _ := input["id"].(string)
	if t.loadFn == nil {
		return "", fmt.Errorf("fetch_full_result: no content store available")
	}
	content, err := t.loadFn(ctx, id)
	if err != nil {
		return "", fmt.Errorf("content %q not found or expired: %w", id, err)
	}
	return content, nil
}
