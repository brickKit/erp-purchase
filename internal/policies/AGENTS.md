# policies：采购政策

按法人的采购政策：要不要审批与审批人（岗位或指定个人，至多一个；申请与订单提交时，PR02、PO03）、超收比例（收货与验收，PO07、PO08）。没设过的法人是起始值：要审批、没有审批人、超收 0%，版本 0。

## 负责与不负责

- 负责：表 `purchase_policy`；路由 `/api/purchase/policies/{legal_entity_id}` 的读取与设置（按版本，版本判断在语句里，两个同版本的修改只成一个）；给别的模块在事务里读政策（`Of`、`NeedsApproval`、`Approver`：流程内核的绑定，没配审批人答 `NO_APPROVER_CONFIGURED`）。
- 不负责：提交时怎样用它（`requisitions`、`orders` 的 `approval.go` 启动审批时用）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `policies.go` | `Policy`、`Of`、`NeedsApproval`、`Approver`；`GetPolicy`、`PutPolicy` |

## 依赖与被依赖

- 依赖：`core`。
- 被依赖：`requisitions`、`orders`、`receipts`、`acceptances`；入口登记路由。

## 规则在哪

`docs/design.md` 的“拥有的数据”（`purchase_policy`）与规则 PR02、PO03、PO07、PO08；数据范围只有法人一个维度（`core.EntityFacts`）。

## 测试

`itest/policies_integration_test.go`（`policies`：起始值、按版本设置、旧版本被拒、格式不对的值（审批人两样都给、岗位不是身份、空主体）、看不见答 404；上海的审批人设成 Bob、北京设成不要审批，后面的步用它）；`itest/approvals_integration_test.go`（没配审批人、岗位而没有员工组件）。
