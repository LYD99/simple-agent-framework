package rule

import (
	"os"
	"path/filepath"
	"strings"
)

// LoadPath loads rules from a path — either a single .md file or a directory.
func LoadPath(path string) ([]Rule, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return LoadDir(path)
	}
	if strings.EqualFold(filepath.Ext(path), ".md") {
		r, err := FromFile(path)
		if err != nil {
			return nil, err
		}
		return []Rule{r}, nil
	}
	return nil, nil
}

func LoadDir(dirPath string) ([]Rule, error) {
	var rules []Rule
	err := filepath.WalkDir(dirPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".md") {
			rule, err := FromFile(path)
			if err != nil {
				return err
			}
			rules = append(rules, rule)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rules, nil
}

// FromFile parses a .md file with optional YAML frontmatter.
//
//	---
//	description: 为AI生成技术方案时提供参考
//	alwaysApply: true
//	---
//	(rule body)
//
// Without frontmatter: first line = description, rest = content, alwaysApply = false.
func FromFile(filePath string) (*FileRule, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	desc, alwaysApply, body := parseFrontmatter(string(data))
	return NewFileRule(name, desc, body, alwaysApply), nil
}

// parseFrontmatter extracts description / alwaysApply from YAML frontmatter.
// Falls back to legacy format (first line = description) when no frontmatter.
func parseFrontmatter(text string) (description string, alwaysApply bool, body string) {
	trimmed := strings.TrimLeft(text, "\xef\xbb\xbf") // strip BOM
	if !strings.HasPrefix(trimmed, "---\n") && !strings.HasPrefix(trimmed, "---\r\n") {
		lines := strings.SplitN(trimmed, "\n", 2)
		description = strings.TrimSpace(lines[0])
		if len(lines) > 1 {
			body = strings.TrimSpace(lines[1])
		}
		return
	}

	rest := trimmed[4:] // skip "---\n"
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		body = strings.TrimSpace(trimmed)
		return
	}
	meta := rest[:idx]
	body = strings.TrimSpace(rest[idx+4:])

	for _, line := range strings.Split(meta, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "description:") {
			description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
		} else if strings.HasPrefix(line, "alwaysApply:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "alwaysApply:"))
			alwaysApply = val == "true"
		}
	}
	return
}
