# Simple Agent Framework — 技术设计文档 (TD)

> 版本: v1.4 | 模块: `github.com/LYD99/simple-agent-framework` | Go 1.24+

---

## 一、项目定位

轻量级、可扩展的 Go 单 Agent 框架，支持 **ReAct / Plan-and-Execute** 策略、**HITL (Human-In-The-Loop)**、**多类型工具**（内置、注册函数、RAG、MCP、Skill、RunCode）、**沙箱运行时**（Local/Docker/E2B 云沙箱）、**可靠性保障**（死循环检测、动态上下文压缩、结构化错误反馈）和 **会话级隔离**（Agent 共享单例 + Session 独立状态）。

设计借鉴 **Eino**(HITL checkpoint/resume)、**LangChainGo**(Chain/Agent/Executor 分层)、**tRPC-Agent-Go**(Session Memory + OTel)、**Swarm-go**(Handoff 模式)、**Codex**(Agent 共享配置 + Session 独立记忆) 中的成熟模式，保持 Go 惯用风格：小接口、`context.Context` 贯穿、functional options 配置、middleware/hook 可插拔。

---

## 二、整体架构图

```
┌──────────────────────────────────────────────────────────────────────┐
│                          User / Caller                               │
│            sess := agent.Session("sid")                              │
│            sess.Run(ctx, userMsg)                                    │
└──────────────────────────────┬───────────────────────────────────────┘
                               │
                               ▼
┌──────────────────────────────────────────────────────────────────────┐
│  Agent (单例, DI 注入一次, 持有共享配置/组件)                           │
│  ┌────────────┐ ┌──────────┐ ┌───────────┐ ┌───────────────────┐    │
│  │  Planner   │ │ Executor │ │ Evaluator │ │ OutputController  │    │
│  │ReAct|P&S   │ │(ToolCall)│ │ (可选)    │ │ (Parse/Validate)  │    │
│  └────────────┘ └──────────┘ └───────────┘ └───────────────────┘    │
│  ┌──────────┐ ┌─────────────┐ ┌──────────────────┐ ┌────────────┐  │
│  │ToolReg   │ │ RuleRegistry│ │ SkillRegistry    │ │Interrupter │  │
│  │(registry)│ │(渐进式披露) │ │(skill_call+view) │ │(HITL)      │  │
│  └──────────┘ └─────────────┘ └──────────────────┘ └────────────┘  │
│  ┌────────────────┐ ┌──────────────────────────────────────────┐    │
│  │    Runtime     │ │        Hook / Middleware                  │    │
│  │Local/Docker/E2B│ │ OnPlanStart · OnToolCall · OnComplete ..│    │
│  └────────────────┘ └──────────────────────────────────────────┘    │
│  ┌──────────────────────────────────────────────────────────────┐    │
│  │  MemoryFactory / ContentStoreFactory  (会话级资源工厂)        │    │
│  └──────────────────────────────────────────────────────────────┘    │
└──────────────────────────────┬───────────────────────────────────────┘
                               │ agent.Session(sessionID)
                               ▼
┌──────────────────────────────────────────────────────────────────────┐
│  Session (每请求/会话独立, 持有会话级状态)                              │
│  ┌──────────────────────────────────────────────────────────────┐    │
│  │  sessionID  │ Memory (独立) │ LoopDetector │ ContentStore   │    │
│  │  Compressor │ PlanAndSolve 状态 │ iteration 计数              │    │
│  └──────────────────────────────────────────────────────────────┘    │
│  ┌──────────────────────────────────────────────────────────────┐    │
│  │              AgentLoop (状态机驱动, 双模式)                     │    │
│  │  ReAct:  INIT → PLANNING → EXEC → [EVAL?] → [COMPLETE]      │    │
│  │  P&S:    INIT → PLAN_GEN → REVIEW → PLAN → EXEC → [EVAL?]  │    │
│  └──────────────────────────────────────────────────────────────┘    │
└──────────────────────────────────────────────────────────────────────┘
                               │
                               ▼
┌──────────────────────────────────────────────────────────────────────┐
│                       LLM Client Layer                               │
│  ┌────────────────┐  ┌──────────────┐  ┌────────────────────────┐   │
│  │  ChatModel     │  │  RootClient  │  │  StreamResponse        │   │
│  │  (interface)   │  │  (HTTP+SSE)  │  │  (channel-based iter)  │   │
│  └────────────────┘  └──────────────┘  └────────────────────────┘   │
│                                                                      │
│  Provider Adapters:  OpenAI │ Anthropic │ Ollama │ Custom           │
└──────────────────────────────────────────────────────────────────────┘
```

**Agent vs Session 职责划分**

| 层级 | 生命周期 | 持有内容 | 并发安全 |
|------|---------|---------|---------|
| **Agent** | 应用级 (DI 注入一次) | Model、Planner、Executor、Evaluator、ToolRegistry、RuleRegistry、SkillRegistry、HookManager、Interrupter、Runtime、MemoryFactory、共享配置 | 是 (只读 + RWMutex) |
| **Session** | 请求/会话级 | sessionID、Memory、LoopDetector、ContentStore、ContextCompressor、PlanAndSolve 状态 | 否 (单会话单 goroutine) |

> **设计原则**: Agent 是无状态的共享单例；所有会话级可变状态 (记忆、循环检测、上下文压缩) 下沉到 Session。同一 Agent 可并发服务多个 Session，互不干扰。

---

## 三、包结构设计

```
github.com/LYD99/simple-agent-framework/
├── go.mod
├── agent/                     # 核心 Agent 包 (public API)
│   ├── agent.go               # Agent struct (共享单例, DI 注入)
│   ├── session.go             # Session struct (会话级状态 + Run/RunStream 入口)
│   ├── loop.go                # AgentLoop 状态机 (在 Session 上下文中运行)
│   ├── loop_detector.go       # 死循环检测器 (per-session)
│   ├── option.go              # functional options
│   └── event.go               # 事件定义 (hook 用)
│
├── prompt/                    # 提示词统一管理 (组装、模板、目录)
│   ├── builder.go             # PromptBuilder — 组装最终 system prompt
│   ├── template.go            # 模板引擎 (Go text/template 封装)
│   └── catalog.go             # Rule/Skill 目录摘要生成
│
├── planner/                   # 规划器
│   ├── planner.go             # Planner interface
│   ├── mode.go                # ExecutionMode 定义 (ReAct / PlanAndSolve)
│   ├── react.go               # ReAct 实现
│   └── plan_and_solve.go      # PlanAndSolve 实现 (含 ActionPlan / Replan)
│
├── executor/                  # 执行器
│   ├── executor.go            # Executor interface + 默认实现
│   └── parallel.go            # 并行工具调用
│
├── evaluator/                 # 评估器 (可选, 动态开关)
│   ├── evaluator.go           # Evaluator interface
│   ├── noop.go                # NoopEvaluator (关闭时内部使用)
│   ├── llm_judge.go           # 基于 LLM 的结果评估
│   ├── rule_based.go          # 基于规则的评估
│   └── composite.go           # 组合评估器
│
├── memory/                    # 记忆管理
│   ├── memory.go              # Memory interface (语义层: 滑窗/摘要策略)
│   ├── buffer.go              # BufferMemory (滑动窗口, 基于 MessageStore)
│   ├── summary.go             # SummaryMemory (摘要压缩, 基于 MessageStore)
│   ├── store.go               # MessageStore 接口 (Memory 实现的内部组合参数)
│   ├── store_inmem.go         # InMemoryMessageStore (零依赖默认引擎)
│   │                          # Redis/SQL/Custom 引擎由调用方按需实现 MessageStore 接口即可
│   ├── compressor.go          # 动态上下文压缩 (截断/淘汰/智能摘要)
│   ├── content_store.go       # ContentStore 接口 (同样通过 Store 引擎抽象)
│   ├── content_store_inmem.go # InMemoryContentStore
│   ├── content_store_redis.go # RedisContentStore
│   ├── content_store_file.go  # FileContentStore
│   └── message.go             # Message 类型定义
│
├── model/                     # LLM 客户端抽象 (支持自定义 Client)
│   ├── model.go               # ChatModel interface
│   ├── message.go             # ChatMessage / ToolCall / Usage
│   ├── option.go              # model options
│   ├── wrap.go                # WrapFunc() / WrapHTTP() 快捷适配
│   └── provider/
│       ├── openai/            # OpenAI 适配
│       │   └── openai.go
│       └── anthropic/         # Anthropic 适配
│           └── anthropic.go
│
├── tool/                      # 工具系统
│   ├── tool.go                # Tool interface + ToolHandler
│   ├── schema.go              # JSON Schema 定义 + 反射生成
│   ├── registry.go            # ToolRegistry
│   ├── builtin/               # 内置工具
│   │   ├── read.go            # 文件读取
│   │   ├── write.go           # 文件写入
│   │   ├── shell.go           # Shell 命令执行
│   │   ├── rule_view.go       # rule_view — 按需读取 Rule 完整内容
│   │   ├── skill_call.go      # skill_call — 触发 Skill 执行
│   │   └── fetch_full_result.go # fetch_full_result — 查询截断内容完整结果
│   └── mcp/                   # MCP 协议工具
│       └── mcp_client.go
│
├── retriever/                 # RAG 检索系统
│   ├── retriever.go           # Retriever interface + Document + SearchOptions
│   ├── semantic.go            # SemanticRetriever (Embedding + VectorStore)
│   ├── keyword.go             # KeywordRetriever (BM25/全文检索)
│   ├── hybrid.go              # HybridRetriever (混合检索 + RRF 合并)
│   ├── rag_tool.go            # RAGTool (Retriever → Tool 封装)
│   ├── embedder/              # Embedding 接口 + 实现
│   │   ├── embedder.go        # Embedder interface
│   │   └── openai.go          # OpenAI Embedding
│   └── vectorstore/           # 向量存储接口 + 实现
│       ├── vectorstore.go     # VectorStore interface
│       └── chroma.go          # Chroma 适配
│
├── runtime/                   # 运行时 / 沙箱
│   ├── runtime.go             # Runtime interface
│   ├── local.go               # 本地执行
│   ├── sandbox.go             # 沙箱工厂 (Docker/Worktree/E2B)
│   └── e2b.go                 # E2B 云沙箱实现 (REST API + envd)
│
├── interrupter/               # HITL 中断控制
│   ├── interrupter.go         # Interrupter interface
│   ├── hitl.go                # HITLHandler 实现
│   └── checkpoint.go          # 检查点 序列化/恢复
│
├── hook/                      # 钩子/中间件
│   ├── hook.go                # Hook interface + HookManager
│   ├── logger.go              # 日志 hook
│   └── outputhook/
│       └── output_controller.go  # LLM 输出解析/校验
│
├── rule/                      # 规则系统 (渐进式披露)
│   ├── rule.go                # Rule interface + FileRule
│   ├── registry.go            # RuleRegistry (增删查)
│   └── loader.go              # LoadDir() / FromFile() 自动扫描加载
│
├── skill/                     # 技能系统 (渐进式披露 + skill_context)
│   ├── skill.go               # Skill interface + DirSkill
│   ├── registry.go            # SkillRegistry
│   ├── loader.go              # LoadDir() / FromDir() 自动扫描加载
│   ├── context.go             # SkillContext (独立上下文 + mini agent loop)
│   └── view_tool.go      # skill_view (读取 Skill 目录文件)
│
├── errors/                    # 统一错误定义
│   └── errors.go
│
└── examples/                  # 使用示例
    ├── simple/
    │   └── main.go            # 最简使用
    ├── react/
    │   └── main.go            # ReAct 模式
    └── hitl/
        └── main.go            # HITL 示例
```

---

## 四、核心接口定义

### 4.1 PromptBuilder — 提示词统一组装

所有提示词逻辑收敛到 `prompt/` 包，由 `PromptBuilder` 统一组装最终 System Prompt。

```go
// prompt/builder.go
type PromptBuilder struct {
    base     string          // 基础 system prompt (来自用户 WithSystemPrompt 或模式默认)
    rules    []RuleSummary   // Rule 摘要列表 (含 AlwaysApply / Content)
    skills   []SkillSummary  // Skill 摘要列表 (含 AlwaysApply / Content)
    sections []Section       // 自定义追加段落
    engine   *TemplateEngine // 模板引擎
}

type Section struct {
    Name     string
    Content  string
    Priority int    // 排序权重
}

func NewBuilder(base string) *PromptBuilder

func (b *PromptBuilder) WithRules(rules []RuleSummary) *PromptBuilder
func (b *PromptBuilder) WithSkills(skills []SkillSummary) *PromptBuilder
func (b *PromptBuilder) AddSection(name, content string, priority int) *PromptBuilder

// Build 输出最终 system prompt 字符串
func (b *PromptBuilder) Build(ctx context.Context) string
```

**默认系统提示词 (prompt/defaults.go)**

Agent 未设置 `WithSystemPrompt()` 时，根据执行模式自动选择默认提示词：

```go
// prompt/defaults.go
const DefaultReActSystemPrompt = `You are an autonomous AI agent operating in a ReAct
(Reason + Act) loop. ...
<core_behavior> THINK → ACT → OBSERVE → ANSWER </core_behavior>
<tool_use_guidelines> ... </tool_use_guidelines>
<output_quality> ... </output_quality>
<error_handling> ... </error_handling>`

const DefaultPlanAndSolveSystemPrompt = `You are an autonomous AI agent operating in
Plan-and-Solve mode. ...
<planning_phase> ANALYZE → DECOMPOSE → ORDER </planning_phase>
<execution_phase> ... </execution_phase>
<replanning> ... </replanning>
<tool_use_guidelines> ... </tool_use_guidelines>
<output_quality> ... </output_quality>
<error_handling> ... </error_handling>`
```

**选择逻辑 (agent.go)**

```go
func (a *Agent) rebuildPromptBuilder() {
    base := a.systemPrompt
    if base == "" {
        base = a.defaultSystemPrompt()  // 根据 currentMode 选择
    }
    ruleSums, skillSums := buildPromptSummaries(a)
    a.promptBuilder = prompt.NewBuilder(base).WithRules(ruleSums).WithSkills(skillSums)
}

func (a *Agent) defaultSystemPrompt() string {
    if a.currentMode == planner.ModePlanAndSolve {
        return prompt.DefaultPlanAndSolveSystemPrompt
    }
    return prompt.DefaultReActSystemPrompt
}
```

`SetExecutionMode()` 切换模式时，若未设自定义 systemPrompt，自动重建为新模式的默认提示词。

**组装流程**

```
PromptBuilder.Build(ctx)
 │
 ├──► 1. base system prompt (用户自定义 或 模式默认)
 │
 ├──► 2. BuildAlwaysOnRules()    → <rules> 块 (alwaysApply=true 完整内容)
 ├──► 3. BuildRuleCatalog()      → <available_rules> 目录 (alwaysApply=false 摘要)
 │
 ├──► 4. BuildAlwaysOnSkills()   → <skills> 块 (alwaysApply=true 完整内容)
 ├──► 5. BuildSkillCatalog()     → <available_skills> 目录 (alwaysApply=false 摘要)
 │
 ├──► 6. 自定义 Sections (按 Priority 排序)
 │
 └──► 7. 拼接返回完整 system prompt string
```

**模板引擎**

```go
// prompt/template.go
type TemplateEngine struct {
    templates map[string]*template.Template
}

func (e *TemplateEngine) Register(name, tmpl string) error
func (e *TemplateEngine) Render(name string, data any) (string, error)
```

**目录/内联生成 (prompt/catalog.go)**

```go
// prompt/catalog.go

type RuleSummary struct {
    Name, Description string
    AlwaysApply       bool
    Content           string  // populated for alwaysApply rules
}

type SkillSummary struct {
    Name, Description string
    AlwaysApply       bool
    Content           string  // populated for alwaysApply skills
}

func BuildAlwaysOnRules(rules []RuleSummary) string   // → <rules>...</rules>
func BuildRuleCatalog(rules []RuleSummary) string      // → <available_rules>...</available_rules>
func BuildAlwaysOnSkills(skills []SkillSummary) string // → <skills>...</skills>
func BuildSkillCatalog(skills []SkillSummary) string   // → <available_skills>...</available_skills>
```

### 4.2 ChatModel — LLM 抽象 (支持自定义 Client)

框架通过 `ChatModel` interface 抽象 LLM 交互。调用方可使用内置 Provider，也可传入自定义 Client 对接任意外部 API（非模型厂商标准接口亦可）。

```go
// model/model.go
type ChatModel interface {
    Generate(ctx context.Context, messages []ChatMessage, opts ...Option) (*ChatResponse, error)
    Stream(ctx context.Context, messages []ChatMessage, opts ...Option) (*StreamIterator, error)
}

type ChatMessage struct {
    Role       Role        `json:"role"`        // system / user / assistant / tool
    Content    string      `json:"content"`
    ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
    ToolCallID string      `json:"tool_call_id,omitempty"`
    Name       string      `json:"name,omitempty"`
}

type ChatResponse struct {
    Message ChatMessage
    Usage   Usage
}

type StreamIterator struct { /* channel-based, Next()/Msg()/Err() */ }
```

#### 4.2.1 自定义 Client

任何实现了 `ChatModel` interface 的类型都可以作为 Agent 的模型客户端，无论底层对接的是模型厂商 API、私有网关、还是完全自定义的外部服务。

**方式一：`WithModel()` — 传入内置 Provider**

```go
a := agent.New(
    agent.WithModel(openai.New("gpt-4o", apiKey)),
)
```

**方式二：`WithModel()` — 传入自定义 ChatModel 实现**

```go
// 对接内部 AI Gateway (非标准 OpenAI 协议)
type InternalGatewayModel struct {
    endpoint string
    appKey   string
    client   *http.Client
}

func (m *InternalGatewayModel) Generate(ctx context.Context, messages []model.ChatMessage, opts ...model.Option) (*model.ChatResponse, error) {
    // 将 ChatMessage 转为内部 Gateway 请求格式
    req := m.buildRequest(messages, opts)
    resp, err := m.client.Post(m.endpoint, "application/json", req)
    // 解析响应，转为 ChatResponse
    return m.parseResponse(resp)
}

func (m *InternalGatewayModel) Stream(ctx context.Context, messages []model.ChatMessage, opts ...model.Option) (*model.StreamIterator, error) {
    // 自定义流式实现
}

a := agent.New(
    agent.WithModel(&InternalGatewayModel{
        endpoint: "https://ai-gateway.internal/v1/chat",
        appKey:   "xxx",
        client:   &http.Client{Timeout: 60 * time.Second},
    }),
)
```

**方式三：`model.WrapFunc()` — 函数式快捷创建**

```go
// 仅需 Generate 的简单场景
customModel := model.WrapFunc(
    func(ctx context.Context, messages []model.ChatMessage, opts ...model.Option) (*model.ChatResponse, error) {
        // 调用任意外部 API
        result, err := callMyAPI(messages)
        return &model.ChatResponse{
            Message: model.ChatMessage{Role: model.RoleAssistant, Content: result},
        }, err
    },
)
a := agent.New(agent.WithModel(customModel))
```

