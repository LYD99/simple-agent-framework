package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	defaultE2BAPIBase = "https://api.e2b.app"
	defaultE2BTimeout = 300
	defaultTemplate   = "base"
)

type E2BConfig struct {
	APIKey     string
	TemplateID string
	APIBaseURL string        // defaults to https://api.e2b.app
	Timeout    int           // sandbox TTL in seconds (default 300)
	EnvVars    map[string]string
	HTTPClient *http.Client
}

type E2BRuntime struct {
	apiKey     string
	apiBase    string
	sandboxID  string
	envdBase   string // envd endpoint for data plane operations
	httpClient *http.Client
}

// --- Control Plane (REST API) request/response types ---

type e2bCreateReq struct {
	TemplateID string            `json:"templateID"`
	Timeout    int               `json:"timeout,omitempty"`
	EnvVars    map[string]string `json:"envVars,omitempty"`
}

type e2bCreateResp struct {
	SandboxID       string `json:"sandboxID"`
	TemplateID      string `json:"templateID"`
	ClientID        string `json:"clientID"`
	EnvdVersion     string `json:"envdVersion"`
	EnvdAccessToken string `json:"envdAccessToken"`
}

// --- Data Plane (envd) request/response types ---

type e2bCommandReq struct {
	Cmd  string            `json:"cmd"`
	Envs map[string]string `json:"envs,omitempty"`
}

type e2bCommandResp struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
	PID      int    `json:"pid"`
	Error    string `json:"error,omitempty"`
}

type e2bAPIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *e2bAPIError) Error() string {
	return fmt.Sprintf("e2b api error %d: %s", e.Code, e.Message)
}

func NewE2B(config E2BConfig) (*E2BRuntime, error) {
	if config.APIKey == "" {
		return nil, fmt.Errorf("e2b: API key is required")
	}

	apiBase := config.APIBaseURL
	if apiBase == "" {
		apiBase = defaultE2BAPIBase
	}
	templateID := config.TemplateID
	if templateID == "" {
		templateID = defaultTemplate
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = defaultE2BTimeout
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}

	rt := &E2BRuntime{
		apiKey:     config.APIKey,
		apiBase:    apiBase,
		httpClient: httpClient,
	}

	resp, err := rt.createSandbox(context.Background(), e2bCreateReq{
		TemplateID: templateID,
		Timeout:    timeout,
		EnvVars:    config.EnvVars,
	})
	if err != nil {
		return nil, fmt.Errorf("e2b: create sandbox: %w", err)
	}

	rt.sandboxID = resp.SandboxID
	rt.envdBase = fmt.Sprintf("https://%s-%s.e2b.dev", resp.SandboxID, resp.ClientID)

	return rt, nil
}

func (r *E2BRuntime) Exec(ctx context.Context, command string, args ...string) (*ExecOutput, error) {
	fullCmd := command
	for _, a := range args {
		fullCmd += " " + a
	}

	body, err := json.Marshal(e2bCommandReq{Cmd: fullCmd})
	if err != nil {
		return nil, fmt.Errorf("e2b: marshal command: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.envdBase+"/commands", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("e2b: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", r.apiKey)

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("e2b: execute command: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("e2b: read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var apiErr e2bAPIError
		if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Message != "" {
			return nil, &apiErr
		}
		return nil, fmt.Errorf("e2b: command failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var cmdResp e2bCommandResp
	if err := json.Unmarshal(respBody, &cmdResp); err != nil {
		return nil, fmt.Errorf("e2b: decode response: %w", err)
	}

	if cmdResp.Error != "" {
		return &ExecOutput{
			Stdout:   cmdResp.Stdout,
			Stderr:   cmdResp.Error,
			ExitCode: cmdResp.ExitCode,
		}, nil
	}

	return &ExecOutput{
		Stdout:   cmdResp.Stdout,
		Stderr:   cmdResp.Stderr,
		ExitCode: cmdResp.ExitCode,
	}, nil
}

func (r *E2BRuntime) Close() error {
	if r.sandboxID == "" {
		return nil
	}
	req, err := http.NewRequest(http.MethodDelete, r.apiBase+"/sandboxes/"+r.sandboxID, nil)
	if err != nil {
		return fmt.Errorf("e2b: build delete request: %w", err)
	}
	req.Header.Set("X-API-Key", r.apiKey)

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("e2b: delete sandbox: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 && resp.StatusCode != 404 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("e2b: delete sandbox %s failed (%d): %s", r.sandboxID, resp.StatusCode, string(body))
	}

	r.sandboxID = ""
	return nil
}

func (r *E2BRuntime) SandboxID() string {
	return r.sandboxID
}

// --- internal helpers ---

func (r *E2BRuntime) createSandbox(ctx context.Context, payload e2bCreateReq) (*e2bCreateResp, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.apiBase+"/sandboxes", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", r.apiKey)

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		var apiErr e2bAPIError
		if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Message != "" {
			return nil, &apiErr
		}
		return nil, fmt.Errorf("create sandbox failed (%d): %s", resp.StatusCode, string(respBody))
	}

	var result e2bCreateResp
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &result, nil
}
