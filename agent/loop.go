package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/LYD99/simple-agent-framework/errors"
	"github.com/LYD99/simple-agent-framework/evaluator"
	"github.com/LYD99/simple-agent-framework/executor"
	"github.com/LYD99/simple-agent-framework/hook"
	"github.com/LYD99/simple-agent-framework/interrupter"
	"github.com/LYD99/simple-agent-framework/memory"
	"github.com/LYD99/simple-agent-framework/model"
	"github.com/LYD99/simple-agent-framework/planner"
)

type LoopState int

const (
	StateInit LoopState = iota
	StatePlanGen
	StatePlanReview
	StatePlanning
	StateInterrupt
	StateExecuting
	StateEvaluating
	StateCompleting
	StateComplete
	StateError
)

// runLoop drives the agent state machine within this session's isolated context.
func (s *Session) runLoop(ctx context.Context) (*AgentResult, error) {
	start := time.Now()
	a := s.agent

	ctx = s.injectSessionContext(ctx)

	state := StateInit
	var iteration int
	var history []planner.StepResult
	var pendingAction *planner.Action
	var pendingReasoning string
	var pendingToolCallID string
	var planGenFirst *planner.PlanResult
	var completeAnswer string
	var lastExec *toolExecSnapshot
	var evalEscalate bool

	a.mu.RLock()
	pl := a.planner
	mode := a.currentMode
	maxIter := a.maxIterations
	outSchema := a.outputSchema
	validateLeft := a.autoRetry
	evalOn := a.evalEnabled && a.evaluator != nil
	pas := a.planAndSolvePlanner
	streamOn := a.streamEnabled && runStreamFromContext(ctx)
	modelMaxTokens := a.modelMaxTokens
	a.mu.RUnlock()

	if pl == nil {
		err := fmt.Errorf("agent: planner is nil (set model or WithPlanner)")
		_ = a.emit(ctx, hook.EventError, ErrorPayload{Error: err, State: "init"})
		return &AgentResult{Error: err, Duration: time.Since(start)}, nil
	}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		switch state {
		case StateInit:
			if mode == planner.ModePlanAndSolve {
				state = StatePlanGen
			} else {
				state = StatePlanning
			}

		case StatePlanGen:
			if pas == nil {
				err := fmt.Errorf("agent: plan-and-solve planner unavailable")
				_ = a.emit(ctx, hook.EventError, ErrorPayload{Error: err, State: "plan_gen"})
				return &AgentResult{Error: err, Duration: time.Since(start)}, nil
			}
			ps, err := s.buildPlanState(ctx, history)
			if err != nil {
				_ = a.emit(ctx, hook.EventError, ErrorPayload{Error: err, State: "plan_gen"})
				return &AgentResult{Error: err, Duration: time.Since(start)}, nil
			}
			if pas.CurrentPlan() == nil {
				res, err := pas.Plan(ctx, ps)
				if err != nil {
					_ = a.emit(ctx, hook.EventError, ErrorPayload{Error: err, State: "plan_gen"})
					return &AgentResult{Error: err, Duration: time.Since(start)}, nil
				}
				planGenFirst = res
			}
			state = StatePlanReview

		case StatePlanReview:
			if a.interrupter != nil {
				digest := planDigest(pas)
				evt := interrupter.InterruptEvent{
					Type:   interrupter.InterruptAfterPlan,
					Action: planner.Action{Type: planner.ActionFinalAnswer, Answer: digest},
				}
				should, err := a.interrupter.ShouldInterrupt(ctx, evt)
				if err != nil {
					_ = a.emit(ctx, hook.EventError, ErrorPayload{Error: err, State: "plan_review"})
					return &AgentResult{Error: err, Duration: time.Since(start)}, nil
				}
				if should {
					hr, err := a.interrupter.WaitForHuman(ctx, evt)
					if err != nil {
						if ctx.Err() != nil {
							return nil, ctx.Err()
						}
						_ = a.emit(ctx, hook.EventError, ErrorPayload{Error: err, State: "plan_review"})
						return &AgentResult{Error: err, Duration: time.Since(start)}, nil
					}
					if hr == nil || !hr.Approved {
						msg := ""
						if hr != nil {
							msg = hr.Message
						}
						_ = s.memory.Add(ctx, model.ChatMessage{Role: model.RoleUser, Content: fmt.Sprintf("Plan review rejected: %s", msg)})
						ps2, err := s.buildPlanState(ctx, history)
						if err != nil {
							return &AgentResult{Error: err, Duration: time.Since(start)}, nil
						}
						if err := pas.Replan(ctx, ps2); err != nil {
							return &AgentResult{Error: err, Duration: time.Since(start)}, nil
						}
						state = StatePlanReview
						continue
					}
				}
			}
			state = StatePlanning

		case StatePlanning:
			iteration++
			if iteration > maxIter {
				return &AgentResult{
					Error:    errors.ErrMaxIterationsExceeded,
					Duration: time.Since(start),
				}, nil
			}
			ps, err := s.buildPlanState(ctx, history)
			if err != nil {
				_ = a.emit(ctx, hook.EventError, ErrorPayload{Error: err, State: "planning"})
				return &AgentResult{Error: err, Duration: time.Since(start)}, nil
			}

			if s.compressor != nil && a.model != nil {
				if s.compressor.ShouldCompress(ps.Messages, modelMaxTokens) {
					compressed, compErr := s.compressor.Compress(ctx, ps.Messages)
					if compErr == nil {
						_ = s.memory.Clear(ctx)
						if len(compressed) > 1 {
							_ = s.memory.Add(ctx, compressed[1:]...)
						}
						ps, err = s.buildPlanState(ctx, history)
						if err != nil {
							_ = a.emit(ctx, hook.EventError, ErrorPayload{Error: err, State: "planning"})
							return &AgentResult{Error: err, Duration: time.Since(start)}, nil
						}
					}
				}
			}

			var res *planner.PlanResult
			if planGenFirst != nil {
				res = planGenFirst
				planGenFirst = nil
			} else {
				t0 := time.Now()
				_ = a.emit(ctx, hook.EventPlanStart, PlanStartPayload{Iteration: iteration})
				if streamOn && mode == planner.ModeReAct && a.model != nil {
					res, err = streamReActPlan(ctx, a, ps)
					if err == model.ErrStreamNotSupported {
						res, err = pl.Plan(ctx, ps)
					}
				} else {
					res, err = pl.Plan(ctx, ps)
				}
				if err != nil {
					_ = a.emit(ctx, hook.EventError, ErrorPayload{Error: err, State: "planning"})
					return &AgentResult{Error: err, Duration: time.Since(start)}, nil
				}
				_ = a.emit(ctx, hook.EventPlanDone, PlanDonePayload{
					Iteration:     iteration,
					Reasoning:     res.Reasoning,
					ActionSummary: summarizePlanAction(res.Action),
					Duration:      time.Since(t0),
				})
			}

			act := res.Action
			pendingReasoning = res.Reasoning
			pendingAction = &act

			switch act.Type {
			case planner.ActionToolCall:
				state = StateInterrupt
			case planner.ActionAskHuman:
				state = StateInterrupt
			case planner.ActionFinalAnswer:
				completeAnswer = strings.TrimSpace(act.Answer)
				state = StateCompleting
			default:
				err := fmt.Errorf("agent: unknown action type")
				_ = a.emit(ctx, hook.EventError, ErrorPayload{Error: err, State: "planning"})
				return &AgentResult{Error: err, Duration: time.Since(start)}, nil
			}

		case StateInterrupt:
			if pendingAction == nil {
				err := fmt.Errorf("agent: no pending action")
				_ = a.emit(ctx, hook.EventError, ErrorPayload{Error: err, State: "interrupt"})
				return &AgentResult{Error: err, Duration: time.Since(start)}, nil
			}
			if evalEscalate {
				evt := interrupter.InterruptEvent{
					Type:   interrupter.InterruptOnEscalate,
					Action: *pendingAction,
				}
				if a.interrupter != nil {
					should, err := a.interrupter.ShouldInterrupt(ctx, evt)
					if err != nil {
						return &AgentResult{Error: err, Duration: time.Since(start)}, nil
					}
					if should {
						hr, err := a.interrupter.WaitForHuman(ctx, evt)
						if err != nil {
							if ctx.Err() != nil {
								return nil, ctx.Err()
							}
							return &AgentResult{Error: errors.ErrHITLTimeout, Duration: time.Since(start)}, nil
						}
						if hr == nil || !hr.Approved {
							msg := ""
							if hr != nil {
								msg = hr.Message
							}
							_ = s.memory.Add(ctx, model.ChatMessage{Role: model.RoleUser, Content: fmt.Sprintf("Escalation review: %s", msg)})
						}
					}
				}
				evalEscalate = false
				pendingAction = nil
				state = StatePlanning
				continue
			}
			evt := interrupter.InterruptEvent{
				Type:   interrupter.InterruptBeforeToolCall,
				Action: *pendingAction,
			}
			if pendingAction.Type == planner.ActionAskHuman {
				evt.Type = interrupter.InterruptOnEscalate
			}
			if a.interrupter != nil {
				should, err := a.interrupter.ShouldInterrupt(ctx, evt)
				if err != nil {
					_ = a.emit(ctx, hook.EventError, ErrorPayload{Error: err, State: "interrupt"})
					return &AgentResult{Error: err, Duration: time.Since(start)}, nil
				}
				if should {
					hr, err := a.interrupter.WaitForHuman(ctx, evt)
					if err != nil {
						if ctx.Err() != nil {
							return nil, ctx.Err()
						}
						_ = a.emit(ctx, hook.EventError, ErrorPayload{Error: err, State: "interrupt"})
						return &AgentResult{Error: errors.ErrHITLTimeout, Duration: time.Since(start)}, nil
					}
					if hr == nil || !hr.Approved {
						msg := ""
						if hr != nil {
							msg = hr.Message
						}
						_ = s.memory.Add(ctx, model.ChatMessage{Role: model.RoleUser, Content: fmt.Sprintf("Action denied or revised: %s", msg)})
						state = StatePlanning
						continue
					}
					if hr.ModifiedInput != nil && pendingAction.Type == planner.ActionToolCall {
						pendingAction.ToolInput = hr.ModifiedInput
					}
					if pendingAction.Type == planner.ActionAskHuman {
						q := strings.TrimSpace(pendingAction.Answer)
						ans := hr.Message
						_ = s.memory.Add(ctx,
							model.ChatMessage{Role: model.RoleAssistant, Content: q},
							model.ChatMessage{Role: model.RoleUser, Content: ans},
						)
						state = StatePlanning
						continue
					}
				} else if pendingAction.Type == planner.ActionAskHuman {
					q := strings.TrimSpace(pendingAction.Answer)
					_ = s.memory.Add(ctx,
						model.ChatMessage{Role: model.RoleAssistant, Content: q},
						model.ChatMessage{Role: model.RoleUser, Content: "(interrupt skipped by interrupter)"},
					)
					state = StatePlanning
					continue
				}
			}
			if pendingAction.Type == planner.ActionAskHuman {
				if a.interrupter == nil {
					q := strings.TrimSpace(pendingAction.Answer)
					_ = s.memory.Add(ctx,
						model.ChatMessage{Role: model.RoleAssistant, Content: q},
						model.ChatMessage{Role: model.RoleUser, Content: "(no human interrupter; provide answer in next turn if needed)"},
					)
				}
				state = StatePlanning
				continue
			}
			pendingToolCallID = fmt.Sprintf("tc_%d_%d", iteration, time.Now().UnixNano())
			args := pendingAction.ToolInput
			if args == nil {
				args = map[string]any{}
			}
			_ = s.memory.Add(ctx, model.ChatMessage{
				Role:    model.RoleAssistant,
				Content: pendingReasoning,
				ToolCalls: []model.ToolCall{{
					ID:        pendingToolCallID,
					Name:      pendingAction.ToolName,
					Arguments: args,
				}},
			})
			state = StateExecuting

		case StateExecuting:
			if pendingAction == nil {
				err := fmt.Errorf("agent: no pending action for execute")
				return &AgentResult{Error: err, Duration: time.Since(start)}, nil
			}

			loopStatus := s.loopDetector.Record(pendingAction.ToolName, pendingAction.ToolInput)
			if loopStatus == LoopTerminate {
				_ = a.emit(ctx, hook.EventError, ErrorPayload{Error: errors.ErrLoopDetected, State: "executing"})
				return &AgentResult{Error: errors.ErrLoopDetected, Duration: time.Since(start)}, nil
			}

			t0 := time.Now()
			_ = a.emit(ctx, hook.EventToolCallStart, ToolCallStartPayload{
				ToolName: pendingAction.ToolName,
				Input:    pendingAction.ToolInput,
			})
			execRes, err := a.executor.Execute(ctx, *pendingAction)
			if err != nil {
				_ = a.emit(ctx, hook.EventError, ErrorPayload{Error: err, State: "executing"})
				return &AgentResult{Error: err, Duration: time.Since(start)}, nil
			}
			lastExec = &toolExecSnapshot{res: execRes, toolCallID: pendingToolCallID, action: *pendingAction}
			_ = a.emit(ctx, hook.EventToolCallDone, ToolCallDonePayload{
				ToolName: pendingAction.ToolName,
				Output:   execRes.Output,
				Error:    execRes.Error,
				Duration: time.Since(t0),
			})

			out := execRes.Output
			if execRes.Error != nil {
				out = execRes.Error.Error()
			}
			out = memory.TruncateToolResult(ctx, out, a.toolResultMaxLen, s.contentStore, pendingToolCallID)
			if loopStatus == LoopWarning {
				out = InjectLoopWarning(out, pendingAction.ToolName, a.loopDetectionThreshold)
			}
			_ = s.memory.Add(ctx, model.ChatMessage{
				Role:       model.RoleTool,
				ToolCallID: pendingToolCallID,
				Name:       pendingAction.ToolName,
				Content:    out,
			})

			msgs, memErr := s.memory.Messages(ctx)
			if memErr == nil {
				pruned := memory.PruneConsecutiveFailures(msgs)
				compacted := memory.CompactStaleToolResults(pruned, a.recentToolResultTokens)
				_ = s.memory.Clear(ctx)
				_ = s.memory.Add(ctx, compacted...)
			}

			step := planner.StepResult{Action: *pendingAction, Output: execRes.Output, Error: execRes.Error}
			history = append(history, step)

			if mode == planner.ModePlanAndSolve && pas != nil {
				if rs := runningPlanStep(pas); rs != nil {
					if execRes.Error != nil {
						pas.MarkStepFailed(rs.StepID, execRes.Error)
					} else {
						pas.MarkStepDone(rs.StepID, execRes.Output)
					}
				}
			}

			pendingAction = nil
			pendingToolCallID = ""

			if evalOn {
				state = StateEvaluating
			} else {
				state = StatePlanning
			}

		case StateEvaluating:
			if !evalOn || a.evaluator == nil {
				state = StatePlanning
				continue
			}
			msgs, err := s.memory.Messages(ctx)
			if err != nil {
				return &AgentResult{Error: err, Duration: time.Since(start)}, nil
			}
			evSt := &evaluator.EvalState{
				Messages:    msgs,
				Iteration:   iteration,
				StepResults: historyToEvalSteps(history),
			}
			t0 := time.Now()
			_ = a.emit(ctx, hook.EventEvalStart, EvalStartPayload{
				Iteration: iteration,
				Steps:     len(history),
			})
			ev, err := a.evaluator.Evaluate(ctx, evSt)
			if err != nil {
				_ = a.emit(ctx, hook.EventError, ErrorPayload{Error: err, State: "evaluating"})
				return &AgentResult{Error: err, Duration: time.Since(start)}, nil
			}
			_ = a.emit(ctx, hook.EventEvalDone, EvalDonePayload{
				Iteration: iteration,
				Steps:     len(history),
				Decision:  decisionName(ev.Decision),
				Feedback:  ev.Feedback,
				Duration:  time.Since(t0),
			})

			switch ev.Decision {
			case evaluator.DecisionContinue:
				state = StatePlanning
			case evaluator.DecisionComplete:
				if strings.TrimSpace(ev.Feedback) != "" {
					completeAnswer = strings.TrimSpace(ev.Feedback)
				} else {
					completeAnswer = lastOutputFromHistory(history)
				}
				state = StateCompleting
			case evaluator.DecisionRetry:
				if lastExec != nil {
					pendingAction = &lastExec.action
					pendingToolCallID = lastExec.toolCallID
					pendingReasoning = ""
					state = StateExecuting
				} else {
					state = StatePlanning
				}
			case evaluator.DecisionEscalate:
				evalEscalate = true
				if pendingAction == nil && lastExec != nil {
					pendingAction = &lastExec.action
				}
				state = StateInterrupt
			case evaluator.DecisionReplan:
				if mode == planner.ModePlanAndSolve && pas != nil {
					ps, err := s.buildPlanState(ctx, history)
					if err != nil {
						return &AgentResult{Error: err, Duration: time.Since(start)}, nil
					}
					if err := pas.Replan(ctx, ps); err != nil {
						return &AgentResult{Error: err, Duration: time.Since(start)}, nil
					}
					planGenFirst = nil
					state = StatePlanGen
				} else {
					state = StatePlanning
				}
			default:
				state = StatePlanning
			}

		case StateCompleting:
			if strings.TrimSpace(completeAnswer) != "" {
				_ = s.memory.Add(ctx, model.ChatMessage{Role: model.RoleAssistant, Content: completeAnswer})
			}
			if outSchema != nil {
				formatted, err := a.validateFinalOutput(completeAnswer)
				if err != nil {
					if validateLeft > 0 {
						validateLeft--
						feedback := formatted
						if feedback == "" {
							feedback = fmt.Sprintf("Output validation failed (%v). Produce a valid answer.", err)
						}
						_ = s.memory.Add(ctx, model.ChatMessage{
							Role:    model.RoleUser,
							Content: feedback,
						})
						state = StatePlanning
						continue
					}
					_ = a.emit(ctx, hook.EventError, ErrorPayload{Error: errors.ErrOutputValidation, State: "completing"})
					return &AgentResult{Error: errors.ErrOutputValidation, Duration: time.Since(start)}, nil
				}
			}
			_ = a.emit(ctx, hook.EventLoopComplete, nil)
			msgs, err := s.memory.Messages(ctx)
			if err != nil {
				return &AgentResult{Error: err, Duration: time.Since(start)}, nil
			}
			return &AgentResult{
				Answer:   completeAnswer,
				Messages: msgs,
				Duration: time.Since(start),
			}, nil

		case StateComplete, StateError:
			return &AgentResult{Duration: time.Since(start)}, nil
		}
	}
}

