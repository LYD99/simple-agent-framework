package skill

import (
	"os"
	"path/filepath"
	"strings"
)

// LoadPath loads skills from a path.
//   - Directory with skill.md → single skill from that dir
//   - Directory without skill.md → scan subdirectories for skill dirs
//   - Single .md file → standalone skill
func LoadPath(path string) ([]Skill, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		if _, err := os.Stat(filepath.Join(path, "skill.md")); err == nil {
			s, err := FromDir(path)
			if err != nil {
				return nil, err
			}
			return []Skill{s}, nil
		}
		return LoadDir(path)
	}
	if strings.EqualFold(filepath.Ext(path), ".md") {
		s, err := FromFile(path)
		if err != nil {
			return nil, err
		}
		return []Skill{s}, nil
	}
	return nil, nil
}

func LoadDir(dirPath string) ([]Skill, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}
	var skills []Skill
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		sub := filepath.Join(dirPath, ent.Name())
		skillMD := filepath.Join(sub, "skill.md")
		if _, err := os.Stat(skillMD); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		s, err := FromDir(sub)
		if err != nil {
			return nil, err
		}
		skills = append(skills, s)
	}
	return skills, nil
}

// FromDir loads a skill from a directory containing skill.md with optional frontmatter.
func FromDir(dirPath string, opts ...Option) (*DirSkill, error) {
	data, err := os.ReadFile(filepath.Join(dirPath, "skill.md"))
	if err != nil {
		return nil, err
	}
	name := filepath.Base(filepath.Clean(dirPath))
	desc, alwaysApply, body := parseFrontmatter(string(data))
	allOpts := append([]Option{WithAlwaysApply(alwaysApply)}, opts...)
	return NewDirSkill(name, desc, dirPath, body, allOpts...), nil
}

// FromFile loads a standalone .md file as a skill.
func FromFile(filePath string, opts ...Option) (*DirSkill, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	desc, alwaysApply, body := parseFrontmatter(string(data))
	basePath := filepath.Dir(filePath)
	allOpts := append([]Option{WithAlwaysApply(alwaysApply)}, opts...)
	return NewDirSkill(name, desc, basePath, body, allOpts...), nil
}

// parseFrontmatter extracts description / alwaysApply from YAML frontmatter.
// Falls back to legacy format (first line = description) when no frontmatter.
func parseFrontmatter(text string) (description string, alwaysApply bool, body string) {
	trimmed := strings.TrimLeft(text, "\xef\xbb\xbf")
	if !strings.HasPrefix(trimmed, "---\n") && !strings.HasPrefix(trimmed, "---\r\n") {
		lines := strings.SplitN(trimmed, "\n", 2)
		description = strings.TrimSpace(lines[0])
		if len(lines) > 1 {
			body = strings.TrimSpace(lines[1])
		}
		return
	}

	rest := trimmed[4:]
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
