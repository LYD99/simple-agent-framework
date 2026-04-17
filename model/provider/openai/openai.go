package openai

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

	"simple-agent-framework/model"
)

const defaultBaseURL = "https://api.openai.com/v1"

type Client struct {
	modelName  string
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

type ClientOption func(*Client)

func WithBaseURL(url string) ClientOption {
	return func(c *Client) {
		c.baseURL = strings.TrimRight(strings.TrimSpace(url), "/")
	}
}

func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		if httpClient != nil {
			c.httpClient = httpClient
		}
	}
}

func WithTimeout(d time.Duration) ClientOption {
	return func(c *Client) {
		c.httpClient = &http.Client{Timeout: d}
	}
}

func New(modelName, apiKey string, opts ...ClientOption) *Client {
	c := &Client{
		modelName: modelName,
		apiKey:    apiKey,
		baseURL:   defaultBaseURL,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) chatCompletionsURL() string {
	return fmt.Sprintf("%s/chat/completions", strings.TrimSuffix(c.baseURL, "/"))
}

// --- OpenAI JSON types (unexported) ---

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature *float64      `json:"temperature,omitempty"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
	TopP        *float64      `json:"top_p,omitempty"`
	Stop        []string      `json:"stop,omitempty"`
	Tools       []chatTool    `json:"tools,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
}

type chatMessage struct {
	Role       string            `json:"role"`
	Content    string            `json:"content,omitempty"`
	ToolCalls  []chatToolCallMsg `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
	Name       string            `json:"name,omitempty"`
}

type chatToolCallMsg struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function functionCallIn `json:"function"`
}

type functionCallIn struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatTool struct {
	Type     string         `json:"type"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters"`
}

type chatResponse struct {
	Choices []chatChoice `json:"choices"`
	Usage   chatUsage    `json:"usage"`
}

type chatChoice struct {
	Message chatMessageResp `json:"message"`
}

type chatMessageResp struct {
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	ToolCalls []toolCallResp `json:"tool_calls,omitempty"`
}

type toolCallResp struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function functionCallResp `json:"function"`
}

type functionCallResp struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type streamChunkResp struct {
	Choices []streamChoice `json:"choices"`
	Usage   *chatUsage     `json:"usage,omitempty"`
}

type streamChoice struct {
	Index        int         `json:"index"`
	Delta        streamDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason,omitempty"`
}

type streamDelta struct {
	Role      string            `json:"role,omitempty"`
	Content   string            `json:"content,omitempty"`
	ToolCalls []streamToolDelta `json:"tool_calls,omitempty"`
}

type streamToolDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function *struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function,omitempty"`
}

type toolCallAccumulator struct {
	id        string
	name      string
	argChunks []string
}

func (a *toolCallAccumulator) addDelta(d streamToolDelta) {
	if d.ID != "" {
		a.id = d.ID
	}
	if d.Function != nil {
		if d.Function.Name != "" {
			a.name = d.Function.Name
		}
		if d.Function.Arguments != "" {
			a.argChunks = append(a.argChunks, d.Function.Arguments)
		}
	}
}

func (a *toolCallAccumulator) toModel() (model.ToolCall, error) {
	argStr := strings.Join(a.argChunks, "")
	var args map[string]any
	if argStr != "" {
		if err := json.Unmarshal([]byte(argStr), &args); err != nil {
			return model.ToolCall{}, err
		}
	}
	if args == nil {
		args = map[string]any{}
	}
	return model.ToolCall{ID: a.id, Name: a.name, Arguments: args}, nil
}

func buildChatRequest(modelName string, messages []model.ChatMessage, call *model.CallOptions, stream bool) (*chatRequest, error) {
	msgs, err := modelMessagesToOpenAI(messages)
	if err != nil {
		return nil, err
	}
	req := &chatRequest{
		Model:    modelName,
		Messages: msgs,
		Stream:   stream,
	}
	if call == nil {
		return req, nil
	}
	if call.Temperature != nil {
		req.Temperature = call.Temperature
	}
	if call.MaxTokens != nil {
		req.MaxTokens = call.MaxTokens
	}
	if call.TopP != nil {
		req.TopP = call.TopP
	}
	if len(call.StopWords) > 0 {
		req.Stop = append([]string(nil), call.StopWords...)
	}
	if len(call.Tools) > 0 {
		req.Tools = toolDefsToChatTools(call.Tools)
	}
	return req, nil
}

