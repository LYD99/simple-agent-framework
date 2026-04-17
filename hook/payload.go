package hook

import "time"

type PlanStartPayload struct {
	Iteration int
}

type PlanDonePayload struct {
	Iteration     int
	Reasoning     string
	ActionSummary string
	Duration      time.Duration
}

type ToolCallStartPayload struct {
	ToolName string
	Input    map[string]any
}

type ToolCallDonePayload struct {
	ToolName string
	Output   string
	Error    error
	Duration time.Duration
}

type EvalStartPayload struct {
	Iteration int
	Steps     int
}

type EvalDonePayload struct {
	Iteration int
	Steps     int
	Decision  string
	Feedback  string
	Duration  time.Duration
}

type ErrorPayload struct {
	Error error
	State string
}
