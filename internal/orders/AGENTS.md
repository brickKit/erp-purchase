# orders：采购订单与它的生命周期

订单的建、读、改、修订与列表；**订单的生命周期**（哪个状态能做什么、状态怎样变）只在这里。收货、验收、退货是行上的进度，不是订单状态：它们的命令只问表“这个状态允不允许”（`receipts`、`acceptances`、`returns`）。

## 负责与不负责

- 负责：表 `purchase_order`、`order_line`（行的进度列由收货、验收、退货的模块写）；生命周期表与订单状态唯一的写入（`Transition`）；路由 `/api/purchase/orders` 的列表、新建、读取、修改，`revise`，与 `submit`、`approve`、`reject`、`close`、`cancel` 五个动作（`Action`）；订单的审批实例的启动、撤回与取代、放行核对（`approval.go`）；订单的事件 `erp.purchase_order.changed.v1`；订单行从申请行来时，取用与退回（经 `requisitions.Apply`）。
- 不负责：申请的状态与数量的核对（`requisitions`）；政策（`policies`）；取证（`masterdata`、`orgunits`）。

## 生命周期（改订单规则先读这里）

`lifecycle.go` 是一张表（`core.Table`）：6 个状态（`draft`、`submitted`、`approved`、`completed`、`closed`、`cancelled`）× 11 个命令（修改、提交、批准、驳回、修订、收货、验收、退货、关闭、取消、完成），每格写拒绝（原文）、忽略、允许或变成某状态（审计的操作），附加条件（`GuardPolicy`、`GuardEvidence`、`GuardFlow`（批准与驳回是对审批任务的决定）、`GuardProgress`、`GuardNothingReceiving`、`GuardNothingStarted`、`GuardAllDone`），规则编号 PO02–PO12（PO01 是新建）。

- 用法：命令先读订单（加锁），`Lifecycle.Decide(状态, 命令)`，按格行事；变状态只经 `Transition`（带前置状态的条件更新、发事件、按格审计）。
- **加一个状态或命令**：在表里加一列或一行并定下每一格（`TestTheLifecycleDecidesEveryCell`）；同步改 `docs/design.md` 的同一张表（`TestTheDesignDocumentIsTheLifecycle` 逐格比较）；集成测试的注释写上规则编号（`TestEveryRuleIsTested`）；契约里的状态枚举一并改。

## 文件

| 文件 | 内容 |
| --- | --- |
| `lifecycle.go` | 状态、命令、附加条件；表 `Lifecycle`；`Set`、`Transition` |
| `lifecycle_test.go` | 单元测试：每一格都定了；设计文档的表与代码逐格相同；每条规则都有集成测试写明 |
| `records.go` | `Order`、`Line`、`Supplier`、`Product`（事件的形状）；读取的 SQL（`cols`、`scan`、`Get`、`Read`）与 `Emit` |
| `lines.go` | `OrderIn`、`LineIn`（契约的 `OrderIn`）；输入的检查（`checkIn`）、事务外的取证与行的计算（`prepare`：部门、法人、币种小数位、供应商可采购、产品可采购与是否库存物资、换算、金额）、`fresh`（取证不超过 W）；草稿整份替换行（`replaceLines`）、修订按行身份保留（`reviseLines`）；从申请行取用的净变化（`takes`）；合计（`total`） |
| `approval.go` | 订单的审批（线协议第 17 章）：流程定义 `Flow`、`Document`、依据摘要 `basis`（供应商、币种、每行的产品、数量、单位、单价、合计；关键修订号是修订号）、内核的钩子 `FlowDocument`（锁订单、法人当天的业务日期、三个维度）与 `FlowEnded`（批准或退回草稿，意见是原因）、`startApproval`（提交与修订时；政策不要审批时自动批准的实例）、`Released`（收货与验收前的放行核对）、`decide`（原来的批准与驳回路由经内核） |
| `progress.go` | `Progressed`：行的进度变了之后发订单事件，每行都收足或验收足时完成（PO12） |
| `orders.go` | `CreateOrder`、`GetOrder`、`UpdateOrder`、`ReviseOrder`（共用 `change`、`header`、`finish`）；`Action`（提交时重新取证 `again`、启动审批；批准与驳回转给 `decide`；取消时撤回或取代审批）；`ReviseOrder` 取代已批准的审批再启动新的；`ListOrders` 与列表的定义 |

## 依赖与被依赖

- 依赖：`core`、`masterdata`、`orgunits`（`Usable`）、`policies`（`Of`）、`requisitions`（`Apply`、`Take`）。
- 被依赖：`receipts`、`acceptances`、`returns`（`Read`、`Get`、`Lifecycle`、`Progressed`、`OrderFacts`、`Released`）；入口登记路由，流程内核的钩子与定义（`Flow`、`FlowDocument`、`FlowEnded`）。

## 规则在哪

`docs/design.md` 的“订单的状态”（同一张表与规则目录 PO01–PO12）；申请转订单：同一页的 PR06；取证与 W：装配项目协议 07；金额：协议 14（行金额按币种小数位舍入，合计不另舍入）。

## 测试

- 单元：`lifecycle_test.go`。
- 集成：`itest/orders_integration_test.go`（`purchaseOrders`：PO01–PO06、PO10、PO11；`fromRequisitions`：PR06）、`itest/approvals_integration_test.go`（`approvals`：审批经内核、放行核对）、`itest/departments_integration_test.go`（部门移动后订单与申请跟着走）。
