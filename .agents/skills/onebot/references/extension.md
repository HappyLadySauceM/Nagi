# 扩展指南：新增业务上下文

以 `internal/echo` 为模板，按以下步骤新增 `internal/<name>`：

1. **application**：定义用例服务。构造函数校验依赖（sender 非空、配置项非空）并
   返回 error；依赖只声明 `onebot/contracts` 的最小能力端口（`Sender`、`Messenger`、
   `GroupAdmin`、`InfoReader`、`RequestApprover` 等），不 import 任何 infrastructure，
   也不依赖聚合接口 `API`。
2. **infrastructure**：实现 `bot/contracts.Handler`（Name/Match/Handle）。Match
   只做廉价判断并保持可测试的纯函数性质；Handle 内部再精确解析文本并调用用例。
   适配器不持有连接或全局状态。
3. **装配**：在 `cmd/app/assembly/work.go` 的 `registerBuiltins` 挂载；需要开关时
   走配置域，禁用时不构造服务也不注册处理器。
4. **配置**：`pkg/config` 增加域结构体、默认值与 Validate 校验 →
   `configs/config-example.yaml` 增加示例注释 → 按需在 `applyEnv`/`applyOverrides`
   与 `cmd/app/options` 补充 `QQBOT_*` 环境变量与 Flags。
5. **测试**：application 用 fakeSender 记录断言；infrastructure 用表驱动验证
   Match/Handle；测试函数以行为命名（TestXxxRepliesYyy），双语注释说明验证什么、
   什么错误会使它失败。

## 边界提醒

- 不在业务上下文直接 import `onebot/infrastructure`、gorilla 或 yaml。
- 不绕过 Dispatcher 自行消费事件通道；事件只有一个消费者。
- 不在 Match 中执行发送动作或其他副作用。
- 不为“未来可能的插件系统”预留注册机制；需要时按本指南再扩展。
