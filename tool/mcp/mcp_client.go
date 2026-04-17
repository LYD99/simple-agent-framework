package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"simple-agent-framework/tool"
)

type MCPTool struct {
	serverURL   string
	toolName    string
	description string
	schema      *tool.SchemaProperty
	httpClient  *http.Client
}

func NewTool(serverURL, toolName string, opts ...MCPOption) *MCPTool {
	t := &MCPTool{
		serverURL: strings.TrimRight(serverURL, "/"),
		toolName:  toolName,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

type MCPOption func(*MCPTool)

func WithDescription(d string) MCPOption {
	return func(t *MCPTool) {
		t.description = d
	}
}

func WithSchema(s *tool.SchemaProperty) MCPOption {
	return func(t *MCPTool) {
		t.schema = s
	}
}

func (t *MCPTool) Name() string {
	return t.toolName
}

func (t *MCPTool) Description() string {
	return t.description
}

func (t *MCPTool) Schema() *tool.SchemaProperty {
	return t.schema
}

func (t *MCPTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/tools/%s", t.serverURL, t.toolName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return string(data), fmt.Errorf("mcp tool %s: HTTP %d", t.toolName, resp.StatusCode)
	}
	return string(data), nil
}

type discoverToolMeta struct {
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Schema      *tool.SchemaProperty  `json:"schema"`
}

type discoverEnvelope struct {
	Tools []discoverToolMeta `json:"tools"`
}

func DiscoverTools(serverURL string) ([]*MCPTool, error) {
	base := strings.TrimRight(serverURL, "/")
	req, err := http.NewRequest(http.MethodGet, base+"/tools", nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("discover tools: HTTP %d", resp.StatusCode)
	}
	var list []discoverToolMeta
	if err := json.Unmarshal(data, &list); err == nil && len(list) > 0 {
		return wrapDiscovered(base, list), nil
	}
	var env discoverEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, err
	}
	if len(env.Tools) == 0 {
		return nil, errors.New("discover tools: empty tool list")
	}
	return wrapDiscovered(base, env.Tools), nil
}

func wrapDiscovered(serverURL string, metas []discoverToolMeta) []*MCPTool {
	out := make([]*MCPTool, 0, len(metas))
	for _, m := range metas {
		if m.Name == "" {
			continue
		}
		opts := []MCPOption{WithDescription(m.Description)}
		if m.Schema != nil {
			opts = append(opts, WithSchema(m.Schema))
		}
		out = append(out, NewTool(serverURL, m.Name, opts...))
	}
	return out
}