type toolExecSnapshot struct {
	res        *executor.ExecResult
	toolCallID string
	action     planner.Action
}

func streamReActPlan(ctx context.Context, a *Agent, state *planner.PlanState) (*planner.PlanResult, error) {
	opts := []model.Option{}
	if toolDefs := toolInfosToModelToolDefs(state.Tools); len(toolDefs) > 0 {
		opts = append(opts, model.WithTools(toolDefs...))
	}
	iter, err := a.model.Stream(ctx, state.Messages, opts...)
	if err != nil {
		return nil, err
	}

	var reasoning strings.Builder
	var toolCalls []model.ToolCall
	for iter.Next() {
		chunk := iter.Chunk()
		if chunk.Delta != "" {
			reasoning.WriteString(chunk.Delta)
			_ = a.emit(ctx, hook.EventStreamChunk, chunk.Delta)
		}
		if len(chunk.ToolCalls) > 0 {
			toolCalls = append(toolCalls[:0], chunk.ToolCalls...)
		}
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}

	reasoningText := strings.TrimSpace(reasoning.String())
	if len(toolCalls) > 0 {
		input, err := normalizeToolInput(toolCalls[0].Arguments)
		if err != nil {
			return nil, fmt.Errorf("react stream: tool arguments: %w", err)
		}
		return &planner.PlanResult{
			Action: planner.Action{
				Type:      planner.ActionToolCall,
				ToolName:  strings.TrimSpace(toolCalls[0].Name),
				ToolInput: input,
			},
			Reasoning: reasoningText,
		}, nil
	}
	return &planner.PlanResult{
		Action: planner.Action{
			Type:   planner.ActionFinalAnswer,
			Answer: reasoningText,
		},
		Reasoning: reasoningText,
	}, nil
}

