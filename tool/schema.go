package tool

import (
	"reflect"
	"strings"
)

type SchemaProperty struct {
	Type        string                     `json:"type"`
	Description string                     `json:"description,omitempty"`
	Enum        []string                   `json:"enum,omitempty"`
	Properties  map[string]*SchemaProperty `json:"properties,omitempty"`
	Required    []string                   `json:"required,omitempty"`
	Items       *SchemaProperty            `json:"items,omitempty"`
}

func GenerateSchema(t reflect.Type) *SchemaProperty {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return generateType(t)
}

func generateType(t reflect.Type) *SchemaProperty {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	switch t.Kind() {
	case reflect.Struct:
		return generateObject(t)
	case reflect.Slice, reflect.Array:
		return &SchemaProperty{
			Type:  "array",
			Items: generateType(t.Elem()),
		}
	case reflect.Map:
		return &SchemaProperty{Type: "object"}
	default:
		return &SchemaProperty{Type: goKindToJSONType(t.Kind())}
	}
}

func generateObject(t reflect.Type) *SchemaProperty {
	props := make(map[string]*SchemaProperty, t.NumField())
	var required []string

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}

		if field.Anonymous && field.Type.Kind() == reflect.Struct {
			embedded := generateObject(field.Type)
			for k, v := range embedded.Properties {
				props[k] = v
			}
			required = append(required, embedded.Required...)
			continue
		}

		name := fieldJSONName(field)
		if name == "-" {
			continue
		}

		prop := generateType(field.Type)

		if desc := field.Tag.Get("description"); desc != "" {
			prop.Description = desc
		}
		if enumTag := field.Tag.Get("enum"); enumTag != "" {
			prop.Enum = strings.Split(enumTag, ",")
		}
		if field.Tag.Get("required") == "true" {
			required = append(required, name)
		}

		props[name] = prop
	}

	schema := &SchemaProperty{
		Type:       "object",
		Properties: props,
	}
	if len(required) > 0 {
		schema.Required = required
	}
	return schema
}

func fieldJSONName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" {
		return f.Name
	}
	name := strings.SplitN(tag, ",", 2)[0]
	if name == "" {
		return f.Name
	}
	return name
}

func goKindToJSONType(k reflect.Kind) string {
	switch k {
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Bool:
		return "boolean"
	default:
		return "string"
	}
}
