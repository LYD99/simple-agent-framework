package retriever

import "context"

// Retriever 统一检索接口
type Retriever interface {
	Retrieve(ctx context.Context, query string, opts ...SearchOption) ([]Document, error)
}

// Document 检索结果文档
type Document struct {
	ID       string            `json:"id"`
	Content  string            `json:"content"`
	Score    float64           `json:"score"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Source   string            `json:"source,omitempty"`
}

// SearchOptions 检索参数
type SearchOptions struct {
	TopK       int               // 返回数量 (默认 5)
	MinScore   float64           // 最低分数阈值
	Filters    map[string]string // 元数据过滤
	Collection string            // 知识库/集合名称
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
