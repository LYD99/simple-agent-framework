package core

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type StreamContentType uint8

const (
	Text     StreamContentType = 1
	ToolCall StreamContentType = 2
)

type StreamMessage struct {
	Role        string `json:"role"`
	ContentType StreamContentType
	Content     string `json:"content"`
}

// Usage defines the common structure for token usage statistics.
type Usage struct {
	PromptTokens            int `json:"prompt_tokens"`
	PromptCacheHitTokens    int `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens   int `json:"prompt_cache_miss_tokens"`
	CompletionTokens        int `json:"completion_tokens"`
	TotalTokens             int `json:"total_tokens"`
	CompletionTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

type StreamResponse struct {
	msgChan <-chan StreamMessage
	errChan <-chan error

	curMsg StreamMessage
	curErr error
}

// Next 阻塞直到获取下一条消息。返回 false 表示结束或出错。
func (s *StreamResponse) Next() bool {
	select {
	case msg, ok := <-s.msgChan:
		if !ok {
			return false
		}
		s.curMsg = msg
		return true
	case err := <-s.errChan:
		if err != nil {
			s.curErr = err
		}
		return false
	}
}

func (s *StreamResponse) Msg() StreamMessage { return s.curMsg }
func (s *StreamResponse) Err() error         { return s.curErr }

// AIClient is a lightweight HTTP client for interacting with AI model providers.
type AIClient struct {
	URL        string
	Token      string
	HTTPClient *http.Client
}

// NewAIClient initializes a client with a 60-second default timeout.
func NewAIClient(url, token string) *AIClient {
	return &AIClient{
		URL:   url,
		Token: token,
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (c *AIClient) Generate(input interface{}, fn func(map[string]interface{}) (string, *Usage, error)) (string, *Usage, error) {
	response, err := c.Request(input)
	if err != nil {
		return "", nil, err
	}
	content, usage, err := fn(response)
	if err != nil {
		return "", nil, err
	}

	return content, usage, nil
}

func (c *AIClient) Stream(input interface{}, fn func(map[string]interface{}) ([]StreamMessage, error)) (*StreamResponse, error) {
    // 缓冲大小可根据需求调整，建议给一定缓冲防止阻塞 API 接收
    msgChan := make(chan StreamMessage, 128)
    errChan := make(chan error, 1)

    // 启动后台协程处理请求
    go func() {
        defer close(msgChan)
        defer close(errChan)

        err := c.StreamRequest(input, func(raw map[string]interface{}) error {
            // 注意：这里根据你传入的 fn 解析 raw 数据
            messages, err := fn(raw)
            if err != nil {
                return err
            }
            // 将解析后的碎片写入通道
            for _, msg := range messages {
                msgChan <- msg
            }
            return nil
        })

        if err != nil {
            errChan <- err
        }
    }()

    return &StreamResponse{
        msgChan: msgChan,
        errChan: errChan,
    }, nil
}

// Request performs the POST request and returns the content string and usage details.
func (c *AIClient) Request(input interface{}) (map[string]interface{}, error) {
	jsonData, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal input: %v", err)
	}

	req, err := http.NewRequest("POST", c.URL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.Token))
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("api error (status %d): %s", resp.StatusCode, string(body))
	}

	var rawResponse map[string]interface{}
	if err := json.Unmarshal(body, &rawResponse); err != nil {
		return nil, fmt.Errorf("failed to unmarshal raw response: %v", err)
	}

	return rawResponse, nil
}

// StreamRequest executes a streaming API call and yields raw maps through a callback.
func (c *AIClient) StreamRequest(input interface{}, callback func(map[string]interface{}) error) error {
	jsonData, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("failed to marshal input: %v", err)
	}

	req, err := http.NewRequest("POST", c.URL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.Token))
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Connection", "keep-alive")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("stream request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("api error (status %d): %s", resp.StatusCode, string(body))
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(data), &raw); err != nil {
			// Skip invalid JSON in stream or handle as error
			continue
		}

		if err := callback(raw); err != nil {
			return err // Allow caller to stop the stream
		}
	}

	return scanner.Err()
}
