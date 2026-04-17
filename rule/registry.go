package rule

import "sync"

type RuleRegistry struct {
	mu    sync.RWMutex
	rules map[string]Rule
	order []string
}

func NewRegistry() *RuleRegistry {
	return &RuleRegistry{
		rules: make(map[string]Rule),
		order: nil,
	}
}

func (r *RuleRegistry) Add(rules ...Rule) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rule := range rules {
		if rule == nil {
			continue
		}
		name := rule.Name()
		if _, exists := r.rules[name]; !exists {
			r.order = append(r.order, name)
		}
		r.rules[name] = rule
	}
}

func (r *RuleRegistry) Remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.rules[name]; !ok {
		return
	}
	delete(r.rules, name)
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

func (r *RuleRegistry) Get(name string) (Rule, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rule, ok := r.rules[name]
	return rule, ok
}

func (r *RuleRegistry) List() []Rule {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Rule, 0, len(r.order))
	for _, name := range r.order {
		if rule, ok := r.rules[name]; ok {
			out = append(out, rule)
		}
	}
	return out
}
