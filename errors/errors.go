package errors

import (
	"encoding/json"
	"errors"
)

var (
	ErrMaxIterationsExceeded = errors.New("max iterations exceeded")
	ErrTokenBudgetExceeded   = errors.New("token budget exceeded")
	ErrTimeout               = errors.New("operation timed out")
	ErrHITLDenied            = errors.New("human denied the action")
	ErrHITLTimeout           = errors.New("human response timed out")
	ErrToolNotFound          = errors.New("tool not registered")
	ErrToolExecFailed        = errors.New("tool execution failed")
	ErrOutputValidation      = errors.New("output validation failed")
	ErrModelUnavailable      = errors.New("model provider unavailable")
	ErrLoopDetected          = errors.New("suspected infinite loop: model repeatedly called same tool with identical parameters")
)

type StructuredValidationError struct {
	Message        string           `json:"message"`
	ExpectedSchema string           `json:"expected_schema"`
	Violations     []FieldViolation `json:"violations"`
	Hint           string           `json:"hint"`
}

type FieldViolation struct {
	Field    string `json:"field"`
	Rule     string `json:"rule"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Message  string `json:"message"`
}

func (e *StructuredValidationError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return "structured validation error"
}

func (e *StructuredValidationError) FormatForModel() (string, error) {
	if e == nil {
		return "null", nil
	}
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
