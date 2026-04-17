package skill

import (
	"context"
	"reflect"
	"time"

	"simple-agent-framework/model"
)

type SkillContext struct {
	skill    Skill
	messages []model.ChatMessage
	tools    []any
	maxIter  int
}

type SkillContextLog struct {
	SkillName  string
	Input      string
	Messages   []model.ChatMessage
	Result     string
	Duration   time.Duration
	TokensUsed int
	Steps      int
}

type toolLike interface {
	Name() string
	Description() string
	Schema() any
	Execute(ctx context.Context, input map[string]any) (string, error)
}

func toolSchemaAny(v any) any {
	rv := reflect.ValueOf(v)
	if (rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface) && rv.IsNil() {
		return nil
	}
	m := rv.MethodByName("Schema")
	if !m.IsValid() || m.Type().NumIn() != 0 || m.Type().NumOut() != 1 {
		return nil
	}
	out := m.Call(nil)
	return out[0].Interface()
}

func asToolLike(v any) (toolLike, bool) {
	type executor interface {
		Name() string
		Description() string
		Execute(ctx context.Context, input map[string]any) (string, error)
	}
	e, ok := v.(executor)
	if !ok {
		return nil, false
	}
	schema := toolSchemaAny(v)
	return &toolAdapter{e: e, schema: schema}, true
}

type toolAdapter struct {
	e      interface {
		Name() string
		Description() string
		Execute(ctx context.Context, input map[string]any) (string, error)
	}
	schema any
}

func (a *toolAdapter) Name() string        { return a.e.Name() }
func (a *toolAdapter) Description() string { return a.e.Description() }
func (a *toolAdapter) Schema() any           { return a.schema }
func (a *toolAdapter) Execute(ctx context.Context, input map[string]any) (string, error) {
	return a.e.Execute(ctx, input)
}

func NewContext(s Skill, userInput string, instruction string) *SkillContext {
	return &SkillContext{
		skill:   s,
		maxIter: s.MaxIterations(),
		tools:   s.Tools(),
		messages: []model.ChatMessage{
			{Role: model.RoleSystem, Content: instruction},
			{Role: model.RoleUser, Content: userInput},
		},
	}
}

func (sc *SkillContext) Run(ctx context.Context, m model.ChatModel) (string, *SkillContextLog) {
	start := time.Now()
	var totalTokens int
	var finalAnswer string
	var steps int

	toolDefs := make([]model.ToolDef, 0)
	byName := make(map[string]toolLike)
	for _, raw := range sc.tools {
		tl, ok := asToolLike(raw)
		if !ok {
			continue
		}
		toolDefs = append(toolDefs, model.ToolDef{
			Name:        tl.Name(),
			Description: tl.Description(),
			Parameters:  tl.Schema(),
		})
		byName[tl.Name()] = tl
	}

	for step := 0; step < sc.maxIter; step++ {
		steps++
		opts := []model.Option{}
		if len(toolDefs) > 0 {
			opts = append(opts, model.WithTools(toolDefs...))
		}
		resp, err := m.Generate(ctx, sc.messages, opts...)
		if err != nil {
			finalAnswer = err.Error()
			break
		}
		if resp.Usage.TotalTokens > 0 {
			totalTokens += resp.Usage.TotalTokens
		} else {
			totalTokens += resp.Usage.PromptTokens + resp.Usage.CompletionTokens
		}

		msg := resp.Message
		sc.messages = append(sc.messages, msg)

		if len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				tl, ok := byName[tc.Name]
				if !ok {
					sc.messages = append(sc.messages, model.ChatMessage{
						Role:       model.RoleTool,
						ToolCallID: tc.ID,
						Name:       tc.Name,
						Content:    "unknown tool: " + tc.Name,
					})
					continue
				}
				args := tc.Arguments
				if args == nil {
					args = map[string]any{}
				}
				out, err := tl.Execute(ctx, args)
				if err != nil {
					out = err.Error()
				}
				sc.messages = append(sc.messages, model.ChatMessage{
					Role:       model.RoleTool,
					ToolCallID: tc.ID,
					Name:       tc.Name,
					Content:    out,
				})
			}
			continue
		}

		if msg.Content != "" {
			finalAnswer = msg.Content
			break
		}
	}

	log := &SkillContextLog{
		SkillName:  sc.skill.Name(),
		Input:      sc.messages[1].Content,
		Messages:   sc.messages,
		Result:     finalAnswer,
		Duration:   time.Since(start),
		TokensUsed: totalTokens,
		Steps:      steps,
	}
	return finalAnswer, log
}
