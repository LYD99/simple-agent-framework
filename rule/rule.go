package rule

// Rule represents a guideline consumed by the agent via progressive disclosure.
//
// Frontmatter format (same as a Cursor Rule):
//
//	---
//	description: one-line summary
//	alwaysApply: true
//	---
//
// alwaysApply=true  → the full content is injected directly into the system prompt.
// alwaysApply=false → only the summary appears in the catalog; the model must
//
//	call rule_view to load the full body on demand.
type Rule interface {
	Name() string
	Description() string
	Content() string
	AlwaysApply() bool
}

// FileRule is a file-backed Rule implementation.
type FileRule struct {
	name        string
	description string
	content     string
	alwaysApply bool
}

func NewFileRule(name, description, content string, alwaysApply bool) *FileRule {
	return &FileRule{name: name, description: description, content: content, alwaysApply: alwaysApply}
}

func (r *FileRule) Name() string        { return r.name }
func (r *FileRule) Description() string { return r.description }
func (r *FileRule) Content() string     { return r.content }
func (r *FileRule) AlwaysApply() bool   { return r.alwaysApply }
