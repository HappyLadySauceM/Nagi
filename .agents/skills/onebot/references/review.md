# 审查清单

## 证据流程

检查端口实现、调用链、装配顺序和测试，不能只看目录分层存在就认定符合架构。
每条问题说明触发条件、实际调用链、后果和替代方案；区分已证明缺陷、可演进风险
和个人偏好。

## 典型建议的处理

| 建议 | 纠正 |
| --- | --- |
| 在 handler 里直接读写 websocket | handler 调用 contracts.Sender；连接归 infrastructure |
| 业务上下文 import onebot/infrastructure | 依赖 onebot/contracts；infrastructure 只在 assembly 出现 |
| 事件通道无界或阻塞读泵 | 有界缓冲 + 满时丢弃告警，读泵永不阻塞 |
| 为单条指令引入消息队列或插件框架 | 先证明跨进程/热插拔需求，当前 Dispatcher + Handler 注册即可 |
| 在 domain 加 JSON 标签图省事 | 线上编解码放 infrastructure wire 模型，domain 保持纯类型 |
| 用关键词扫描代替架构判断 | 技能引导判断，测试与审查共同验证 |

## 完成前逐项核对

- 依赖方向与 docs/architecture.md 一致；pkg 不导入 internal/cmd；跨上下文仅
  contracts；assembly 之外无端口绑定。
- domain 无 JSON/binding 标签；标识为命名类型；返回切片与 map 已复制。
- 日志不含 access token、完整消息体或未脱敏 URL；日志消息为英文，结构化字段。
- goroutine 生命周期有界：Run 循环可被 ctx 取消；channel 关闭路径唯一且有锁保护。
- echo pending 表无泄漏；超时、取消、关闭三条路径都清理。
- 管理 API 保持 {code,message,data} 信封与安全响应头；对外错误为安全英文短句。
- 测试验证行为而非复制实现；并发改动有 -race 结果；跳过项已说明。
- `just verify` 通过；最终 diff 只含任务范围，未回退用户修改。
