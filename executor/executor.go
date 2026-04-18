package executor

import (
	"context"
	"time"

	"github.com/LYD99/simple-agent-framework/planner"
)

type Executor interface {
	Execute(ctx context.Context, action planner.Action) (*ExecResult, error)
}

type ExecResult struct {
	ToolName string
	Output   string
	Error    error
	Duration time.Duration
}
