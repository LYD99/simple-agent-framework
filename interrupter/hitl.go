package interrupter

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/LYD99/simple-agent-framework/planner"
)

// ApprovalFunc 审批回调函数类型
type ApprovalFunc func(event InterruptEvent) (*HumanResponse, error)

type HITLConfig struct {
	RequireApproval []string      // 需要审批的工具名列表 (空 = 全部需要)
	AutoApproveRead bool          // 读取类操作自动通过
	WaitTimeout     time.Duration // 等待人类响应超时
}

type HITLHandler struct {
	config     HITLConfig
	approvalFn ApprovalFunc
}

func NewHITL(approvalFn ApprovalFunc, opts ...HITLOption) *HITLHandler {
	h := &HITLHandler{
		config:     HITLConfig{},
		approvalFn: approvalFn,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

type HITLOption func(*HITLHandler)

func WithRequireApproval(tools ...string) HITLOption {
	return func(h *HITLHandler) {
		h.config.RequireApproval = append([]string(nil), tools...)
	}
}

func WithAutoApproveRead(auto bool) HITLOption {
	return func(h *HITLHandler) {
		h.config.AutoApproveRead = auto
	}
}

func WithWaitTimeout(d time.Duration) HITLOption {
	return func(h *HITLHandler) {
		h.config.WaitTimeout = d
	}
}

func isReadClassTool(toolName string) bool {
	name := strings.ToLower(strings.TrimSpace(toolName))
	if name == "" {
		return false
	}
	// 常见读取/查询类工具命名启发式
	keywords := []string{
		"read", "get", "list", "fetch", "query", "search", "retrieve", "lookup", "view", "download",
	}
	for _, kw := range keywords {
		if strings.Contains(name, kw) {
			return true
		}
	}
	return false
}

func toolInList(toolName string, list []string) bool {
	for _, t := range list {
		if t == toolName {
			return true
		}
	}
	return false
}

func (h *HITLHandler) ShouldInterrupt(ctx context.Context, event InterruptEvent) (bool, error) {
	switch event.Type {
	case InterruptAfterPlan, InterruptOnEscalate:
		return true, nil
	case InterruptBeforeToolCall:
		return h.shouldInterruptForTool(event.Action)
	default:
		return true, nil
	}
}

func (h *HITLHandler) shouldInterruptForTool(action planner.Action) (bool, error) {
	if action.Type != planner.ActionToolCall {
		return false, nil
	}
	toolName := action.ToolName
	if h.config.AutoApproveRead && isReadClassTool(toolName) {
		return false, nil
	}
	if len(h.config.RequireApproval) > 0 {
		return toolInList(toolName, h.config.RequireApproval), nil
	}
	return true, nil
}

func (h *HITLHandler) WaitForHuman(ctx context.Context, event InterruptEvent) (*HumanResponse, error) {
	if h.approvalFn == nil {
		return nil, errors.New("interrupter: approval function is nil")
	}
	waitCtx := ctx
	if h.config.WaitTimeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, h.config.WaitTimeout)
		defer cancel()
	}
	type result struct {
		resp *HumanResponse
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		resp, err := h.approvalFn(event)
		ch <- result{resp: resp, err: err}
	}()
	select {
	case <-waitCtx.Done():
		return nil, waitCtx.Err()
	case r := <-ch:
		return r.resp, r.err
	}
}
