package agent

import "github.com/LYD99/simple-agent-framework/hook"

type PlanStartPayload = hook.PlanStartPayload

type PlanDonePayload = hook.PlanDonePayload

type ToolCallStartPayload = hook.ToolCallStartPayload

type ToolCallDonePayload = hook.ToolCallDonePayload

type ErrorPayload = hook.ErrorPayload

type EvalStartPayload = hook.EvalStartPayload

type EvalDonePayload = hook.EvalDonePayload
