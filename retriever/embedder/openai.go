package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

type OpenAIEmbedder struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
}

func NewOpenAI(apiKey, model string) *OpenAIEmbedder {
	return &OpenAIEmbedder{
		apiKey:  apiKey,
		model:   model,
		baseURL: "https://api.openai.com/v1",
		client:  http.DefaultClient,
	}
}

type embeddingsRequest struct {
	Input any    `json:"input"`
	Model string `json:"model"`
}

type embeddingsResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (e *OpenAIEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	base := strings.TrimRight(e.baseURL, "/")
	body, err := json.Marshal(embeddingsRequest{Input: texts, Model: e.model})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var out embeddingsResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("embeddings decode: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := resp.Status
		if out.Error != nil && out.Error.Message != "" {
			msg = out.Error.Message
		}
		return nil, fmt.Errorf("embeddings %s: %s", resp.Status, msg)
	}
	if len(out.Data) != len(texts) {
		return nil, fmt.Errorf("embeddings: got %d vectors for %d inputs", len(out.Data), len(texts))
	}
	sort.Slice(out.Data, func(i, j int) bool { return out.Data[i].Index < out.Data[j].Index })
	vecs := make([][]float64, len(out.Data))
	for i := range out.Data {
		vecs[i] = out.Data[i].Embedding
	}
	return vecs, nil
}
