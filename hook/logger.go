package hook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

const loggerPreviewLimit = 160

type LoggerMode int

const (
	LoggerModeConcise LoggerMode = iota
	LoggerModeDetailed
)

// LoggerHook writes hook events to an io.Writer as human-readable lines.
type LoggerHook struct {
	w      io.Writer
	prefix string
	mode   LoggerMode
}

// NewLogger creates a LoggerHook that writes to w with the default prefix "[agent]".
func NewLogger(w io.Writer) *LoggerHook {
	return &LoggerHook{w: w, prefix: "[agent]", mode: LoggerModeDetailed}
}

// NewLoggerWithPrefix creates a LoggerHook with a custom prefix.
func NewLoggerWithPrefix(w io.Writer, prefix string) *LoggerHook {
	return &LoggerHook{w: w, prefix: prefix, mode: LoggerModeDetailed}
}

// NewLoggerWithMode creates a LoggerHook with the default prefix and mode.
func NewLoggerWithMode(w io.Writer, mode LoggerMode) *LoggerHook {
	return &LoggerHook{w: w, prefix: "[agent]", mode: mode}
}

// NewLoggerWithPrefixAndMode creates a LoggerHook with a custom prefix and mode.
func NewLoggerWithPrefixAndMode(w io.Writer, prefix string, mode LoggerMode) *LoggerHook {
	return &LoggerHook{w: w, prefix: prefix, mode: mode}
}

// OnEvent implements Hook.
func (l *LoggerHook) OnEvent(ctx context.Context, event Event) error {
	_ = ctx
	ts := event.Timestamp.Format(time.RFC3339)
	name := eventTypeName(event.Type)
	fmt.Fprintf(l.w, "%s %s %s", ts, l.prefix, name)

	switch event.Type {
	case EventPlanDone:
		if p, ok := planDonePayload(event.Payload); ok {
			fmt.Fprintf(l.w, " iteration=%d", p.Iteration)
			if l.mode == LoggerModeDetailed {
				fmt.Fprintf(l.w, " duration=%s", p.Duration)
				if reasoning := previewText(p.Reasoning, loggerPreviewLimit); reasoning != "" {
					fmt.Fprintf(l.w, " reasoning=%q", reasoning)
				} else if action := previewText(p.ActionSummary, loggerPreviewLimit); action != "" {
					fmt.Fprintf(l.w, " action=%q", action)
				}
			}
			fmt.Fprintf(l.w, "\n")
		} else {
			fmt.Fprintf(l.w, " payload_type=%T\n", event.Payload)
		}
	case EventToolCallStart:
		if p, ok := toolCallStartPayload(event.Payload); ok {
			fmt.Fprintf(l.w, " tool=%q", p.ToolName)
			if l.mode == LoggerModeDetailed {
				if input := previewJSON(p.Input, loggerPreviewLimit); input != "" {
					fmt.Fprintf(l.w, " input=%s", input)
				}
			}
			fmt.Fprintf(l.w, "\n")
		} else {
			fmt.Fprintf(l.w, " payload_type=%T\n", event.Payload)
		}
	case EventToolCallDone:
		if p, ok := toolCallDonePayload(event.Payload); ok {
			fmt.Fprintf(l.w, " tool=%q", p.ToolName)
			if l.mode == LoggerModeDetailed {
				fmt.Fprintf(l.w, " duration=%s", p.Duration)
				if output := previewText(p.Output, loggerPreviewLimit); output != "" {
					fmt.Fprintf(l.w, " output=%q", output)
				}
			}
			if p.Error != nil {
				fmt.Fprintf(l.w, " err=%q", p.Error.Error())
			}
			fmt.Fprintf(l.w, "\n")
		} else {
			fmt.Fprintf(l.w, " payload_type=%T\n", event.Payload)
		}
	case EventEvalStart:
		if p, ok := evalStartPayload(event.Payload); ok {
			fmt.Fprintf(l.w, " iteration=%d steps=%d\n", p.Iteration, p.Steps)
		} else {
			fmt.Fprintf(l.w, "\n")
		}
	case EventEvalDone:
		if p, ok := evalDonePayload(event.Payload); ok {
			fmt.Fprintf(l.w, " iteration=%d steps=%d decision=%q", p.Iteration, p.Steps, p.Decision)
			if l.mode == LoggerModeDetailed {
				fmt.Fprintf(l.w, " duration=%s", p.Duration)
				if fb := previewText(p.Feedback, loggerPreviewLimit); fb != "" {
					fmt.Fprintf(l.w, " feedback=%q", fb)
				}
			}
			fmt.Fprintf(l.w, "\n")
		} else {
			fmt.Fprintf(l.w, " payload_type=%T\n", event.Payload)
		}
	case EventError:
		if p, ok := errorPayload(event.Payload); ok {
			errStr := "<nil>"
			if p.Error != nil {
				errStr = p.Error.Error()
			}
			fmt.Fprintf(l.w, " error=%q state=%q\n", errStr, p.State)
		} else {
			fmt.Fprintf(l.w, " payload_type=%T\n", event.Payload)
		}
	case EventRuleView:
		if name, ok := stringPayload(event.Payload); ok {
			fmt.Fprintf(l.w, " rule=%q\n", name)
		} else {
			fmt.Fprintf(l.w, " payload=%s\n", previewJSON(event.Payload, loggerPreviewLimit))
		}
	case EventSkillCallStart:
		if payload, ok := stringMapPayload(event.Payload); ok {
			fmt.Fprintf(l.w, " skill=%q", payload["skill"])
			if l.mode == LoggerModeDetailed {
				if input := previewText(payload["input"], loggerPreviewLimit); input != "" {
					fmt.Fprintf(l.w, " input=%q", input)
				}
			}
			fmt.Fprintf(l.w, "\n")
		} else {
			fmt.Fprintf(l.w, " payload=%s\n", previewJSON(event.Payload, loggerPreviewLimit))
		}
	case EventSkillCallDone:
		if payload, ok := stringMapPayload(event.Payload); ok {
			fmt.Fprintf(l.w, " skill=%q", payload["skill"])
			if l.mode == LoggerModeDetailed {
				if output := previewText(payload["output"], loggerPreviewLimit); output != "" {
					fmt.Fprintf(l.w, " output=%q", output)
				}
			}
			fmt.Fprintf(l.w, "\n")
		} else {
			fmt.Fprintf(l.w, " payload=%s\n", previewJSON(event.Payload, loggerPreviewLimit))
		}
	case EventSkillContextLog:
		if l.mode == LoggerModeDetailed {
			switch v := event.Payload.(type) {
			case string:
				fmt.Fprintf(l.w, " log=%q\n", previewText(v, loggerPreviewLimit))
			default:
				fmt.Fprintf(l.w, " log=%s\n", previewJSON(event.Payload, loggerPreviewLimit))
			}
		} else {
			fmt.Fprintf(l.w, "\n")
		}
	default:
		fmt.Fprintf(l.w, "\n")
	}
	return nil
}

