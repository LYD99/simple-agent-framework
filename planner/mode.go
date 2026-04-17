package planner

type ExecutionMode int

const (
	ModeReAct ExecutionMode = iota
	ModePlanAndSolve
)

func (m ExecutionMode) String() string {
	switch m {
	case ModeReAct:
		return "ReAct"
	case ModePlanAndSolve:
		return "PlanAndSolve"
	default:
		return "Unknown"
	}
}
