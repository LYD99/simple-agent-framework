package vectorstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/LYD99/simple-agent-framework/retriever"
)

type ChromaStore struct {
	baseURL    string
	collection string
	client     *http.Client
	mu         sync.Mutex
	collID     string
}

func NewChroma(baseURL, collection string) *ChromaStore {
	return &ChromaStore{
		baseURL:    strings.TrimRight(baseURL, "/"),
		collection: collection,
		client:     http.DefaultClient,
	}
}

func (c *ChromaStore) ensureCollection(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.collID != "" {
		return c.collID, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/collections", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var cols []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &cols); err != nil {
		var wrap struct {
			Data []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"data"`
		}
		if err2 := json.Unmarshal(body, &wrap); err2 != nil || len(wrap.Data) == 0 {
			return "", fmt.Errorf("chroma list collections: %w", err)
		}
		cols = wrap.Data
	}
	for i := range cols {
		if cols[i].Name == c.collection {
			c.collID = cols[i].ID
			return c.collID, nil
		}
	}
	createBody, _ := json.Marshal(map[string]string{"name": c.collection})
	req2, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/collections", bytes.NewReader(createBody))
	if err != nil {
		return "", err
	}
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := c.client.Do(req2)
	if err != nil {
		return "", err
	}
	defer resp2.Body.Close()
	b2, err := io.ReadAll(resp2.Body)
	if err != nil {
		return "", err
	}
	var created struct {
		ID string `json:"id"`
	}
	if resp2.StatusCode < 200 || resp2.StatusCode >= 300 {
		return "", fmt.Errorf("chroma create collection: %s: %s", resp2.Status, string(b2))
	}
	if err := json.Unmarshal(b2, &created); err != nil {
		return "", err
	}
	c.collID = created.ID
	return c.collID, nil
}

func (c *ChromaStore) Upsert(ctx context.Context, docs []retriever.Document, vectors [][]float64) error {
	if len(docs) == 0 {
		return nil
	}
	if len(docs) != len(vectors) {
		return fmt.Errorf("chroma upsert: %d docs vs %d vectors", len(docs), len(vectors))
	}
	id, err := c.ensureCollection(ctx)
	if err != nil {
		return err
	}
	ids := make([]string, len(docs))
	docsStr := make([]string, len(docs))
	meta := make([]map[string]string, len(docs))
	for i := range docs {
		ids[i] = docs[i].ID
		if ids[i] == "" {
			ids[i] = fmt.Sprintf("doc-%d", i)
		}
		docsStr[i] = docs[i].Content
		m := map[string]string{}
		for k, v := range docs[i].Metadata {
			m[k] = v
		}
		if docs[i].Source != "" {
			m["source"] = docs[i].Source
		}
		meta[i] = m
	}
	payload := map[string]any{
		"ids":        ids,
		"embeddings": vectors,
		"documents":  docsStr,
		"metadatas":  meta,
	}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/collections/"+id+"/upsert", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("chroma upsert: %s: %s", resp.Status, string(b))
	}
	_ = b
	return nil
}

func (c *ChromaStore) Search(ctx context.Context, vector []float64, topK int) ([]retriever.Document, error) {
	if topK <= 0 {
		topK = 5
	}
	id, err := c.ensureCollection(ctx)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{
		"query_embeddings": [][]float64{vector},
		"n_results":        topK,
		"include":          []string{"documents", "metadatas", "distances"},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/collections/"+id+"/query", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("chroma query: %s: %s", resp.Status, string(raw))
	}
	var out struct {
		IDs       [][]string         `json:"ids"`
		Documents [][]string         `json:"documents"`
		Metadatas [][]map[string]any `json:"metadatas"`
		Distances [][]float64        `json:"distances"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if len(out.IDs) == 0 || len(out.IDs[0]) == 0 {
		return nil, nil
	}
	n := len(out.IDs[0])
	res := make([]retriever.Document, 0, n)
	for i := 0; i < n; i++ {
		d := retriever.Document{ID: out.IDs[0][i], Metadata: map[string]string{}}
		if len(out.Documents) > 0 && len(out.Documents[0]) > i {
			d.Content = out.Documents[0][i]
		}
		if len(out.Distances) > 0 && len(out.Distances[0]) > i {
			dist := out.Distances[0][i]
			d.Score = 1.0 / (1.0 + dist)
		}
		if len(out.Metadatas) > 0 && len(out.Metadatas[0]) > i && out.Metadatas[0][i] != nil {
			for k, v := range out.Metadatas[0][i] {
				d.Metadata[k] = fmt.Sprint(v)
			}
			if s, ok := d.Metadata["source"]; ok {
				d.Source = s
			}
		}
		res = append(res, d)
	}
	return res, nil
}
