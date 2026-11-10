# acceptances：服务验收

已批准订单的服务与非库存物资行按数量、按金额或两者一起验收（PO08），一个事务写完：验收单、行、订单行的已验收、每行一条验收事实（给财务的服务匹配与期末暂估，协议 05）。不经库存。

## 负责与不负责

- 负责：表 `acceptance`、`acceptance_line`；路由 `/api/purchase/orders/{id}/acceptances`（建）与 `/api/purchase/acceptances/{id}`（读）；订单行的 `accepted_qty`、`accepted_amt` 两列；事实 `erp.purchase_acceptance.recorded.v1`（事实身份是验收行身份）。
- 不负责：订单在哪个状态能验收（`orders` 的表，`Accept` 一格）；完成（`orders.Progressed`）；库存物资（`receipts`）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `acceptances.go` | `Acceptance`、`Line`；`plan`（本订单的服务行、每行一次、数量或金额，金额不给时按单价乘数量、按币种舍入）；`CreateAcceptance`（数量与金额两维都受订购数乘（1 + 超收比例）约束，在一条 `UPDATE` 里判断）；`GetAcceptance`；读取 |

## 依赖与被依赖

- 依赖：`core`、`masterdata`（`CurrencyScale`、`LegalEntityOf`）、`orders`（`Read`、`Get`、`Lifecycle`、`Progressed`、`OrderFacts`）、`policies`（`Of`）。
- 被依赖：入口登记路由。

## 规则在哪

`docs/design.md` 的 PO08、PO11、PO12 与“收货、验收与退货”；验收与收货同一种执行权限（`erp.purchase.receipt.create`、`erp.purchase.receipt.read`），数据范围是订单的三个维度（复制到验收单上）。

## 测试

`itest/acceptances_integration_test.go`（`acceptances`：按数量、按金额、超收比例的两维、库存物资行与空行被拒、有验收的订单不能取消、草稿不能验收、验收足即完成、完成后不能再验收）。