**方式四：`model.WrapHTTP()` — 基于 HTTP 的快捷适配**

```go
// 对接任意 HTTP API，只需提供请求/响应转换函数
customModel := model.WrapHTTP(
    "https://my-api.example.com/chat",
    model.WithHTTPHeaders(map[string]string{"X-Api-Key": "xxx"}),
    model.WithRequestMapper(func(messages []model.ChatMessage) ([]byte, error) {
        // ChatMessage → 自定义请求 JSON
    }),
    model.WithResponseMapper(func(body []byte) (*model.ChatResponse, error) {
        // 响应 JSON → ChatResponse
    }),
)
a := agent.New(agent.WithModel(customModel))
```

### 4.3 Planner — 规划器

```go
// planner/planner.go
type Planner interface {
    Plan(ctx context.Context, state *PlanState) (*PlanResult, error)
}

type PlanState struct {
    Messages    []model.ChatMessage
    Tools       []tool.Tool
    History     []StepResult        // 历史步骤结果
}

type PlanResult struct {
    Action     Action     // ToolCall / FinalAnswer / AskHuman
    Reasoning  string     // 思考过程 (用于日志/调试)
}

type Action struct {
    Type       ActionType            // ToolCall / FinalAnswer / AskHuman
    ToolName   string
    ToolInput  map[string]any
    Answer     string                // FinalAnswer 时使用
}

type ActionType int
const (
    ActionToolCall    ActionType = iota
    ActionFinalAnswer
    ActionAskHuman
)
```

#### 4.2.1 执行模式：ReAct vs PlanAndSolve

框架支持两种执行模式，通过 `WithExecutionMode()` 在创建 Agent 时选择，也可通过 `agent.SetExecutionMode()` 在运行时动态切换。

```go
// planner/mode.go
type ExecutionMode int
const (
    ModeReAct        ExecutionMode = iota  // 默认: 逐步思考-行动-观察
    ModePlanAndSolve                       // 先生成完整计划，再逐步执行
)
```

**模式一：ReAct (默认)**

每一轮独立地 Thought → Action → Observation，由 LLM 实时决定下一步。支持可选 Evaluator。

```
Loop:
  ┌─► Planner.Plan() → 单个 Action (ToolCall / FinalAnswer)
  │       │
  │       ▼
  │   Executor.Execute() → Observation
  │       │
  │       ▼
  │   [evalEnabled?]
  │       ├── Yes → Evaluator.Evaluate() → Continue / Retry / Escalate
  │       └── No  → 跳过
  │       │
  │       ▼
  │   结果写入 Memory → 回到 Planner 决定下一步
  └───────┘
```

```go
// planner/react.go
type ReActPlanner struct {
    model  model.ChatModel
    prompt string  // ReAct system prompt 模板
}

func (p *ReActPlanner) Plan(ctx context.Context, state *PlanState) (*PlanResult, error) {
    // 将 History 中的 thought/action/observation 序列拼入 messages
    // 单次 LLM 调用 → 解析出 Action
}
```

**模式二：PlanAndSolve**

先调用 LLM 生成完整的多步计划（Plan 阶段），再逐步执行每个子任务（Solve 阶段）。执行过程中若发现计划需要调整，触发 Replan。

```
Phase 1 — Plan:
  Planner.Plan() → 返回 ActionPlan (完整步骤列表)

Phase 2 — Solve:
  ┌─► 取 plan.Steps[i] → Executor.Execute()
  │       │
  │       ▼
  │   Evaluator (可选) → 判断是否 Replan
  │       │
  │       ├── OK → i++ → 继续下一步
  │       └── Replan → 回到 Phase 1 (带已完成步骤上下文)
  └───────┘
```

```go
// planner/plan_and_solve.go
type PlanAndSolvePlanner struct {
    model       model.ChatModel
    planPrompt  string  // 生成计划的 prompt
    solvePrompt string  // 执行单步的 prompt
}

// Plan 在 PlanAndSolve 模式下返回的 PlanResult 包含完整步骤列表
type ActionPlan struct {
    Steps       []PlanStep
    CurrentStep int
}

type PlanStep struct {
    StepID      int
    Description string       // 步骤描述
    Action      Action       // 对应的工具调用
    Status      StepStatus   // Pending / Running / Done / Failed
    Result      string       // 执行结果
}

type StepStatus int
const (
    StepPending  StepStatus = iota
    StepRunning
    StepDone
    StepFailed
    StepSkipped  // Replan 时被跳过的步骤
)
```

**两种模式对比**

| 维度 | ReAct | PlanAndSolve |
|-----|-------|-------------|
| 规划方式 | 逐步，每轮 LLM 决定 | 先全局规划，再逐步执行 |
| LLM 调用次数 | 每步 1 次 plan | Plan 阶段 1 次 + 每步可选 |
| 适用场景 | 探索性任务、简单多步 | 复杂有序任务、可预见步骤 |
| 中途调整 | 天然支持 (每步重新决策) | 需要 Replan 机制 |
| 上下文消耗 | 逐步累积，可能较大 | Plan 上下文集中，Solve 可裁剪 |
| 可解释性 | 中 (散落在每步 reasoning) | 高 (有完整计划可审查) |
| Evaluator | 支持 (可选，每步执行后评估) | 支持 (可选，额外支持 Replan 决策) |

**运行时切换**

```go
// agent/agent.go
func (a *Agent) SetExecutionMode(mode planner.ExecutionMode) {
    a.mu.Lock()
    defer a.mu.Unlock()
    switch mode {
    case planner.ModeReAct:
        a.planner = a.reactPlanner
    case planner.ModePlanAndSolve:
        a.planner = a.planAndSolvePlanner
    }
    a.currentMode = mode
}
```

Agent 内部持有两个 Planner 实例，切换仅替换引用，对正在运行的循环不生效（下一次 `Run` 生效），保证线程安全。

### 4.4 Executor — 执行器

```go
// executor/executor.go
type Executor interface {
    Execute(ctx context.Context, action planner.Action, registry *tool.ToolRegistry) (*ExecResult, error)
}

type ExecResult struct {
    ToolName   string
    Output     string
    Error      error
    Duration   time.Duration
}
```

### 4.5 Evaluator — 评估器 (可选组件，动态开关)

Evaluator 是**可选**的。调用方可在创建 Agent 时决定是否启用，也可在运行时动态开关。

```go
// evaluator/evaluator.go
type Evaluator interface {
    Evaluate(ctx context.Context, state *EvalState) (*EvalResult, error)
}

type EvalResult struct {
    Decision   Decision  // Continue / Complete / Retry / Escalate
    Feedback   string
}

type Decision int
const (
    DecisionContinue Decision = iota
    DecisionComplete
    DecisionRetry
    DecisionEscalate  // 升级给人类
)
```

#### 4.5.1 Evaluator 动态开关设计

**核心原则**：当 Evaluator 未启用时，AgentLoop 跳过 EVALUATING 状态，工具执行完直接回到 PLANNING，由 Planner 自行判断是否结束。

```go
// evaluator/noop.go — 空实现，关闭评估时使用
type NoopEvaluator struct{}

func (n *NoopEvaluator) Evaluate(ctx context.Context, state *EvalState) (*EvalResult, error) {
    return &EvalResult{Decision: DecisionContinue}, nil
}
```

**创建时配置**

```go
// 默认不开启 evaluator
a := agent.New(
    agent.WithModel(m),
    agent.WithPlanner(react.New(m)),
)

// 显式开启 evaluator
a := agent.New(
    agent.WithModel(m),
    agent.WithPlanner(react.New(m)),
    agent.WithEvaluator(llmjudge.New(m)),  // 传入即开启
)
```

**运行时动态切换**

```go
// agent/agent.go
func (a *Agent) EnableEvaluator(ev evaluator.Evaluator) {
    a.mu.Lock()
    defer a.mu.Unlock()
    a.evaluator = ev
    a.evalEnabled = true
}

func (a *Agent) DisableEvaluator() {
    a.mu.Lock()
    defer a.mu.Unlock()
    a.evalEnabled = false
}
```

**AgentLoop 中的分支逻辑**

```go
// agent/loop.go — EXECUTING 状态完成后的转移
case StateExecuting:
    // ... 执行工具 ...
    if a.evalEnabled && a.evaluator != nil {
        state = StateEvaluating   // 有 evaluator → 进入评估
    } else {
        state = StatePlanning     // 无 evaluator → 直接回到规划
    }
```

**状态机路径对比 (两种模式通用)**

```
开启 Evaluator (ReAct / PlanAndSolve 均适用):
  PLANNING → EXECUTING → EVALUATING → PLANNING (Continue)
                                     → COMPLETING (Complete)
                                     → EXECUTING (Retry)
                                     → PLAN_GEN (Replan, PlanAndSolve 独有)

关闭 Evaluator (ReAct / PlanAndSolve 均适用):
  PLANNING → EXECUTING → PLANNING (直接回到规划，由 Planner 判断终止)
```

**预置 Evaluator 实现**

| 实现 | 说明 |
|-----|------|
| `NoopEvaluator` | 空操作，始终返回 Continue (关闭评估时的内部实现) |
| `LLMJudgeEvaluator` | 调用 LLM 评判执行结果质量，返回 Continue/Complete/Retry |
| `RuleBasedEvaluator` | 基于规则判断 (如检查输出是否包含关键字、是否符合格式) |
| `CompositeEvaluator` | 组合多个 Evaluator，全部通过才 Complete |

### 4.6 Memory — 记忆 (两层架构：语义层 + 数据引擎层)

Memory 内部分为两层，但**对 Agent 只暴露一个概念 `MemoryFactory`**：
- **语义层 (`Memory`)**: 负责"记住什么、怎么裁剪" (BufferMemory 滑窗 / SummaryMemory 摘要)
- **数据引擎 (`MessageStore`)**: 负责"消息存在哪" (内存 / Redis / SQL / 自定义)，**仅作为具体 Memory 实现的内部组合**。

所有 Memory 实现都**必须**通过 `MessageStore` 访问底层存储，不得直接持有 `[]ChatMessage`。这样同一套语义策略可以搭配任意存储引擎，也支持跨进程 / 持久化 / 水平扩展。

**Agent 不感知 `MessageStore`**，也不需要任何 `MessageStoreFactory` 类型。用户在自己的 `MemoryFactory` 闭包里直接 `new` 一个 `MessageStore` 传给 `memory.NewBuffer` / `memory.NewSummary`——引擎切换是工厂函数体内的一次替换，Agent 层零改动。

```go
// memory/memory.go — 语义层接口
type Memory interface {
    Messages(ctx context.Context) ([]model.ChatMessage, error)
    Add(ctx context.Context, msgs ...model.ChatMessage) error
    Clear(ctx context.Context) error
}
```

```go
// memory/store.go — 底层数据引擎接口 (per-session)
//
// 每个 MessageStore 实例对应一个 sessionID 的消息序列，
// Memory 语义层只调用这些原子操作，不关心底层是内存/Redis/DB。
type MessageStore interface {
    // Append 追加一条或多条消息，保持插入顺序
    Append(ctx context.Context, msgs ...model.ChatMessage) error
    // List 返回全部消息 (按插入顺序)
    List(ctx context.Context) ([]model.ChatMessage, error)
    // Replace 原子替换全部消息 (压缩/摘要场景用)
    Replace(ctx context.Context, msgs []model.ChatMessage) error
    // Len 当前消息条数 (用于滑窗判断, 避免拉全量)
    Len(ctx context.Context) (int, error)
    // TrimHead 从头部裁剪 n 条 (滑窗场景用, 引擎可优化为 O(1))
    TrimHead(ctx context.Context, n int) error
    // Clear 清空当前 session 的消息
    Clear(ctx context.Context) error
    // Close 释放资源 (如 redis/db 连接可选择 no-op)
    Close() error
}
```

> **注意**: `MessageStore` 没有对应的 `MessageStoreFactory` 类型——它纯粹是 `memory.NewBuffer(store, ...)` / `memory.NewSummary(store, ...)` 等构造器的参数，由用户在 `MemoryFactory` 闭包内即时构造即可。

#### 4.6.1 内置 MessageStore 实现

```
            MessageStore (interface)
                  │
       ┌──────────┼──────────┬───────────┐
       ▼          ▼          ▼           ▼
   InMemory    Redis        SQL       Custom     ← 实现接口即可接入任意引擎
  (sync.Mutex) (LPUSH/LRANGE) (messages 表)
```

| 实现 | 底层 | 适用场景 | 特点 |
|------|------|---------|------|
| `InMemoryMessageStore` | `sync.Mutex + []ChatMessage` | 单机、单进程、测试 | 零依赖 (默认) |
| `RedisMessageStore` | Redis List (`LPUSH`/`LRANGE`/`LTRIM`) | 分布式、会话跨实例恢复 | TTL 自动过期 |
| `SQLMessageStore` | `messages(session_id, seq, role, content, ...)` | 强持久化、审计、分析 | 支持事务与查询 |
| `CustomMessageStore` | 用户自定义 (Mongo/DynamoDB/…) | 已有存储基建 | 实现 7 个方法即可 |

#### 4.6.2 BufferMemory / SummaryMemory 的重构

```go
// memory/buffer.go
type BufferMemory struct {
    store       MessageStore // 底层引擎，不直接持有 slice
    maxMessages int
}

func NewBuffer(store MessageStore, maxMessages int) *BufferMemory {
    return &BufferMemory{store: store, maxMessages: maxMessages}
}

func (m *BufferMemory) Add(ctx context.Context, msgs ...model.ChatMessage) error {
    if err := m.store.Append(ctx, msgs...); err != nil {
        return err
    }
    n, _ := m.store.Len(ctx)
    if m.maxMessages > 0 && n > m.maxMessages {
        return m.store.TrimHead(ctx, n-m.maxMessages)
    }
    return nil
}

func (m *BufferMemory) Messages(ctx context.Context) ([]model.ChatMessage, error) {
    return m.store.List(ctx)
}

func (m *BufferMemory) Clear(ctx context.Context) error {
    return m.store.Clear(ctx)
}
```

`SummaryMemory` 同理 — 所有读写都经由 `store`，摘要压缩后通过 `Replace` 原子重建。

#### 4.6.3 ContentStore 同样遵循"引擎可插拔"

原 ContentStore 已经设计了 `InMemory/File/Redis` 多实现 (见 10.9.2)，语义和 `MessageStore` 一致：截断的完整内容通过独立的数据引擎持久化，调用方可自由替换。Agent 对外只暴露 `WithMemoryFactory` / `WithContentStoreFactory` 两个入口；未配置时框架以 `InMemory*` 作为零依赖回退并在日志中提示，便于生产环境发现。

### 4.7 Tool — 工具

```go
// tool/tool.go
type Tool interface {
    Name() string
    Description() string
    Schema() *SchemaProperty
    Execute(ctx context.Context, input map[string]any) (string, error)
}
```

### 4.8 Interrupter — HITL 中断

```go
// interrupter/interrupter.go
type Interrupter interface {
    ShouldInterrupt(ctx context.Context, event InterruptEvent) (bool, error)
    WaitForHuman(ctx context.Context, event InterruptEvent) (*HumanResponse, error)
}

type InterruptEvent struct {
    Type       InterruptType  // BeforeToolCall / AfterPlan / OnEscalate
    Action     planner.Action
    AgentState *AgentSnapshot
}

type HumanResponse struct {
    Approved   bool
    Message    string   // 人类反馈
    ModifiedInput map[string]any  // 可选：人类修改后的工具输入
}
```

### 4.9 Retriever — 检索器 (RAG)

```go
// retriever/retriever.go
type Retriever interface {
    Retrieve(ctx context.Context, query string, opts ...SearchOption) ([]Document, error)
}

type Document struct {
    ID       string            `json:"id"`
    Content  string            `json:"content"`
    Score    float64           `json:"score"`
    Metadata map[string]string `json:"metadata,omitempty"`
    Source   string            `json:"source,omitempty"`
}
```

RAG 通过 `retriever.NewRAGTool()` 封装为标准 Tool，支持 Semantic / Keyword / Hybrid / Custom 检索策略。

### 4.10 Rule — 规则 (渐进式披露 + Cursor Rule 格式)

Rule 采用 **Cursor Rule 风格 YAML Frontmatter** 格式，通过 `alwaysApply` 字段决定披露方式：

```go
// rule/rule.go
type Rule interface {
    Name() string
    Description() string  // 一句话摘要 (来自 frontmatter description 字段)
    Content() string      // 完整正文 (frontmatter 后的 body)
    AlwaysApply() bool    // true=直接注入 system prompt; false=按需 rule_view
}
```

| `alwaysApply` | 行为 |
|---|---|
| `true` | 完整内容直接注入 System Prompt `<rules>` 块 |
| `false` (默认) | 仅 name+description 出现在 `<available_rules>` 目录，模型按需调用 `rule_view` 加载 |

### 4.11 Skill — 技能 / 操作手册 (渐进式披露 + skill_context)

Skill 同样支持 YAML Frontmatter，通过 `alwaysApply` 决定是否直接注入或按需加载：

```go
// skill/skill.go
type Skill interface {
    Name() string
    Description() string  // 一句话摘要 (来自 frontmatter description 字段)
    BasePath() string     // Skill 目录路径 (含 skill.md + 其他文件)
    Instruction() string  // skill.md 完整内容 (frontmatter 后的 body)
    Tools() []any         // Skill 内部可用工具 (用 any 避免循环依赖)
    MaxIterations() int   // skill_context 内最大迭代
    AlwaysApply() bool    // true=Instruction 直接注入 system prompt; false=按需 skill_call
}
```

| `alwaysApply` | 行为 |
|---|---|
| `true` | Instruction 直接注入 System Prompt `<skills>` 块 |
| `false` (默认) | 仅摘要出现在 `<available_skills>` 目录，模型调用 `skill_call` 创建 `skill_context` |

### 4.12 Hook — 生命周期钩子

```go
// hook/hook.go
type Hook interface {
    OnEvent(ctx context.Context, event Event) error
}

type Event struct {
    Type      EventType
    Payload   any       // 根据 Type 断言具体类型
    Timestamp time.Time
}

type EventType int
const (
    EventPlanStart EventType = iota
    EventPlanDone
    EventToolCallStart
    EventToolCallDone
    EventEvalStart
    EventEvalDone
    EventLoopComplete
    EventError
    EventStreamChunk
    EventSkillContextLog  // skill_context 完整中间上下文日志
    EventRuleView         // 渐进式披露：模型按需加载 rule 完整内容
    EventSkillCallStart   // 模型调用 skill_call 开始
    EventSkillCallDone    // skill_call 执行完成
)
```

### 4.13 Session — 会话级上下文 (Agent 共享, Session 隔离)

Agent 是共享单例 (DI 注入一次)，可并发服务多个请求。每个请求/会话由独立的 `Session` 承载，所有会话级可变状态 (记忆、死循环检测、上下文压缩) 下沉到 Session。

