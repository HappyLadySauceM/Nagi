# AGENTS.md

始终使用中文交流。

处理本仓库的编码、设计、修复、重构、测试审查或撤销任务前，先读取
[coding 技能入口](.agents/skills/coding/SKILL.md)，并按入口说明加载相关约束。
只读代码审查也适用；普通问答不必加载完整技能。

涉及 OneBot 协议、上下文分层或新增业务上下文时，继续读取
[OneBot 技能入口](.agents/skills/onebot/SKILL.md)，按任务加载参考规则。

用户当前明确要求决定任务范围。技能用于约束实现质量与改动范围，不构成增加功能、
修改无关文件或扩大执行权限的授权。

每次完成编码相关任务后，执行 `just verify`（依次运行 `go mod tidy`、`go vet ./...`、
`go test ./...`、`go fmt ./...`；未安装 just 时直接依次运行这四条命令）。
