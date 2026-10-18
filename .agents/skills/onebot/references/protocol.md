# 协议与运行时不变量

## 帧与解复用

- 入站帧二分：带 echo 的动作响应交给 ResponseSink；带 post_type 的交给 EventSink；
  两者皆无则丢弃并 Debug 记录，不 panic。
- 动作响应信封为 {status,retcode,wording,data}；retcode 0 与 1（async 已提交）
  视为成功，其余转换为 APIError。
- message 字段兼容段数组与字符串两种形式；段数据值统一字符串化，复杂结构保留
  JSON 文本；事件已知字段入类型化视图，其余进 Extra。

## 传输与并发

- gorilla 不允许并发写：所有写出经 Transport.mu 串行化，写超时与 ctx 期限取较早者。
- 重连退避从 ReconnectInitial 指数增长到 ReconnectMax，成功后复位；退避带随机
  抖动，避免重连风暴。
- 拨号携带 `Authorization: Bearer <token>`；日志只输出 endpoint 主机名，绝不输出
  token、查询串或完整消息体。
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
- 等待条件用 waitFor(cond) 有界轮询；重连测试统计升级次数而非休眠计时。
- Send 的测试若需返回响应，用 onSend 钩子在发送线程内回填，避免外部竞态。