```go
// agent/session.go

type Session struct {
    id            string
    agent         *Agent           // 引用共享 Agent (只读)
    memory        memory.Memory    // 会话独立记忆
    loopDetector  *LoopDetector    // 会话独立死循环检测器
    contentStore  memory.ContentStore // 会话独立截断内容存储
    compressor    *memory.ContextCompressor // 会话独立上下文压缩器
}

type SessionOption func(*Session)

// Session 获取或创建一个会话 (同一 sessionID 返回同一 Session)
func (a *Agent) Session(sessionID string, opts ...SessionOption) *Session

// NewSession 创建一个匿名会话 (自动生成 UUID)
func (a *Agent) NewSession(opts ...SessionOption) *Session

// Run 在会话上下文中执行 (会话级 Memory 隔离)
// 返回的 AgentResult.SessionID 始终填充本次会话 ID
func (s *Session) Run(ctx context.Context, input string) (*AgentResult, error)

// RunStream 流式执行
func (s *Session) RunStream(ctx context.Context, input string) (*AgentResult, error)

// ID 返回会话 ID
func (s *Session) ID() string

// Messages 返回当前会话的历史消息
func (s *Session) Messages(ctx context.Context) ([]model.ChatMessage, error)

// AgentResult 执行结果
type AgentResult struct {
    SessionID string               // 本次会话 ID (首次自动生成，后续可回传复用)
    Answer    string
    Messages  []model.ChatMessage
    Usage     model.Usage
    Error     error
    Duration  time.Duration
}
```

**Agent 提供 Session 工厂 + 便捷 Run (支持可选 sessionID)**

```go
// agent/agent.go

type Agent struct {
    mu sync.RWMutex

    // --- 共享组件 (应用级, 只读) ---
    name          string
    model         model.ChatModel
    planner       planner.Planner
    executor      executor.Executor
    evaluator     evaluator.Evaluator
    toolRegistry  *tool.ToolRegistry
    interrupter   interrupter.Interrupter
    hookManager   *hook.HookManager
    ruleRegistry  *rule.RuleRegistry
    skillRegistry *skill.SkillRegistry
    promptBuilder *prompt.PromptBuilder
    systemPrompt  string

    reactPlanner        planner.Planner
    planAndSolvePlanner *planner.PlanAndSolvePlanner
    currentMode         planner.ExecutionMode
    evalEnabled         bool

    // --- 共享配置 (决定 Session 默认行为) ---
    maxIterations          int
    timeout                time.Duration
    streamEnabled          bool
    outputSchema           any
    autoRetry              int
    loopDetectionThreshold int
    toolResultMaxLen       int
    recentToolResultTokens int
    maxContextRatio        float64
    compressConfig         *CompressAgentConfig

    // --- 会话级资源工厂 (只暴露语义层 / ContentStore 两个入口) ---
    // memoryFactory 内部自由组合 MessageStore 引擎 (InMemory/Redis/SQL/Custom)
    // Agent 不感知 MessageStoreFactory — 完全由 MemoryFactory 闭包管理。
    memoryFactory       MemoryFactory       // 会话级语义层 Memory
    contentStoreFactory ContentStoreFactory // 截断内容存储引擎

    // --- 会话管理 ---
    sessions sync.Map // map[string]*Session
}

// Run 便捷方法：支持可选 sessionID，不传则自动创建新会话
// result.SessionID 始终返回会话 ID，调用方可保存用于后续复用
func (a *Agent) Run(ctx context.Context, input string, sessionID ...string) (*AgentResult, error)
func (a *Agent) RunStream(ctx context.Context, input string, sessionID ...string) (*AgentResult, error)
```

**MemoryFactory — 会话级 Memory 工厂**

```go
// agent/option.go

// MemoryFactory 根据 sessionID 创建独立的 Memory 实例
type MemoryFactory func(sessionID string) memory.Memory

// ContentStoreFactory 根据 sessionID 创建独立的 ContentStore 实例
type ContentStoreFactory func(sessionID string) memory.ContentStore
```

默认情况下每个 Session 使用 `BufferMemory(InMemoryMessageStore, 100)` + `InMemoryContentStore` 作为**零依赖回退**，并在 Agent 初始化时打印一条告警日志。生产环境只需要覆写 `WithMemoryFactory` / `WithContentStoreFactory`，在闭包里自由选择持久化引擎：

```go
// 默认 (测试/脚本场景): 内存引擎, 进程退出即丢失
a := agent.New(agent.WithModel(m))

// 推荐: Redis 引擎 — MemoryFactory 闭包内部组合 MessageStore, Agent 不感知引擎细节
a := agent.New(
    agent.WithModel(m),
    agent.WithMemoryFactory(func(sid string) memory.Memory {
        store := memory.NewRedisMessageStore(redisClient, "agent:msgs:"+sid, 24*time.Hour)
        return memory.NewBuffer(store, 100) // 滑窗策略 + Redis 引擎
    }),
    agent.WithContentStoreFactory(func(sid string) memory.ContentStore {
        return memory.NewRedisContentStore(redisClient, "agent:content:"+sid, 24*time.Hour)
    }),
)

// 进阶: 语义层 + 引擎层在同一个工厂中组合 (SQL 持久化 + 摘要策略)
a := agent.New(
    agent.WithModel(m),
    agent.WithMemoryFactory(func(sid string) memory.Memory {
        store := memory.NewSQLMessageStore(sqldb, sid)    // 底层 SQL 引擎
        return memory.NewSummary(store, summarizer, 50)   // 语义层摘要策略
    }),
)
```

**Session 创建流程**

```
agent.Session("user-123-conv-456")
 │
 ├──► sessions.LoadOrStore(sessionID)
 │         │
 │         ├── 已存在 → 返回已有 Session (保持对话上下文)
 │         │
 │         └── 不存在 → 创建新 Session:
 │                │
 │                ├── memory = memoryFactory(sessionID)
 │                ├── contentStore = contentStoreFactory(sessionID)
 │                ├── loopDetector = NewLoopDetector(threshold)
 │                ├── compressor = NewContextCompressor(...)
 │                └── 返回新 Session
 │
 └──► session.Run(ctx, "帮我查天气")
           │
           └──► runLoop(ctx)  // AgentLoop 在 Session 上下文中运行
                              // 使用 session.memory (非 agent.memory)
```

**多会话并发示例**

```go
// Agent 共享单例 (wire 注入一次)
a := agent.New(
    agent.WithModel(openai.New("gpt-4o", apiKey)),
    agent.WithTools(readTool, writeTool, shellTool),
    agent.WithMemoryFactory(func(sid string) memory.Memory {
        // 在工厂闭包内部自由选择引擎 — Agent 对此无感知
        store := memory.NewRedisMessageStore(rdb, "agent:msgs:"+sid, time.Hour)
        return memory.NewBuffer(store, 100)
    }),
)

// HTTP handler — 每个请求对应独立会话
func handleChat(w http.ResponseWriter, r *http.Request) {
    sessionID := r.Header.Get("X-Session-ID") // 首次为空
    input := parseInput(r)

    // 方式一: 便捷 Run (自动 resolve session)
    result, err := a.Run(r.Context(), input, sessionID)
    // result.SessionID 始终有值，首次自动生成，后续复用
    w.Header().Set("X-Session-ID", result.SessionID)

    // 方式二: 显式 Session 管理
    // sess := a.Session(sessionID)
    // result, err := sess.Run(r.Context(), input)
    // ...
}
```

> **向后兼容**: 保留 `agent.Run(ctx, input)` 作为便捷方法，内部自动创建匿名 Session (等价于 `agent.NewSession().Run(ctx, input)`)。适用于单次执行、脚本等不需要会话管理的简单场景。

---

## 五、调用链路图

### 5.1 完整单轮调用链路 (含可靠性保障)

```
User
 │
 ▼
sess := agent.Session("session-123")
sess.Run(ctx, "帮我查上海天气")
 │
 │  Session 持有独立 memory / loopDetector / contentStore / compressor
 │
 │ ┌──────────────────────────────────────────────────────────────────────┐
 │ │  StatePlanning                                                      │
 │ │                                                                     │
 │ │  ┌── L4 智能压缩 ──────────────────────────────────────────────┐    │
 │ │  │ compressor.ShouldCompress(messages, 128000)?                │    │
 │ │  │   ├── Yes → compressor.Compress(ctx, messages)              │    │
 │ │  │   │           → 压缩 Agent (独立 LLM 调用) 生成摘要           │    │
 │ │  │   │           → memory.Clear() + memory.Add(压缩后消息)       │    │
 │ │  │   └── No  → 保持不变                                        │    │
 │ │  └────────────────────────────────────────────────────────────┘    │
 │ │                                                                     │
 │ │  hook.OnEvent(EventPlanStart)                                       │
 │ │        │                                                            │
 │ │        ▼                                                            │
 │ │  memory.Messages() → 获取历史上下文                                   │
 │ │        │                                                            │
 │ │        ▼                                                            │
 │ │  planner.Plan(ctx, state)  ──► ChatModel.Generate()                 │
 │ │        │                              │                             │
 │ │        │                              ▼                             │
 │ │        │                        LLM Provider (HTTP/SSE)             │
 │ │        │                                                            │
 │ │        ▼                                                            │
 │ │  PlanResult{ Action: ToolCall("get_weather", {"city":"上海"}) }      │
 │ │        │                                                            │
 │ │        ├── ActionToolCall    → StateInterrupt                       │
 │ │        ├── ActionAskHuman   → StateInterrupt                       │
 │ │        └── ActionFinalAnswer → StateCompleting                      │
 │ └──────────────────────────────────────────────────────────────────────┘
 │
 │ ┌──────────────────────────────────────────────────────────────────────┐
 │ │  StateInterrupt (HITL 检查)                                         │
 │ │                                                                     │
 │ │  interrupter.ShouldInterrupt()                                      │
 │ │        │                                                            │
 │ │        ├── true  → interrupter.WaitForHuman() → HumanResponse       │
 │ │        │              ├── Approved  → StateExecuting                 │
 │ │        │              └── Denied   → memory.Add(反馈) → StatePlanning│
 │ │        │                                                            │
 │ │        └── false → StateExecuting                                   │
 │ │                                                                     │
 │ │  memory.Add(assistant tool_call message)                            │
 │ └──────────────────────────────────────────────────────────────────────┘
 │
 │ ┌──────────────────────────────────────────────────────────────────────┐
 │ │  StateExecuting                                                     │
 │ │                                                                     │
 │ │  ┌── 死循环检测 ──────────────────────────────────────────────┐     │
 │ │  │ loopDetector.Record(toolName, toolInput)                   │     │
 │ │  │   ├── LoopTerminate → return ErrLoopDetected (直接终止)     │     │
 │ │  │   ├── LoopWarning   → 标记, 执行后注入警告                  │     │
 │ │  │   └── LoopNormal    → 继续                                │     │
 │ │  └────────────────────────────────────────────────────────────┘     │
 │ │        │                                                            │
 │ │        ▼                                                            │
 │ │  hook.OnEvent(EventToolCallStart)                                   │
 │ │        │                                                            │
 │ │        ▼                                                            │
 │ │  executor.Execute(ctx, action)                                      │
 │ │        │                                                            │
 │ │        ▼                                                            │
 │ │  tool.Execute(ctx, input)  ──► 实际工具调用                          │
 │ │        │                         │                                  │
 │ │        │                  ┌──────┴──────────────────────┐           │
 │ │        │                  │ Runtime 分派:                │           │
 │ │        │                  │  Local → 本地 exec           │           │
 │ │        │                  │  Docker → 容器内执行          │           │
 │ │        │                  │  E2B → envd REST API 远程执行 │           │
 │ │        │                  └─────────────────────────────┘           │
 │ │        │                                                            │
 │ │        ▼                                                            │
 │ │  ExecResult{ Output: "上海 25°C 晴" }                               │
 │ │        │                                                            │
 │ │  ┌── L2 结果裁剪 ────────────────────────────────────────────┐     │
 │ │  │ TruncateToolResult(output, maxLen, contentStore, callID)   │     │
 │ │  │   → 超长则截断, 完整内容持久化到 ContentStore                │     │
 │ │  └────────────────────────────────────────────────────────────┘     │
 │ │        │                                                            │
 │ │  ┌── LoopWarning? ───────────────────────────────────────────┐     │
 │ │  │ InjectLoopWarning(output, toolName, threshold)             │     │
 │ │  │   → 在 tool_result 头部注入死循环警告提示                    │     │
 │ │  └────────────────────────────────────────────────────────────┘     │
 │ │        │                                                            │
 │ │        ▼                                                            │
 │ │  memory.Add(tool_result message)                                    │
 │ │        │                                                            │
 │ │  ┌── L1 + L3 上下文压缩 ─────────────────────────────────────┐     │
 │ │  │ msgs = memory.Messages()                                   │     │
 │ │  │ → PruneConsecutiveFailures(msgs)     // L1 剔除连续失败      │     │
 │ │  │ → CompactStaleToolResults(msgs, 40k) // L3 淘汰陈旧结果      │     │
 │ │  │ → memory.Clear() + memory.Add(压缩后消息)                   │     │
 │ │  └────────────────────────────────────────────────────────────┘     │
 │ │        │                                                            │
 │ │        ▼                                                            │
 │ │  hook.OnEvent(EventToolCallDone)                                    │
 │ │        │                                                            │
 │ │        ├── evalEnabled=true  → StateEvaluating                      │
 │ │        └── evalEnabled=false → StatePlanning (回到顶部循环)          │
 │ └──────────────────────────────────────────────────────────────────────┘
 │
 │ ┌──────────────────────────────────────────────────────────────────────┐
 │ │  StateEvaluating (可选)                                              │
 │ │                                                                     │
 │ │  evaluator.Evaluate(ctx, evalState)                                 │
 │ │        │                                                            │
 │ │        ├── Continue  → StatePlanning                                │
 │ │        ├── Complete  → StateCompleting                              │
 │ │        ├── Retry     → StateExecuting (重试当前)                     │
 │ │        ├── Escalate  → StateInterrupt (强制 HITL)                   │
 │ │        └── Replan    → StatePlanGen (P&S 模式)                      │
 │ └──────────────────────────────────────────────────────────────────────┘
 │
 │ ┌──────────────────────────────────────────────────────────────────────┐
 │ │  StateCompleting                                                    │
 │ │                                                                     │
 │ │  memory.Add(assistant FinalAnswer message)                          │
 │ │        │                                                            │
 │ │  ┌── 结构化输出校验 ─────────────────────────────────────────┐     │
 │ │  │ validateFinalOutput(answer)                                │     │
 │ │  │   ├── 通过 → COMPLETE                                     │     │
 │ │  │   └── 失败 (retryLeft > 0)                                │     │
 │ │  │         → StructuredValidationError.FormatForModel()       │     │
 │ │  │         → memory.Add(结构化错误反馈 as user message)        │     │
 │ │  │         → StatePlanning (模型基于结构化反馈修正)              │     │
 │ │  └────────────────────────────────────────────────────────────┘     │
 │ │        │                                                            │
 │ │        ▼                                                            │
 │ │  hook.OnEvent(EventLoopComplete)                                    │
 │ │        │                                                            │
 │ │        ▼                                                            │
 │ │  return AgentResult{ Answer: "上海今天25°C..." }                     │
 │ └──────────────────────────────────────────────────────────────────────┘
```

### 5.2 流式调用链路 (Stream)

```
sess := agent.Session("session-123")
sess.RunStream(ctx, "...")
 │
 ├──► 启动 goroutine 运行 AgentLoop (在 Session 上下文中)
 │         │
 │         ├──► planner 使用 ChatModel.Stream()
 │         │         │
 │         │         ▼
 │         │    StreamIterator.Next()
 │         │         │
 │         │         ├──► hook.OnEvent(EventStreamChunk)  ─► 实时推送 token
 │         │         └──► 累积完整 response
 │         │
 │         ├──► executor / evaluator 同非流式
 │         │
 │         └──► 通过 channel 输出给调用方
 │
 └──► return *AgentStreamResponse (channel-based iterator)
```

### 5.3 E2B 云沙箱调用链路

Shell 等工具使用 Runtime 接口执行命令。当 Runtime 为 E2B 时，命令通过 REST API 发送至远程沙箱执行:

```
ShellTool.Execute(ctx, {"cmd": "ls -la"})
 │
 ├──► runtime.Exec(ctx, "ls", "-la")
 │         │
 │         ▼ (根据 SandboxType 分派)
 │
 │    ┌── SandboxDocker/Worktree ─────────────────────────────────┐
 │    │ LocalRuntime.Exec() → os/exec.Command → 本地/容器进程       │
 │    └────────────────────────────────────────────────────────────┘
 │
 │    ┌── SandboxE2B ─────────────────────────────────────────────┐
 │    │ E2BRuntime.Exec()                                         │
 │    │    │                                                      │
 │    │    ├──► POST https://{sandboxID}-{clientID}.e2b.dev/commands
 │    │    │         Body: {"cmd": "ls -la"}                      │
 │    │    │         Headers: X-API-Key, Content-Type             │
 │    │    │                                                      │
 │    │    ▼                                                      │
 │    │    E2B envd (远程沙箱内执行)                                │
 │    │    │                                                      │
 │    │    ▼                                                      │
 │    │    Response: {stdout, stderr, exitCode}                   │
 │    └────────────────────────────────────────────────────────────┘
 │
 └──► ExecOutput{ Stdout: "...", Stderr: "", ExitCode: 0 }
```

**E2B 沙箱生命周期:**

```
NewSandbox(SandboxConfig{Type: SandboxE2B, E2B: &E2BConfig{...}})
 │
 ├──► NewE2B(config)
 │         │
 │         ├──► POST https://api.e2b.app/sandboxes
 │         │         Body: {templateID, timeout, envVars}
 │         │         Headers: X-API-Key
 │         │         │
 │         │         ▼
 │         │    201 Created: {sandboxID, clientID, envdVersion}
 │         │
 │         └──► envdBase = https://{sandboxID}-{clientID}.e2b.dev
 │
 ├──► sandbox.Exec(ctx, cmd, args...)   // 可多次调用
 │         └──► POST envdBase/commands
 │
 └──► sandbox.Close()
           └──► DELETE https://api.e2b.app/sandboxes/{sandboxID}
```

---

## 六、数据流转图

