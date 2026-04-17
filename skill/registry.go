package skill

import "sync"

type SkillRegistry struct {
	mu     sync.RWMutex
	skills map[string]Skill
	order  []string
}

func NewRegistry() *SkillRegistry {
	return &SkillRegistry{
		skills: make(map[string]Skill),
		order:  nil,
	}
}

func (r *SkillRegistry) Add(skills ...Skill) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, skill := range skills {
		if skill == nil {
			continue
		}
		name := skill.Name()
		if _, exists := r.skills[name]; !exists {
			r.order = append(r.order, name)
		}
		r.skills[name] = skill
	}
}

func (r *SkillRegistry) Remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.skills[name]; !ok {
		return
	}
	delete(r.skills, name)
	idx := -1
	for i, n := range r.order {
		if n == name {
			idx = i
			break
		}
	}
	if idx >= 0 {
		r.order = append(r.order[:idx], r.order[idx+1:]...)
	}
}

func (r *SkillRegistry) Get(name string) (Skill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.skills[name]
	return s, ok
}

func (r *SkillRegistry) List() []Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Skill, 0, len(r.order))
	for _, name := range r.order {
		if s, ok := r.skills[name]; ok {
			out = append(out, s)
		}
	}
	return out
}
