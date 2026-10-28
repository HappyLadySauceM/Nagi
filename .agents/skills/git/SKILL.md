---
name: git
description: 在 Nagi 仓库提交、改写提交（amend/rebase/squash/cherry-pick）、创建或删除分支、发起或合并 PR 时，约束提交信息格式（Conventional Commits，基于 Angular 约定）、提交粒度与分支命名。普通问答和只读查看历史不需要使用。
---

# git

提交信息、分支名与 PR 标题共用一套 Conventional Commits（基于 Angular 约定），
让历史可按 type/scope 过滤，人工提交与 CI 自动化提交保持同一形态。

## 提交信息格式

```text
<type>(<scope>): <description>

[可选正文]

[可选 footer]
```

- `type` 必填，只用下方词表里的小写值，不自造。
- `scope` 可省略；有值时用小写与连字符，写受影响模块，不写文件路径。
- `description` 用祈使句或动宾短语，不以句号结尾，控制在 50 字符内（硬上限 72）。
- 标题与正文之间空一行；正文说明「为什么」，不复述 diff，每行不超过 72 字符。
- 破坏性变更在 type/scope 后加 `!`，并在 footer 写 `BREAKING CHANGE: <影响>`；
  关联 issue 用 footer `Refs: #123`，关闭用 `Closes: #123`。

### type 词表

| type | 用途 |
| --- | --- |
| feat | 新增用户可见能力 |
| fix | 修复缺陷 |
| docs | 只改文档或说明性注释 |
| style | 只改格式，不改行为 |
| refactor | 重构，不新增能力也不修缺陷 |
| perf | 性能优化 |
| test | 新增或调整测试 |
| build | 构建脚本、依赖、镜像 |
| ci | CI/CD 工作流与仓库自动化 |
| chore | 配置、脚本、版本号、清理等杂务 |
| revert | 回滚既有提交，正文写被回滚的 commit |

### scope（本仓库）

- 领域模块：`agent`、`bot`、`onebot`、`conversation`、`intent`、`memory`、`scope`、
  `directory`、`plugins`、`echo`、`audit`、`observability`。
- 技术层：`api`、`web`、`db`、`test`、`ci`、`docs`、`scripts`。
- 一个提交跨多个模块时，scope 取最主要的一个，其余在正文说明。

## 提交粒度

- 一个提交只做一件事，能独立 review 与回滚；大改动按可编译、可回滚的最小单元拆分。
- 格式化、重命名与行为改动分开提交，避免 reviewer 在噪声里找逻辑。
- 不提交调试输出、临时文件、生成物、密钥与本地配置；不提交 WIP 中间态。
- 功能改动提交前先按 [review 技能](../review/SKILL.md) 完成审查与功能测试。

## 分支与 PR

- 分支名用小写 kebab-case，前缀与 type 对齐：`feat/`、`fix/`、`ci/`、`docs/`、`chore/`。
- PR 标题复用同一条提交信息格式；正文写清动机、影响面与验证方式。
- 合并前保持分支与目标分支同步，优先 rebase，避免无意义的 merge 提交。

## 自动化提交

- 自动化产物的显示文本与它产出的提交共用同一套格式：workflow 的 `name:`、Image Updater
  的自动提交、PR 标题都写成 `<type>(<scope>): <description>`，前缀与该自动化实际做的事一致。
- `.github/workflows/ghcr-retention.yml`（显示名
  `chore(cleanup): Update from GHCR cleanup automation`）只调 GitHub API 删除旧 package
  版本，不产生提交；不要为它补提交动作。
- 清理类自动化统一 `chore(cleanup): ...`，镜像同步与写回统一 `chore(image): ...`。
- 修改构建与清理链路时按 `ci:` 或 `chore(ci):` 记录，例如
  `ci: 镜像改用 7 位短 SHA tag`、`feat(ci): 新增 ghcr 包版本保留策略`。

## 反例

```text
update code                  # 缺 type/scope，无法按类型检索
fix: 修复 bug                # 描述空洞，正文没有触发条件
chore: update nagi image     # 缺 scope，描述不是祈使句
docs: 修改 README.md         # 描述写文件名，应写改了什么
```

## 完成条件

提交信息符合 `<type>(<scope>): <description>`、单个提交可独立回滚、PR 标题与之一致时，
即可结束本次提交操作。不要重写已推送的他人提交，也不为满足规范扩大改动范围。
