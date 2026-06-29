package model

import (
	"context"
)

// StreamHandler 流式输出处理接口
// 用户实现此接口来自定义流式输出的接收方式
type StreamHandler interface {
	// OnChunk 接收流式输出的一个 chunk
	OnChunk(ctx context.Context, chunk StreamChunk) error

	// OnComplete 流式输出完成时调用
	OnComplete(ctx context.Context, usage *Usage) error

	// OnError 发生错误时调用
	OnError(ctx context.Context, err error) error
}

// NopStreamHandler 空实现（用于不需要流式输出的场景）
type NopStreamHandler struct{}

func (h *NopStreamHandler) OnChunk(ctx context.Context, chunk StreamChunk) error {
	return nil
}

func (h *NopStreamHandler) OnComplete(ctx context.Context, usage *Usage) error {
	return nil
}

func (h *NopStreamHandler) OnError(ctx context.Context, err error) error {
	return nil
}

// FuncStreamHandler 函数形式的 StreamHandler 适配器
type FuncStreamHandler struct {
	OnChunkFunc    func(ctx context.Context, chunk StreamChunk) error
	OnCompleteFunc func(ctx context.Context, usage *Usage) error
	OnErrorFunc    func(ctx context.Context, err error) error
}

func (h *FuncStreamHandler) OnChunk(ctx context.Context, chunk StreamChunk) error {
	if h.OnChunkFunc != nil {
		return h.OnChunkFunc(ctx, chunk)
	}
	return nil
}

func (h *FuncStreamHandler) OnComplete(ctx context.Context, usage *Usage) error {
	if h.OnCompleteFunc != nil {
		return h.OnCompleteFunc(ctx, usage)
	}
	return nil
}

func (h *FuncStreamHandler) OnError(ctx context.Context, err error) error {
	if h.OnErrorFunc != nil {
		return h.OnErrorFunc(ctx, err)
	}
	return nil
}

// StreamHandlerFunc 将函数转换为 StreamHandler
func StreamHandlerFunc(
	onChunk func(ctx context.Context, chunk StreamChunk) error,
	onComplete func(ctx context.Context, usage *Usage) error,
	onError func(ctx context.Context, err error) error,
) StreamHandler {
	return &FuncStreamHandler{
		OnChunkFunc:    onChunk,
		OnCompleteFunc: onComplete,
		OnErrorFunc:    onError,
	}
}
