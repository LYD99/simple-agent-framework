package memory

import (
	"context"

	"simple-agent-framework/model"
)

type Memory interface {
	Messages(ctx context.Context) ([]model.ChatMessage, error)
	Add(ctx context.Context, msgs ...model.ChatMessage) error
	Clear(ctx context.Context) error
}
