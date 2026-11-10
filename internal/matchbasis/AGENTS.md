# matchbasis：匹配依据（系统面）

`MatchService.Basis`：按订单或按订单行，给 `erp/finance` 的三单匹配与期末暂估读“订购（订单单位与基本单位各一个数）、已收（净额，按收货流水）、已验收”（D50、协议 05）。只读，现在的数，不按用户筛选（调用者是服务身份）。

## 负责与不负责

- 负责：一张订单的全部行，或问到的行（至多 500，不认识的略过）；每个库存物资行的收货流水与每条流水已过账的退货量（退货中、失败的不算）；不认识的订单答 `NOT_FOUND`，两种问法都没有或都有答 `REQUEST_INVALID`。
- 不负责：谁能调（入口 `purchase/module.go` 的 `Services` 只许 `erp/finance`）；已收、已验收怎样来（`receipts`、`acceptances`、`returns`）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `matchbasis.go` | `Basis`：两条 SQL，行与流水 |

## 依赖与被依赖

- 依赖：`core`；读 `purchase_order`、`order_line`、`receipt_movement`、`return_line`（别的模块的表，只读）。
- 被依赖：入口的 `services.go`。

## 规则在哪

契约 `contract/proto/be/erp/purchase/v3/purchase.proto` 的 `MatchService`；装配项目协议 05（匹配依据、期末暂估）与决定 D50。

## 测试

`itest/system_integration_test.go`（`systemSurface`：已完成又退过货的订单、按行问服务行、不认识的订单与空问法、别的组件被拒）。