```
┌─────────────────────────────────────────────────────────────────┐
│              Session.Run 数据流 (含可靠性保障)                     │
│              Session 持有独立 Memory / LoopDetector / ContentStore │
└─────────────────────────────────────────────────────────────────┘

 UserMessage ─────┐
                  │
                  ▼
           ┌──────────────────┐     ┌──────────────────┐
           │ session.memory    │◄────│ 历史 Messages     │
           │  .Messages()      │     │ (per-session)    │
           └────┬─────────────┘     └──────────────────┘
                │
          ┌─────▼──────────────────────────────────────┐
          │  L4 智能压缩 (Planner 调用前)                 │
          │  ShouldCompress? → Compress → 重建 messages  │
          └─────┬──────────────────────────────────────┘
                │
                ▼
        ┌───────────────┐
        │   PlanState   │  = Messages + Tools[] + History[]
        └───────┬───────┘
                │
                ▼
        ┌───────────────┐    ┌────────────────────────┐
        │    Planner    │───►│  ChatModel.Generate()  │
        │   .Plan()     │    │  → LLM API 请求        │
        └───────┬───────┘    │  messages + tools JSON  │
                │            └────────────────────────┘
                ▼
        ┌───────────────┐
        │  PlanResult   │  = Action{ type, toolName, input } + Reasoning
        └───────┬───────┘
                │
         ┌──────┴──────┐
         │             │
    ToolCall      FinalAnswer
         │             │
         ▼             ▼
  ┌──────────────────┐ ┌─────────────────────────────────┐
  │ LoopDetector     │ │ OutputController                 │
  │ .Record()        │ │ .ValidateOutput()                │
  │ ├ Terminate→终止 │ │ ├ 通过 → AgentResult              │
  │ ├ Warning→标记   │ │ └ 失败 → StructuredValidation    │
  │ └ Normal→继续    │ │          Error.FormatForModel()  │
  └────────┬─────────┘ │          → 反馈回 Planner 重试    │
           │            └──────────────┬──────────────────┘
           ▼                           │
  ┌────────────────┐                   ▼
  │   Executor     │             ┌───────────┐
  │   .Execute()   │             │AgentResult│ ─── 返回给 User
  └───────┬────────┘             └───────────┘
          │
          ▼
  ┌───────────────────────────┐
  │ ExecResult{output}        │
  │       │                   │
  │  L2: TruncateToolResult   │ → 超长截断, 完整内容 → ContentStore
  │       │                   │
  │  LoopWarning?             │ → InjectLoopWarning (注入警告)
  └───────┬───────────────────┘
          │
          ▼
  ┌───────────────────────────┐
  │  Memory.Add(tool_result)  │
  │       │                   │
  │  L1: PruneConsecutive     │ → 剔除连续失败冗余
  │      Failures             │
  │       │                   │
  │  L3: CompactStale         │ → 淘汰超出 40k token 的旧结果
  │      ToolResults          │
  └───────┬───────────────────┘
          │
          ▼
  ┌──────────────────┐
  │ evalEnabled?     │
  └──┬────────────┬──┘
     │            │
    Yes           No ──► 直接回到 Planner (由 Planner 判断终止)
     │
     ▼
  ┌──────────┐
  │EvalState │  = Messages + StepResults
  └────┬─────┘
       │
       ▼
  ┌───────────┐
  │ Evaluator │
  │.Evaluate()│
  └─────┬─────┘
        │
        ▼
  ┌───────────┐
  │EvalResult │  → Continue (回到 Planner) / Complete / Retry
  │           │  → Escalate / Replan (PlanAndSolve 独有)
  └───────────┘
```

---

## 七、状态机设计

### 7.1 AgentLoop 状态机 (Session 上下文, 双模式 + Evaluator + 可靠性保障)

AgentLoop 在 Session 上下文中运行，所有会话级状态 (Memory, LoopDetector, ContentStore, Compressor) 来自 Session 实例。

```
                    ┌─────────┐
                    │  INIT   │
                    └────┬────┘
                         │ 读取 session.memory / session.loopDetector / Agent 共享 Config
                         │ 选择 ExecutionMode (来自 Agent 共享配置)
                         │
                  ┌──────┴──────┐
                  │ Mode?       │
                  └──┬───────┬──┘
                     │       │
                  ReAct   PlanAndSolve
                     │       │
                     │       ▼
                     │  ┌───────────┐
                     │  │ PLAN_GEN  │  生成完整计划 (PlanAndSolve 独有)
                     │  └─────┬─────┘
                     │        │ planResult.ActionPlan
                     │        ▼
                     │  ┌───────────┐
                     │  │PLAN_REVIEW│  (可选 HITL: 审查计划)
                     │  └─────┬─────┘
                     │        │
                     ▼        ▼
                    ┌──────────────────────────────────────────┐
          ┌────────│ PLANNING                                  │◄──────┐
          │        │ ┌────────────────────────────────────┐    │       │
          │        │ │ [L4] compressor.ShouldCompress()?   │    │       │
          │        │ │  Yes → Compress → 重建 messages     │    │       │
          │        │ └────────────────────────────────────┘    │       │
          │        │                                           │       │
          │        │ planner.Plan() → Action                   │       │
          │        └────┬─────────────────────────────────────┘       │
          │             │                                              │
          │     ┌───────┴───────┐                                      │
          │     │ ActionType?   │                                      │
          │     └───┬───────┬───┘                                      │
          │         │       │                                          │
          │    ToolCall   FinalAnswer                                  │
          │         │       │                                          │
          │         ▼       ▼                                          │
          │  ┌──────────┐ ┌────────────────────────────────────┐      │
          │  │INTERRUPT? │ │COMPLETING                          │      │
          │  │(HITL chk) │ │ ┌──────────────────────────────┐  │      │
          │  └──┬───┬───┘ │ │ validateFinalOutput()         │  │      │
          │     │   │      │ │  ├ 通过 → COMPLETE             │  │      │
          │  approve deny  │ │  └ 失败 + retryLeft > 0       │  │      │
          │     │   │      │ │    → StructuredValidation     │  │      │
          │     │   ▼      │ │      Error.FormatForModel()   │  │      │
          │     │ ┌──────┐ │ │    → feedback → PLANNING ─────┼──┼──────┤
          │     │ │DENIED│ │ └──────────────────────────────┘  │      │
          │     │ └──┬───┘ │ 通过:                              │      │
          │     │    │     │  COMPLETE → return AgentResult     │      │
          │     │    └─────┼───► Memory.Add(反馈) → PLANNING ──┼──────┤
          │     │          └────────────────────────────────────┘      │
          │     ▼                                                      │
          │  ┌────────────────────────────────────────────┐            │
          │  │ EXECUTING                                   │            │
          │  │ ┌────────────────────────────────────────┐  │            │
          │  │ │ [死循环检测] loopDetector.Record()       │  │            │
          │  │ │  ├ Terminate → return ErrLoopDetected   │  │            │
          │  │ │  └ Warning/Normal → 继续                │  │            │
          │  │ └────────────────────────────────────────┘  │            │
          │  │                                             │            │
          │  │ executor.Execute() → ExecResult              │            │
          │  │                                             │            │
          │  │ ┌────────────────────────────────────────┐  │            │
          │  │ │ [L2] TruncateToolResult → 截断+持久化   │  │            │
          │  │ │ [Warning?] InjectLoopWarning            │  │            │
          │  │ │ memory.Add(tool_result)                  │  │            │
          │  │ │ [L1] PruneConsecutiveFailures            │  │            │
          │  │ │ [L3] CompactStaleToolResults             │  │            │
          │  │ └────────────────────────────────────────┘  │            │
          │  └─────┬──────────────────────────────────────┘            │
          │        │                                                   │
          │  ┌─────▼───────────┐                                       │
          │  │ evalEnabled?    │                                       │
          │  └──┬──────────┬──┘                                       │
          │     │          │                                           │
          │    Yes         No ──► 直接回到 PLANNING ───────────────────┤
          │     ▼                                                      │
          │  ┌────────────┐                                            │
          │  │ EVALUATING │                                            │
          │  └──┬────┬────┘                                            │
          │     │    │                                                 │
          │  Continue Retry   Complete   Escalate   Replan             │
          │     │    │          │          │           │                │
          │     │    │          ▼          ▼           │                │
          │     │    │     COMPLETE   INTERRUPT        │                │
          │     │    │                (强制 HITL)      │                │
          │     │    │                                 │                │
          │     │    └─► 重试当前步骤 ─────────────────┤                │
          │     │                                     │                │
          │     │    [PlanAndSolve] Replan ◄───────────┘                │
          │     │         │                                            │
          │     │         └─► 回到 PLAN_GEN 重新生成计划                 │
          │     └──────────────────────────────────────────────────────┘
          │
          │  Error at any state (包括 ErrLoopDetected)
          ▼
     ┌─────────┐
     │  ERROR  │  → hook.OnEvent(EventError) → 可恢复则重试，否则终止
     └─────────┘
```

### 7.2 状态定义

```go
type LoopState int
const (
    StateInit       LoopState = iota
    StatePlanGen                       // PlanAndSolve 独有: 生成完整计划
    StatePlanReview                    // PlanAndSolve 独有: 计划审查 (可选 HITL)
    StatePlanning
    StateInterrupt
    StateExecuting
    StateEvaluating
    StateCompleting
    StateComplete
    StateError
)
```

### 7.3 状态转换规则

> 所有状态在 Session 上下文中运行。Memory/LoopDetector/ContentStore/Compressor 均为 Session 独有，Agent 级组件 (Planner/Executor/Evaluator/ToolRegistry) 为共享只读。

| 当前状态 | 触发条件 | 目标状态 | 模式 | 可靠性相关 |
|---------|---------|---------|------|-----------|
| Init | Mode=ReAct | Planning | ReAct | |
| Init | Mode=PlanAndSolve | PlanGen | P&S | |
| PlanGen | 计划生成成功 | PlanReview | P&S | |
| PlanGen | Error | Error | P&S | |
| PlanReview | HITL 审查通过 / HITL 未启用 | Planning | P&S | |
| PlanReview | HITL 审查拒绝 (需修改) | PlanGen (带反馈) | P&S | |
| Planning | 进入后先检测 L4 | (内部压缩) | 通用 | **L4 智能压缩** |
| Planning | Action = ToolCall | Interrupt (HITL检查) | 通用 | |
| Planning | Action = FinalAnswer | Completing | 通用 | |
| Planning | Action = AskHuman | Interrupt | 通用 | |
| Planning | Error | Error | 通用 | |
| Interrupt | Approved / 无需中断 | Executing | 通用 | |
| Interrupt | Denied | Planning (带反馈) | 通用 | |
| Interrupt | Timeout / Cancel | Error | 通用 | |
| Executing | loopDetector → Terminate | Error (ErrLoopDetected) | 通用 | **死循环终止** |
| Executing | loopDetector → Warning | Executing (注入警告) | 通用 | **死循环警告** |
| Executing | 工具执行后 | (内部 L2/L1/L3) | 通用 | **L2截断 + L1剔除 + L3淘汰** |
| Executing | 成功 + evalEnabled=true | Evaluating | 通用 | |
| Executing | 成功 + evalEnabled=false | Planning | 通用 | |
| Executing | 失败 (可重试) | Executing (重试) | 通用 | |
| Executing | 失败 (不可重试) | Error | 通用 | |
| Evaluating | Continue | Planning | 通用 | |
| Evaluating | Complete | Completing | 通用 | |
| Evaluating | Retry | Executing | 通用 | |
| Evaluating | Escalate | Interrupt | 通用 | |
| Evaluating | Replan | PlanGen (带已完成步骤) | P&S | |
| Completing | 输出校验通过 | Complete | 通用 | |
| Completing | 输出校验失败 (可重试) | Planning (带结构化错误) | 通用 | **结构化错误反馈** |
| Error | 可恢复 | Planning | 通用 | |
| Error | 不可恢复 | Complete (带 error) | 通用 | |

---

## 八、配置梳理

### 8.1 Agent 级配置

```go
type AgentConfig struct {
    // 基本
    Name           string        // Agent 名称
    SystemPrompt   string        // 系统提示词
    
    // 执行控制
    MaxIterations  int           // 最大循环次数 (默认 10)
    MaxTokenBudget int           // token 预算上限
    Timeout        time.Duration // 整体超时 (默认 5min)
    
    // 执行模式
    ExecutionMode  ExecutionMode // ReAct (默认) / PlanAndSolve
    
    // Evaluator 开关
    EvalEnabled    bool          // 是否启用 Evaluator (默认 false)
    
    // 输出控制
    OutputSchema   any           // 期望输出结构 (可选, 用于 OutputController)
    AutoRetry      int           // 输出校验失败自动重试次数
    
    // 流式
    StreamEnabled  bool          // 是否启用流式输出
    
    // 会话级资源工厂 (Agent 只感知语义层 / ContentStore 两个入口)
    // MessageStore 引擎在 MemoryFactory 闭包内部自由组合, 不出现在 Agent 配置上。
    MemoryFactory       MemoryFactory       // 语义层 Memory 工厂 (默认: BufferMemory(InMemoryMessageStore, 100) + 告警日志)
    ContentStoreFactory ContentStoreFactory // 截断内容存储工厂 (默认: InMemoryContentStore + 告警日志)
    
    // 死循环检测
    LoopDetectionThreshold int   // 连续相同工具调用阈值 (默认 3)
    
    // 动态上下文压缩
    ToolResultMaxLen       int              // 单条 tool_result 最大字符数 (默认 30000)
    RecentToolResultTokens int              // 保留最近工具结果的 token 预算 (默认 40000)
    CompressAgent          *CompressAgentConfig // 智能压缩 Agent 配置 (nil 使用默认)
    MaxContextRatio        float64          // 触发智能压缩的上下文占比 (默认 0.9)
}
```

### 8.2 Model 配置

```go
type ModelConfig struct {
    Provider    string        // "openai" / "anthropic" / "ollama"
    BaseURL     string
    APIKey      string
    Model       string        // "gpt-4o" / "claude-sonnet-4-20250514" 等
    Temperature float64
    MaxTokens   int
    Timeout     time.Duration // 单次请求超时
}
```

### 8.3 Tool 配置

```go
type ToolConfig struct {
    Timeout       time.Duration // 单个工具执行超时 (默认 30s)
    MaxRetries    int           // 工具失败重试次数
    ParallelLimit int           // 并行工具调用上限
    Sandbox       bool          // 是否在沙箱中执行
}
```

### 8.6 Skill 配置

```go
type SkillConfig struct {
    MaxIterations  int           // Skill 内部 mini loop 最大迭代 (默认 5)
    Timeout        time.Duration // 单个 Skill 执行超时 (默认 60s)
    LogViewContext bool          // 是否上报 skill_context 中间上下文 (默认 true)
}
```

### 8.4 HITL 配置

```go
type HITLConfig struct {
    Enabled         bool
    RequireApproval []string      // 需要审批的工具名列表 (空 = 全部)
    AutoApproveRead bool          // 读取类操作自动通过
    WaitTimeout     time.Duration // 等待人类响应超时
}
```

### 8.5 Memory 配置 (会话级)

Agent 对外只暴露**一个** Memory 相关工厂：`MemoryFactory`。用户在它的闭包内部自由组合 `MessageStore` 引擎（InMemory / Redis / SQL / Custom）与语义层策略（Buffer / Summary），框架层对具体引擎完全无感知。

```go
// memory/memory.go — 语义层接口 (见 4.6)
type Memory interface {
    Messages(ctx context.Context) ([]model.ChatMessage, error)
    Add(ctx context.Context, msgs ...model.ChatMessage) error
    Clear(ctx context.Context) error
}

// memory/store.go — 底层数据引擎接口 (仅作为 Memory 实现的内部组合参数, 见 4.6.1)
type MessageStore interface { /* Append/List/Replace/Len/TrimHead/Clear/Close */ }

// 工厂类型 — Agent 只认识这两个
type MemoryFactory       func(sessionID string) memory.Memory
type ContentStoreFactory func(sessionID string) memory.ContentStore

// 零依赖回退 (未显式配置时使用, 并在日志警告)
func defaultMemoryFactory(sessionID string) memory.Memory {
    // 组合: 内置 InMemory 引擎 + BufferMemory 滑窗策略
    return memory.NewBuffer(memory.NewInMemoryMessageStore(), 100)
}
func defaultContentStoreFactory(sessionID string) memory.ContentStore {
    return memory.NewInMemoryContentStore()
}

// 配置选项
type MemoryConfig struct {
    Type         string // "buffer" / "summary"
    MaxMessages  int    // BufferMemory 最大消息数
    MaxTokens    int    // 按 token 数裁剪
}
```

**Functional Options**

```go
// 【推荐】自定义语义层 Memory 工厂 — 引擎在闭包内部自由组合
// (不存在 WithMessageStoreFactory: MessageStore 只是 Memory 实现的内部组件)
func WithMemoryFactory(f MemoryFactory) AgentOption

// 自定义 ContentStore 工厂 (截断内容存储引擎)
func WithContentStoreFactory(f ContentStoreFactory) AgentOption

// 向后兼容: WithMemory 仍然可用, 但等价于固定工厂
// 内部实现: WithMemory(m) → WithMemoryFactory(func(_ string) Memory { return m })
// ⚠️ 使用 WithMemory 时所有 Session 共享同一 Memory, 仅适用于单 Session 场景
func WithMemory(m memory.Memory) AgentOption
```

**典型配置场景**

```go
// 场景 1: 单机测试 — 零依赖, 默认 InMemory 引擎 (日志会提示 "using in-memory fallback")
a := agent.New(agent.WithModel(m))

// 场景 2: Redis 引擎 + 滑窗策略 (推荐生产基础配置)
rdb := redis.NewClient(&redis.Options{Addr: "redis:6379"})
a := agent.New(
    agent.WithModel(m),
    agent.WithMemoryFactory(func(sid string) memory.Memory {
        store := memory.NewRedisMessageStore(rdb, "agent:msgs:"+sid, 24*time.Hour)
        return memory.NewBuffer(store, 100)
    }),
    agent.WithContentStoreFactory(func(sid string) memory.ContentStore {
        return memory.NewRedisContentStore(rdb, "agent:content:"+sid, 24*time.Hour)
    }),
)

// 场景 3: SQL 持久化 + 摘要策略
sqldb, _ := sql.Open("postgres", dsn)
a := agent.New(
    agent.WithModel(m),
    agent.WithMemoryFactory(func(sid string) memory.Memory {
        store := memory.NewSQLMessageStore(sqldb, sid)   // SQL 引擎
        return memory.NewSummary(store, summarizer, 50)  // 摘要策略
    }),
)

// 场景 4: 自定义引擎 (实现 MessageStore 7 个方法即可)
type MongoMessageStore struct { /* ... */ }
a := agent.New(
    agent.WithModel(m),
    agent.WithMemoryFactory(func(sid string) memory.Memory {
        store := &MongoMessageStore{coll: mongoColl, sessionID: sid}
        return memory.NewBuffer(store, 200)
    }),
)
```

> **默认行为**: 未显式配置 `WithMemoryFactory` 时，框架使用 `BufferMemory(InMemoryMessageStore, 100)` 作为零依赖回退，并在 Agent 初始化日志中打印一条 `[WARN] memory: no MemoryFactory configured; falling back to BufferMemory backed by InMemoryMessageStore...` 以提醒生产环境切换到持久化引擎。

### 8.7 Runtime / 沙箱配置

