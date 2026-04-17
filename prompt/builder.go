package prompt

import (
	"context"
	"sort"
	"strings"
)

type Section struct {
	Name     string
	Content  string
	Priority int
}

type PromptBuilder struct {
	base     string
	rules    []RuleSummary
	skills   []SkillSummary
	sections []Section
	engine   *TemplateEngine
}

func NewBuilder(base string) *PromptBuilder {
	return &PromptBuilder{base: base}
}

func (b *PromptBuilder) WithRules(rules []RuleSummary) *PromptBuilder {
	b.rules = rules
	return b
}

func (b *PromptBuilder) WithSkills(skills []SkillSummary) *PromptBuilder {
	b.skills = skills
	return b
}

func (b *PromptBuilder) AddSection(name, content string, priority int) *PromptBuilder {
	b.sections = append(b.sections, Section{Name: name, Content: content, Priority: priority})
	return b
}

func (b *PromptBuilder) WithTemplateEngine(e *TemplateEngine) *PromptBuilder {
	b.engine = e
	return b
}

func (b *PromptBuilder) Build(ctx context.Context) string {
	_ = ctx
	var parts []string

	if b.base != "" {
		parts = append(parts, b.base)
	}

	// Always-on rules: inject full content inline
	if block := BuildAlwaysOnRules(b.rules); block != "" {
		parts = append(parts, block)
	}
	// On-demand rules: catalog only (LLM uses rule_view)
	if catalog := BuildRuleCatalog(b.rules); catalog != "" {
		parts = append(parts, catalog)
	}

	// Always-on skills: inject instruction inline
	if block := BuildAlwaysOnSkills(b.skills); block != "" {
		parts = append(parts, block)
	}
	// On-demand skills: catalog only (LLM uses skill_call)
	if catalog := BuildSkillCatalog(b.skills); catalog != "" {
		parts = append(parts, catalog)
	}

	sort.Slice(b.sections, func(i, j int) bool {
		return b.sections[i].Priority < b.sections[j].Priority
	})
	for _, s := range b.sections {
		parts = append(parts, s.Content)
	}

	return strings.Join(parts, "\n\n")
}
