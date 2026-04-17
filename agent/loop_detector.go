package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const DefaultLoopThreshold = 3

type LoopStatus int

const (
	LoopNormal LoopStatus = iota
	LoopWarning
	LoopTerminate
)

type toolCallSig struct {
	ToolName  string
	ParamHash string
}

type LoopDetector struct {
	threshold int
	history   []toolCallSig
	warned    bool
}

func NewLoopDetector(threshold int) *LoopDetector {
	if threshold <= 0 {
		threshold = DefaultLoopThreshold
	}
	return &LoopDetector{threshold: threshold}
}

func (d *LoopDetector) Record(toolName string, params map[string]any) LoopStatus {
	sig := toolCallSig{ToolName: toolName, ParamHash: hashParams(params)}
	d.history = append(d.history, sig)
	if len(d.history) < d.threshold {
		return LoopNormal
	}
	window := d.history[len(d.history)-d.threshold:]
	first := window[0]
	for i := 1; i < len(window); i++ {
		if window[i] != first {
			d.warned = false
			return LoopNormal
		}
	}
	if !d.warned {
		d.warned = true
		return LoopWarning
	}
	return LoopTerminate
}

func hashParams(params map[string]any) string {
	b, err := json.Marshal(params)
	if err != nil {
		b = nil
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func InjectLoopWarning(toolResult string, toolName string, threshold int) string {
	warning := fmt.Sprintf(`<loop_detection_warning>
You have called tool "%s" %d times consecutively with identical parameters. Please reconsider your approach:
1. Check if previous results already satisfy the requirement
2. Consider using a different tool or modifying the parameters
3. If the task is complete, output your final answer directly
WARNING: Calling the same tool with the same parameters again will terminate execution.
</loop_detection_warning>

%s`, toolName, threshold, toolResult)
	return warning
}
