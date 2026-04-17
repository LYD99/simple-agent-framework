package retriever

import "context"

type KeywordIndex interface {
	Search(ctx context.Context, query string, topK int) ([]Document, error)
	Index(ctx context.Context, docs []Document) error
}

type KeywordRetriever struct {
	index KeywordIndex
}

func NewKeywordRetriever(idx KeywordIndex) *KeywordRetriever {
	return &KeywordRetriever{index: idx}
}

func (r *KeywordRetriever) Retrieve(ctx context.Context, query string, opts ...SearchOption) ([]Document, error) {
	o := ApplySearchOptions(opts...)
	docs, err := r.index.Search(ctx, query, o.TopK)
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
