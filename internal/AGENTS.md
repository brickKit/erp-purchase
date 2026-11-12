# internal/：业务模块

采购的业务，每个模块一个包。只给本仓库引用（Go 的 `internal`）：别的组件编译期就引用不了，跨组件只走契约（决定 0101）。

先按下表找到模块，再读那个目录的 `AGENTS.md`。

## 模块

| 包 | 一句话 | 依赖（本组件内） |
| --- | --- | --- |
| `receipts` | 收货：经库存入库、收货计价依据与撤回、按库存的流水补齐已收、重发的工作项 | `core`、`masterdata`、`orders`、`policies` |
| `acceptances` | 服务验收：按数量或金额、验收事实 | `core`、`masterdata`、`orders`、`policies` |
| `returns` | 采购退货：按收货流水的可退扣减、逐行经库存出库、重发的工作项 | `core`、`masterdata`、`orders` |
| `orders` | 采购订单与它的生命周期（一张表，状态唯一的写入）；申请行转订单行的取用与退回 | `core`、`masterdata`、`orgunits`、`policies`、`requisitions` |
| `requisitions` | 采购申请与它的生命周期（一张表，状态唯一的写入）；被订单取用的数量 | `core`、`masterdata`、`orgunits`、`policies` |
| `policies` | 按法人的采购政策：要不要审批、审批人、超收比例 | `core` |
| `orgunits` | 部门投影（协议 02） | `core`、`masterdata` |
| `matchbasis` | 系统面的匹配依据（`MatchService.Basis`，给 `erp/finance`） | `core` |
| `facts` | 系统面的增量事实按身份读取（`FactService.Read`） | `core` |
| `masterdata`（共用） | 事务外向主数据组件取证 | `core` |
| `core`（共用） | 名字、资源与操作的定义、错误与校验、`Base` | — |

分层（每个包只引用比它低的层，没有环）：

| 层 | 包 |
| --- | --- |
| 6 | `receipts`、`acceptances`、`returns` |
| 5 | `orders` |
| 4 | `requisitions` |
| 3 | `orgunits` |
| 2 | `masterdata`、`policies`、`matchbasis`、`facts` |
| 1 | `core` |

核对：`go list -f '{{.Imports}}' ./internal/<包>`。

## 共同的写法

- **每个模块的函数先收 `m *core.Base`**（组件的运行时：数据库、调用、事件、审计、日志），入口包的 `Module` 持有它、登记时适配；模块之间直接调对方导出的函数。
- **名字在 `core`**：资源、操作、维度、事件类型、依赖的 ID、错误原因都在 `core/names.go`、`core/rules.go`；资源定义（`core.Definitions()`）是全组件一张表，因为列表能筛选的字段、维度的映射是按资源而不是按模块定的。
- **生命周期是一张表**：申请与订单在哪个状态能做什么、状态怎样变，只在它模块的 `lifecycle.go`（`core.Table`：通用的格、`Decide`、`Refused` 与三项核对在 `core/lifecycle.go`）；别处按格行事，变状态只经该模块的 `Transition`。
- **订单行的进度列各有唯一的写入**：`receiving_base_qty` 与 `received_base_qty` 只在 `receipts`（建收货、`record`、`fail`）与 `returns`（过账减已收）；`accepted_qty`、`accepted_amt` 只在 `acceptances`。写完都调 `orders.Progressed`（发订单事件、收足即完成）。
- **增量事实只经 `core.Fact`**：发事件与存进 `purchase_fact` 在同一个事务，`FactService.Read` 才答得出漏掉的事实。
- **审批经流程内核**：`core.Base.Flow` 是 SDK 的流程内核（入口建）；申请与订单各在自己的 `approval.go` 里给出流程定义、锁单据的钩子、结束处理、依据摘要，提交时启动、取消与修订时撤回或取代、执行前放行核对；实例与任务的状态只由内核写。
- **事务外取证、事务内写**：上游调用都在 `masterdata`，在事务之前；写入、审计（`core.Change`）与发件表（`core.Emit`）同一个事务。

## 加一个模块

一个新的业务能力有自己的表、路由与规则时才建模块（拿不准时放进最近的模块，宁可少切）：建目录、写 `AGENTS.md` 与 `CLAUDE.md`（`@AGENTS.md`），把它加进上面的表与仓库根的模块表，在 `purchase/module.go` 登记它的路由与工作。

## 测试

模块没有单独的单元测试：业务规则由 `itest/` 的流程测试覆盖，每个模块的说明写了它在哪个文件里被测。
