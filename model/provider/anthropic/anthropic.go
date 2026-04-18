package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/LYD99/simple-agent-framework/model"
)

const defaultBaseURL = "https://api.anthropic.com/v1"
const defaultAPIVersion = "2023-06-01"
const defaultMaxTokens = 4096

type Client struct {
	modelName  string
	apiKey     string
	baseURL    string
	apiVersion string
	httpClient *http.Client
}

type ClientOption func(*Client)

func WithBaseURL(url string) ClientOption {
	return func(c *Client) {
		c.baseURL = strings.TrimRight(strings.TrimSpace(url), "/")
	}
}

func WithAPIVersion(v string) ClientOption {
	return func(c *Client) {
		if strings.TrimSpace(v) != "" {
			c.apiVersion = strings.TrimSpace(v)
		}
	}
}

func WithHTTPClient(h *http.Client) ClientOption {
	return func(c *Client) {
		if h != nil {
			c.httpClient = h
		}
	}
}

func New(modelName, apiKey string, opts ...ClientOption) *Client {
	c := &Client{
		modelName:  modelName,
		apiKey:     apiKey,
		baseURL:    defaultBaseURL,
		apiVersion: defaultAPIVersion,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) messagesURL() string {
	return fmt.Sprintf("%s/messages", strings.TrimSuffix(c.baseURL, "/"))
}

// --- request / response types ---

type messagesRequest struct {
	Model         string            `json:"model"`
	MaxTokens     int               `json:"max_tokens"`
	System        string            `json:"system,omitempty"`
	Messages      []anthropicReqMsg `json:"messages"`
	Tools         []anthropicTool   `json:"tools,omitempty"`
	Stream        bool              `json:"stream,omitempty"`
	Temperature   *float64          `json:"temperature,omitempty"`
	TopP          *float64          `json:"top_p,omitempty"`
	StopSequences []string          `json:"stop_sequences,omitempty"`
}

type anthropicReqMsg struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"input_schema"`
}

type messagesResponse struct {
	Role       string         `json:"role"`
	Content    []contentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      anthropicUsage `json:"usage"`
}

type contentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func toolDefsToAnthropic(tools []model.ToolDef) []anthropicTool {
	out := make([]anthropicTool, 0, len(tools))
	for _, t := range tools {
		schema := t.Parameters
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}
	return out
}

func modelMessagesToAnthropic(messages []model.ChatMessage) (system string, msgs []anthropicReqMsg, err error) {
	var sysParts []string
	for _, m := range messages {
		if m.Role == model.RoleSystem {
			sysParts = append(sysParts, m.Content)
		}
	}
	system = strings.Join(sysParts, "\n\n")

	i := 0
	for i < len(messages) {
		m := messages[i]
		switch m.Role {
		case model.RoleSystem:
			i++
			continue
		case model.RoleTool:
			var blocks []map[string]any
			for i < len(messages) && messages[i].Role == model.RoleTool {
				tm := messages[i]
				if tm.ToolCallID == "" {
					return "", nil, fmt.Errorf("anthropic: tool message missing tool_call_id")
				}
				blocks = append(blocks, map[string]any{
					"type":        "tool_result",
					"tool_use_id": tm.ToolCallID,
					"content":     tm.Content,
				})
				i++
			}
			msgs = append(msgs, anthropicReqMsg{Role: "user", Content: blocks})
		case model.RoleUser:
			msgs = append(msgs, anthropicReqMsg{Role: "user", Content: m.Content})
			i++
		case model.RoleAssistant:
			msgs = append(msgs, anthropicReqMsg{Role: "assistant", Content: assistantContent(m)})
			i++
		default:
			return "", nil, fmt.Errorf("anthropic: unsupported role %q", m.Role)
		}
	}
	return system, msgs, nil
}

func assistantContent(m model.ChatMessage) any {
	if len(m.ToolCalls) == 0 {
		return m.Content
	}
	blocks := make([]map[string]any, 0, len(m.ToolCalls)+1)
	if strings.TrimSpace(m.Content) != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
	}
	for _, tc := range m.ToolCalls {
		blocks = append(blocks, map[string]any{
			"type":  "tool_use",
			"id":    tc.ID,
			"name":  tc.Name,
			"input": tc.Arguments,
		})
	}
	return blocks
}

func buildMessagesRequest(modelName string, messages []model.ChatMessage, call *model.CallOptions, stream bool) (*messagesRequest, error) {
	system, msgs, err := modelMessagesToAnthropic(messages)
	if err != nil {
		return nil, err
	}
	maxTok := defaultMaxTokens
	if call != nil && call.MaxTokens != nil {
		maxTok = *call.MaxTokens
	}
	req := &messagesRequest{
		Model:     modelName,
		MaxTokens: maxTok,
		System:    system,
		Messages:  msgs,
		Stream:    stream,
	}
	if call != nil {
		if call.Temperature != nil {
			req.Temperature = call.Temperature
		}
		if call.TopP != nil {
			req.TopP = call.TopP
		}
		if len(call.StopWords) > 0 {
			req.StopSequences = append([]string(nil), call.StopWords...)
		}
		if len(call.Tools) > 0 {
			req.Tools = toolDefsToAnthropic(call.Tools)
		}
	}
	return req, nil
}

func anthropicResponseToModel(resp *messagesResponse) (model.ChatMessage, error) {
	msg := model.ChatMessage{Role: model.RoleAssistant, Content: ""}
	var textParts []string
	for _, b := range resp.Content {
		switch b.Type {
		case "text":
			textParts = append(textParts, b.Text)
		case "tool_use":
			var input map[string]any
			if len(b.Input) > 0 && string(b.Input) != "null" {
				if err := json.Unmarshal(b.Input, &input); err != nil {
					return model.ChatMessage{}, fmt.Errorf("anthropic: tool %q input: %w", b.Name, err)
				}
			}
			if input == nil {
				input = map[string]any{}
			}
			msg.ToolCalls = append(msg.ToolCalls, model.ToolCall{
				ID:        b.ID,
				Name:      b.Name,
				Arguments: input,
			})
		}
	}
	msg.Content = strings.Join(textParts, "")
	return msg, nil
}

func (c *Client) setHeaders(req *http.Request, stream bool) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", c.apiVersion)
	if c.apiKey != "" {
		req.Header.Set("x-api-key", c.apiKey)
	}
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
}

// Generate implements model.ChatModel.
func (c *Client) Generate(ctx context.Context, messages []model.ChatMessage, opts ...model.Option) (*model.ChatResponse, error) {
	call := model.ApplyOptions(opts...)
	body, err := buildMessagesRequest(c.modelName, messages, call, false)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.messagesURL(), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	c.setHeaders(req, false)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: read body: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("anthropic: status %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed messagesResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("anthropic: decode response: %w", err)
	}
	msg, err := anthropicResponseToModel(&parsed)
	if err != nil {
		return nil, err
	}
	u := parsed.Usage
	total := u.InputTokens + u.OutputTokens
	return &model.ChatResponse{
		Message: msg,
		Usage: model.Usage{
			PromptTokens:     u.InputTokens,
			CompletionTokens: u.OutputTokens,
			TotalTokens:      total,
		},
	}, nil
}

// --- streaming ---

type streamBlockAccum struct {
	kind      string // "text" | "tool_use"
	id, name  string
	text      strings.Builder
	jsonParts []string
}

func (c *Client) Stream(ctx context.Context, messages []model.ChatMessage, opts ...model.Option) (*model.StreamIterator, error) {
	call := model.ApplyOptions(opts...)
	body, err := buildMessagesRequest(c.modelName, messages, call, true)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.messagesURL(), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	c.setHeaders(req, true)
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: stream request: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("anthropic: status %d: %s", resp.StatusCode, string(b))
	}

	msgCh := make(chan model.StreamChunk, 128)
	errCh := make(chan error, 1)

	go func() {
		defer resp.Body.Close()
		defer close(msgCh)
		defer close(errCh)

		blocks := make(map[int]*streamBlockAccum)
		var usageAcc anthropicUsage

		scanner := bufio.NewScanner(resp.Body)
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 1024*1024)

		for scanner.Scan() {
			select {
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			default:
			}

			line := scanner.Text()
			if line == "" || !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}

			var base struct {
				Type  string `json:"type"`
				Error *struct {
					Type    string `json:"type"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(data), &base); err != nil {
				continue
			}
			if base.Type == "error" && base.Error != nil {
				errCh <- fmt.Errorf("anthropic: %s", base.Error.Message)
				return
			}

			switch base.Type {
			case "message_start":
				var ev struct {
					Message struct {
						Usage anthropicUsage `json:"usage"`
					} `json:"message"`
				}
				if json.Unmarshal([]byte(data), &ev) == nil {
					usageAcc.InputTokens = ev.Message.Usage.InputTokens
				}
			case "content_block_start":
				var ev struct {
					Index        int `json:"index"`
					ContentBlock struct {
						Type string `json:"type"`
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"content_block"`
				}
				if err := json.Unmarshal([]byte(data), &ev); err != nil {
					continue
				}
				blocks[ev.Index] = &streamBlockAccum{
					kind: ev.ContentBlock.Type,
					id:   ev.ContentBlock.ID,
					name: ev.ContentBlock.Name,
				}
			case "content_block_delta":
				var ev struct {
					Index int `json:"index"`
					Delta struct {
						Type        string `json:"type"`
						Text        string `json:"text"`
						PartialJSON string `json:"partial_json"`
					} `json:"delta"`
				}
				if err := json.Unmarshal([]byte(data), &ev); err != nil {
					continue
				}
				acc, ok := blocks[ev.Index]
				if !ok {
					continue
				}
				switch ev.Delta.Type {
				case "text_delta":
					if ev.Delta.Text != "" {
						msgCh <- model.StreamChunk{Delta: ev.Delta.Text}
						acc.text.WriteString(ev.Delta.Text)
					}
				case "input_json_delta":
					if ev.Delta.PartialJSON != "" {
						acc.jsonParts = append(acc.jsonParts, ev.Delta.PartialJSON)
					}
				}
			case "message_delta":
				var ev struct {
					Usage anthropicUsage `json:"usage"`
				}
				if json.Unmarshal([]byte(data), &ev) == nil {
					usageAcc.OutputTokens = ev.Usage.OutputTokens
				}
			case "message_stop":
				// finalization below
			case "ping", "content_block_stop":
				// ignore
			}
		}
		if err := scanner.Err(); err != nil {
			errCh <- fmt.Errorf("anthropic: stream read: %w", err)
			return
		}

		indices := make([]int, 0, len(blocks))
		for k := range blocks {
			indices = append(indices, k)
		}
		sort.Ints(indices)
		var toolCalls []model.ToolCall
		for _, idx := range indices {
			acc := blocks[idx]
			if acc == nil || acc.kind != "tool_use" {
				continue
			}
			argStr := strings.Join(acc.jsonParts, "")
			var args map[string]any
			if argStr != "" {
				if err := json.Unmarshal([]byte(argStr), &args); err != nil {
					errCh <- fmt.Errorf("anthropic: tool %q arguments: %w", acc.name, err)
					return
				}
			}
			if args == nil {
				args = map[string]any{}
			}
			toolCalls = append(toolCalls, model.ToolCall{ID: acc.id, Name: acc.name, Arguments: args})
		}

		var usagePtr *model.Usage
		if usageAcc.InputTokens > 0 || usageAcc.OutputTokens > 0 {
			u := model.Usage{
				PromptTokens:     usageAcc.InputTokens,
				CompletionTokens: usageAcc.OutputTokens,
				TotalTokens:      usageAcc.InputTokens + usageAcc.OutputTokens,
			}
			usagePtr = &u
		}

		msgCh <- model.StreamChunk{
			ToolCalls: toolCalls,
			Done:      true,
			Usage:     usagePtr,
		}
	}()

	return model.NewStreamIterator(msgCh, errCh), nil
}
