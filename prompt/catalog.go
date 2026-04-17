package prompt

import (
	"fmt"
	"strings"
)

type RuleSummary struct {
	Name        string
	Description string
	AlwaysApply bool
	Content     string // populated for alwaysApply rules
}

type SkillSummary struct {
	Name        string
	Description string
	AlwaysApply bool
	Content     string // populated for alwaysApply skills
}

// BuildAlwaysOnRules renders alwaysApply=true rules as inline <rules> block.
func BuildAlwaysOnRules(rules []RuleSummary) string {
	var b strings.Builder
	first := true
	for _, r := range rules {
		if !r.AlwaysApply {
			continue
		}
		if first {
			b.WriteString("<rules>\n")
			first = false
		}
		fmt.Fprintf(&b, "<rule name=%q>\n%s\n</rule>\n", r.Name, r.Content)
	}
	if !first {
		b.WriteString("</rules>")
	}
	return b.String()
}

// BuildRuleCatalog renders on-demand rules (alwaysApply=false) as a catalog.
// The LLM uses rule_view to progressively disclose full content.
func BuildRuleCatalog(rules []RuleSummary) string {
	var items []RuleSummary
	for _, r := range rules {
		if !r.AlwaysApply {
			items = append(items, r)
		}
	}
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<available_rules>\n")
	b.WriteString("Use the rule_view tool to read the full content of a rule when needed.\n")
	for _, r := range items {
		fmt.Fprintf(&b, "  <rule name=%q>%s</rule>\n", r.Name, r.Description)
	}
	b.WriteString("</available_rules>")
	return b.String()
}

// BuildAlwaysOnSkills renders alwaysApply=true skills as inline <skills> block.
func BuildAlwaysOnSkills(skills []SkillSummary) string {
	var b strings.Builder
	first := true
	for _, s := range skills {
		if !s.AlwaysApply {
			continue
		}
		if first {
			b.WriteString("<skills>\n")
			first = false
		}
		fmt.Fprintf(&b, "<skill name=%q>\n%s\n</skill>\n", s.Name, s.Content)
	}
	if !first {
		b.WriteString("</skills>")
	}
	return b.String()
}

// BuildSkillCatalog renders on-demand skills (alwaysApply=false) as a catalog.
// The LLM uses skill_call to progressively disclose instruction and create skill_context.
func BuildSkillCatalog(skills []SkillSummary) string {
	var items []SkillSummary
	for _, s := range skills {
		if !s.AlwaysApply {
			items = append(items, s)
		}
	}
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<available_skills>\n")
	b.WriteString("Use the skill_call tool to invoke a skill when needed.\n")
	for _, s := range items {
		fmt.Fprintf(&b, "  <skill name=%q>%s</skill>\n", s.Name, s.Description)
	}
	b.WriteString("</available_skills>")
	return b.String()
}
