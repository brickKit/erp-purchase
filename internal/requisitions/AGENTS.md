# requisitions：采购申请

申请的一生：草稿、修改、提交、批准或驳回、取消、被订单取用（PR06）；**申请的生命周期**（哪个状态能做什么、状态怎样变）只在这里。

## 负责与不负责

- 负责：表 `requisition`、`requisition_line`；生命周期表与申请状态唯一的写入（`Transition`）；路由 `/api/purchase/requisitions` 的列表、新建、读取、修改，与 `submit`、`approve`、`reject`、`cancel` 四个动作（`Action`）；申请的事件 `erp.purchase_requisition.changed.v1`；订单行从申请行取用与退回（`Apply`：核对、记数、全部转完关闭、退回后重开）。
- 不负责：法人、部门、产品的取证（`masterdata`、`orgunits`）；政策（`policies`）；表的通用写法与核对（`core/lifecycle.go`）。

## 生命周期（改申请规则先读这里）

`lifecycle.go` 是一张表（`core.Table`）：6 个状态（`draft`、`submitted`、`approved`、`rejected`、`cancelled`、`closed`）× 8 个命令（修改、提交、批准、驳回、取消、转订单、全部转完、退回），每格写拒绝（原文）、忽略、允许或变成某状态（审计的操作），附加条件（`GuardPolicy`：政策不要审批时直接批准；`GuardNotSubmitter`：批准人不是提交人；`GuardNothingOrdered`：没转过订单才能取消已批准的；`GuardAllOrdered`、`GuardNotAllOrdered`：关闭与重开），规则编号 PR01–PR06。

- 用法：命令先读申请（加锁），`Lifecycle.Decide(状态, 命令)`，按格行事；变状态只经 `Transition`（带前置状态的条件更新、发事件、按格审计）。
- **加一个状态或命令**：在表里加一列或一行并定下每一格（`TestTheLifecycleDecidesEveryCell`）；同步改 `docs/design.md` 的同一张表（`TestTheDesignDocumentIsTheLifecycle` 逐格比较）；集成测试的注释写上规则编号（`TestEveryRuleIsTested`）；契约里的状态枚举一并改。

## 文件

| 文件 | 内容 |
| --- | --- |
| `lifecycle.go` | 状态、命令、附加条件；表 `Lifecycle`；`Set`、`Transition` |
| `lifecycle_test.go` | 单元测试：每一格都定了；设计文档的表与代码逐格相同；每条规则都有集成测试写明 |
| `records.go` | `Requisition`、`Line`、`Product`（事件的形状）；读取的 SQL（`cols`、`scan`、`get`、`read`）与 `emit` |
| `ordering.go` | `Take`、`Apply`（按申请身份顺序加锁：只从已批准、同法人、同产品同单位的申请行取，不超过申请数量；退回不看状态）、`settle`（全部转完关闭、退回后重开，发事件） |
| `requisitions.go` | 输入的检查与事务外的取证（`prepare`：部门、法人、产品可采购、单位能换算成基本单位）；`CreateRequisition`（申请人是建的人）、`GetRequisition`、`UpdateRequisition`（法人不改，行整份替换，按版本）、`Action`；`ListRequisitions` 与列表的定义 |

## 依赖与被依赖

- 依赖：`core`、`masterdata`、`orgunits`（`Usable`）、`policies`（`Of`）。
- 被依赖：`orders`（`Apply`、`Take`）；入口登记路由。

## 规则在哪

`docs/design.md` 的“订单的状态”之后的申请一节（同一张表与规则 PR01–PR06）；数据范围的负责人维度是申请人（`requester_id`）。

## 测试

- 单元：`lifecycle_test.go`。
- 集成：`itest/requisitions_integration_test.go`（`requisitions`：PR01–PR05 的每一格、看不见答 404、列表按状态与单号、只看北京的人只列北京的）；`itest/orders_integration_test.go`（`fromRequisitions`：PR06）。