func toolInfosToModelToolDefs(tools []planner.ToolInfo) []model.ToolDef {
	if len(tools) == 0 {
		return nil
	}
	out := make([]model.ToolDef, 0, len(tools))
	for _, t := range tools {
		params := t.Schema
		if params == nil {
			params = map[string]any{}
		}
		out = append(out, model.ToolDef{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  params,
		})
	}
	return out
}

func normalizeToolInput(v map[string]any) (map[string]any, error) {
	if v == nil {
		return map[string]any{}, nil
	}
	for k, val := range v {
		s, ok := val.(string)
		if !ok {
			continue
		}
		s = strings.TrimSpace(s)
		if s == "" || !json.Valid([]byte(s)) || (s[0] != '{' && s[0] != '[') {
			continue
		}
		var nested any
		if err := json.Unmarshal([]byte(s), &nested); err == nil {
			if m, ok := nested.(map[string]any); ok {
				v[k] = m
			}
		}
	}
	return v, nil
}

func historyToEvalSteps(h []planner.StepResult) []evaluator.StepResult {
	out := make([]evaluator.StepResult, 0, len(h))
	for _, s := range h {
		out = append(out, evaluator.StepResult{
			ToolName: s.Action.ToolName,
			Input:    s.Action.ToolInput,
			Output:   s.Output,
			Error:    s.Error,
		})
	}
	return out
}

