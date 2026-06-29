package metrics

import (
	"context"
	"sync"
	"time"
)

type MetricsHandler interface {
	OnMetrics(ctx context.Context, metric ModelCallMetric) error
}

type ModelCallMetric struct {
	Timestamp        time.Time
	ModelName        string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Duration         time.Duration
	RPM              float64
	TPM              float64
}

type SlidingWindowCounter struct {
	mu         sync.Mutex
	windowSize time.Duration
	calls      []time.Time
	tokens     []int
	head       int
	count      int
}

func NewSlidingWindowCounter(windowSize time.Duration, maxSamples int) *SlidingWindowCounter {
	return &SlidingWindowCounter{
		windowSize: windowSize,
		calls:      make([]time.Time, maxSamples),
		tokens:     make([]int, maxSamples),
	}
}

func (s *SlidingWindowCounter) Add(now time.Time, tokenCount int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanup(now)

	idx := (s.head + s.count) % len(s.calls)
	s.calls[idx] = now
	s.tokens[idx] = tokenCount

	if s.count < len(s.calls) {
		s.count++
	} else {
		s.head = (s.head + 1) % len(s.calls)
	}
}

func (s *SlidingWindowCounter) cleanup(now time.Time) {
	cutoff := now.Add(-s.windowSize)
	expiredCount := 0

	for i := 0; i < s.count; i++ {
		idx := (s.head + i) % len(s.calls)
		if s.calls[idx].After(cutoff) {
			break
		}
		s.calls[idx] = time.Time{}
		s.tokens[idx] = 0
		expiredCount++
	}

	if expiredCount > 0 {
		s.head = (s.head + expiredCount) % len(s.calls)
		s.count -= expiredCount
	}
}

func (s *SlidingWindowCounter) RPM(now time.Time) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanup(now)

	if s.count == 0 {
		return 0
	}

	windowSeconds := s.windowSize.Seconds()
	if windowSeconds <= 0 {
		return 0
	}

	return float64(s.count) / windowSeconds * 60
}

func (s *SlidingWindowCounter) TPM(now time.Time) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanup(now)

	if s.count == 0 {
		return 0
	}

	totalTokens := 0
	for i := 0; i < s.count; i++ {
		idx := (s.head + i) % len(s.calls)
		totalTokens += s.tokens[idx]
	}

	windowSeconds := s.windowSize.Seconds()
	if windowSeconds <= 0 {
		return 0
	}

	return float64(totalTokens) / windowSeconds * 60
}
