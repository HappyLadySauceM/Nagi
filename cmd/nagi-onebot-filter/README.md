# 单目标 OneBot 演练过滤器

从仓库根目录执行 `go build -o /tmp/nagi-onebot-filter ./cmd/nagi-onebot-filter`。独立进程在桥接和 SnowLuma 之间建立一对正向 WebSocket。桥接填过滤器的回环地址及下游 Token，并沿用已核实的 `expect-qq`。过滤器使用独立的上游 Token 连接 SnowLuma。

配置是权限为 `0600` 的 JSON 文件。下面都是合成值。`state_dir` 应指向同一工作负载重启后仍可访问的 `0700` 专用状态目录，实际部署用受管凭据注入两个 Token。

```json
{
  "listen": "127.0.0.1:33001",
  "upstream": "ws://127.0.0.1:3001/onebot",
  "upstream_token": "synthetic-upstream-token",
  "downstream_token": "synthetic-downstream-token",
  "state_dir": "/tmp/nagi-onebot-filter-state",
  "bot_id": 1,
  "kind": "group",
  "target_id": 20,
  "sender_id": 2,
  "trigger": "synthetic one-time trigger"
}
```

一次批准对应一个新状态目录和一份完整配置。由操作人在确认配置绑定的账号、会话、触发人及本轮唯一触发文本后，将配置文件的 SHA-256 写入状态目录内权限为 `0600` 的 `armed` 文件。传给程序 `-config` 的是该配置文件路径。程序在开始监听前验证摘要并持久创建 `claimed`。缺少授权或已存在 `claimed` 时启动失败。保留原 `claimed` 作为演练证据。短时连接恢复由桥接重新拨号，过滤器沿用原额度。进程重启需有独立授权，且先完成上次投递对账。

上游 `get_login_info` 核对通过后，过滤器才转发匹配账号的生命周期和心跳，以及首条匹配全部条件的触发消息。群只接受 `normal`，私聊只接受 `friend` 且目标和发送人相同。触发文本必须完全相同。发送额度在唯一匹配触发消息的连接内生效，写出前扣减。群发送允许纯文本或桥接的“仅 @触发人 + 文本”，私聊允许纯文本。动作只转发一次，单独匹配上游 echo 后改回桥接 echo。只有明确回执才由桥接按原规则确认，发生未知投递按业务台账核查。

切换前确认旧 API 无在途任务和投递，并确认 `3001` 无旧 WS。Infra 将网络放行限于过滤器的工作负载身份，桥接只配置过滤器回环端点。若放行边界只能到 Pod，把过滤器和桥接的不同进程视为同一网络身份，并核对桥接端点仅为回环。过滤器准备完成后才启动桥接，接回前核对原端点和旧 schema。恢复时依次停桥接、停过滤器、确认 `3001` 连接归零、撤销临时放行，再启动旧 API 并确认单活。

`go test -race ./cmd/nagi-onebot-filter` 验证合成账号、启动授权、默认拒绝、并发连接、单笔动作、回执路由和重连。完整桥接验收还应核对真实进程的首帧和隔离数据库台账。
