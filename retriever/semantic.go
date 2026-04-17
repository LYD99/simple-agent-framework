package retriever

import "context"

type EmbedderLike interface {
	Embed(ctx context.Context, texts []string) ([][]float64, error)
}

type VectorStoreLike interface {
	Search(ctx context.Context, vector []float64, topK int) ([]Document, error)
}

type SemanticRetriever struct {
	embedder    EmbedderLike
	vectorStore VectorStoreLike
}

func NewSemanticRetriever(e EmbedderLike, vs VectorStoreLike) *SemanticRetriever {
	return &SemanticRetriever{embedder: e, vectorStore: vs}
}

func (r *SemanticRetriever) Retrieve(ctx context.Context, query string, opts ...SearchOption) ([]Document, error) {
	o := ApplySearchOptions(opts...)
	vecs, err := r.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 || len(vecs[0]) == 0 {
		return nil, nil
	}
	docs, err := r.vectorStore.Search(ctx, vecs[0], o.TopK)
	if err != nil {
		return nil, err
	}
	if o.MinScore <= 0 {
		return docs, nil
	}
	out := docs[:0]
	for _, d := range docs {
		if d.Score >= o.MinScore {
			out = append(out, d)
		}
	}
	return out, nil
}
