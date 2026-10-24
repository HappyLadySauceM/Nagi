# QQBot Web 控制台

QQBot 管理后台前端：Vite + React + TypeScript + Tailwind + shadcn/ui + TanStack Router/Query。
构建产物是静态文件，Go 侧不引入 Node 运行时。

## 命令

```bash
pnpm install --frozen-lockfile   # 安装依赖（提交了 pnpm-lock.yaml）
pnpm dev                         # 开发服务器 127.0.0.1:5173
pnpm lint                        # ESLint
pnpm typecheck                   # tsc --noEmit
pnpm test                        # Vitest（API 边界）
pnpm build                       # 类型检查 + 生产构建到 dist/
pnpm preview                     # 预览 dist/
```

## 连接后端

开发服务器把 `/api`、`/livez`、`/readyz` 代理到 `http://127.0.0.1:5080`。

后端 `server.admin-token` 为空时无需额外配置；已启用时二选一：

```bash
# 方式一：仅本次启动注入
QQBOT_ADMIN_TOKEN=<server.admin-token> pnpm dev

# 方式二：写入 web/.env.local（已被 .gitignore 忽略）
cp .env.example .env.local   # 再编辑 QQBOT_ADMIN_TOKEN
```

令牌只在 Vite dev server 侧写入 `Authorization` 头，**不会**进入浏览器 bundle、
`localStorage` 或 URL。指向其他后端地址时设置 `QQBOT_API_TARGET`。

开发服务器默认只接受 `localhost` 与回环 IP 的 Host 头，因为代理会注入令牌，
放开 Host 校验会让 DNS rebinding 页面借到管理员权限。确需通过隧道域名访问时，
用 `QQBOT_DEV_ALLOWED_HOSTS=dev.example.com` 追加白名单，不要整体放开。

生产部署时由同源反向代理（Caddy/Nginx）托管 `dist/` 并代理 `/api`、`/livez`、`/readyz`；
SPA 回退到 `index.html`，但 `/api/` 的 404 不能被改写成 200 HTML。

## 已实现范围（W0 + W1）

- 应用外壳：可折叠侧栏、面包屑顶栏、全局状态胶囊、移动端抽屉、深/浅双主题（默认深色）。
- 概览 `/dashboard`：四张 KPI 卡、连接与运行卡、任务状态/意图原因/记忆状态分布。
- QQ 账号 `/accounts`：账号与连接、运行时快照、处理器清单、复制账号 ID。
- 数据可信：真实 0 显示 0；请求失败显示「不可获取」并给出可操作原因，不伪装成空数据。
- 无数据库时账号页仍可用：`/api/v1/accounts` 不依赖数据库，旧后端自动回退到 `/api/v1/status`。
- 首屏分层加载：`/accounts` 按需加载，框架/路由/动画运行时拆分为可缓存 chunk。

未实现（后续阶段）：联系人/群/成员、作用域编辑、长期记忆、会话中心、Jev 决策明细、
远程登录与命令面板。主导航不提供这些假入口。

## 接口契约要点

以下结论已对照 Go handler 实测，后续阶段（W2–W4）请沿用：

- 响应信封统一为 `{ code, message, data }`；非 2xx 仅使用 `message` 作为安全文案。
- 目录列表（`/contacts`、`/groups`、`/groups/{id}/members`）序列化为 PascalCase：
  `Items/Total/Page/Limit`、`UserID/GroupID/Nickname/...`。
- 记忆列表是例外：分页键为小写 `items/total/page/limit`，条目字段仍是 PascalCase。
- 写入接口（作用域 PUT、记忆 POST/PATCH）只接受 snake_case 请求体；作用域 PUT 是整份覆盖。
- `/api/v1/metrics` 是聚合计数（`message_jobs`、`intent_decisions_24h`、`messages_24h`、`memories`），
  不是时间序列，因此界面不做趋势曲线。
- 数据库未配置时依赖库的路由不注册，Go 返回纯文本 `404 page not found`；
  若配置了 admin-token，未注册路由会先被 Auth 拦截成 401。两者都需要与「资源不存在」区分。
- `/api/v1/accounts` 只读 OneBot 连接，由 `system` handler 无条件注册，无数据库也可用；
  旧后端未注册该路由时，控制台回退到 `/api/v1/status` 并在界面标注来源。
- `/readyz` 的 503 只表示 OneBot 未就绪，进程存活只看 `/livez`；网络失败不能显示成机器人离线。

## 目录结构

```text
src/
├─ app/           路由、Provider、AppShell（layout）
├─ components/    ui（shadcn）、主题、状态、反馈态、Magic UI 与 Motion Primitives 组件
├─ features/      dashboard、accounts、system（查询 hooks）
├─ lib/           api（client/contracts/adapters）、format、query-keys、utils
└─ styles/        globals.css（双主题令牌）
```

## 第三方许可

- [shadcn/ui](https://ui.shadcn.com) — MIT，`src/components/ui` 为其源码的本地副本/改写。
- [Magic UI](https://magicui.design) — MIT，`number-ticker.tsx` 与 `shine-border.tsx` 基于其实现改写。
- [Motion Primitives](https://motion-primitives.com) — MIT，`animated-group.tsx` 基于其实现改写。
- [TanStack Router / Query](https://tanstack.com) — MIT。
- 布局与密度参考 `satnaing/shadcn-admin` 与 `arhamkhnz/next-shadcn-admin-dashboard`，未复制其登录、演示用户或演示数据。
