package retriever

import (
	"context"
	"fmt"
	"strings"

	"simple-agent-framework/tool"
)

type ResultFormatter interface {
	Format(docs []Document) string
}

type DefaultFormatter struct{}

func (f DefaultFormatter) Format(docs []Document) string {
	var b strings.Builder
	for i, d := range docs {
		src := d.Source
		if src == "" {
			if d.Metadata != nil {
				src = d.Metadata["source"]
			}
		}
		fmt.Fprintf(&b, "[%d] (score: %.4f, source: %s)\n%s\n", i+1, d.Score, src, d.Content)
		if i+1 < len(docs) {
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

type RAGTool struct {
	name        string
	description string
	retriever   Retriever
	formatter   ResultFormatter
}

var _ tool.Tool = (*RAGTool)(nil)

type RAGToolOption func(*RAGTool)

func WithFormatter(f ResultFormatter) RAGToolOption {
	return func(t *RAGTool) {
		t.formatter = f
	}
}

func NewRAGTool(name, desc string, r Retriever, opts ...RAGToolOption) *RAGTool {
	t := &RAGTool{
		name:        name,
		description: desc,
		retriever:   r,
		formatter:   DefaultFormatter{},
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

func (t *RAGTool) Name() string {
	return t.name
}

func (t *RAGTool) Description() string {
	return t.description
}

func (t *RAGTool) Schema() *tool.SchemaProperty {
	return &tool.SchemaProperty{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"query": {
				Type:        "string",
				Description: "Natural language search query for the knowledge base",
			},
		},
		Required: []string{"query"},
	}
}

func (t *RAGTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	raw, ok := input["query"]
	if !ok {
		return "", fmt.Errorf("missing query")
	}
	q, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("query must be a string")
	}
	docs, err := t.retriever.Retrieve(ctx, q)
	if err != nil {
		return "", err
	}
	if t.formatter == nil {
		return DefaultFormatter{}.Format(docs), nil
	}
	return t.formatter.Format(docs), nil
}
