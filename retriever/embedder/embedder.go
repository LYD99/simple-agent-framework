package embedder

import "context"

type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float64, error)
}
