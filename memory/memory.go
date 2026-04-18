package memory

import (
	"context"

	"github.com/LYD99/simple-agent-framework/model"
)

type Memory interface {
	Messages(ctx context.Context) ([]model.ChatMessage, error)
	Add(ctx context.Context, msgs ...model.ChatMessage) error
	Clear(ctx context.Context) error
}
