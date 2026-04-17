package outputhook

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/tailscale/hujson"

	agenterrs "simple-agent-framework/errors"
)

// OutputController handles the extraction, repair, and validation of LLM outputs.
type OutputController struct {
	// SchemaValidator is the instance used for struct-level validation tags.
	schemaValidator *validator.Validate
	// OutputSchema stores the reflect.Type of the target data structure.
	OutputSchema reflect.Type
	// AutoRetryTimes is a configuration hint for external retry loops.
	AutoRetryTimes int
}

// NewOutputController creates a new instance.
// schema: can be an instance of a struct (MyStruct{}), a slice ([]int{}), or basic types (0, "", false).
func NewOutputController(schema any, AutoRetryTimes int) *OutputController {
	return &OutputController{
		schemaValidator: validator.New(),
		OutputSchema:    reflect.TypeOf(schema),
		AutoRetryTimes:  AutoRetryTimes,
	}
}

// ValidateOutput orchestrates the full pipeline: Extract -> Repair -> Unmarshal -> Validate.
func (oc *OutputController) ValidateOutput(output string) (any, error) {
	raw := strings.TrimSpace(output)
	if raw == "" {
		return nil, errors.New("input text is empty")
	}

	// 1. Extract the core content (removes Markdown tags or conversational noise).
	content := oc.extractContent(raw)

	// 2. Dynamically create a pointer to the target type (e.g., *int, *MyStruct, *[]string).
	ptr := reflect.New(oc.OutputSchema).Interface()

	// 3. Prepare data for Unmarshal.
	finalBytes := []byte(content)

	// Only attempt JSON repair if the content looks like a JSON object or array.
	if strings.HasPrefix(content, "{") || strings.HasPrefix(content, "[") {
		if standardized, err := oc.repairJSON(content); err == nil {
			finalBytes = standardized
		}
	}

	// 4. Perform Unmarshal.
	err := json.Unmarshal(finalBytes, ptr)

	// 5. Fallback for basic types if Unmarshal fails (handles cases where LLM omits quotes for strings/numbers).
	if err != nil {
		if errFallback := oc.fallbackBasicTypes(content, ptr); errFallback == nil {
			err = nil
		} else {
			return nil, oc.buildStructuredError(
				fmt.Sprintf("JSON parsing failed: %v", err),
				[]agenterrs.FieldViolation{{
					Field:    "parse_error",
					Rule:     "",
					Expected: "",
					Actual:   content,
					Message:  err.Error(),
				}},
			)
		}
	}

	// 6. Data Validation.
	// Elem() gets the actual value the pointer points to.
	val := reflect.ValueOf(ptr).Elem().Interface()

	switch oc.OutputSchema.Kind() {
	case reflect.Struct:
		if err := oc.schemaValidator.Struct(val); err != nil {
			return nil, oc.validationErrorsToStructured(err)
		}
	case reflect.Slice, reflect.Array:
		if err := oc.schemaValidator.Struct(ptr); err != nil {
			return nil, oc.validationErrorsToStructured(err)
		}
	default:
		// For basic types, additional custom logic can be added here if needed.
	}

	return val, nil
}

// extractContent isolates the JSON or raw value from the LLM's prose.
func (oc *OutputController) extractContent(input string) string {
	// Priority 1: Match Markdown code blocks (e.g., ```json { ... } ```).
	// This regex handles optional 'json' label and captures the content inside.
	codeBlock := regexp.MustCompile("```(?:json)?\\s*([\\s\\S]*?)\\s*```")
	if match := codeBlock.FindStringSubmatch(input); len(match) > 1 {
		return strings.TrimSpace(match[1])
	}

	k := oc.OutputSchema.Kind()

	// Priority 2: Use boundary detection for complex types.
	if k == reflect.Struct {
		if start, end := strings.Index(input, "{"), strings.LastIndex(input, "}"); start != -1 && end > start {
			return input[start : end+1]
		}
	} else if k == reflect.Slice || k == reflect.Array {
		if start, end := strings.Index(input, "["), strings.LastIndex(input, "]"); start != -1 && end > start {
			return input[start : end+1]
		}
	}

	// Priority 3: Return raw input for basic types (int, string, bool).
	return input
}

// repairJSON uses hujson to fix common LLM mistakes like trailing commas.
func (oc *OutputController) repairJSON(input string) ([]byte, error) {
	ast, err := hujson.Parse([]byte(input))
	if err != nil {
		return nil, err
	}
	ast.Standardize() // Fixes trailing commas, comments, etc.
	return ast.Pack(), nil
}

// fallbackBasicTypes handles cases where the LLM returns unquoted values for basic Go types.
func (oc *OutputController) fallbackBasicTypes(content string, ptr any) error {
	v := reflect.ValueOf(ptr).Elem()
	cleanContent := strings.Trim(content, "\"") // Handle cases with mixed quotes

	switch v.Kind() {
	case reflect.String:
		v.SetString(content)
		return nil
	case reflect.Int, reflect.Int64:
		i, err := strconv.ParseInt(cleanContent, 10, 64)
		if err == nil {
			v.SetInt(i)
			return nil
		}
	case reflect.Float64:
		f, err := strconv.ParseFloat(cleanContent, 64)
		if err == nil {
			v.SetFloat(f)
			return nil
		}
	case reflect.Bool:
		b, err := strconv.ParseBool(strings.ToLower(cleanContent))
		if err == nil {
			v.SetBool(b)
			return nil
		}
	}
	return errors.New("type mismatch and fallback failed")
}

func (oc *OutputController) buildStructuredError(message string, violations []agenterrs.FieldViolation) *agenterrs.StructuredValidationError {
	return &agenterrs.StructuredValidationError{
		Message:        message,
		ExpectedSchema: oc.OutputSchema.String(),
		Violations:     violations,
		Hint:           "Please output valid JSON strictly matching the expected schema. Pay attention to required fields, field types, and enum constraints.",
	}
}

func (oc *OutputController) validationErrorsToStructured(err error) *agenterrs.StructuredValidationError {
	var ve validator.ValidationErrors
	if errors.As(err, &ve) {
		violations := make([]agenterrs.FieldViolation, 0, len(ve))
		for _, fe := range ve {
			violations = append(violations, agenterrs.FieldViolation{
				Field:    fe.StructNamespace(),
				Rule:     fe.Tag(),
				Expected: fe.Param(),
				Actual:   fmt.Sprintf("%v", fe.Value()),
				Message:  fe.Error(),
			})
		}
		return oc.buildStructuredError("schema validation failed", violations)
	}
	return oc.buildStructuredError(err.Error(), []agenterrs.FieldViolation{{
		Field:    "validation",
		Rule:     "",
		Expected: "",
		Actual:   "",
		Message:  err.Error(),
	}})
}
