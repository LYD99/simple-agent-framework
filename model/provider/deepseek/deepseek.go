package deepseek

import (
	"net/http"
	"time"

	"simple-agent-framework/model/provider/openai"
)

const defaultBaseURL = "https://api.deepseek.com/v1"

type Client = openai.Client
type ClientOption = openai.ClientOption

func WithBaseURL(url string) ClientOption {
	return openai.WithBaseURL(url)
}

func WithHTTPClient(httpClient *http.Client) ClientOption {
	return openai.WithHTTPClient(httpClient)
}

func WithTimeout(d time.Duration) ClientOption {
	return openai.WithTimeout(d)
}

func New(modelName, apiKey string, opts ...ClientOption) *Client {
	allOpts := make([]ClientOption, 0, len(opts)+1)
	allOpts = append(allOpts, openai.WithBaseURL(defaultBaseURL))
	allOpts = append(allOpts, opts...)
	return openai.New(modelName, apiKey, allOpts...)
}
