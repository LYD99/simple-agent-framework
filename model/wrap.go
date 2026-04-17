package model

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

type GenerateFunc func(ctx context.Context, messages []ChatMessage, opts ...Option) (*ChatResponse, error)

type funcModel struct {
	fn GenerateFunc
}

func (f *funcModel) Generate(ctx context.Context, messages []ChatMessage, opts ...Option) (*ChatResponse, error) {
	return f.fn(ctx, messages, opts...)
}

func (f *funcModel) Stream(ctx context.Context, messages []ChatMessage, opts ...Option) (*StreamIterator, error) {
	return nil, ErrStreamNotSupported
}

func WrapFunc(fn GenerateFunc) ChatModel {
	return &funcModel{fn: fn}
}

type HTTPOption func(*httpModelConfig)

type httpModelConfig struct {
	headers        map[string]string
	requestMapper  func([]ChatMessage, *CallOptions) ([]byte, error)
	responseMapper func([]byte) (*ChatResponse, error)
	timeout        time.Duration
}

func WithHTTPHeaders(h map[string]string) HTTPOption {
	return func(c *httpModelConfig) {
		if c.headers == nil {
			c.headers = make(map[string]string, len(h))
		}
		for k, v := range h {
			c.headers[k] = v
		}
	}
}

func WithRequestMapper(fn func([]ChatMessage, *CallOptions) ([]byte, error)) HTTPOption {
	return func(c *httpModelConfig) {
		c.requestMapper = fn
	}
}

func WithResponseMapper(fn func([]byte) (*ChatResponse, error)) HTTPOption {
	return func(c *httpModelConfig) {
		c.responseMapper = fn
	}
}

func WithHTTPTimeout(d time.Duration) HTTPOption {
	return func(c *httpModelConfig) {
		c.timeout = d
	}
}

type httpModel struct {
	url    string
	config httpModelConfig
	client *http.Client
}

func WrapHTTP(url string, opts ...HTTPOption) ChatModel {
	cfg := httpModelConfig{
		headers: make(map[string]string),
	}
	for _, o := range opts {
		o(&cfg)
	}
	client := &http.Client{}
	if cfg.timeout > 0 {
		client.Timeout = cfg.timeout
	}
	return &httpModel{
		url:    url,
		config: cfg,
		client: client,
	}
}

func (m *httpModel) Generate(ctx context.Context, messages []ChatMessage, opts ...Option) (*ChatResponse, error) {
	if m.config.requestMapper == nil {
		return nil, fmt.Errorf("model: WithRequestMapper is required")
	}
	if m.config.responseMapper == nil {
		return nil, fmt.Errorf("model: WithResponseMapper is required")
	}
	callOpts := ApplyOptions(opts...)
	body, err := m.config.requestMapper(messages, callOpts)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range m.config.headers {
		req.Header.Set(k, v)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("model: unexpected status %d: %s", resp.StatusCode, string(respBody))
	}
	return m.config.responseMapper(respBody)
}

func (m *httpModel) Stream(ctx context.Context, messages []ChatMessage, opts ...Option) (*StreamIterator, error) {
	return nil, ErrStreamNotSupported
}
