package model

import (
	"context"
	"time"

	"github.com/LYD99/simple-agent-framework/metrics"
)

type metricsModel struct {
	model        ChatModel
	modelName    string
	handler      metrics.MetricsHandler
	counter      *metrics.SlidingWindowCounter
	windowSize   time.Duration
	maxSamples   int
}

func WrapWithMetrics(model ChatModel, modelName string, handler metrics.MetricsHandler) ChatModel {
	return WrapWithMetricsAndWindow(model, modelName, handler, 60*time.Second, 1000)
}

func WrapWithMetricsAndWindow(model ChatModel, modelName string, handler metrics.MetricsHandler, windowSize time.Duration, maxSamples int) ChatModel {
	return &metricsModel{
		model:        model,
		modelName:    modelName,
		handler:      handler,
		counter:      metrics.NewSlidingWindowCounter(windowSize, maxSamples),
		windowSize:   windowSize,
		maxSamples:   maxSamples,
	}
}

func (m *metricsModel) Generate(ctx context.Context, messages []ChatMessage, opts ...Option) (*ChatResponse, error) {
	start := time.Now()
	resp, err := m.model.Generate(ctx, messages, opts...)
	duration := time.Since(start)
	now := time.Now()

	if err == nil && resp != nil {
		m.counter.Add(now, resp.Usage.TotalTokens)
		m.reportMetrics(now, resp.Usage, duration)
	}

	return resp, err
}

func (m *metricsModel) Stream(ctx context.Context, messages []ChatMessage, opts ...Option) (*StreamIterator, error) {
	start := time.Now()
	iter, err := m.model.Stream(ctx, messages, opts...)
	if err != nil {
		return nil, err
	}

	msgCh := make(chan StreamChunk, 128)
	errCh := make(chan error, 1)

	go func() {
		defer close(msgCh)
		defer close(errCh)

		var finalUsage *Usage

		for iter.Next() {
			chunk := iter.Chunk()
			if chunk.Usage != nil {
				finalUsage = chunk.Usage
			}
			select {
			case <-ctx.Done():
				return
			case msgCh <- chunk:
			}
		}

		if err := iter.Err(); err != nil {
			select {
			case <-ctx.Done():
			case errCh <- err:
			}
			return
		}

		duration := time.Since(start)
		now := time.Now()

		if finalUsage != nil {
			m.counter.Add(now, finalUsage.TotalTokens)
			m.reportMetrics(now, *finalUsage, duration)
		}
	}()

	return NewStreamIterator(msgCh, errCh), nil
}

func (m *metricsModel) reportMetrics(now time.Time, usage Usage, duration time.Duration) {
	if m.handler == nil {
		return
	}

	metric := metrics.ModelCallMetric{
		Timestamp:        now,
		ModelName:        m.modelName,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
		Duration:         duration,
		RPM:              m.counter.RPM(now),
		TPM:              m.counter.TPM(now),
	}

	_ = m.handler.OnMetrics(context.Background(), metric)
}