func toolDefsToChatTools(tools []model.ToolDef) []chatTool {
	out := make([]chatTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, chatTool{
			Type: "function",
			Function: toolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}
	return out
}

func modelMessagesToOpenAI(messages []model.ChatMessage) ([]chatMessage, error) {
	out := make([]chatMessage, 0, len(messages))
	for _, m := range messages {
		cm := chatMessage{
			Role:       string(m.Role),
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		}
		if len(m.ToolCalls) > 0 {
			cm.ToolCalls = make([]chatToolCallMsg, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				argBytes, err := json.Marshal(tc.Arguments)
				if err != nil {
					return nil, fmt.Errorf("openai: tool %q arguments: %w", tc.Name, err)
				}
				cm.ToolCalls = append(cm.ToolCalls, chatToolCallMsg{
					ID:   tc.ID,
					Type: "function",
					Function: functionCallIn{
						Name:      tc.Name,
						Arguments: string(argBytes),
					},
				})
			}
		}
		out = append(out, cm)
	}
	return out, nil
}

func chatMessageRespToModel(m chatMessageResp) (model.ChatMessage, error) {
	msg := model.ChatMessage{
		Role:    model.Role(m.Role),
		Content: m.Content,
	}
	for _, tc := range m.ToolCalls {
		var args map[string]any
		if tc.Function.Arguments != "" {
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				return model.ChatMessage{}, fmt.Errorf("openai: tool %q arguments: %w", tc.Function.Name, err)
			}
		}
		if args == nil {
			args = map[string]any{}
		}
		msg.ToolCalls = append(msg.ToolCalls, model.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: args,
		})
	}
	return msg, nil
}

// Generate implements model.ChatModel.
func (c *Client) Generate(ctx context.Context, messages []model.ChatMessage, opts ...model.Option) (*model.ChatResponse, error) {
	call := model.ApplyOptions(opts...)
	body, err := buildChatRequest(c.modelName, messages, call, false)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.chatCompletionsURL(), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai: request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openai: read body: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("openai: status %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed chatResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("openai: decode response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("openai: empty choices")
	}
	msg, err := chatMessageRespToModel(parsed.Choices[0].Message)
	if err != nil {
		return nil, err
	}
	return &model.ChatResponse{
		Message: msg,
		Usage: model.Usage{
			PromptTokens:     parsed.Usage.PromptTokens,
			CompletionTokens: parsed.Usage.CompletionTokens,
			TotalTokens:      parsed.Usage.TotalTokens,
		},
	}, nil
}

// Stream implements model.ChatModel.
func (c *Client) Stream(ctx context.Context, messages []model.ChatMessage, opts ...model.Option) (*model.StreamIterator, error) {
	call := model.ApplyOptions(opts...)
	body, err := buildChatRequest(c.modelName, messages, call, true)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.chatCompletionsURL(), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Connection", "keep-alive")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai: stream request: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai: status %d: %s", resp.StatusCode, string(b))
	}

	msgCh := make(chan model.StreamChunk, 128)
	errCh := make(chan error, 1)

	go func() {
		defer resp.Body.Close()
		defer close(msgCh)
		defer close(errCh)

		accum := make(map[int]*toolCallAccumulator)
		var lastUsage *chatUsage

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
			if strings.TrimSpace(data) == "[DONE]" {
				break
			}

			var chunk streamChunkResp
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}
			if chunk.Usage != nil {
				lastUsage = chunk.Usage
			}
			if len(chunk.Choices) == 0 {
				continue
			}
			ch := chunk.Choices[0]
			if ch.Delta.Content != "" {
				msgCh <- model.StreamChunk{Delta: ch.Delta.Content}
			}
			for _, td := range ch.Delta.ToolCalls {
				acc, ok := accum[td.Index]
				if !ok {
					acc = &toolCallAccumulator{}
					accum[td.Index] = acc
				}
				acc.addDelta(td)
			}
		}
		if err := scanner.Err(); err != nil {
			errCh <- fmt.Errorf("openai: stream read: %w", err)
			return
		}

		indices := make([]int, 0, len(accum))
		for k := range accum {
			indices = append(indices, k)
		}
		sort.Ints(indices)
		var toolCalls []model.ToolCall
		for _, idx := range indices {
			tc, err := accum[idx].toModel()
			if err != nil {
				errCh <- fmt.Errorf("openai: finalize tool call %d: %w", idx, err)
				return
			}
			toolCalls = append(toolCalls, tc)
		}

		var usagePtr *model.Usage
		if lastUsage != nil {
			u := model.Usage{
				PromptTokens:     lastUsage.PromptTokens,
				CompletionTokens: lastUsage.CompletionTokens,
				TotalTokens:      lastUsage.TotalTokens,
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
