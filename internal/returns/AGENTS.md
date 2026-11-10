# returns：采购退货

退货引用订单的收货流水（PO09）：建退货单的事务里每行先从它的流水的“可退”里扣（超出整笔拒绝，`RETURN_EXCEEDS_RECEIPT`，不建任何东西）；然后逐行经 `erp/inventory` 的 `StockService.Return` 出库（退货行身份即库存的幂等键），在事务之外。

## 负责与不负责

- 负责：表 `purchase_return`、`return_line`；收货流水的 `returned_base_qty`（退货中与已退）；路由 `/api/purchase/orders/{id}/returns`（建，`Orchestration`）与 `/api/purchase/returns` 的列表、读取；后台工作 `JobReturn`（还在退货中的行按同一身份重发）；事实 `erp.purchase_return.posted.v1`（事实身份是退货行身份）；过账的行减订单行的已收。
- 不负责：订单在哪个状态能退货（`orders` 的表，`Return` 一格：已批准、已完成、已关闭都能）；收货流水从哪来（`receipts`）；库存怎样出库与它的第二次核对（`erp/inventory`）。

## 怎样走

- 建退货（`CreateReturn`）：事务里锁订单、查表，每行的流水必须是本订单的收货流水（否则 400），“已退 + 本次 ≤ 收进”在一条 `UPDATE` 里判断；写退货单、行（`returning`）、工作项、事件。
- 同步调一次 `issue`（3 秒）：逐行调库存；答复 → `settle` 过账（记出库流水、已收减少、发退货事实）；库存拒绝（它的领域错误）→ 该行失败，数量退回流水；不明 → 留给工作项。全部行定了答 200，还有行在退货中答 202。完成之后的退货不让订单回到已批准（PO12 的说明）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `records.go` | `Return`、`Line`（事件的形状）；读取的 SQL、`emit`、`settled` |
| `returns.go` | 输入检查、`CreateReturn`、`issue`、`refused`、`settle`、`ReturnJob`、`GetReturn`、`ListReturns` |

## 依赖与被依赖

- 依赖：`core`、`masterdata`（`Conn`、`LegalEntityOf`）、`orders`（`Read`、`Get`、`Lifecycle`、`Progressed`、`OrderFacts`）。
- 被依赖：入口登记路由与工作。

## 规则在哪

`docs/design.md` 的 PO09 与“收货、验收与退货”；装配项目协议 05b（采购退货）、协议 05 的共同规则（增量事实）。

## 测试

`itest/returns_integration_test.go`（`returns`：已完成的订单退货后仍是已完成、超出流水的可退整笔拒绝且不建单、库存拒绝时失败并退回数量、库存不答复时 202 与工作项重发、已关闭的订单能退、别的订单的流水被拒、草稿不能退）。