```go
type SandboxType int
const (
    SandboxDocker   SandboxType = iota
    SandboxWorktree
    SandboxE2B
)

type SandboxConfig struct {
    Type       SandboxType
    Image      string        // Docker image (SandboxDocker)
    WorkDir    string
    GitRepo    string        // Git 仓库路径 (SandboxWorktree)
    BranchName string
    E2B        *E2BConfig    // E2B 专属配置 (SandboxE2B)
}

type E2BConfig struct {
    APIKey     string            // E2B API Key (必填)
    TemplateID string            // 沙箱模板 ID (默认 "base")
    APIBaseURL string            // API 地址 (默认 https://api.e2b.app)
    Timeout    int               // 沙箱存活秒数 (默认 300)
    EnvVars    map[string]string // 环境变量
    HTTPClient *http.Client      // 自定义 HTTP 客户端 (可选)
}
```

| 沙箱类型 | 说明 | 特点 |
|---------|------|------|
| SandboxDocker | Docker 容器沙箱 | 本地隔离，需要 Docker 环境 |
| SandboxWorktree | Git Worktree 沙箱 | 轻量级分支隔离 |
| SandboxE2B | E2B 云沙箱 | 远程隔离，无本地依赖，REST API 调用 |

---

## 九、扩展性设计

### 9.1 接口驱动 — 所有核心组件均为 interface

```
             ChatModel (interface)
                  │
     ┌────────┬───┼────┬────────┐
     ▼        ▼   ▼    ▼        ▼
  OpenAI  Anthropic Ollama  WrapFunc  Custom  ← 实现接口即可对接任意 API

             Planner (interface)
                  │
           ┌──────┼──────┐
           ▼      ▼      ▼
        ReAct  PlanExec  Custom    ← 新增规划策略只需实现接口

             Memory (interface, 语义层)        Agent 唯一感知层 (通过 MemoryFactory 创建)
                  │
           ┌──────┴──────┐
           ▼             ▼
        Buffer        Summary            ← 滑窗 / 摘要策略
           │             │
           └──────┬──────┘
                  ▼
          MessageStore (仅 Memory 实现的内部组合参数, Agent 不感知)
                  │
       ┌──────────┼──────────┬──────────┐
       ▼          ▼          ▼          ▼
   InMemory    Redis        SQL       Custom       ← 用户在 MemoryFactory 闭包里
                                                      直接 new 并传给 Memory 构造器

             Runtime (interface)
                  │
           ┌──────┼──────┬──────┬──────┐
           ▼      ▼      ▼      ▼      ▼
        Local  Docker  Worktree  E2B  Custom  ← 实现 Exec()/Close() 即可

             Tool (interface)
                  │
        ┌────┬────┼────┬────┬─────┬─────┐
        ▼    ▼    ▼    ▼    ▼     ▼     ▼
     Read Write Shell  MCP  Fn  RAG   Skill  ← 注册即可用 (RAG/Skill 均封装为 Tool)

             Rule (interface)             渐进式披露
                  │                   prompt 只放目录摘要
           ┌──────┼──────┐            模型按需 rule_view 加载
           ▼      ▼      ▼
       Safety  CodeStyle Custom   ← 选择性注入

             Skill (interface)           渐进式披露
                  │                   prompt 只放目录摘要
           ┌──────┼──────┐            模型按需 skill_call 触发
           ▼      ▼      ▼            skill_context 独立上下文
       Deploy  Debug  Custom      ← 选择性注入
```

### 9.2 Functional Options 配置模式

```go
// 最简 (ReAct 默认, 无 Evaluator, 自动使用 DefaultReActSystemPrompt)
a1 := agent.New(
    agent.WithModel(openai.New("gpt-4o", apiKey)),
    agent.WithTools(readTool, writeTool, shellTool),
    agent.WithMaxIterations(15),
)
result, _ := a1.Run(ctx, "帮我查天气")         // 自动创建新会话
sid := result.SessionID                        // 拿到会话 ID

// 用 SessionID 复用会话 (保留对话记忆)
result2, _ := a1.Run(ctx, "再查北京的", sid)   // 同一会话

// 也可显式管理 Session
sess := a1.Session("user-123")
result3, _ := sess.Run(ctx, "帮我查上海天气")
result4, _ := sess.Run(ctx, "再查北京的")       // 同一会话

// ReAct + Evaluator
a1e := agent.New(
    agent.WithModel(openai.New("gpt-4o", apiKey)),
    agent.WithEvaluator(llmjudge.New(openai.New("gpt-4o", apiKey))),
    agent.WithTools(readTool, writeTool, shellTool),
)

// 全功能: PlanAndSolve + 自定义 MemoryFactory + HITL
a2 := agent.New(
    agent.WithModel(openai.New("gpt-4o", apiKey)),
    agent.WithExecutionMode(planner.ModePlanAndSolve),
    agent.WithEvaluator(llmjudge.New(openai.New("gpt-4o", apiKey))),
    // MemoryFactory 内部一次性组合数据引擎 + 语义层; Agent 无需感知 MessageStore
    agent.WithMemoryFactory(func(sid string) memory.Memory {
        store := memory.NewRedisMessageStore(rdb, "agent:msgs:"+sid, time.Hour)
        return memory.NewBuffer(store, 20)
    }),
    agent.WithTools(readTool, writeTool, shellTool, hybridRAG),
    agent.WithRules(safetyRule, codeStyleRule),
    agent.WithSkills(deploySkill, debugSkill),
    agent.WithRulePaths("./rules", "./extra/safety.md"),   // 路径注入：目录或单文件
    agent.WithSkillPaths("./skills", "./extra/debug/"),    // 路径注入：目录或单 skill.md
    agent.WithHITL(hitl.New(hitl.RequireApproval("shell", "write"))),
    agent.WithHook(hook.NewLogger(os.Stdout)),
    agent.WithTimeout(3 * time.Minute),
)

// 也可运行时路径注入
a2.InjectRules("./rules")
a2.InjectSkills("./skills")

// 运行时动态操作 (Agent 级)
a2.SetExecutionMode(planner.ModeReAct)
a2.DisableEvaluator()
a2.EnableEvaluator(ruleeval.New(myRules))
a2.AddRule(newRule)
a2.RemoveRule("code_style")
a2.AddSkill(newSkill)
a2.RemoveSkill("deploy")

// Agent.Run — 不传 sessionID → 新建匿名会话; 传 sessionID → 复用已有会话
result, _ := a2.Run(ctx, "一次性任务")                  // 新会话
result2, _ := a2.Run(ctx, "继续", result.SessionID)    // 复用会话
```

### 9.3 Hook / Middleware 链

```
Request ──► Hook1.OnEvent ──► Hook2.OnEvent ──► ... ──► HookN.OnEvent
                                                              │
                                                              ▼
                                                        Core Logic
                                                              │
                                                              ▼
Response ◄── Hook1.OnEvent ◄── Hook2.OnEvent ◄── ... ◄── HookN.OnEvent
```

Hook 注册为有序切片，按序执行。支持的扩展点:

| Hook Event | 触发时机 | 典型用途 |
|-----------|---------|---------|
| EventPlanStart | Planner 调用前 | 日志、指标 |
| EventPlanDone | Planner 返回后 | 思考过程记录 |
| EventToolCallStart | 工具调用前 | 审计、限流 |
| EventToolCallDone | 工具调用后 | 结果缓存 |
| EventSkillContextLog | Skill 执行完毕 | **中间上下文日志上报** |
| EventEvalStart | 评估前 | 调试 |
| EventEvalDone | 评估后 | 指标 |
| EventStreamChunk | 流式 token 到达 | 实时推送 |
| EventLoopComplete | 循环结束 | 统计、通知 |
| EventError | 错误发生 | 告警、恢复 |

### 9.4 Tool 扩展方式

**方式一：实现 Tool interface**
```go
type MyTool struct{}
func (t *MyTool) Name() string { return "my_tool" }
func (t *MyTool) Description() string { return "..." }
func (t *MyTool) Schema() *tool.SchemaProperty { return ... }
func (t *MyTool) Execute(ctx context.Context, input map[string]any) (string, error) { ... }
```

**方式二：函数快捷注册 (保留现有 ToolRegistry 模式)**
```go
registry.Register("get_weather", "获取天气", WeatherInput{}, func(ctx context.Context, input map[string]any) (string, error) {
    // ...
})
```

**方式三：MCP 协议工具 (远程)**
```go
mcpTool := mcp.NewTool("http://mcp-server:8080", "search_docs")
registry.AddTool(mcpTool)
```

### 9.5 RAG 检索系统 (Tool 形式)

RAG 以标准 Tool 形式接入 Agent，模型按需调用 `rag_search` 检索知识库。框架提供**统一 Retriever 接口** + **多种内置检索策略** + **调用方自定义检索**。

#### 9.5.1 核心接口

```go
// retriever/retriever.go

// Retriever 统一检索接口 — 所有检索策略的抽象
type Retriever interface {
    Retrieve(ctx context.Context, query string, opts ...SearchOption) ([]Document, error)
}

// Document 检索结果文档
type Document struct {
    ID       string            `json:"id"`
    Content  string            `json:"content"`
    Score    float64           `json:"score"`
    Metadata map[string]string `json:"metadata,omitempty"`
    Source   string            `json:"source,omitempty"`
}

// SearchOption 检索参数
type SearchOptions struct {
    TopK       int               // 返回数量 (默认 5)
    MinScore   float64           // 最低分数阈值
    Filters    map[string]string // 元数据过滤
    Collection string            // 知识库/集合名称
}
```

#### 9.5.2 检索策略

```
            Retriever (interface)
                  │
      ┌───────────┼───────────┬──────────────┐
      ▼           ▼           ▼              ▼
  Semantic    Keyword      Hybrid        Custom
  (向量检索)  (关键词检索)  (混合检索)    (调用方自定义)
```

**语义检索 (Semantic)**

基于 Embedding 向量相似度检索，适用于语义理解场景。

```go
// retriever/semantic.go
type SemanticRetriever struct {
    embedder    Embedder       // 文本 → 向量
    vectorStore VectorStore    // 向量存储/检索
}

// Embedder 文本向量化接口
type Embedder interface {
    Embed(ctx context.Context, texts []string) ([][]float64, error)
}

// VectorStore 向量存储接口
type VectorStore interface {
    Search(ctx context.Context, vector []float64, topK int) ([]Document, error)
    Upsert(ctx context.Context, docs []Document, vectors [][]float64) error
}
```

**关键词检索 (Keyword)**

基于 BM25 或全文检索，适用于精确匹配场景。

```go
// retriever/keyword.go
type KeywordRetriever struct {
    index KeywordIndex
}

// KeywordIndex 关键词索引接口
type KeywordIndex interface {
    Search(ctx context.Context, query string, topK int) ([]Document, error)
    Index(ctx context.Context, docs []Document) error
}
```

**混合检索 (Hybrid)**

融合语义检索和关键词检索结果，通过 RRF (Reciprocal Rank Fusion) 或加权合并排序。

```go
// retriever/hybrid.go
type HybridRetriever struct {
    semantic Retriever
    keyword  Retriever
    merger   ResultMerger
}

// ResultMerger 结果合并策略
type ResultMerger interface {
    Merge(semanticResults, keywordResults []Document) []Document
}

// 内置合并策略
type RRFMerger struct {
    K             int     // RRF 参数 (默认 60)
    SemanticWeight float64 // 语义权重 (默认 0.7)
    KeywordWeight  float64 // 关键词权重 (默认 0.3)
}
```

**检索策略对比**

| 策略 | 原理 | 优势 | 适用场景 |
|-----|------|------|---------|
| Semantic | Embedding 向量相似度 | 理解语义、同义词匹配 | 开放式问答、模糊查询 |
| Keyword | BM25 / 全文索引 | 精确匹配、速度快 | 专有名词、代码检索 |
| Hybrid | 融合 Semantic + Keyword | 兼顾语义和精确 | 通用场景 (推荐默认) |
| Custom | 调用方自定义 | 完全可控 | 特殊数据源、私有协议 |

#### 9.5.3 RAG Tool — 封装为标准工具

```go
// retriever/rag_tool.go
type RAGTool struct {
    name        string
    description string
    retriever   Retriever
    formatter   ResultFormatter   // 将 []Document 格式化为模型可读文本
}

func NewRAGTool(name, description string, r Retriever, opts ...RAGToolOption) *RAGTool {
    return &RAGTool{
        name:        name,
        description: description,
        retriever:   r,
        formatter:   DefaultFormatter{},
    }
}

// 实现 Tool interface
func (t *RAGTool) Name() string        { return t.name }
func (t *RAGTool) Description() string { return t.description }
func (t *RAGTool) Schema() *tool.SchemaProperty {
    return &tool.SchemaProperty{
        Type: "object",
        Properties: map[string]*tool.SchemaProperty{
            "query":      {Type: "string", Description: "检索查询"},
            "top_k":      {Type: "integer", Description: "返回数量 (默认 5)"},
            "collection": {Type: "string", Description: "知识库名称 (可选)"},
        },
        Required: []string{"query"},
    }
}

func (t *RAGTool) Execute(ctx context.Context, input map[string]any) (string, error) {
    query := input["query"].(string)
    opts := parseSearchOptions(input)

    docs, err := t.retriever.Retrieve(ctx, query, opts...)
    if err != nil {
        return "", err
    }

    return t.formatter.Format(docs), nil
}

// ResultFormatter 检索结果格式化
type ResultFormatter interface {
    Format(docs []Document) string
}

// DefaultFormatter 默认格式化: 编号 + 内容 + 来源
type DefaultFormatter struct{}
func (f DefaultFormatter) Format(docs []Document) string {
    // [1] (score: 0.95, source: api_doc.md)
    // 内容...
    //
    // [2] (score: 0.87, source: faq.md)
    // 内容...
}
```

#### 9.5.4 调用方自定义检索

调用方可通过三种方式接入自有检索能力：

**方式一：实现 Retriever interface (推荐)**

```go
// 接入已有的 Elasticsearch / Milvus / 自研检索服务
type MyESRetriever struct {
    client *elasticsearch.Client
}

func (r *MyESRetriever) Retrieve(ctx context.Context, query string, opts ...SearchOption) ([]Document, error) {
    // 自定义检索逻辑
}

ragTool := retriever.NewRAGTool("search_docs", "检索内部文档", &MyESRetriever{client})
```

**方式二：函数式快捷创建**

```go
ragTool := retriever.NewRAGToolFunc("search_api",
    "检索 API 文档",
    func(ctx context.Context, query string, opts retriever.SearchOptions) ([]retriever.Document, error) {
        // 自定义检索逻辑
        return docs, nil
    },
)
```

**方式三：直接注册为普通 Tool (完全自定义)**

```go
// 不走 Retriever 接口，直接实现 Tool，拥有完全自定义的 schema 和逻辑
type MySearchTool struct{}
func (t *MySearchTool) Name() string { return "search_internal" }
func (t *MySearchTool) Execute(ctx context.Context, input map[string]any) (string, error) {
    // 完全自定义
}
```

#### 9.5.5 注入方式

```go
// 内置语义检索
semanticRAG := retriever.NewRAGTool("search_docs", "语义检索知识库",
    retriever.NewSemanticRetriever(
        embedder.NewOpenAI(apiKey, "text-embedding-3-small"),
        vectorstore.NewChroma("http://localhost:8000", "my_collection"),
    ),
)

// 内置混合检索
hybridRAG := retriever.NewRAGTool("search_hybrid", "混合检索知识库",
    retriever.NewHybridRetriever(
        retriever.NewSemanticRetriever(emb, vs),
        retriever.NewKeywordRetriever(bm25Index),
        retriever.WithRRF(60, 0.7, 0.3),   // RRF 合并策略
    ),
)

// 自定义检索
customRAG := retriever.NewRAGTool("search_custom", "检索内部系统", &MyRetriever{})

// 注入 Agent — 与其他 Tool 一样选择性注入
a := agent.New(
    agent.WithModel(m),
    agent.WithTools(readTool, writeTool, hybridRAG, customRAG),  // RAG 就是 Tool
    agent.WithSkills(deploySkill),
    agent.WithRules(safetyRule),
)
```

#### 9.5.6 RAG 调用链路

```
主 Agent Loop
 │
 ├──► planner.Plan()
 │         │
 │         ▼
 │    PlanResult: ToolCall("search_docs", {"query":"如何配置数据库连接池"})
 │
 ├──► executor.Execute()
 │         │
 │         ▼
 │    RAGTool.Execute(ctx, input)
 │         │
 │         ├──► Retriever.Retrieve(ctx, query, opts)
 │         │         │
 │         │    ┌────┴─────────────────────────────────┐
 │         │    │ Semantic: Embed(query) → VectorSearch │
 │         │    │ Keyword:  BM25Search(query)           │
 │         │    │ Hybrid:   Both → RRF Merge            │
 │         │    │ Custom:   调用方自定义逻辑              │
 │         │    └──────────────────────────────────────┘
 │         │         │
 │         │         ▼
 │         │    []Document{...}
 │         │
 │         ├──► formatter.Format(docs)
 │         │
 │         └──► return 格式化后的检索结果
 │
 ├──► memory.Add(tool_call_msg, tool_result_msg)
 │
 └──► 继续 Agent Loop (模型基于检索结果回答)
```

#### 9.5.7 VectorStore / Embedder 扩展

```
         Embedder (interface)
              │
       ┌──────┼──────┐
       ▼      ▼      ▼
    OpenAI  Local   Custom    ← 实现 Embed() 即可

        VectorStore (interface)
              │
       ┌──────┼──────┬──────┐
       ▼      ▼      ▼      ▼
    Chroma  Milvus  Pinecone Custom  ← 实现 Search()/Upsert() 即可

        KeywordIndex (interface)
              │
       ┌──────┼──────┐
       ▼      ▼      ▼
    BM25    ES      Custom    ← 实现 Search()/Index() 即可
```

### 9.6 Rule 系统 (渐进式披露 + Cursor Rule 格式)

Rule 是对 Agent 行为的约束/指导规则，采用 **Cursor Rule 风格 YAML Frontmatter** 格式，通过 `alwaysApply` 字段实现 **渐进式披露**：

- `alwaysApply: true` → 完整内容直接注入 System Prompt `<rules>` 块 (always-on)
- `alwaysApply: false` (默认) → 仅 name + description 出现在 `<available_rules>` 目录，模型按需调用 `rule_view` 加载

```go
// rule/rule.go
type Rule interface {
    Name() string
    Description() string  // 一句话摘要 (来自 frontmatter description 字段)
    Content() string      // 完整规则正文 (frontmatter 后的 body)
    AlwaysApply() bool    // true=直接注入; false=按需 rule_view
}
```

#### 9.6.1 Frontmatter 文件格式 (Cursor Rule 风格)

**标准格式**: .md 文件顶部使用 YAML frontmatter

```markdown
---
description: 安全约束规则，包含危险命令黑名单和权限边界
alwaysApply: true
---

## 安全规则
1. 禁止执行 rm -rf 等危险命令
2. 不得访问 /etc/passwd 等敏感文件
...
```

