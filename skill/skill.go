package skill

// Skill is the interface for an operational playbook (SOP) — a reusable
// procedure the agent can run on demand.
//
// Frontmatter format (same as Rule):
//
//	---
//	description: one-line summary
//	alwaysApply: true
//	---
//
// alwaysApply=true  → Instruction is injected directly into the system prompt.
// alwaysApply=false → Only the summary appears in the catalog; the model must
//
//	invoke skill_call to spin up an isolated skill_context.
type Skill interface {
	Name() string
	Description() string
	BasePath() string
	Instruction() string
	Tools() []any
	MaxIterations() int
	AlwaysApply() bool
}

// DirSkill is a directory-backed Skill implementation.
type DirSkill struct {
	name        string
	description string
	basePath    string
	instruction string
	tools       []any
	maxIter     int
	alwaysApply bool
}

// Option configures a DirSkill.
type Option func(*DirSkill)

func WithTools(tools ...any) Option {
	return func(s *DirSkill) { s.tools = tools }
}

func WithMaxIterations(n int) Option {
	return func(s *DirSkill) { s.maxIter = n }
}

func WithAlwaysApply(v bool) Option {
	return func(s *DirSkill) { s.alwaysApply = v }
}

func NewDirSkill(name, description, basePath, instruction string, opts ...Option) *DirSkill {
	s := &DirSkill{
		name:        name,
		description: description,
		basePath:    basePath,
		instruction: instruction,
		maxIter:     10,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *DirSkill) Name() string        { return s.name }
func (s *DirSkill) Description() string { return s.description }
func (s *DirSkill) BasePath() string    { return s.basePath }
func (s *DirSkill) Instruction() string { return s.instruction }
func (s *DirSkill) Tools() []any        { return s.tools }
func (s *DirSkill) MaxIterations() int  { return s.maxIter }
func (s *DirSkill) AlwaysApply() bool   { return s.alwaysApply }
