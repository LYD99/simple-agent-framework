package retriever

import "context"

// Retriever is the unified retrieval interface used by RAG pipelines.
type Retriever interface {
	Retrieve(ctx context.Context, query string, opts ...SearchOption) ([]Document, error)
}

// Document is a single retrieval result.
type Document struct {
	ID       string            `json:"id"`
	Content  string            `json:"content"`
	Score    float64           `json:"score"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Source   string            `json:"source,omitempty"`
}

// SearchOptions describes the retrieval parameters.
type SearchOptions struct {
	TopK       int               // Max number of results (default 5).
	MinScore   float64           // Minimum score threshold.
	Filters    map[string]string // Metadata filters.
	Collection string            // Knowledge base / collection name.
}

// SearchOption functional option
type SearchOption func(*SearchOptions)

func WithTopK(k int) SearchOption {
	return func(o *SearchOptions) { o.TopK = k }
}

func WithMinScore(s float64) SearchOption {
	return func(o *SearchOptions) { o.MinScore = s }
}

func WithFilters(f map[string]string) SearchOption {
	return func(o *SearchOptions) { o.Filters = f }
}

func WithCollection(c string) SearchOption {
	return func(o *SearchOptions) { o.Collection = c }
}

func ApplySearchOptions(opts ...SearchOption) *SearchOptions {
	o := &SearchOptions{TopK: 5}
	for _, opt := range opts {
		opt(o)
	}
	return o
}
