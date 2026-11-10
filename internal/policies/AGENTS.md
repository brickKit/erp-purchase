# policies：采购政策

按法人的采购政策：要不要审批（申请与订单提交时，PR02、PO03）、超收比例（收货与验收，PO07、PO08）。没设过的法人是起始值：要审批、超收 0%，版本 0。

## 负责与不负责

- 负责：表 `purchase_policy`；路由 `/api/purchase/policies/{legal_entity_id}` 的读取与设置（按版本，版本判断在语句里，两个同版本的修改只成一个）；给别的模块在事务里读政策（`Of`、`NeedsApproval`）。
- 不负责：提交时怎样用它（`requisitions`、以后的订单模块按生命周期表的条件 `GuardPolicy` 用）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `policies.go` | `Policy`、`Of`、`NeedsApproval`；`GetPolicy`、`PutPolicy` |

## 依赖与被依赖

- 依赖：`core`。
- 被依赖：`requisitions`、`orders`、`receipts`、`acceptances`；入口登记路由。

## 规则在哪

`docs/design.md` 的“拥有的数据”（`purchase_policy`）与规则 PR02、PO03、PO07、PO08；数据范围只有法人一个维度（`core.EntityFacts`）。

## 测试

`itest/policies_integration_test.go`（`policies`：起始值、按版本设置、旧版本被拒、格式不对的值、看不见答 404；北京设成不要审批，后面的步用它）。
