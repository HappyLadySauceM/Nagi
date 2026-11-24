# AGENTS.md

始终使用中文交流。

处理本仓库的编码、设计、修复、重构、测试审查或撤销任务前，先读取
[coding 技能入口](.agents/skills/coding/SKILL.md)，并按入口说明加载相关约束。
只读代码审查也适用；普通问答不必加载完整技能。

提交、改写提交（amend/rebase/squash）、创建分支或发起 PR 前，先读取
[git 技能入口](.agents/skills/git/SKILL.md)，按 Conventional Commits 约束提交信息与粒度。

涉及 OneBot 协议、上下文分层或新增业务上下文时，继续读取
[OneBot 技能入口](.agents/skills/onebot/SKILL.md)，按任务加载参考规则。

用户当前明确要求决定任务范围。技能用于约束实现质量与改动范围，不构成增加功能、
修改无关文件或扩大执行权限的授权。

每次完成编码相关任务后，执行 `just verify`（依次运行 `go mod tidy`、`go vet ./...`、
`go test ./...`、`go fmt ./...`；未安装 just 时直接依次运行这四条命令）。

需要本地后端和前端时执行 `just run`（后台启动两者，compose 未启动时先拉起；PID 与日志写入
`.run/`；后端默认 `127.0.0.1:5080`，前端默认 `127.0.0.1:5173`）。本次任务用 `just run` 拉起、
且不再需要时，执行 `just exit` 停止。启动前已经在跑的服务不要停。

## 功能模块完成后验证并提交

一个功能模块的实现和必要验证完成后，核对差异只包含这个模块，再按 git 技能提交到当前任务分支。
用户明确说先不要提交时，只报告检查结果。

功能改动按 [功能交付验证](.agents/skills/coding/references/testing.md#功能交付验证) 执行本地检查，
涉及页面行为时通过真实浏览器从正常入口试用。开 PR 后核对实际 CI 结果。独立审查按任务需要安排。
