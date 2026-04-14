Single Agent
1、ReAct,Plan
2、HITL
3、Base Tool（Read、Write）、Function Tool（register）、RAG Tool、MCP Tool、Skill、RunCode Tool
4、Shabox、Diff worktree


```go
type Agent struct {
    Planner    Planner        // ReAct 或 HTLP 逻辑
    Memory     Memory         // 上下文管理
    Tools      *ToolRegistry  // 各种 Tool 的注册表
    Runtime    *Runtime       // Sandbox / RunCode 环境
    Interrupter HITLHandler   // 人机协同挂钩
    OutputController OutputController // 输出控制器
}
```