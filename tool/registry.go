package tool

import (
	"context"
	"fmt"
	"reflect"
)

type ToolHandler func(input map[string]interface{}) (string, error)

type ToolRegistry struct {
	tools map[string]Tool
	order []string
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]Tool),
	}
}

func (r *ToolRegistry) AddTool(t Tool) {
	name := t.Name()
	if _, ok := r.tools[name]; !ok {
		r.order = append(r.order, name)
	}
	r.tools[name] = t
}

func (r *ToolRegistry) Register(name, description string, inputSchema any, handler ToolHandler) {
	schema := GenerateSchema(reflect.TypeOf(inputSchema))
	r.AddTool(&funcTool{
		name:        name,
		description: description,
		schema:      schema,
		handler:     handler,
	})
}

func (r *ToolRegistry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

func (r *ToolRegistry) Tools() []Tool {
	out := make([]Tool, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.tools[name])
	}
	return out
}

func (r *ToolRegistry) Execute(ctx context.Context, name string, input map[string]any) (string, error) {
	t, ok := r.tools[name]
	if !ok {
		return "", fmt.Errorf("tool %q not registered", name)
	}
	return t.Execute(ctx, input)
}

func (r *ToolRegistry) Names() []string {
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

type funcTool struct {
	name        string
	description string
	schema      *SchemaProperty
	handler     ToolHandler
}

func (f *funcTool) Name() string {
	return f.name
}

func (f *funcTool) Description() string {
	return f.description
}

func (f *funcTool) Schema() *SchemaProperty {
	return f.schema
}

func (f *funcTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	_ = ctx
	return f.handler(input)
}
