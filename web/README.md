# Nagi Web 控制台

Nagi 管理后台前端：Vite + React + TypeScript + Tailwind + shadcn/ui + TanStack Router/Query。
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
NAGI_ADMIN_TOKEN=<server.admin-token> pnpm dev

# 方式二：写入 web/.env.local（已被 .gitignore 忽略）
cp .env.example .env.local   # 再编辑 NAGI_ADMIN_TOKEN
```

令牌只在 Vite dev server 侧写入 `Authorization` 头，**不会**进入浏览器 bundle、
`localStorage` 或 URL。指向其他后端地址时设置 `NAGI_API_TARGET`。

开发服务器默认只接受 `localhost` 与回环 IP 的 Host 头，因为代理会注入令牌，
放开 Host 校验会让 DNS rebinding 页面借到管理员权限。确需通过隧道域名访问时，
用 `NAGI_DEV_ALLOWED_HOSTS=dev.example.com` 追加白名单，不要整体放开。

生产部署时由同源反向代理（Caddy/Nginx）托管 `dist/` 并代理 `/api`、`/livez`、`/readyz`；
SPA 回退到 `index.html`，但 `/api/` 的 404 不能被改写成 200 HTML。

## 控制台入口

- 在「联系人」管理当前账号的好友、群、成员与作用域策略。旧的 `/scopes` 路由会转到联系人页面。
- 在「知识与记忆」审核或修改记忆、查看角色提示词、设置抽取参数和查看任务。在「意图识别」查看参与判断与投递记录。
- 在「账号连接」查看连接与同步状态，在「系统配置」管理模型与限额。完整页面入口见[路由定义](src/app/router.tsx)。

## 接口契约要点

- 响应信封统一为 `{ code, message, data }`；非 2xx 仅使用 `message` 作为安全文案。
- 目录列表（`/contacts`、`/groups`、`/groups/{id}/members`）序列化为 PascalCase：
  `Items/Total/Page/Limit`、`UserID/GroupID/Nickname/...`。
- 记忆列表是例外：分页键为小写 `items/total/page/limit`，条目字段仍是 PascalCase。
- 写入接口（作用域 PUT、记忆 POST/PATCH）只接受 snake_case 请求体；作用域 PUT 是整份覆盖。
- `/api/v1/metrics` 是聚合计数（`message_jobs`、`intent_decisions_24h`、`messages_24h`、`memories`），
  不是时间序列，因此界面不做趋势曲线；支持 `?bot_id=` 按账号过滤。
- 数据库未配置时依赖库的路由不注册，Go 返回纯文本 `404 page not found`；
  若配置了 admin-token，未注册路由会先被 Auth 拦截成 401。两者都需要与「资源不存在」区分。
- `/api/v1/accounts` 返回 `{ connected, db_configured, accounts[] }`，每个账号含 `bot_id`（未识别时省略）、
  `nickname`、`endpoint`、`connected`、`self_id_known`、`rejected` 等字段；不依赖数据库。
- 账号级读取都带 `bot_id`：多账号未指定时返回 `400`，账号身份未识别时返回 `409`。
- `POST /api/v1/accounts/{bot_id}/sync` 是唯一目录同步入口，且要求该账号在线。
- `/readyz` 的 503 只表示没有任何 OneBot 连接就绪，进程存活只看 `/livez`。
- 作用域 `GET`/`PUT` 都需要 `bot_id`；已保存策略的 `ScopeType`/`ScopeID` 由仓储补齐。
- 群成员 `refresh=true` 按显式 `bot_id` 执行；账号未同步过目录时返回 `409`，界面提示先同步。
- 记忆创建遇到完全相同内容时返回 `duplicate:true` 且**不会**激活，即使请求带 `approve`。
- 记忆 `PATCH` 支持修订内容，`status=active` 用于审批。传入 `status=deleted` 或调用 `DELETE` 会永久删除条目。
- 工具白名单已从数据库与界面移除：工具能力由 Agent 运行时注册，不再由自由文本配置。

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
