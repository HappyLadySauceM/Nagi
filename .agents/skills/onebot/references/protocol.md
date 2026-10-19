# 协议与运行时不变量

## 帧与解复用

- 入站帧二分：带 echo 的动作响应交给 ResponseSink；带 post_type 的交给 EventSink；
  两者皆无则丢弃并 Debug 记录，不 panic。
- 动作响应信封为 {status,retcode,wording,data}；retcode 0 与 1（async 已提交）
  视为成功，其余转换为 APIError；retcode=1 时 data 为 null，类型化解码容错为零值
  （application 的 decodeJSON）。
- message 字段兼容段数组与 CQ 字符串两种形式：数组走 `domain.SegmentsFromWire`，
  字符串走 `domain.ParseCQ`（`&amp;/&#91;/&#93;/&#44;` 转义反转义，key 与 value 均处理，
  畸形/未闭合码整体降级为字面文本，永不失败）；事件已知字段入类型化视图，
  其余进 Extra；notice/request/meta 通过 domain 访问器（NoticeType/RequestType/
  MetaType/Flag/OperatorID 等）读取。
- 合并转发节点 `domain.CustomNode` 的 content 以线上形状 JSON 文本内嵌于 data，
  `encodeSegments` 发送时还原为真实数组；`domain.WireSegments/SegmentsFromWire`
  是已发布语言的统一编解码助手，infrastructure 与合并转发共用，不得再造第二份。

## 传输与并发

- gorilla 不允许并发写：所有写出经 Transport.mu 串行化，写超时与 ctx 期限取较早者。
- 重连退避从 ReconnectInitial 指数增长到 ReconnectMax，成功后复位；退避带随机
  抖动，避免重连风暴。
- 拨号携带 `Authorization: Bearer <token>`；日志只输出 endpoint 主机名，绝不输出
  token、查询串或完整消息体。
- 心跳看门狗：HeartbeatTimeout > 0 时读泵按窗口刷新 SetReadDeadline，窗口内没有
  任何入站帧即断开，超时错误包装为 "heartbeat timeout after %s"；≤0 关闭。
  判定依据是"任意入站帧活跃度"而非仅 heartbeat 帧，实现更简且对无心跳实现更稳。
- 事件通道容量 eventBuffer；满时丢弃并告警，绝不阻塞读泵。

## echo 匹配

- echo 全局唯一（`qqbot-<seq>`）；pending 表注册、等待、删除须成对出现，超时与
  ctx 取消路径都要清理，不泄漏 chan。
- Call 同时受 call-timeout 与 ctx 约束；超时返回包装后的 ErrTimeout，取消返回
  取消原因。

## 生命周期

关闭顺序：WS → HTTP（各 goroutine 内响应取消）→ 等待在途协程。
transport.Run 自身不关闭事件通道；通道由 ClientService.Close 唯一关闭。
Dispatcher 以事件通道关闭或 ctx 取消为退出条件，退出不视为错误。

## 测试模式

- fakeTransport 以互斥锁保护 sent，并提供 sentFrames() 快照；并发断言只用快照，
  直接读字段会被 `go test -race` 判定竞争。
- 帧路由测试用 httptest + websocket.Upgrader 伪造 SnowLuma：校验 Authorization
  头、推送事件帧、按 echo 回显；连接保活用持续 ReadMessage 直到客户端断开。
- 心跳看门狗测试：服务端不发任何帧 → 断言重连（升级次数 ≥ 2）；服务端以固定
  间隔发 heartbeat → 在有界观察窗口内断言升级次数保持 1。
- 类型化动作测试用 onSend 钩子回填 `{status,retcode:0,echo,data}`，断言解码后的
  领域结果；数组形响应（好友/群列表）逐条断言；CQ 字符串与段数组两种 message
  形态都要覆盖。
- 等待条件用 waitFor(cond) 有界轮询；重连测试统计升级次数而非休眠计时。
- Send 的测试若需返回响应，用 onSend 钩子在发送线程内回填，避免外部竞态。
