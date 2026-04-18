package executor

import (
	"context"
	"sync"
	"time"

	"github.com/LYD99/simple-agent-framework/planner"
	"github.com/LYD99/simple-agent-framework/tool"
)

type ParallelExecutor struct {
	registry *tool.ToolRegistry
	limit    int
}

func NewParallel(registry *tool.ToolRegistry, limit int) *ParallelExecutor {
	return &ParallelExecutor{registry: registry, limit: limit}
}

// Execute runs a single tool action.
func (e *ParallelExecutor) Execute(ctx context.Context, action planner.Action) (*ExecResult, error) {
	start := time.Now()
	output, err := e.registry.Execute(ctx, action.ToolName, action.ToolInput)
	return &ExecResult{
		ToolName: action.ToolName,
		Output:   output,
		Error:    err,
		Duration: time.Since(start),
	}, nil
}

// ExecuteBatch runs multiple tool actions in parallel, preserving input order in the result slice.
func (e *ParallelExecutor) ExecuteBatch(ctx context.Context, actions []planner.Action) ([]*ExecResult, error) {
	n := len(actions)
	if n == 0 {
		return nil, nil
	}
	limit := e.limit
	if limit <= 0 || limit > n {
		limit = n
	}

	sem := make(chan struct{}, limit)
	results := make([]*ExecResult, n)
	var wg sync.WaitGroup

	for i := range actions {
		i := i
		action := actions[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results[i] = &ExecResult{
					ToolName: action.ToolName,
					Error:    ctx.Err(),
					Duration: 0,
				}
				return
			}
			defer func() { <-sem }()

			start := time.Now()
			output, err := e.registry.Execute(ctx, action.ToolName, action.ToolInput)
			results[i] = &ExecResult{
				ToolName: action.ToolName,
				Output:   output,
				Error:    err,
				Duration: time.Since(start),
			}
		}()
	}
	wg.Wait()
	return results, nil
}
