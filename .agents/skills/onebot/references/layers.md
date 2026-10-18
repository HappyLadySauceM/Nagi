# 分层与边界（Go 实现约束）

## 依赖

domain 仅依赖标准库；contracts 只定义稳定接口和快照 DTO。application 依赖本上下文
domain、contracts、端口及其他上下文 contracts；infrastructure 实现端口。HTTP 只能
通过 application 和 contracts 调用用例，不能引用领域对象或拼装连接细节。

`cmd/app/assembly` 是精确的装配例外：允许把 Transport、ClientService、Dispatcher
与业务 Handler 显式绑定，不包含业务规则。技术工具与配置不必套用分层。

`pkg` 不导入 `internal` 或 `cmd`；跨上下文仅访问对方 contracts。例外：
`onebot/domain` 是 OneBot v11 协议的“已发布语言”，业务上下文经 `onebot/contracts`
引用协议值类型属于允许的例外；除此之外不得引用他上下文 domain 或 application 内部。

## 状态和接口

标识、枚举与角色使用命名类型（BotID、UserID、GroupID、MessageID、PostType…）；
Event 等值类型按值传递，返回切片与 map 前复制；domain 不使用 JSON、binding 标签，
线上编解码放在 infrastructure 的 wire 模型。

动作构造（NewSendPrivateMessage 等）返回 `(Action, error)`，在构造时校验目标与
内容并深复制消息段，不依赖调用方自律；校验失败不得产生半构造动作。

## 包职责与公开边界

| 包 | 可以出现 | 不应出现 |
| --- | --- | --- |
| domain | 命名类型、校验构造、纯策略、业务方法 | JSON/binding 标签、gorilla、yaml、传输细节 |
| application | 类型化用例、端口（Transport、EventSink、ResponseSink）、echo 匹配、sentinel 错误 | 拨号、JSON wire 模型、conn 读写 |
| contracts | 稳定接口、只读快照 DTO | 领域别名、传输连接类型、可变状态 |
| infrastructure | wire 模型、拨号、重连、帧解复用、锁 | 其他上下文内部对象、业务规则 |
| HTTP（routes） | 解析、用例调用、信封转换 | 业务事务、领域构造、直接操作连接 |

## 错误与 context

sentinel 错误（ErrNotConnected、ErrTimeout、ErrClosed）配合 errors.Is/As 判断；
APIError 承载 retcode/status/wording，不泄露载荷。错误包装使用英文短语前缀
（如 `send action %s: %w`）。

Go 的 context 只传播取消与期限，不藏连接或事务；单纯同步接口不为“异步”增加
goroutine。

## 生命周期端口

Transport 由 infrastructure 实现并在 assembly 绑定；ClientService 同时充当
ResponseSink 与 EventSink。事件通道只能由 ClientService.Close 在 eventsMu 写锁
保护下关闭，HandleEvent 持读锁检查后再发送，禁止其他路径 close(chan)。分发器对
单个 Handler panic 恢复；分发器成功计数与处理器内部计数语义分离，不互相替代。
