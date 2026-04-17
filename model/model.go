package model

import (
	"context"
	"errors"
)

var ErrStreamNotSupported = errors.New("stream not supported")

type ChatModel interface {
	Generate(ctx context.Context, messages []ChatMessage, opts ...Option) (*ChatResponse, error)
	Stream(ctx context.Context, messages []ChatMessage, opts ...Option) (*StreamIterator, error)
}

type StreamIterator struct {
	msgChan <-chan StreamChunk
	errChan <-chan error
	cur     StreamChunk
	err     error
}

type StreamChunk struct {
	Delta     string
	ToolCalls []ToolCall
	Done      bool
	Usage     *Usage
}

func NewStreamIterator(msgCh <-chan StreamChunk, errCh <-chan error) *StreamIterator {
	return &StreamIterator{msgChan: msgCh, errChan: errCh}
}

func (s *StreamIterator) Next() bool {
	select {
	case chunk, ok := <-s.msgChan:
		if !ok {
			return false
		}
		s.cur = chunk
		return true
	case err := <-s.errChan:
		if err != nil {
			s.err = err
		}
		return false
	}
}

func (s *StreamIterator) Chunk() StreamChunk { return s.cur }
func (s *StreamIterator) Err() error         { return s.err }