func planDonePayload(v any) (*PlanDonePayload, bool) {
	switch p := v.(type) {
	case *PlanDonePayload:
		if p == nil {
			return nil, false
		}
		return p, true
	case PlanDonePayload:
		return &p, true
	default:
		return nil, false
	}
}

func toolCallStartPayload(v any) (*ToolCallStartPayload, bool) {
	switch p := v.(type) {
	case *ToolCallStartPayload:
		if p == nil {
			return nil, false
		}
		return p, true
	case ToolCallStartPayload:
		return &p, true
	default:
		return nil, false
	}
}

func toolCallDonePayload(v any) (*ToolCallDonePayload, bool) {
	switch p := v.(type) {
	case *ToolCallDonePayload:
		if p == nil {
			return nil, false
		}
		return p, true
	case ToolCallDonePayload:
		return &p, true
	default:
		return nil, false
	}
}

func evalStartPayload(v any) (*EvalStartPayload, bool) {
	switch p := v.(type) {
	case *EvalStartPayload:
		if p == nil {
			return nil, false
		}
		return p, true
	case EvalStartPayload:
		return &p, true
	default:
		return nil, false
	}
}

func evalDonePayload(v any) (*EvalDonePayload, bool) {
	switch p := v.(type) {
	case *EvalDonePayload:
		if p == nil {
			return nil, false
		}
		return p, true
	case EvalDonePayload:
		return &p, true
	default:
		return nil, false
	}
}

func errorPayload(v any) (*ErrorPayload, bool) {
	switch p := v.(type) {
	case *ErrorPayload:
		if p == nil {
			return nil, false
		}
		return p, true
	case ErrorPayload:
		return &p, true
	default:
		return nil, false
	}
}

func stringPayload(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

func stringMapPayload(v any) (map[string]string, bool) {
	switch p := v.(type) {
	case map[string]string:
		return p, true
	default:
		return nil, false
	}
}

func previewJSON(v any, limit int) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%q", previewText(fmt.Sprint(v), limit))
	}
	return fmt.Sprintf("%q", previewText(string(b), limit))
}

func previewText(s string, limit int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if s == "" || limit <= 0 {
		return s
	}
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "...(truncated)"
}

func eventTypeName(t EventType) string {
	switch t {
	case EventPlanStart:
		return "PLAN_START"
	case EventPlanDone:
		return "PLAN_DONE"
	case EventToolCallStart:
		return "TOOL_CALL_START"
	case EventToolCallDone:
		return "TOOL_CALL_DONE"
	case EventEvalStart:
		return "EVAL_START"
	case EventEvalDone:
		return "EVAL_DONE"
	case EventLoopComplete:
		return "LOOP_COMPLETE"
	case EventError:
		return "ERROR"
	case EventStreamChunk:
		return "STREAM_CHUNK"
	case EventSkillContextLog:
		return "SKILL_CONTEXT_LOG"
	case EventRuleView:
		return "RULE_VIEW"
	case EventSkillCallStart:
		return "SKILL_CALL_START"
	case EventSkillCallDone:
		return "SKILL_CALL_DONE"
	default:
		return fmt.Sprintf("EVENT_%d", t)
	}
}
