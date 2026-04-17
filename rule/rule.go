package rule

// Rule 规则接口 — 渐进式披露范式
//
// Frontmatter 格式 (同 Cursor Rule):
//
//	---
//	description: 一句话摘要
//	alwaysApply: true
//	---
//
// alwaysApply=true  → 完整内容直接注入 System Prompt
// alwaysApply=false → 仅摘要出现在 catalog，模型按需调用 rule_view 加载
type Rule interface {
	Name() string
	Description() string
	Content() string
	AlwaysApply() bool
}

// FileRule 基于文件的 Rule 实现
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
