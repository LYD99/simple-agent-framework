package tool

import "context"

// Tool is the unified abstraction for every callable capability (built-in,
// MCP, RAG, Skill, …).
type Tool interface {
	Name() string
	Description() string
	Schema() *SchemaProperty
	Execute(ctx context.Context, input map[string]any) (string, error)
}
