package prompt

import (
	"bytes"
	"fmt"
	"sync"
	"text/template"
)

type TemplateEngine struct {
	mu        sync.RWMutex
	templates map[string]*template.Template
}

func NewTemplateEngine() *TemplateEngine {
	return &TemplateEngine{
		templates: make(map[string]*template.Template),
	}
}

func (e *TemplateEngine) Register(name, tmpl string) error {
	if name == "" {
		return fmt.Errorf("prompt: empty template name")
	}
	t, err := template.New(name).Parse(tmpl)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.templates[name] = t
	return nil
}

func (e *TemplateEngine) Render(name string, data any) (string, error) {
	e.mu.RLock()
	t, ok := e.templates[name]
	e.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("prompt: template %q not registered", name)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