| Frontmatter 字段 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `description` | string | (第一行回退) | 一句话摘要，写入 system prompt 目录或 catalog |
| `alwaysApply` | bool | `false` | `true` = 完整内容注入 prompt; `false` = catalog + rule_view |

**无 frontmatter 回退** (向后兼容): 第一行 = description, 其余 = content, alwaysApply = false

#### 9.6.2 渐进式披露机制

```
System Prompt 由 prompt.PromptBuilder 统一组装 (见 4.1):

  PromptBuilder.Build(ctx) → 最终 system prompt 字符串
    ├── 基础提示词
    ├── BuildAlwaysOnRules()    → <rules> 块 (alwaysApply=true 的规则完整内容)
    ├── BuildRuleCatalog()      → <available_rules> 目录 (alwaysApply=false 的摘要)
    ├── BuildAlwaysOnSkills()   → <skills> 块 (alwaysApply=true 的技能完整内容)
    ├── BuildSkillCatalog()     → <available_skills> 目录 (alwaysApply=false 的摘要)
    └── 自定义 Sections

输出示例 (混合 alwaysApply=true 和 false):
┌─────────────────────────────────────────────────────────┐
│ 1. 基础提示词                                            │
│ 2. <rules>                                              │
│      <rule name="safety">                               │
│        ## 安全规则                                       │  ← alwaysApply=true
│        1. 禁止执行 rm -rf 等危险命令                      │     完整内容直接注入
│        2. 不得访问 /etc/passwd 等敏感文件                  │
│      </rule>                                            │
│    </rules>                                             │
│ 3. <available_rules>                                    │
│      Use the rule_view tool to read the full content... │
│      <rule name="code_style">Go 代码风格规范</rule>      │  ← alwaysApply=false
│      <rule name="naming">命名约定</rule>                 │     仅摘要
│    </available_rules>                                   │
│ 4. <available_skills>                                   │
│      ...                                                │
│    </available_skills>                                  │
└─────────────────────────────────────────────────────────┘

对于 alwaysApply=false 的 Rule，模型按需调用:

  tool_call("rule_view", {"name": "code_style"})
      │
      ▼ hook.Emit(EventRuleView, "code_style")
  返回 Rule 完整正文 → 模型据此约束后续行为
```

#### 9.6.3 rule_view 内置工具

```go
// tool/builtin/rule_view.go
// 使用回调函数而非直接引用 RuleRegistry，避免循环依赖
type RuleViewTool struct {
    loadFn func(name string) (string, error)
}

func NewRuleViewTool(loadFn func(name string) (string, error)) *RuleViewTool

func (t *RuleViewTool) Name() string        { return "rule_view" }
func (t *RuleViewTool) Description() string { return "查看指定规则的完整内容" }
```

**注册时机**: Agent 自动在 `ensureBuiltinToolsUnlocked()` 中注册，回调内触发 `EventRuleView` hook：

```go
// agent/agent.go — ensureBuiltinToolsUnlocked
a.toolRegistry.AddTool(builtin.NewRuleViewTool(func(name string) (string, error) {
    r, ok := a.ruleRegistry.Get(name)
    if !ok {
        return "", fmt.Errorf("rule not found: %s", name)
    }
    _ = a.hookManager.Emit(context.Background(), hook.Event{
        Type:    hook.EventRuleView,
        Payload: name,
    })
    return r.Content(), nil
}))
```

#### 9.6.4 Rule 注入方式

**方式一：`WithRulePaths` — 创建时路径注入 (推荐)**

```go
a := agent.New(
    agent.WithModel(openai.New("gpt-4o", apiKey)),
    agent.WithRulePaths("./rules", "./extra/safety.md"),  // 支持目录或单文件
)
```

**方式二：`agent.InjectRules(path)` — 运行时目录扫描加载**

```go
a.InjectRules("./rules")
```

**方式三：`WithRules` — 编程式注入**

```go
safetyRule := rule.NewFileRule("safety", "安全约束规则", "禁止执行危险操作", true)
a := agent.New(agent.WithRules(safetyRule))
```

**方式四：运行时动态增删**

```go
a.AddRule(newRule)
a.RemoveRule("code_style")
```

**目录约定**

```
rules/
├── safety.md              # 文件名(去后缀) = Rule name
├── code_style.md          # frontmatter.description = 摘要
├── naming_convention.md   # frontmatter.alwaysApply = 披露策略
└── error_handling.md
```

**加载 API**

```go
// rule/loader.go

// LoadPath — 智能加载：单 .md 文件或目录递归扫描
func LoadPath(path string) ([]Rule, error)

// LoadDir — 递归目录加载 (filepath.WalkDir)
func LoadDir(dirPath string) ([]Rule, error)

// FromFile — 单文件加载，解析 YAML frontmatter
func FromFile(filePath string) (*FileRule, error)
```

**FileRule 实现**

```go
type FileRule struct {
    name        string
    description string
    content     string
    alwaysApply bool
}

func NewFileRule(name, description, content string, alwaysApply bool) *FileRule
func (r *FileRule) Name() string        { return r.name }
func (r *FileRule) Description() string { return r.description }
func (r *FileRule) Content() string     { return r.content }
func (r *FileRule) AlwaysApply() bool   { return r.alwaysApply }
```

**RuleRegistry**

```go
// rule/registry.go
type RuleRegistry struct {
    mu    sync.RWMutex
    rules map[string]Rule
    order []string
}

func (r *RuleRegistry) Add(rules ...Rule)
func (r *RuleRegistry) Remove(name string)
func (r *RuleRegistry) Get(name string) (Rule, bool)
func (r *RuleRegistry) List() []Rule  // 按注册顺序返回
```

### 9.7 Skill 系统 (渐进式披露 + 独立上下文 + Frontmatter)

Skill 是 **操作手册 (SOP)**，对应一个独立领域的完整能力。参照 Claude Code / Cursor 的 Skill 范式，同样采用 **YAML Frontmatter** 格式。

**渐进式披露** (由 `alwaysApply` 控制)：
- `alwaysApply: true` → Instruction 直接注入 System Prompt `<skills>` 块
- `alwaysApply: false` (默认) → 仅 name + description 写入 `<available_skills>` 目录；模型按需调用 `skill_call` 加载完整 `skill.md` 并在独立 `skill_context` 上下文中执行

**核心设计原则**：
1. **懒加载** — alwaysApply=false 的 Skill 只放目录摘要，`skill_call` 时才加载完整 SOP
2. **always-on** — alwaysApply=true 的 Skill Instruction 直接注入 system prompt，无需 skill_call
3. **独立上下文** — `skill_call` 创建 `skill_context`，模型在其中自主执行，可通过 `skill_view` 读取 Skill 目录下的其他文件
4. **上下文隔离** — 执行结束后主 Memory 只保留 `tool_call` + `tool_result`，`skill_context` 中间上下文仅做日志上报
5. **Hook 事件** — `EventSkillCallStart` / `EventSkillCallDone` / `EventSkillContextLog`

```go
// skill/skill.go
type Skill interface {
    Name() string
    Description() string  // 一句话摘要 (来自 frontmatter description 字段)
    BasePath() string     // Skill 目录根路径 (包含 skill.md 及其他文件)
    Instruction() string  // skill.md 完整内容 (frontmatter 后的 body)
    Tools() []any         // Skill 内部可用工具 (用 any 避免循环依赖)
    MaxIterations() int   // skill_context 内最大迭代次数
    AlwaysApply() bool    // true=Instruction 注入 prompt; false=按需 skill_call
}
```

#### 9.7.1 Skill 目录结构 (参照 Cursor Skill)

```
skills/
├── deploy/
│   ├── skill.md              # 主 SOP 文档 (skill_call 时加载)
│   ├── checklist.md          # 部署检查清单 (skill_view 按需读取)
│   ├── rollback_guide.md     # 回滚指南
│   └── config_template.yaml  # 配置模板
│
├── debug/
│   ├── skill.md              # 调试 SOP
│   ├── common_errors.md      # 常见错误表
│   └── log_patterns.md       # 日志模式
│
└── code_review/
    ├── skill.md              # Code Review SOP
    └── standards.md          # 编码标准
```

#### 9.7.2 渐进式加载流程

```
Phase 0 — 注册 (创建 Agent 时):
  System Prompt 只写入目录:
  ┌─────────────────────────────────────────────────────┐
  │ <available_skills>                                  │
  │   <skill name="deploy">                             │
  │     执行应用部署流程，包含版本检查、构建、部署、验证     │
  │   </skill>                                          │
  │   <skill name="debug">                              │
  │     系统调试技能，定位和修复生产环境问题               │
  │   </skill>                                          │
  │ </available_skills>                                 │
  └─────────────────────────────────────────────────────┘

Phase 1 — skill_call (模型决定使用某个 Skill):
  主 Agent:
    tool_call("skill_call", {"name": "deploy", "input": "部署 v2.1 到 prod"})
        │
        ▼
    1. 加载 skills/deploy/skill.md → 作为 skill_context 的 system prompt
    2. 创建 skill_context (独立上下文)
    3. 进入 Phase 2

Phase 2 — skill_context 内部执行 (独立上下文):
  ┌─────────────────────────────────────────────────────┐
  │ skill_context (独立 messages 序列)                    │
  │                                                     │
  │ system: [skill.md 完整内容]                          │
  │ user: "部署 v2.1 到 prod"                            │
  │                                                     │
  │ 模型可用的工具:                                       │
  │ ├── skill_view(path)  ← 读取 skill 目录下文件   │
  │ ├── check_version(...)     ← Skill 专属内部工具      │
  │ ├── run_build(...)                                  │
  │ └── deploy(...)                                     │
  │                                                     │
  │ 模型自主执行 mini loop:                               │
  │ → skill_view("checklist.md") → 读取检查清单      │
  │ → check_version({}) → "v2.0.9"                      │
  │ → run_build({...}) → "构建成功"                       │
  │ → skill_view("rollback_guide.md") → 备查        │
  │ → deploy({...}) → "部署成功"                          │
  │ → FinalAnswer: "部署完成, v2.1.3, 耗时 45s"           │
  └─────────────────────────────────────────────────────┘
        │
        ▼
Phase 3 — 上下文回收:
  主 Memory 写入 (仅两条):
    assistant: tool_call("skill_call", {"name":"deploy", ...})
    tool: "部署完成, v2.1.3, 耗时 45s"

  skill_context 完整中间上下文 → hook.OnEvent(EventSkillContextLog) → 日志系统
```

#### 9.7.3 skill_call + skill_view 内置工具

```go
// tool/builtin/skill_call.go — 主 Agent 工具: 触发 Skill 执行
type SkillCallTool struct {
    registry *skill.SkillRegistry
    model    model.ChatModel
    hooks    []hook.Hook
}

func (t *SkillCallTool) Name() string        { return "skill_call" }
func (t *SkillCallTool) Description() string { return "调用指定技能执行复杂任务" }
func (t *SkillCallTool) Schema() *tool.SchemaProperty {
    return &tool.SchemaProperty{
        Type: "object",
        Properties: map[string]*tool.SchemaProperty{
            "name":  {Type: "string", Description: "技能名称"},
            "input": {Type: "string", Description: "任务描述/输入"},
        },
        Required: []string{"name", "input"},
    }
}

func (t *SkillCallTool) Execute(ctx context.Context, input map[string]any) (string, error) {
    name := input["name"].(string)
    userInput := input["input"].(string)

    s, ok := t.registry.Get(name)
    if !ok {
        return "", fmt.Errorf("skill %q not found", name)
    }

    // 1. 加载 skill.md 完整内容
    instruction := s.Instruction()

    // 2. 创建 skill_context
    view := skill.NewView(s, userInput, instruction)

    // 3. 在 skill_context 中执行 mini agent loop
    result, viewLog := view.Run(ctx, t.model)

    // 4. 日志上报中间上下文
    for _, h := range t.hooks {
        h.OnEvent(ctx, hook.Event{Type: hook.EventSkillContextLog, Payload: viewLog})
    }

    // 5. 只返回最终结果
    return result, nil
}
```

```go
// skill/view_tool.go — skill_context 内部工具: 读取 Skill 目录文件
type SkillViewTool struct {
    basePath string  // Skill 目录根路径
}

func (t *SkillViewTool) Name() string        { return "skill_view" }
func (t *SkillViewTool) Description() string { return "读取当前 Skill 目录下的文件" }
func (t *SkillViewTool) Schema() *tool.SchemaProperty {
    return &tool.SchemaProperty{
        Type: "object",
        Properties: map[string]*tool.SchemaProperty{
            "path": {Type: "string", Description: "相对于 Skill 目录的文件路径"},
        },
        Required: []string{"path"},
    }
}

func (t *SkillViewTool) Execute(ctx context.Context, input map[string]any) (string, error) {
    relPath := input["path"].(string)
    fullPath := filepath.Join(t.basePath, filepath.Clean(relPath))

    // 安全检查: 防止路径逃逸
    if !strings.HasPrefix(fullPath, t.basePath) {
        return "", fmt.Errorf("path escapes skill directory")
    }

    content, err := os.ReadFile(fullPath)
    if err != nil {
        return "", fmt.Errorf("read %s: %w", relPath, err)
    }
    return string(content), nil
}
```

#### 9.7.4 skill_context 独立上下文实现

```go
// skill/view.go
type SkillContext struct {
    skill       Skill
    messages    []model.ChatMessage  // 独立上下文
    tools       []tool.Tool          // skill_view 工具 + Skill 专属工具
    maxIter     int
}

type SkillContextLog struct {
    SkillName  string
    Input      string
    Messages   []model.ChatMessage  // 完整中间上下文 (仅日志)
    Result     string
    Duration   time.Duration
    TokensUsed int
    Steps      int
}

func NewContext(s Skill, userInput string, instruction string) *SkillContext {
    // 组装 skill_context 内部可用工具
    viewTools := []tool.Tool{
        &SkillViewTool{basePath: s.BasePath()},  // 始终可用: 读取 Skill 目录
    }
    viewTools = append(viewTools, s.Tools()...)       // 追加 Skill 专属工具

    return &SkillContext{
        skill:   s,
        maxIter: s.MaxIterations(),
        tools:   viewTools,
        messages: []model.ChatMessage{
            {Role: model.RoleSystem, Content: instruction},
            {Role: model.RoleUser,   Content: userInput},
        },
    }
}

// Run 在独立上下文中执行 mini agent loop
func (v *SkillContext) Run(ctx context.Context, m model.ChatModel) (string, *SkillContextLog) {
    start := time.Now()
    var totalTokens int

    for step := 0; step < v.maxIter; step++ {
        // model.Generate → 解析出 Action
        // Action = ToolCall → 在 v.tools 中查找并执行 → 追加到 v.messages
        // Action = FinalAnswer → break
    }

    return finalAnswer, &SkillContextLog{
        SkillName: v.skill.Name(),
        Messages:  v.messages,    // 完整中间过程 → 仅日志上报
        Result:    finalAnswer,
        Duration:  time.Since(start),
        TokensUsed: totalTokens,
        Steps:     step,
    }
}
```

#### 9.7.5 上下文隔离: 主 Memory vs skill_context

```
主 Agent Memory (对外可见, 精简):
┌────────────────────────────────────────────────────────────┐
│ ... 前序对话 ...                                            │
│ assistant: tool_call("skill_call",                         │
│              {"name":"deploy","input":"部署 v2.1 到 prod"}) │  ← 仅这两条
│ tool: "部署完成, v2.1.3, 耗时 45s"                          │  ← 写入主 Memory
│ ... 后续对话 ...                                            │
└────────────────────────────────────────────────────────────┘

skill_context 内部上下文 (仅日志上报, 不写入主 Memory):
┌────────────────────────────────────────────────────────────┐
│ system: [skill.md 完整 SOP 内容]                            │
│ user: "部署 v2.1 到 prod"                                   │
│ assistant: 先查看检查清单...                                  │
│ assistant: tool_call("skill_view",{"path":"checklist.md"})│ ← 读 Skill 文件
│ tool: "## 部署检查清单\n1. 版本确认 ..."                      │
│ assistant: 开始执行检查清单第一步...                           │
│ assistant: tool_call("check_version", {...})                │
│ tool: "v2.0.9"                                             │
│ assistant: tool_call("run_build", {...})                    │
│ tool: "构建成功"                                            │
│ assistant: 构建完成，查看回滚指南备用...                       │
│ assistant: tool_call("skill_view",{"path":"rollback_guide.md"})│
│ tool: "## 回滚指南\n..."                                    │
│ assistant: tool_call("deploy", {...})                       │
│ tool: "部署成功"                                            │
│ assistant: "部署完成, v2.1.3, 耗时 45s"                      │ ← 最终结果
└────────────────────────────────────────────────────────────┘
       │
       └──► hook.OnEvent(EventSkillContextLog) → 日志系统 / 可观测平台
```

#### 9.7.6 Skill 注入方式

**方式一：`agent.InjectSkills(path)` — 指定目录自动扫描加载 (推荐)**

```go
// 一行注入：自动遍历 skills/ 下每个子目录，生成 Skill 实现并注册
a.InjectSkills("./skills")
```

**目录约定**

```
skills/                        ← InjectSkills 扫描此目录
├── deploy/                    ← 子目录名 = Skill name
│   ├── skill.md               ← 必须: 主 SOP 文档
│   ├── checklist.md           ← 可选: skill_view 按需读取
│   ├── rollback_guide.md
│   └── config_template.yaml
│
├── debug/                     ← 另一个 Skill
│   ├── skill.md
│   ├── common_errors.md
│   └── log_patterns.md
│
└── code_review/
    ├── skill.md
    └── standards.md
```

**skill.md 文件格式约定 (YAML Frontmatter)**

```markdown
---
description: 执行应用部署流程，包含版本检查、构建、部署、验证
alwaysApply: false
---

## 部署 SOP
你是部署专家。按照以下步骤执行:
1. 调用 skill_view("checklist.md") 读取检查清单
2. 检查当前版本
3. 运行构建
4. 执行部署
5. 验证健康检查
...
```

无 frontmatter 时向后兼容：第一行 = description, 其余 = instruction, alwaysApply = false

**skill_call 注册** — Agent 自动在 `ensureBuiltinToolsUnlocked()` 中注册，回调内触发 Hook 事件：

```go
// agent/agent.go — ensureBuiltinToolsUnlocked
a.toolRegistry.AddTool(builtin.NewSkillCallTool(func(ctx context.Context, name, userInput string) (string, error) {
    s, ok := a.skillRegistry.Get(name)
    if !ok {
        return "", fmt.Errorf("skill not found: %s", name)
    }
    _ = a.hookManager.Emit(ctx, hook.Event{Type: hook.EventSkillCallStart, Payload: map[string]string{"skill": name, "input": userInput}})
    sc := skill.NewContext(s, userInput, s.Instruction())
    out, log := sc.Run(ctx, a.model)
    _ = a.hookManager.Emit(ctx, hook.Event{Type: hook.EventSkillContextLog, Payload: log})
    _ = a.hookManager.Emit(ctx, hook.Event{Type: hook.EventSkillCallDone, Payload: map[string]string{"skill": name, "output": out}})
    return out, nil
}))
```

