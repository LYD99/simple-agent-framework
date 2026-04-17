package tool

import "context"

// Tool 工具接口 — 所有工具（内置、MCP、RAG、Skill）的统一抽象
type Tool interface {
	Name() string
	Description() string
	Schema() *SchemaProperty
	Execute(ctx context.Context, input map[string]any) (string, error)
}
