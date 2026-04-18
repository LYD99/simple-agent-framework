package interrupter

import (
	"context"

	"github.com/LYD99/simple-agent-framework/planner"
)

type InterruptType int

const (
	InterruptBeforeToolCall InterruptType = iota
	InterruptAfterPlan
	InterruptOnEscalate
)

type Interrupter interface {
	ShouldInterrupt(ctx context.Context, event InterruptEvent) (bool, error)
	WaitForHuman(ctx context.Context, event InterruptEvent) (*HumanResponse, error)
}

type InterruptEvent struct {
	Type   InterruptType
	Action planner.Action
}

type HumanResponse struct {
	Approved      bool
	Message       string
	ModifiedInput map[string]any
}