#### 9.7.6 Skill 注入方式

**方式一：`WithSkillPaths` — 创建时路径注入 (推荐)**

```go
a := agent.New(
    agent.WithModel(openai.New("gpt-4o", apiKey)),
    agent.WithSkillPaths("./skills", "./extra/debug/"),  // 支持目录或单文件
)
```

**方式二：`agent.InjectSkills(path)` — 运行时目录扫描加载**

```go
a.InjectSkills("./skills")
```

**方式三：编程式创建 + WithSkills 注入**

```go
deploySkill, _ := skill.FromDir("./skills/deploy/",
    skill.WithTools(checkVersionTool, deployTool),
    skill.WithMaxIterations(8),
)
a := agent.New(agent.WithSkills(deploySkill))
```

**方式四：运行时动态增删**

```go
a.AddSkill(newSkill)
a.RemoveSkill("deploy")
```

**加载 API**

```go
// skill/loader.go

// LoadPath — 智能加载：skill 目录 / 父目录 / 单 .md 文件
func LoadPath(path string) ([]Skill, error)

// LoadDir — 扫描父目录下每个含 skill.md 的子目录
func LoadDir(dirPath string) ([]Skill, error)

// FromDir — 从含 skill.md 的目录加载，解析 YAML frontmatter
func FromDir(dirPath string, opts ...Option) (*DirSkill, error)

// FromFile — 从单个 .md 文件加载为独立 skill
func FromFile(filePath string, opts ...Option) (*DirSkill, error)
```

**DirSkill 实现**

```go
type DirSkill struct {
    name        string
    description string
    basePath    string
    instruction string
    tools       []any
    maxIter     int
    alwaysApply bool
}

func NewDirSkill(name, description, basePath, instruction string, opts ...Option) *DirSkill

// Option 配置选项
func WithTools(tools ...any) Option
func WithMaxIterations(n int) Option
func WithAlwaysApply(v bool) Option
```

**注入行为统一流程** (WithRulePaths / WithSkillPaths / InjectRules / InjectSkills):
1. 加载并解析 frontmatter → 注册到对应 Registry
2. `rebuildPromptBuilder()` — 区分 alwaysApply=true (注入 `<rules>`/`<skills>` 块) 和 false (catalog)
3. `ensureBuiltinToolsUnlocked()` — 注册 `rule_view` / `skill_call` 内置工具 (有 Rule/Skill 时自动注册)

#### 9.7.7 三种披露策略对比

| 维度 | 全量注入 (传统) | alwaysApply=true | alwaysApply=false (渐进式披露) |
|-----|---------------|-----------------|-------------------------------|
| System Prompt 体积 | 大 (所有内容) | 中 (仅 always-on 的内容) | 小 (仅 name + description 目录) |
| 上下文窗口利用率 | 低 | 中 (关键规则始终可用) | 高 (按需加载) |
| 模型自主性 | 被动 | 被动 (规则已就绪) | 主动 (模型判断何时需要何种信息) |
| Skill 内文件访问 | 不支持 | 不支持 (无 skill_context) | `skill_view` 按需读取 |
| 适用场景 | 少量简短 Rule | 核心安全/风格规则 | Rule/Skill 数量多或内容长 |
| Hook 事件 | 无 | 无 | EventRuleView / EventSkillCallStart/Done |

---

## 十、可靠性保障

### 10.1 超时与取消

```
context.Context 贯穿全链路:

ctx (总超时)
 ├── planner ctx  (单次 LLM 调用超时)
 ├── executor ctx (工具执行超时)
 ├── HITL ctx     (等待人类超时)
 └── evaluator ctx

任一层 context.Done() 触发时，整个 Agent Run 优雅终止。
```

### 10.2 重试策略

| 组件 | 重试策略 | 说明 |
|------|---------|------|
| LLM 调用 | 指数退避 + jitter, max 3次 | 仅对 5xx/超时重试，非 4xx |
| 工具执行 | 可配置, max N次 | 幂等工具可安全重试 |
| 输出校验 | 回到 Planning, max N次 | 将校验错误作为反馈 |

### 10.3 资源防护

```go
// 防止 LLM 触发的无限循环
if iteration >= config.MaxIterations {
    return AgentResult{Error: ErrMaxIterationsExceeded}
}

// Token 预算控制
if totalTokens >= config.MaxTokenBudget {
    return AgentResult{Error: ErrTokenBudgetExceeded}
}

// 并行工具调用使用 semaphore 限制
sem := make(chan struct{}, config.ToolParallelLimit)
```

### 10.4 错误分类与处理

```go
// errors/errors.go
var (
    ErrMaxIterationsExceeded = errors.New("max iterations exceeded")
    ErrTokenBudgetExceeded   = errors.New("token budget exceeded")
    ErrTimeout               = errors.New("operation timed out")
    ErrHITLDenied            = errors.New("human denied the action")
    ErrHITLTimeout           = errors.New("human response timed out")
    ErrToolNotFound          = errors.New("tool not registered")
    ErrToolExecFailed        = errors.New("tool execution failed")
    ErrOutputValidation      = errors.New("output validation failed")
    ErrModelUnavailable      = errors.New("model provider unavailable")
    ErrLoopDetected          = errors.New("suspected infinite loop: model repeatedly called same tool with identical parameters")
)
```

### 10.5 可观测性

```
Agent Run
 │
 ├── Structured Logging (hook/logger.go)
 │   └── 每个 Event 带 timestamp, duration, state
 │
 ├── Metrics (可选 hook)
 │   ├── agent_iterations_total
 │   ├── agent_tool_calls_total
 │   ├── agent_llm_latency_seconds
 │   ├── agent_tool_latency_seconds
 │   └── agent_tokens_used_total
 │
 └── Tracing (可选, 预留 OpenTelemetry span)
     ├── span: agent.run
     │   ├── span: planner.plan
     │   │   └── span: model.generate
     │   ├── span: planner.plan_gen         (PlanAndSolve 模式)
     │   ├── span: executor.execute
     │   │   └── span: tool.{name}
     │   └── span: evaluator.evaluate       (仅 evalEnabled=true 时)
```

### 10.6 Checkpoint / 恢复 (HITL 场景)

```
Agent 运行中断时:

1. 序列化 AgentSnapshot:
   ┌─────────────────────────┐
   │  AgentSnapshot          │
   │  ├── LoopState          │
   │  ├── Iteration          │
   │  ├── Messages[]         │
   │  ├── StepResults[]      │
   │  ├── PendingAction      │
   │  └── TokensUsed         │
   └─────────────────────────┘

2. 持久化到 CheckpointStore (内存 / 文件 / Redis)

3. 恢复时:
   snapshot := store.Load(runID)
   agent.Resume(ctx, snapshot, humanResponse)
```

### 10.7 结构化错误提示

输出结构校验失败时，返回**结构化错误信息**，帮助模型精准定位问题并修正输出，而非返回笼统的错误字符串。

**StructuredValidationError**

```go
// errors/errors.go

type StructuredValidationError struct {
    Message        string           `json:"message"`         // 错误概述
    ExpectedSchema string           `json:"expected_schema"` // 期望的 JSON Schema (简要)
    Violations     []FieldViolation `json:"violations"`      // 字段级校验失败详情
    Hint           string           `json:"hint"`            // 修正建议
}

type FieldViolation struct {
    Field    string `json:"field"`    // 字段路径 (如 "result.items[0].name")
    Rule     string `json:"rule"`     // 违反的规则 (required / type_mismatch / enum / pattern / min / max)
    Expected string `json:"expected"` // 期望值描述
    Actual   string `json:"actual"`   // 实际值描述
    Message  string `json:"message"`  // 可读的错误说明
}

func (e *StructuredValidationError) Error() string {
    return e.Message
}

// FormatForModel 将结构化错误格式化为模型易于解析的 JSON 文本
func (e *StructuredValidationError) FormatForModel() string {
    // 输出格式见下方示例
}
```

**OutputController 集成**

```go
// hook/outputhook/output_controller.go

func (oc *OutputController) ValidateOutput(raw string, schema any) (*ValidatedOutput, error) {
    // 1. 解析 raw → 结构体
    // 2. 校验 → 收集所有 FieldViolation
    // 3. 失败时构造 StructuredValidationError
    // 4. 将 FormatForModel() 结果作为反馈写入 messages，触发 Planner 重试
}
```

**AgentLoop 中的错误反馈流程**

```
COMPLETING 状态:
  outputController.ValidateOutput(raw, schema)
    │
    ├── 通过 → COMPLETE
    │
    └── 失败 (retryCount < AutoRetry)
          │
          ▼
        构造 StructuredValidationError
          │
          ├── error.FormatForModel() → 结构化错误文本
          │
          ▼
        memory.Add(assistantMsg, errorFeedbackMsg)
          │  errorFeedbackMsg.Content = error.FormatForModel()
          │  errorFeedbackMsg.Role = model.RoleUser
          │
          ▼
        回到 PLANNING (模型基于结构化反馈修正输出)
```

**结构化错误输出示例**

```json
{
  "message": "输出格式校验失败: 3 个字段不符合预期",
  "violations": [
    {"field": "result.items[0].name", "rule": "required", "message": "缺少必填字段 name"},
    {"field": "result.status", "rule": "enum", "expected": "success|failed", "actual": "ok", "message": "status 值不在允许范围内"},
    {"field": "result.count", "rule": "type", "expected": "integer", "actual": "string", "message": "类型不匹配，实际为 string \"5\""}
  ],
  "hint": "请严格按照 expected_schema 输出 JSON，注意: 1) 不要遗漏 required 字段; 2) 字段类型必须匹配; 3) enum 字段只能使用允许的值",
  "expected_schema": {"type": "object", "required": ["status", "items"], "properties": {"..."}}
}
```

### 10.8 死循环检测

检测模型是否陷入死循环（连续重复调用相同工具且入参完全一致），采用**两阶段策略：先警告、再终止**。

**LoopDetector**

```go
// agent/loop_detector.go

const DefaultLoopThreshold = 3 // 默认连续相同调用阈值

type LoopDetector struct {
    threshold int           // 可配置阈值
    history   []toolCallSig // 历史工具调用签名
    warned    bool          // 是否已发出警告
}

type toolCallSig struct {
    ToolName  string
    ParamHash string // 入参 JSON 的 SHA256 hash
}

func NewLoopDetector(threshold int) *LoopDetector {
    if threshold <= 0 {
        threshold = DefaultLoopThreshold
    }
    return &LoopDetector{threshold: threshold}
}

// Record 记录一次工具调用，返回检测结果
func (d *LoopDetector) Record(toolName string, params map[string]any) LoopStatus {
    sig := toolCallSig{
        ToolName:  toolName,
        ParamHash: hashParams(params),
    }
    d.history = append(d.history, sig)

    if len(d.history) < d.threshold {
        return LoopNormal
    }

    // 检查最近 threshold 次调用是否全部相同
    recent := d.history[len(d.history)-d.threshold:]
    allSame := true
    for _, s := range recent {
        if s != recent[0] {
            allSame = false
            break
        }
    }

    if !allSame {
        d.warned = false // 出现不同调用，重置警告状态
        return LoopNormal
    }

    if !d.warned {
        d.warned = true
        return LoopWarning  // 首次达到阈值 → 警告
    }
    return LoopTerminate    // 警告后仍重复 → 终止
}

type LoopStatus int
const (
    LoopNormal    LoopStatus = iota // 正常
    LoopWarning                      // 达到阈值，警告模型
    LoopTerminate                    // 警告后仍重复，终止执行
)
```

**两阶段策略**

```
Phase 1 — 警告 (LoopWarning):
  连续 N 次 (默认 3) 相同工具调用后，在 tool_result 中注入提示:

  ┌──────────────────────────────────────────────────────────────────────┐
  │ <loop_detection_warning>                                            │
  │   You have called tool "{tool_name}" {N} times consecutively with  │
  │   identical parameters. Please reconsider your approach:            │
  │   1. Check if previous results already satisfy the requirement      │
  │   2. Consider using a different tool or modifying the parameters    │
  │   3. If the task is complete, output your final answer directly     │
  │   WARNING: Calling the same tool with the same parameters again     │
  │   will terminate execution.                                         │
  │ </loop_detection_warning>                                           │
  │                                                                     │
  │ {original tool_result}                                              │
  └──────────────────────────────────────────────────────────────────────┘

Phase 2 — 终止 (LoopTerminate):
  警告后模型仍调用相同工具 + 相同入参:
    → 停止执行
    → 返回 AgentResult{Error: ErrLoopDetected}
    → hook.OnEvent(EventError, payload: LoopDetectionPayload)
```

**AgentLoop 集成**

```go
// agent/loop.go — EXECUTING 状态

case StateExecuting:
    status := a.loopDetector.Record(action.ToolName, action.ToolInput)

    switch status {
    case LoopTerminate:
        return AgentResult{Error: ErrLoopDetected}

    case LoopWarning:
        result := executor.Execute(ctx, action, registry)
        result.Output = injectLoopWarning(result.Output, action.ToolName, a.loopDetector.threshold)

    default:
        result := executor.Execute(ctx, action, registry)
    }
```

### 10.9 动态上下文压缩

Agent 长时间运行时，上下文持续增长会导致 token 消耗过大、超出模型上下文窗口、信息检索效率降低。框架提供**四层递进式上下文压缩策略**，参考 OpenCode 的上下文管理实践。

```
上下文压缩策略 (按优先级依次应用):

  ┌─────────────────────────────────────────────────────────┐
  │ L1: 剔除连续失败调用                                       │
  │     同一工具连续失败 → 仅保留最后一次 tool_call + tool_result │
  ├─────────────────────────────────────────────────────────┤
  │ L2: 工具调用结果裁剪                                       │
  │     单条 tool_result 超长 → 截断，完整内容持久化到存储引擎    │
  ├─────────────────────────────────────────────────────────┤
  │ L3: 缩减陈旧 tool_result                                  │
  │     每次 tool call 后检测 → 仅保留最近 40k tokens 的结果     │
  │     过期结果替换为 [Old tool result content cleared]         │
  ├─────────────────────────────────────────────────────────┤
  │ L4: 智能压缩 (Summarization Agent)                        │
  │     总上下文超过模型窗口 90% → 触发压缩 Agent 生成摘要        │
  └─────────────────────────────────────────────────────────┘
```

#### 10.9.1 剔除连续失败调用

同一工具连续调用失败时（如参数错误、权限不足），中间的失败记录对模型后续决策价值低，仅保留最后一次失败的上下文。

```go
// memory/compressor.go

// PruneConsecutiveFailures 剔除同一工具连续失败的中间调用
// 只保留最后一次 tool_call (assistant message) + tool_result (tool message)
func PruneConsecutiveFailures(messages []model.ChatMessage) []model.ChatMessage {
    // 遍历 messages，检测连续的 tool_call + error tool_result 模式:
    //   - 相邻的 (assistant tool_call, tool error_result) 对
    //   - 同一 toolName 连续出现
    //   - 仅保留最后一对，移除前面的冗余对
}
```

**示例**

```
压缩前:
  assistant: tool_call("shell", {"cmd": "ls /nonexist"})    ← 移除
  tool: "error: No such file or directory"                   ← 移除
  assistant: tool_call("shell", {"cmd": "ls /nonexist"})    ← 移除
  tool: "error: No such file or directory"                   ← 移除
  assistant: tool_call("shell", {"cmd": "ls /nonexist"})    ← 保留
  tool: "error: No such file or directory"                   ← 保留

压缩后:
  assistant: tool_call("shell", {"cmd": "ls /nonexist"})
  tool: "error: No such file or directory"
```

#### 10.9.2 工具调用结果裁剪

单条 tool_result 过长时（如读取大文件、Shell 命令大量输出），截断内容并将完整结果持久化到存储引擎，截断后尾部增加提示信息。

```go
// memory/compressor.go

const DefaultToolResultMaxLen = 30000 // 默认单条 tool_result 最大字符数

// TruncateToolResult 截断过长的工具调用结果
// 保留头部 80% + 尾部 20%，中间插入截断提示，完整内容持久化
func TruncateToolResult(
    ctx context.Context,
    content string,
    maxLen int,
    store ContentStore,
    callID string,
) string {
    if len(content) <= maxLen {
        return content
    }

    // 1. 完整内容持久化到存储引擎
    _ = store.Store(ctx, callID, content)

    // 2. 头尾保留策略 (参考 OpenCode 截断方式)
    headLen := maxLen * 4 / 5   // 头部保留 80%
    tailLen := maxLen - headLen // 尾部保留 20%

    truncated := content[:headLen] +
        "\n\n... [content truncated] ...\n" +
        fmt.Sprintf("[Original length: %d chars, truncated to %d chars. "+
            "Full content persisted with ID: %s. "+
            "Use tool 'fetch_full_result' with this ID to retrieve the complete content.]\n\n",
            len(content), maxLen, callID) +
        content[len(content)-tailLen:]

    return truncated
}
```

**ContentStore — 完整内容持久化接口**

```go
// memory/content_store.go

// ContentStore 用于持久化被截断的工具调用完整结果
type ContentStore interface {
    Store(ctx context.Context, key string, content string) error
    Load(ctx context.Context, key string) (string, error)
}
```

**内置 ContentStore 实现**

```
        ContentStore (interface)
              │
       ┌──────┼──────┬──────┐
       ▼      ▼      ▼      ▼
   InMemory  File   Redis  Custom  ← 实现 Store()/Load() 即可
```

```go
// 默认: 内存存储 (适用于单次运行)
type InMemoryContentStore struct {
    data sync.Map
}

func (s *InMemoryContentStore) Store(ctx context.Context, key, content string) error {
    s.data.Store(key, content)
    return nil
}

func (s *InMemoryContentStore) Load(ctx context.Context, key string) (string, error) {
    v, ok := s.data.Load(key)
    if !ok {
        return "", fmt.Errorf("content %q not found", key)
    }
    return v.(string), nil
}

// 可选: 文件存储
type FileContentStore struct {
    dir string // 存储目录
}

// 可选: Redis 存储 (适用于分布式/持久化场景)
type RedisContentStore struct {
    client *redis.Client
    ttl    time.Duration
}
```

**fetch_full_result — 内置工具: 查询完整工具调用结果**

当 tool_result 被截断时，模型可通过 `fetch_full_result` 工具按 ID 从 ContentStore 中检索完整内容。

```go
// tool/builtin/fetch_full_result.go

type FetchFullResultTool struct {
    store memory.ContentStore
}

func (t *FetchFullResultTool) Name() string        { return "fetch_full_result" }
func (t *FetchFullResultTool) Description() string {
    return "Retrieve the full content of a previously truncated tool result by its persisted ID"
}
func (t *FetchFullResultTool) Schema() *tool.SchemaProperty {
    return &tool.SchemaProperty{
        Type: "object",
        Properties: map[string]*tool.SchemaProperty{
            "id": {Type: "string", Description: "The persisted content ID from the truncation notice"},
        },
        Required: []string{"id"},
    }
}

func (t *FetchFullResultTool) Execute(ctx context.Context, input map[string]any) (string, error) {
    id := input["id"].(string)
    content, err := t.store.Load(ctx, id)
    if err != nil {
        return "", fmt.Errorf("content %q not found or expired: %w", id, err)
    }
    return content, nil
}
```

