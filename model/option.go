package model

type Option func(*CallOptions)

type CallOptions struct {
	Temperature *float64
	MaxTokens   *int
	TopP        *float64
	StopWords   []string
	Tools       []ToolDef
}

type ToolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

func WithTemperature(t float64) Option { return func(o *CallOptions) { o.Temperature = &t } }
func WithMaxTokens(n int) Option       { return func(o *CallOptions) { o.MaxTokens = &n } }
func WithTopP(p float64) Option        { return func(o *CallOptions) { o.TopP = &p } }
func WithStopWords(s ...string) Option { return func(o *CallOptions) { o.StopWords = s } }
func WithTools(tools ...ToolDef) Option {
	return func(o *CallOptions) { o.Tools = tools }
}

func ApplyOptions(opts ...Option) *CallOptions {
	o := &CallOptions{}
	for _, opt := range opts {
		opt(o)
	}
	return o
}
