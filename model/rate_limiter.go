package model

import (
	"context"
	"sync"
	"time"
)

// TokenBucket 令牌桶限流器
type TokenBucket struct {
	mu       sync.Mutex
	capacity float64 // 桶容量
	tokens   float64 // 当前令牌数
	refillRate float64 // 每秒补充令牌数
	lastRefill time.Time // 上次补充时间
}

// NewTokenBucket 创建令牌桶
func NewTokenBucket(capacity int, refillRate float64) *TokenBucket {
	return &TokenBucket{
		capacity:   float64(capacity),
		tokens:     float64(capacity),
		refillRate: refillRate,
		lastRefill: time.Now(),
	}
}

// Allow 检查是否允许请求通过
func (t *TokenBucket) Allow() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.refill()

	if t.tokens >= 1 {
		t.tokens -= 1
		return true
	}
	return false
}

// Wait 等待获取令牌直到 context 取消或获取成功
func (t *TokenBucket) Wait(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		t.mu.Lock()
		t.refill()

		if t.tokens >= 1 {
			t.tokens -= 1
			t.mu.Unlock()
			return nil
		}

		// 计算需要等待的时间
		waitTime := time.Duration((1 - t.tokens) / t.refillRate * float64(time.Second))
		t.mu.Unlock()

		// 等待一小段时间后重试
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitTime):
		}
	}
}

// refill 补充令牌
func (t *TokenBucket) refill() {
	now := time.Now()
	elapsed := now.Sub(t.lastRefill).Seconds()
	addTokens := elapsed * t.refillRate
	t.tokens += addTokens
	if t.tokens > t.capacity {
		t.tokens = t.capacity
	}
	t.lastRefill = now
}

// Tokens 返回当前令牌数
func (t *TokenBucket) Tokens() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.tokens
}

// SlidingWindow 滑动窗口限流器
type SlidingWindow struct {
	mu         sync.Mutex
	windowSize time.Duration // 窗口大小
	maxReqs    int           // 窗口内最大请求数
	requests   []time.Time   // 请求时间记录（环形缓冲区）
	head       int           // 缓冲区头位置
	count      int           // 当前窗口内请求数
}

// NewSlidingWindow 创建滑动窗口限流器
func NewSlidingWindow(windowSize time.Duration, maxReqs int) *SlidingWindow {
	return &SlidingWindow{
		windowSize: windowSize,
		maxReqs:    maxReqs,
		requests:   make([]time.Time, maxReqs),
	}
}

// Allow 检查是否允许请求通过
func (s *SlidingWindow) Allow() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanup()

	if s.count < s.maxReqs {
		s.addRequest(time.Now())
		return true
	}
	return false
}

// Wait 等待直到 context 取消或请求被允许
func (s *SlidingWindow) Wait(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		s.mu.Lock()
		s.cleanup()

		if s.count < s.maxReqs {
			s.addRequest(time.Now())
			s.mu.Unlock()
			return nil
		}

		// 计算需要等待的时间
		oldest := s.requests[s.head]
		waitTime := s.windowSize - time.Since(oldest)
		s.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitTime):
		}
	}
}

// cleanup 清理过期的请求记录（标准大厂工程级写法）
func (s *SlidingWindow) cleanup() {
	// 注意：实际工程中，此函数应该在持有 mu 锁的情况下被调用
	now := time.Now()
	cutoff := now.Add(-s.windowSize)

	// 从 head 开始，逐个检查过期的元素
	// 因为时间是单调递增的，过期的必然连续堆积在 head 附近
	expiredCount := 0
	for i := 0; i < s.count; i++ {
		idx := (s.head + i) % len(s.requests)

		if s.requests[idx].After(cutoff) {
			// 遇到了第一个没过期的请求，后面的一定也没过期，直接退出循环
			break
		}

		// 属于过期数据，清除脏数据（良好的工程习惯）
		s.requests[idx] = time.Time{}
		expiredCount++
	}

	// 如果有数据过期了，更新 head 和 count
	if expiredCount > 0 {
		s.head = (s.head + expiredCount) % len(s.requests)
		s.count -= expiredCount
	}
}

// addRequest 添加新请求
func (s *SlidingWindow) addRequest(t time.Time) {
	idx := (s.head + s.count) % len(s.requests)
	s.requests[idx] = t
	if s.count < len(s.requests) {
		s.count++
	} else {
		// 缓冲区满了，头向前移动
		s.head = (s.head + 1) % len(s.requests)
	}
}

// Count 返回当前窗口内请求数
func (s *SlidingWindow) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanup()
	return s.count
}

// RateLimiter 组合限流器（令牌桶 + 滑动窗口）
type RateLimiter struct {
	tokenBucket   *TokenBucket
	slidingWindow *SlidingWindow
}

// NewRateLimiter 创建组合限流器
// tokenCapacity: 令牌桶容量（每秒补充 tokensPerSecond 个令牌）
// tokensPerSecond: 每秒补充令牌数
// windowSize: 滑动窗口大小
// maxInWindow: 滑动窗口内最大请求数
func NewRateLimiter(tokenCapacity int, tokensPerSecond float64, windowSize time.Duration, maxInWindow int) *RateLimiter {
	return &RateLimiter{
		tokenBucket:   NewTokenBucket(tokenCapacity, tokensPerSecond),
		slidingWindow: NewSlidingWindow(windowSize, maxInWindow),
	}
}

// Allow 检查是否允许请求通过（两个条件都满足）
func (r *RateLimiter) Allow() bool {
	return r.tokenBucket.Allow() && r.slidingWindow.Allow()
}

// Wait 等待直到两个条件都满足
func (r *RateLimiter) Wait(ctx context.Context) error {
	// 先等令牌桶
	if err := r.tokenBucket.Wait(ctx); err != nil {
		return err
	}
	// 再等滑动窗口
	return r.slidingWindow.Wait(ctx)
}
