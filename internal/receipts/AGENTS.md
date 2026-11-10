# receipts：收货

已批准订单的库存物资行经 `erp/inventory` 入库（PO07）：收货单先在事务里建成“收货中”，带每行的收货计价依据与一个工作项；事务之外调库存的 `StockService.Receive`（收货身份即幂等键）。已收数量只来自库存的入库流水。

## 负责与不负责

- 负责：表 `receipt`、`receipt_line`、`receipt_movement`；路由 `/api/purchase/orders/{id}/receipts`（建收货，`Orchestration`）、`/api/purchase/receipts` 的列表与读取；收货计价依据与它的撤回（增量事实，`core.Fact`）；后台工作 `JobReceive`（结果不明时按同一身份重发，直到库存答复）；订阅库存的流水事件（`OnMovement`）；订单行的 `receiving_base_qty`、`received_base_qty` 两列。
- 不负责：订单在哪个状态能收货（`orders` 的表，`Receive` 一格）；完成（`orders.Progressed`，PO12）；超收比例从哪来（`policies`）；库存怎样入库（`erp/inventory`）。

## 怎样走

- 建收货（`CreateReceipt`）：事务外核对行（本订单的库存物资行、同一仓库、经 `mdm/product` 换成基本单位）；事务里锁订单、查表、按政策的超收比例在一条 `UPDATE` 里判断“收货中 + 已收 + 本次 ≤ 订购 ×（1 + 比例）”，超出整笔拒绝（`OVER_RECEIPT`）；写收货单、行、计价依据（不含税、每基本单位）、工作项、事件。
- 同步调一次 `receive`（3 秒）：库存答复 → `record` 每条流水；库存拒绝（它的领域错误、非“不可用”）→ `fail`：收货单 `failed`、退回收货中的数量、每行发计价依据的撤回；不明 → 留给工作项。回答 200 已过账、202 还在收货中、409 `RECEIPT_FAILED`（问题的 `metadata.receipt_id`）。
- `record`（`movements.go`）：答复与流水事件都走这里，按流水身份去重：已收加、收货中减，全部行有流水时收货单过账（审计记在收货人名下）；然后 `orders.Progressed`（发订单事件、收足即完成）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `records.go` | `Receipt`、`Line`（事件的形状）；读取的 SQL 与 `emit` |
| `receipts.go` | 输入检查、`plan`、`CreateReceipt`、`receive`、`refused`（拒绝与不明的分界）、`fail`、`ReceiveJob`、`GetReceipt`、`ListReceipts` |
| `movements.go` | `OnMovement`（只认 `ref_kind = purchase_receipt` 的入库流水，行参考是本组件的收货行）、`record` |

## 依赖与被依赖

- 依赖：`core`、`masterdata`（`Conn`、`Convert`、`LegalEntityOf`）、`orders`（`Read`、`Get`、`Lifecycle`、`Progressed`、`OrderFacts`）、`policies`（`Of`）。
- 被依赖：入口登记路由、订阅与工作。

## 规则在哪

`docs/design.md` 的“收货、验收与退货”与 PO07、PO10、PO12；装配项目协议 05b（收货计价依据与撤回）、协议 05 的共同规则（增量事实、事实身份）、协议 01（工作项）。

## 测试

`itest/receipts_integration_test.go`（`receipts`：过账与计价依据、超收比例（上海 10%、北京 0%，收货中也计入）、收足即完成、库存不答复时 202 与工作项重发、答复丢失由流水事件补齐且只算一次、库存拒绝时失败与撤回、服务行与草稿订单不能收货、收货中不能关闭与取消）。