func decisionName(d evaluator.Decision) string {
	switch d {
	case evaluator.DecisionContinue:
		return "continue"
	case evaluator.DecisionComplete:
		return "complete"
	case evaluator.DecisionRetry:
		return "retry"
	case evaluator.DecisionEscalate:
		return "escalate"
	case evaluator.DecisionReplan:
		return "replan"
	default:
		return fmt.Sprintf("decision_%d", d)
	}
}

func summarizePlanAction(action planner.Action) string {
	switch action.Type {
	case planner.ActionToolCall:
		return fmt.Sprintf("tool_call:%s", strings.TrimSpace(action.ToolName))
	case planner.ActionAskHuman:
		if answer := strings.TrimSpace(action.Answer); answer != "" {
			return fmt.Sprintf("ask_human:%s", answer)
		}
		return "ask_human"
	case planner.ActionFinalAnswer:
		return "final_answer"
	default:
		return "unknown"
	}
}

func lastOutputFromHistory(h []planner.StepResult) string {
	for i := len(h) - 1; i >= 0; i-- {
		if strings.TrimSpace(h[i].Output) != "" {
			return h[i].Output
		}
	}
	return ""
}

func runningPlanStep(pas *planner.PlanAndSolvePlanner) *planner.PlanStep {
	ap := pas.CurrentPlan()
	if ap == nil {
		return nil
	}
	for i := range ap.Steps {
		if ap.Steps[i].Status == planner.StepRunning {
			return &ap.Steps[i]
		}
	}
	return nil
}

func planDigest(pas *planner.PlanAndSolvePlanner) string {
	if pas == nil {
		return ""
	}
	ap := pas.CurrentPlan()
	if ap == nil {
		return ""
	}
	var b strings.Builder
	for _, s := range ap.Steps {
		fmt.Fprintf(&b, "- step %d: %s (%v)\n", s.StepID, s.Description, s.Action.Type)
	}
	return strings.TrimSpace(b.String())
}
