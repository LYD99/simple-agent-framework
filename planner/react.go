package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LYD99/simple-agent-framework/model"
)

const defaultReActSystem = `You are a ReAct agent. Reason step by step in your visible text, call tools when you need external actions or data, and give a concise final answer in plain text when the task is done.`

type ReActPlanner struct {
	model        model.ChatModel
	systemPrompt string
}

type ReActOption func(*ReActPlanner)

func WithSystemPrompt(p string) ReActOption {
	return func(r *ReActPlanner) {
		r.systemPrompt = p
	}
}

func NewReAct(m model.ChatModel, opts ...ReActOption) *ReActPlanner {
	p := &ReActPlanner{model: m}
	for _, o := range opts {
		o(p)
	}
	return p
}

func (p *ReActPlanner) Plan(ctx context.Context, state *PlanState) (*PlanResult, error) {
	if p.model == nil {
		return nil, fmt.Errorf("react: model is nil")
	}
	msgs := buildReActMessages(state, p.systemPrompt)
	toolDefs := toolInfosToToolDefs(state.Tools)
	opts := []model.Option{}
	if len(toolDefs) > 0 {
		opts = append(opts, model.WithTools(toolDefs...))
	}
	resp, err := p.model.Generate(ctx, msgs, opts...)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("react: empty response")
	}
	msg := resp.Message
	reasoning := strings.TrimSpace(msg.Content)
	if len(msg.ToolCalls) > 0 {
		tc := msg.ToolCalls[0]
		input, err := normalizeToolInput(tc.Arguments)
		if err != nil {
			return nil, fmt.Errorf("react: tool arguments: %w", err)
		}
		return &PlanResult{
			Action: Action{
				Type:      ActionToolCall,
				ToolName:  strings.TrimSpace(tc.Name),
				ToolInput: input,
			},
			Reasoning: reasoning,
		}, nil
	}
	return &PlanResult{
		Action: Action{
			Type:   ActionFinalAnswer,
			Answer: strings.TrimSpace(msg.Content),
		},
		Reasoning: reasoning,
	}, nil
}

func buildReActMessages(state *PlanState, systemPrompt string) []model.ChatMessage {
	if len(state.Messages) > 0 {
		return state.Messages
	}
	sp := strings.TrimSpace(systemPrompt)
	if sp == "" {
		sp = defaultReActSystem
	}
	return []model.ChatMessage{{Role: model.RoleSystem, Content: sp}}
}

func toolInfosToToolDefs(tools []ToolInfo) []model.ToolDef {
	if len(tools) == 0 {
		return nil
	}
	out := make([]model.ToolDef, 0, len(tools))
	for _, t := range tools {
		params := t.Schema
		if params == nil {
			params = map[string]any{}
		}
		out = append(out, model.ToolDef{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  params,
		})
	}
	return out
}

func normalizeToolInput(v map[string]any) (map[string]any, error) {
	if v == nil {
		return map[string]any{}, nil
	}
	for k, val := range v {
		switch x := val.(type) {
		case string:
			s := strings.TrimSpace(x)
			if s == "" {
				continue
			}
			if json.Valid([]byte(s)) && (s[0] == '{' || s[0] == '[') {
				var nested any
				if err := json.Unmarshal([]byte(s), &nested); err == nil {
					if m, ok := nested.(map[string]any); ok {
						v[k] = m
					}
				}
			}
		}
	}
	return v, nil
}
