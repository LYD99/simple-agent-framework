package core

type Agent struct {
	Planner   Planner   // ReAct 或 HTLP 逻辑
	Executor  Executor  // 执行器，负责执行 Planner 生成的计划
	Evaluator Evaluator // 评估器，负责评估执行结果并提供反馈

	Memory           Memory           // 上下文管理
	Tools            *ToolRegistry    // 各种 Tool 的注册表
	Skills           []Skill          // 各种技能
	Runtime          *Runtime         // Sandbox / RunCode 环境
	Interrupter      HITLHandler      // 人机协同挂钩
	OutputController OutputController // 输出控制器
}
