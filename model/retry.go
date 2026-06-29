package model

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// RetryableError 可重试的错误接口
type RetryableError interface {
	IsRetryable() bool
	GetRetryAfter() time.Duration
}

// DefaultRetryableError 默认可重试错误
type DefaultRetryableError struct {
	err         string
	retryable   bool
	retryAfter  time.Duration
}

func (e *DefaultRetryableError) Error() string { return e.err }
func (e *DefaultRetryableError) IsRetryable() bool { return e.retryable }
func (e *DefaultRetryableError) GetRetryAfter() time.Duration { return e.retryAfter }

var (
	// ErrMaxRetriesExceeded 超过最大重试次数
	ErrMaxRetriesExceeded = errors.New("max retries exceeded")
	// ErrNonRetryable 非可重试错误
	ErrNonRetryable = errors.New("non-retryable error")
)

// ExponentialBackoff 指数退避配置
type ExponentialBackoff struct {
	BaseDelay    time.Duration // 基础延迟（默认 1s）
	MaxDelay     time.Duration // 最大延迟（默认 60s）
	MaxRetries   int           // 最大重试次数（默认 3）
	Multiplier   float64       // 延迟乘数（默认 2.0）
	Jitter       bool          // 是否添加随机抖动
}

// DefaultExponentialBackoff 返回默认配置
func DefaultExponentialBackoff() *ExponentialBackoff {
	return &ExponentialBackoff{
		BaseDelay:  1 * time.Second,
		MaxDelay:   60 * time.Second,
		MaxRetries: 3,
		Multiplier: 2.0,
		Jitter:     true,
	}
}

// ExponentialBackoffOption 配置选项
type ExponentialBackoffOption func(*ExponentialBackoff)

func WithBaseDelay(d time.Duration) ExponentialBackoffOption {
	return func(e *ExponentialBackoff) { e.BaseDelay = d }
}

func WithMaxDelay(d time.Duration) ExponentialBackoffOption {
	return func(e *ExponentialBackoff) { e.MaxDelay = d }
}

func WithMaxRetries(n int) ExponentialBackoffOption {
	return func(e *ExponentialBackoff) { e.MaxRetries = n }
}

func WithMultiplier(m float64) ExponentialBackoffOption {
	return func(e *ExponentialBackoff) { e.Multiplier = m }
}

func WithJitter(j bool) ExponentialBackoffOption {
	return func(e *ExponentialBackoff) { e.Jitter = j }
}

// Do 执行带指数退避的重试
func (e *ExponentialBackoff) Do(ctx context.Context, fn func() error) error {
	var lastErr error
	delay := e.BaseDelay

	for attempt := 0; attempt <= e.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}

		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		// 检查是否是可重试错误
		if !e.isRetryable(lastErr) {
			return lastErr
		}

		// 计算下一次延迟
		delay = e.nextDelay(delay)
	}

	return fmt.Errorf("%w: %v", ErrMaxRetriesExceeded, lastErr)
}

// isRetryable 判断错误是否可重试
func (e *ExponentialBackoff) isRetryable(err error) bool {
	if err == nil {
		return false
	}

	// 检查是否是 RetryableError
	var re RetryableError
	if errors.As(err, &re) {
		return re.IsRetryable()
	}

	// 检查 HTTP 429 错误
	var httpErr *HTTPStatusError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusTooManyRequests {
		return true
	}

	// 检查是否是 context 取消
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// 默认可重试的网络错误
	return true
}

// nextDelay 计算下一次延迟
func (e *ExponentialBackoff) nextDelay(currentDelay time.Duration) time.Duration {
	delay := time.Duration(float64(currentDelay) * e.Multiplier)
	if delay > e.MaxDelay {
		delay = e.MaxDelay
	}

	if e.Jitter {
		// 添加 0-25% 的随机抖动
		jitter := time.Duration(float64(delay) * 0.25 * (float64(time.Now().UnixNano()%100) / 100.0))
		delay += jitter
	}

	return delay
}

// HTTPStatusError HTTP 状态错误
type HTTPStatusError struct {
	StatusCode int
	Message    string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("http status %d: %s", e.StatusCode, e.Message)
}

func (e *HTTPStatusError) IsRetryable() bool {
	// 429 Too Many Requests 和 5xx 服务器错误可重试
	return e.StatusCode == http.StatusTooManyRequests || (e.StatusCode >= 500 && e.StatusCode < 600)
}

func (e *HTTPStatusError) GetRetryAfter() time.Duration {
	// 429 错误通常会有 Retry-After 头，这里简化处理
	if e.StatusCode == http.StatusTooManyRequests {
		return 1 * time.Second
	}
	return 0
}

// IsRateLimitError 判断是否是限流错误
func IsRateLimitError(err error) bool {
	var re *HTTPStatusError
	if errors.As(err, &re) {
		return re.StatusCode == http.StatusTooManyRequests
	}
	return false
}

// RetryWithBackoff 带退避的重试辅助函数
func RetryWithBackoff(ctx context.Context, opts ...ExponentialBackoffOption) error {
	eb := DefaultExponentialBackoff()
	for _, opt := range opts {
		opt(eb)
	}

	var lastErr error
	delay := eb.BaseDelay

	for attempt := 0; attempt <= eb.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}

		lastErr = ctx.Err()
		if lastErr != nil {
			return lastErr
		}

		delay = eb.nextDelay(delay)
	}

	return fmt.Errorf("%w: %v", ErrMaxRetriesExceeded, lastErr)
}
