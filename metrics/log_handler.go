package metrics

import (
	"context"
	"fmt"
	"io"
	"time"
)

type LogMetricsHandler struct {
	w      io.Writer
	prefix string
}

func NewLogMetricsHandler(w io.Writer) *LogMetricsHandler {
	return &LogMetricsHandler{w: w, prefix: "[metrics]"}
}

func NewLogMetricsHandlerWithPrefix(w io.Writer, prefix string) *LogMetricsHandler {
	return &LogMetricsHandler{w: w, prefix: prefix}
}

func (h *LogMetricsHandler) OnMetrics(ctx context.Context, metric ModelCallMetric) error {
	_ = ctx
	ts := metric.Timestamp.Format(time.RFC3339)
	fmt.Fprintf(h.w, "%s %s model=%q duration=%s prompt_tokens=%d completion_tokens=%d total_tokens=%d rpm=%.2f tpm=%.2f\n",
		ts, h.prefix, metric.ModelName, metric.Duration,
		metric.PromptTokens, metric.CompletionTokens, metric.TotalTokens,
		metric.RPM, metric.TPM,
	)
	return nil
}
