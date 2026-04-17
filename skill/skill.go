package skill

// Skill 技能接口 — 操作手册 (SOP) 抽象
//
// Frontmatter 格式 (同 Rule):
//
//	---
//	description: 一句话摘要
//	alwaysApply: true
//	---
//
// alwaysApply=true  → Instruction 直接注入 System Prompt
// alwaysApply=false → 仅摘要出现在 catalog，模型调用 skill_call 创建 skill_context
type Skill interface {
	Name() string
	Description() string
	BasePath() string
	Instruction() string
	Tools() []any
	MaxIterations() int
	AlwaysApply() bool
}

// DirSkill 基于目录的 Skill 实现
type DirSkill struct {
	name        string
	description string
	basePath    string
	instruction string
	tools       []any
	maxIter     int
	alwaysApply bool
}

// Option DirSkill 配置选项
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
