package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-playground/validator/v10"
)

// OutputController handles the extraction, repair, and validation of LLM outputs.
type OutputController struct {
	// SchemaValidator is the instance used for struct-level validation tags.
	SchemaValidator *validator.Validate
	// OutputSchema stores the reflect.Type of the target data structure.
	OutputSchema reflect.Type
	// AutoRetryTimes is a configuration hint for external retry loops.
	AutoRetryTimes int
}

// NewOutputController creates a new instance.
// schema: can be an instance of a struct (MyStruct{}), a slice ([]int{}), or basic types (0, "", false).
func NewOutputController(schema interface{}, v *validator.Validate) *OutputController {
	return &OutputController{
		SchemaValidator: v,
		OutputSchema:    reflect.TypeOf(schema),
		AutoRetryTimes:  3,
	}
}

// ValidateOutput orchestrates the full pipeline: Extract -> Repair -> Unmarshal -> Validate.
func (oc *OutputController) ValidateOutput(output string) (interface{}, error) {
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
			return nil, fmt.Errorf("parsing failed: %v (extracted: %s)", err, content)
		}
	}

	// 6. Data Validation.
	// Elem() gets the actual value the pointer points to.
	val := reflect.ValueOf(ptr).Elem().Interface()

	switch oc.OutputSchema.Kind() {
	case reflect.Struct:
		// Validate struct fields based on tags.
		if err := oc.SchemaValidator.Struct(val); err != nil {
			return nil, err
		}
	case reflect.Slice, reflect.Array:
		// Validate elements within a slice.
		if err := oc.SchemaValidator.Struct(ptr); err != nil {
			return nil, err
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
	codeBlock := regexp.MustCompile(`(?s)[\s\S]*?"json)?\s*(.*?)\s*` + "```" + `[\s\S]*`)
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
func (oc *OutputController) fallbackBasicTypes(content string, ptr interface{}) error {
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
