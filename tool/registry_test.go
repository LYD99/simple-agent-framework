package tool

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

type WeatherInput struct {
	Location string `json:"location" description:"City name, e.g. Shanghai" required:"true"`
	Unit     string `json:"unit"     description:"Temperature unit"          enum:"celsius,fahrenheit"`
}

type SearchInput struct {
	Query   string   `json:"query"   description:"Search keywords"   required:"true"`
	Limit   int      `json:"limit"   description:"Maximum number of results"`
	Filters []string `json:"filters" description:"List of filter expressions"`
}

type Address struct {
	City   string `json:"city"   description:"City"   required:"true"`
	Street string `json:"street" description:"Street"`
}

type UserInput struct {
	Name    string  `json:"name"    description:"User name" required:"true"`
	Age     int     `json:"age"     description:"Age"`
	Active  bool    `json:"active"  description:"Whether the user is active"`
	Score   float64 `json:"score"   description:"Score"`
	Address Address `json:"address" description:"Address"`
}

type EmbeddedBase struct {
	Verbose bool `json:"verbose" description:"Whether to emit verbose output"`
}

type CommandInput struct {
	EmbeddedBase
	Command string `json:"command" description:"Command to execute" required:"true"`
}

type literalTool struct {
	name        string
	description string
	schema      *SchemaProperty
	result      string
}

func (l *literalTool) Name() string {
	return l.name
}

func (l *literalTool) Description() string {
	return l.description
}

func (l *literalTool) Schema() *SchemaProperty {
	return l.schema
}

func (l *literalTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	_ = ctx
	_ = input
	return l.result, nil
}

func TestGenerateSchema_BasicStruct(t *testing.T) {
	schema := GenerateSchema(reflect.TypeOf(WeatherInput{}))

	if schema.Type != "object" {
		t.Fatalf("expected type 'object', got %q", schema.Type)
	}
	if len(schema.Properties) != 2 {
		t.Fatalf("expected 2 properties, got %d", len(schema.Properties))
	}
	if schema.Properties["location"] == nil {
		t.Fatal("missing property 'location'")
	}
	if schema.Properties["location"].Type != "string" {
		t.Errorf("location type: want 'string', got %q", schema.Properties["location"].Type)
	}
	if schema.Properties["unit"].Enum == nil || len(schema.Properties["unit"].Enum) != 2 {
		t.Errorf("unit enum: want 2 values, got %v", schema.Properties["unit"].Enum)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "location" {
		t.Errorf("required: want [location], got %v", schema.Required)
	}
}

func TestGenerateSchema_SliceField(t *testing.T) {
	schema := GenerateSchema(reflect.TypeOf(SearchInput{}))

	filters := schema.Properties["filters"]
	if filters == nil {
		t.Fatal("missing property 'filters'")
	}
	if filters.Type != "array" {
		t.Errorf("filters type: want 'array', got %q", filters.Type)
	}
	if filters.Items == nil || filters.Items.Type != "string" {
		t.Errorf("filters items type: want 'string', got %v", filters.Items)
	}
}

func TestGenerateSchema_NestedStruct(t *testing.T) {
	schema := GenerateSchema(reflect.TypeOf(UserInput{}))

	addr := schema.Properties["address"]
	if addr == nil {
		t.Fatal("missing property 'address'")
	}
	if addr.Type != "object" {
		t.Errorf("address type: want 'object', got %q", addr.Type)
	}
	if addr.Properties["city"] == nil {
		t.Fatal("missing nested property 'city'")
	}
	if len(addr.Required) != 1 || addr.Required[0] != "city" {
		t.Errorf("address required: want [city], got %v", addr.Required)
	}
}

func TestGenerateSchema_EmbeddedStruct(t *testing.T) {
	schema := GenerateSchema(reflect.TypeOf(CommandInput{}))

	if schema.Properties["verbose"] == nil {
		t.Fatal("embedded field 'verbose' should be flattened into parent")
	}
	if schema.Properties["command"] == nil {
		t.Fatal("missing property 'command'")
	}
	if len(schema.Required) != 1 || schema.Required[0] != "command" {
		t.Errorf("required: want [command], got %v", schema.Required)
	}
}

func TestGenerateSchema_NumericTypes(t *testing.T) {
	schema := GenerateSchema(reflect.TypeOf(UserInput{}))

	if schema.Properties["age"].Type != "integer" {
		t.Errorf("age type: want 'integer', got %q", schema.Properties["age"].Type)
	}
	if schema.Properties["score"].Type != "number" {
		t.Errorf("score type: want 'number', got %q", schema.Properties["score"].Type)
	}
	if schema.Properties["active"].Type != "boolean" {
		t.Errorf("active type: want 'boolean', got %q", schema.Properties["active"].Type)
	}
}

func TestGenerateSchema_JSONOutput(t *testing.T) {
	schema := GenerateSchema(reflect.TypeOf(WeatherInput{}))
	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		t.Fatalf("json marshal failed: %v", err)
	}
	t.Logf("WeatherInput schema:\n%s", string(data))
}

func TestToolRegistry_AddTool(t *testing.T) {
	reg := NewToolRegistry()
	schema := GenerateSchema(reflect.TypeOf(WeatherInput{}))
	lt := &literalTool{
		name:        "literal",
		description: "literal tool",
		schema:      schema,
		result:      "ok",
	}
	reg.AddTool(lt)

	if got, ok := reg.Get("literal"); !ok || got != lt {
		t.Fatalf("Get: want literal tool, ok=%v", ok)
	}
	names := reg.Names()
	if len(names) != 1 || names[0] != "literal" {
		t.Errorf("Names: want [literal], got %v", names)
	}
	tools := reg.Tools()
	if len(tools) != 1 || tools[0].Name() != "literal" {
		t.Errorf("Tools: want one tool named literal, got %+v", tools)
	}
	ctx := context.Background()
	result, err := reg.Execute(ctx, "literal", map[string]any{})
	if err != nil || result != "ok" {
		t.Fatalf("Execute: err=%v result=%q", err, result)
	}
}

func TestToolRegistry_Get(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register("get_weather", "Get the weather", WeatherInput{}, func(input map[string]interface{}) (string, error) {
		return "sunny", nil
	})

	got, ok := reg.Get("get_weather")
	if !ok {
		t.Fatal("expected tool to exist")
	}
	if got.Name() != "get_weather" || got.Description() != "Get the weather" {
		t.Errorf("Get: wrong tool: name=%q desc=%q", got.Name(), got.Description())
	}
	if got.Schema() == nil || got.Schema().Type != "object" {
		t.Fatal("Get: expected object schema")
	}

	_, ok = reg.Get("missing")
	if ok {
		t.Error("Get: expected false for missing tool")
	}
}

func TestToolRegistry_RegisterAndExecute(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register("get_weather", "Get the weather", WeatherInput{}, func(input map[string]interface{}) (string, error) {
		return "sunny, 25°C", nil
	})

	tools := reg.Tools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].Name() != "get_weather" {
		t.Errorf("tool name: want 'get_weather', got %q", tools[0].Name())
	}

	ctx := context.Background()
	result, err := reg.Execute(ctx, "get_weather", map[string]any{"location": "Shanghai"})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if result != "sunny, 25°C" {
		t.Errorf("result: want 'sunny, 25°C', got %q", result)
	}

	_, err = reg.Execute(ctx, "non_existent", nil)
	if err == nil {
		t.Error("expected error for non-existent tool")
	}
}