> **注**: 当 Agent 配置了 `ContentStore` 且存在 tool_result 截断行为时，`fetch_full_result` 工具自动注册到 ToolRegistry。返回的完整内容同样受 L2 截断保护（避免再次撑爆上下文），但截断阈值可适当放大（如 2x `ToolResultMaxLen`）。

#### 10.9.3 缩减陈旧 tool_result

每次 tool call 完成后，检测上下文中的历史 tool_result。仅保留最近约 40k tokens 的工具调用结果，超出部分替换为占位符（参考 OpenCode `[Old tool result content cleared]` 写法）。

```go
// memory/compressor.go

const DefaultRecentToolResultTokens = 40000 // 保留最近工具结果的 token 预算

// CompactStaleToolResults 缩减陈旧的工具调用结果
// 从最新 tool_result 向前累计 token，超出 budget 的旧 tool_result 替换为占位符
func CompactStaleToolResults(messages []model.ChatMessage, tokenBudget int) []model.ChatMessage {
    result := make([]model.ChatMessage, len(messages))
    copy(result, messages)

    var tokenCount int
    for i := len(result) - 1; i >= 0; i-- {
        if result[i].Role != model.RoleTool {
            continue
        }
        // 已被清理的不重复计算
        if result[i].Content == "[Old tool result content cleared]" {
            continue
        }
        msgTokens := estimateTokens(result[i].Content)
        tokenCount += msgTokens
        if tokenCount > tokenBudget {
            result[i].Content = "[Old tool result content cleared]"
        }
    }
    return result
}
```

**示例**

```
压缩前 (假设 budget = 40k tokens):
  tool: [read_file result: 8k tokens]     ← 超出 budget → 替换
  tool: [shell result: 5k tokens]          ← 超出 budget → 替换
  tool: [search result: 15k tokens]        ← 在 budget 内 → 保留
  tool: [read_file result: 12k tokens]     ← 在 budget 内 → 保留
  tool: [shell result: 10k tokens]         ← 最新, 在 budget 内 → 保留

压缩后:
  tool: "[Old tool result content cleared]"
  tool: "[Old tool result content cleared]"
  tool: [search result: 15k tokens]
  tool: [read_file result: 12k tokens]
  tool: [shell result: 10k tokens]
```

#### 10.9.4 智能压缩 (Summarization Agent)

当总上下文长度超过模型上下文窗口的指定比例（默认 90%）时，自动触发独立的**压缩 Agent** 对历史上下文进行智能摘要。创建 Agent 时可自定义压缩 Agent 的模型配置、提示词和摘要结构，否则使用主 Agent 相同的模型配置 + 默认提示词 + 默认摘要结构（参考 OpenCode）。

**压缩 Agent 配置**

```go
// agent/option.go

type CompressAgentConfig struct {
    Model           model.ChatModel // 压缩 agent 使用的模型 (nil → 使用主 agent 模型)
    Prompt          string          // 自定义压缩提示词 (空 → 使用默认提示词)
    SummarySchema   *SummarySchema  // 自定义摘要结构 (nil → 使用默认结构)
    MaxContextRatio float64         // 触发压缩的上下文占用比例 (默认 0.9)
}
```

**默认压缩提示词 (参考 OpenCode)**

```go
// memory/compressor.go

const DefaultCompressPrompt = `You are a conversation context compression assistant. Your task is to compress the conversation history below into a structured summary while preserving all critical information.

Requirements:
1. Preserve the user's core goals and specific instructions exactly as stated
2. Retain all important findings, conclusions, and error messages
3. Record completed steps with their outcomes (success/failure/partial)
4. Keep all key file paths, code references, URLs, and identifiers
5. Do not omit any unfinished tasks, pending decisions, or open questions
6. Use concise language — eliminate redundancy but never lose meaning

Output the summary in the following structure (in the original conversation language):`
```

**默认摘要结构 (参考 OpenCode)**

```go
// memory/compressor.go

type ConversationSummary struct {
    // 目标: 用户的核心目标和需求描述
    Goals string `json:"goals"`

    // 指令: 用户给出的关键指令、约束和偏好
    Instructions string `json:"instructions"`

    // 发现: 重要的发现、结论、错误信息、技术决策
    Findings string `json:"findings"`

    // 进度: 已完成的步骤和当前进展 + 待完成事项清单
    Progress string `json:"progress"`

    // 参考资料: 关键文件路径、代码片段、URL 等引用
    ReferenceFiles string `json:"reference_files"`
}

const DefaultSummaryTemplate = `## Conversation Summary

### Goals
{{.Goals}}

### Instructions
{{.Instructions}}

### Findings
{{.Findings}}

### Progress
{{.Progress}}

### Reference Files
{{.ReferenceFiles}}`
```

**压缩流程**

```
AgentLoop 每轮 planner.Plan() 调用前:
  │
  ├──► 计算当前上下文 token 数
  │         │
  │         ▼
  │    totalTokens / modelMaxTokens > MaxContextRatio (默认 0.9)?
  │         │
  │         ├── No  → 继续正常执行
  │         │
  │         └── Yes → 触发智能压缩
  │                     │
  │                     ▼
  │              ┌────────────────────────────────────────────┐
  │              │ Compress Agent (独立 LLM 调用)               │
  │              │                                            │
  │              │ Input:                                      │
  │              │   system: CompressPrompt (默认或自定义)       │
  │              │   user: 当前 messages 序列化                  │
  │              │                                            │
  │              │ Model:                                      │
  │              │   config.CompressAgent.Model ?? 主 Agent 模型 │
  │              │                                            │
  │              │ Output:                                     │
  │              │   ConversationSummary (结构化摘要)            │
  │              └─────────────────┬──────────────────────────┘
  │                                │
  │                                ▼
  │              重建上下文:
  │              ┌────────────────────────────────────────────┐
  │              │ messages = [                                │
  │              │   {Role: system, Content: 原 system prompt}  │
  │              │   {Role: user, Content: 摘要文本}            │
  │              │   ... 最近 N 条未压缩消息 (保留完整上下文) ... │
  │              │ ]                                           │
  │              └────────────────────────────────────────────┘
  │
  └──► 继续 AgentLoop → planner.Plan(ctx, state)
```

**智能压缩实现**

```go
// memory/compressor.go

type ContextCompressor struct {
    config CompressAgentConfig
    model  model.ChatModel
}

func NewContextCompressor(mainModel model.ChatModel, config CompressAgentConfig) *ContextCompressor {
    m := config.Model
    if m == nil {
        m = mainModel // 未配置则使用主 agent 模型
    }
    return &ContextCompressor{config: config, model: m}
}

// ShouldCompress 判断是否需要触发压缩
func (c *ContextCompressor) ShouldCompress(messages []model.ChatMessage, modelMaxTokens int) bool {
    currentTokens := estimateMessagesTokens(messages)
    ratio := float64(currentTokens) / float64(modelMaxTokens)
    maxRatio := c.config.MaxContextRatio
    if maxRatio <= 0 {
        maxRatio = 0.9
    }
    return ratio > maxRatio
}

// Compress 执行智能压缩，返回压缩后的消息序列
func (c *ContextCompressor) Compress(ctx context.Context, messages []model.ChatMessage) ([]model.ChatMessage, error) {
    prompt := c.config.Prompt
    if prompt == "" {
        prompt = DefaultCompressPrompt
    }

    // 1. 序列化历史 messages 为压缩 agent 输入
    conversationText := serializeMessages(messages)

    // 2. 调用压缩模型
    resp, err := c.model.Generate(ctx, []model.ChatMessage{
        {Role: model.RoleSystem, Content: prompt},
        {Role: model.RoleUser, Content: conversationText},
    })
    if err != nil {
        return messages, err // 压缩失败则保留原上下文，不阻断主流程
    }

    // 3. 解析摘要
    summary := parseSummary(resp.Message.Content)
    summaryText := renderSummaryTemplate(summary)

    // 4. 重建上下文: 原 system + 摘要 (作为 user message) + 最近未压缩消息
    compressed := rebuildMessages(messages, summaryText)
    return compressed, nil
}
```

#### 10.9.5 压缩策略调用时机

```
每次 tool call 完成后，依次应用 L1 ~ L3:

  executor.Execute() → ExecResult
    │
    ├──► L2: TruncateToolResult(result.Output, maxLen, store, callID)
    │         → 截断过长结果 (写入 Memory 前)
    │
    ├──► memory.Add(toolCallMsg, toolResultMsg)
    │
    ├──► L1: PruneConsecutiveFailures(messages)
    │         → 剔除连续失败的冗余记录
    │
    └──► L3: CompactStaleToolResults(messages, tokenBudget)
              → 淘汰陈旧工具结果

每轮 Planner 调用前，检测 L4:

  planner.Plan() 调用前
    │
    ├──► compressor.ShouldCompress(messages, modelMaxTokens)?
    │         │
    │         ├── Yes → compressor.Compress(ctx, messages) → 更新 messages
    │         └── No  → 保持不变
    │
    └──► planner.Plan(ctx, state)
```

#### 10.9.6 配置项

```go
// agent/option.go — 新增 functional options

func WithLoopDetectionThreshold(n int) Option        // 死循环检测阈值 (默认: 3)
func WithToolResultMaxLen(n int) Option              // 单条 tool_result 最大字符数 (默认: 30000)
func WithContentStore(store ContentStore) Option     // 截断内容持久化存储 (默认: InMemoryContentStore)
func WithRecentToolResultTokens(n int) Option        // 保留最近工具结果的 token 预算 (默认: 40000)
func WithCompressAgent(config CompressAgentConfig) Option // 智能压缩 Agent 配置
func WithMaxContextRatio(ratio float64) Option       // 触发智能压缩的上下文占比 (默认: 0.9)
```

**使用示例**

```go
// 最简: 全部使用默认值 (死循环检测阈值 3, 截断 30000 字符, 保留 40k tokens, 90% 触发压缩)
a := agent.New(
    agent.WithModel(m),
    agent.WithTools(readTool, writeTool, shellTool),
)

// 自定义配置
a := agent.New(
    agent.WithModel(m),
    agent.WithTools(readTool, writeTool, shellTool),
    agent.WithLoopDetectionThreshold(5),                       // 放宽死循环检测阈值
    agent.WithToolResultMaxLen(50000),                         // 放宽截断阈值
    agent.WithContentStore(memory.NewRedisContentStore(rdb)),  // Redis 持久化截断内容
    agent.WithRecentToolResultTokens(60000),                   // 保留更多最近结果
    agent.WithMaxContextRatio(0.85),                           // 更早触发压缩
    agent.WithCompressAgent(agent.CompressAgentConfig{
        Model:  openai.New("gpt-4o-mini", apiKey),  // 压缩用小模型降本
        Prompt: customCompressPrompt,                // 自定义压缩提示词
    }),
)
```

---

## 十一、与现有代码的映射关系

| 现有代码 | 设计中对应模块 | 改动说明 |
|---------|-------------|---------|
| `core/tool/define.go` | `tool/tool.go` + `tool/schema.go` | 拆分 Tool interface 和 Schema 定义 |
| `core/tool/helper.go` | `tool/schema.go` | 合并至 schema 文件 |
| `core/tool/regostry.go` | `tool/registry.go` | 修正拼写，升级为支持 Tool interface |
| `core/tool/registry_test.go` | `tool/registry_test.go` | 适配新接口 |
| `core/client/root_client.go` | `model/provider/openai/openai.go` + `model/wrap.go` | 包装为 ChatModel 实现 + 快捷适配器 |
| `core/client/define.go` | `model/message.go` | 统一消息类型 |
| `core/client/stream_response.go` | `model/model.go` (StreamIterator) | 合入模型层 |
| `core/hook/outputhook/` | `hook/outputhook/` | 保留，增加与 Agent 的集成 |
| `core/agent/agent.go` | `agent/agent.go` + `agent/session.go` | 重构: Agent 共享单例 + Session 会话级隔离 |
| `core/agent/planner.go` | `planner/react.go` | 修正 package，实现 ReAct |
| `core/memory/memory.go` | `memory/memory.go` + `buffer.go` + `store.go` | 两层架构: 语义层 Memory + 数据引擎层 MessageStore (InMemory/Redis/SQL) |
| `core/runtime/runtime.go` | `runtime/runtime.go` + `local.go` + `e2b.go` | 实现 Runtime interface + E2B 云沙箱 |
| `core/interrupter/hitl.go` | `interrupter/hitl.go` | 实现 HITL 完整逻辑 |
| _(新增)_ | `agent/session.go` | Session 会话级状态 (Memory/LoopDetector/ContentStore) |
| _(新增)_ | `agent/loop.go` | AgentLoop 状态机 (在 Session 上下文中运行) |
| _(新增)_ | `executor/executor.go` | Executor 实现 |
| _(新增)_ | `planner/mode.go` | ExecutionMode 枚举 |
| _(新增)_ | `planner/plan_and_solve.go` | PlanAndSolve 策略 (含 ActionPlan / Replan) |
| _(新增)_ | `evaluator/evaluator.go` | Evaluator interface (可选, 动态开关) |
| _(新增)_ | `evaluator/noop.go` | NoopEvaluator (关闭评估时使用) |
| _(新增)_ | `evaluator/llm_judge.go` | LLM 评估器 |
| _(新增)_ | `evaluator/rule_based.go` | 规则评估器 |
| _(新增)_ | `evaluator/composite.go` | 组合评估器 |
| _(新增)_ | `tool/builtin/` | 内置工具 |
| _(新增)_ | `tool/mcp/` | MCP 工具 |
| _(新增)_ | `prompt/builder.go` | PromptBuilder — system prompt 统一组装 |
| _(新增)_ | `prompt/defaults.go` | DefaultReActSystemPrompt / DefaultPlanAndSolveSystemPrompt |
| _(新增)_ | `prompt/template.go` | 模板引擎 |
| _(新增)_ | `prompt/catalog.go` | BuildAlwaysOnRules/Skills + BuildRuleCatalog/SkillCatalog |
| _(新增)_ | `rule/rule.go` | Rule interface (含 AlwaysApply) + FileRule |
| _(新增)_ | `rule/registry.go` | RuleRegistry (增删查, sync.RWMutex 安全) |
| _(新增)_ | `rule/loader.go` | LoadPath/LoadDir/FromFile + YAML frontmatter 解析 |
| _(新增)_ | `retriever/retriever.go` | Retriever interface + Document |
| _(新增)_ | `retriever/semantic.go` | SemanticRetriever (Embedding + VectorStore) |
| _(新增)_ | `retriever/keyword.go` | KeywordRetriever (BM25) |
| _(新增)_ | `retriever/hybrid.go` | HybridRetriever (混合 + RRF 合并) |
| _(新增)_ | `retriever/rag_tool.go` | RAGTool (Retriever → Tool 封装) |
| _(新增)_ | `retriever/embedder/` | Embedder interface + OpenAI 实现 |
| _(新增)_ | `retriever/vectorstore/` | VectorStore interface + Chroma 实现 |
| _(新增)_ | `tool/builtin/rule_view.go` | rule_view 内置工具 (按需加载 Rule 正文) |
| _(新增)_ | `tool/builtin/skill_call.go` | skill_call 内置工具 (触发 Skill 执行) |
| _(新增)_ | `skill/skill.go` | Skill interface (含 AlwaysApply) + DirSkill + Option |
| _(新增)_ | `skill/registry.go` | SkillRegistry |
| _(新增)_ | `skill/loader.go` | LoadPath/LoadDir/FromDir/FromFile + YAML frontmatter 解析 |
| _(新增)_ | `skill/context.go` | SkillContext 独立上下文 + mini loop |
| _(新增)_ | `skill/view_tool.go` | skill_view (读取 Skill 目录文件) |
| _(新增)_ | `runtime/e2b.go` | E2B 云沙箱 (REST API 控制面 + envd 数据面) |
| _(新增)_ | `agent/loop_detector.go` | 死循环检测器 (LoopDetector, 两阶段策略) |
| _(新增)_ | `memory/store.go` | MessageStore 接口 (Memory 实现的内部组合参数, Agent 不感知) |
| _(新增)_ | `memory/store_inmem.go` | 默认 InMemoryMessageStore (Redis/SQL/Custom 由调用方按需实现接口) |
| _(新增)_ | `memory/compressor.go` | 动态上下文压缩 (L1~L4 四层策略 + ContextCompressor) |
| _(新增)_ | `memory/content_store.go` + `content_store_{inmem,redis,file}.go` | ContentStore 接口 + 多引擎实现 |
| _(新增)_ | `tool/builtin/fetch_full_result.go` | fetch_full_result 内置工具 (查询截断内容完整结果) |
| _(新增)_ | `errors/errors.go` | 统一错误 + StructuredValidationError |
| _(新增)_ | `examples/` | 使用示例 |

---

## 十二、技术选型总结

| 关注点 | 选型 | 理由 |
|-------|------|------|
| 接口设计 | 小接口 + functional options | Go 惯用，低耦合 |
| 会话管理 | Agent 共享单例 + Session 会话级隔离 + MemoryFactory | 支持 DI 注入, 多会话并发安全 |
| 状态管理 | 显式状态机 (enum + switch) | 可调试、可序列化 |
| 并发 | context.Context + errgroup + semaphore + sync.Map (sessions) | 标准 Go 并发原语 |
| 流式 | channel-based iterator | 已有实现验证，简洁 |
| 配置 | functional options (非 YAML/JSON) + WithRulePaths/WithSkillPaths | 类型安全，IDE 友好，支持路径批量注入 |
| Rule/Skill 格式 | Cursor Rule 风格 YAML Frontmatter (description + alwaysApply) | 与 Cursor 生态对齐，无额外 YAML 依赖 |
| 渐进式披露 | alwaysApply=true 注入 + alwaysApply=false catalog + rule_view/skill_call | 灵活分层：核心规则始终可用，长文档按需加载 |
| 系统提示词 | 模式默认 (ReAct/PlanAndSolve) + 用户自定义覆盖 | 开箱即用，参考 Claude Code 风格，XML 结构化指令 |
| 输出解析 | 保留 hujson + validator | 已验证有效 |
| 日志/观测 | Hook 链 (预留 OTel) | 可插拔，不侵入 |
| HITL | Interrupter interface + Checkpoint | 借鉴 Eino 模式 |
| 工具协议 | Tool interface + MCP client | 本地+远程统一 |
| 沙箱运行时 | Runtime interface + 多后端 (Local/Docker/E2B) | 标准接口 + 云原生沙箱 |
| 错误处理 | sentinel errors + wrap + StructuredValidationError | Go 标准模式 + 结构化反馈 |
