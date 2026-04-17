package planner

import (
	"context"

	"simple-agent-framework/model"
)

type Planner interface {
	Plan(ctx context.Context, state *PlanState) (*PlanResult, error)
}

type PlanState struct {
	Messages []model.ChatMessage
	Tools    []ToolInfo
	History  []StepResult
}

type ToolInfo struct {
	Name        string
	Description string
	Schema      any
}

type StepResult struct {
	Action Action
	Output string
	Error  error
}

type PlanResult struct {
	Action    Action
	Reasoning string
}

type Action struct {
	Type      ActionType
	ToolName  string
	ToolInput map[string]any
	Answer    string
}

type ActionType int

const (
	ActionToolCall ActionType = iota
	ActionFinalAnswer
	ActionAskHuman
)
