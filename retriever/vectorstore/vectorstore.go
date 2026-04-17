package vectorstore

import (
	"context"

	"simple-agent-framework/retriever"
)

type VectorStore interface {
	Search(ctx context.Context, vector []float64, topK int) ([]retriever.Document, error)
	Upsert(ctx context.Context, docs []retriever.Document, vectors [][]float64) error
}
