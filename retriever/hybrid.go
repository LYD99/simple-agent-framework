package retriever

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
)

type ResultMerger interface {
	Merge(semanticResults, keywordResults []Document) []Document
}

type RRFMerger struct {
	K              int
	SemanticWeight float64
	KeywordWeight  float64
}

func (m *RRFMerger) Merge(semantic, keyword []Document) []Document {
	k := m.K
	if k <= 0 {
		k = 60
	}
	sw, kw := m.SemanticWeight, m.KeywordWeight
	if sw == 0 && kw == 0 {
		sw, kw = 1, 1
	}
	type agg struct {
		doc   Document
		score float64
	}
	byID := make(map[string]*agg)
	add := func(list []Document, weight float64, prefix string) {
		for i, d := range list {
			id := d.ID
			if id == "" {
				id = prefix + fmt.Sprintf("%d", i)
			}
			rank := i + 1
			contrib := weight / float64(k+rank)
			if a, ok := byID[id]; ok {
				a.score += contrib
			} else {
				cp := d
				cp.ID = id
				byID[id] = &agg{doc: cp, score: contrib}
			}
		}
	}
	add(semantic, sw, "sem:")
	add(keyword, kw, "kw:")
	out := make([]Document, 0, len(byID))
	for _, a := range byID {
		a.doc.Score = a.score
		if math.IsNaN(a.doc.Score) || math.IsInf(a.doc.Score, 0) {
			a.doc.Score = 0
		}
		out = append(out, a.doc)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].ID < out[j].ID
		}
		return out[i].Score > out[j].Score
	})
	return out
}

type HybridRetriever struct {
	semantic Retriever
	keyword  Retriever
	merger   ResultMerger
}

func NewHybridRetriever(semantic, keyword Retriever, merger ResultMerger) *HybridRetriever {
	return &HybridRetriever{semantic: semantic, keyword: keyword, merger: merger}
}

func (r *HybridRetriever) Retrieve(ctx context.Context, query string, opts ...SearchOption) ([]Document, error) {
	var semDocs, kwDocs []Document
	var semErr, kwErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		semDocs, semErr = r.semantic.Retrieve(ctx, query, opts...)
	}()
	go func() {
		defer wg.Done()
		kwDocs, kwErr = r.keyword.Retrieve(ctx, query, opts...)
	}()
	wg.Wait()
	if semErr != nil && kwErr != nil {
		return nil, fmt.Errorf("hybrid: semantic: %v; keyword: %v", semErr, kwErr)
	}
	if semErr != nil {
		return nil, semErr
	}
	if kwErr != nil {
		return nil, kwErr
	}
	if r.merger == nil {
		return append(append([]Document{}, semDocs...), kwDocs...), nil
	}
	return r.merger.Merge(semDocs, kwDocs), nil
}
