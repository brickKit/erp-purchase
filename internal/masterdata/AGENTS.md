# masterdata：向主数据取证（共用）

向四个主数据组件（`mdm/org`、`mdm/partner`、`mdm/product`、`mdm/currency`）取证据与快照。这里的每个调用都在事务之外（线协议 P4.20）：先取证，再开事务。不可达从不当作“不存在”（协议 07），答 `DEPENDENCY_UNAVAILABLE`。

## 负责与不负责

- 负责：法人（`LegalEntityOf`）、部门的来源读取（`OrgUnitRead`）、供应商的资格与快照（`SupplierOf`，用途 `purchase`）、产品的资格与快照（`ProductsOf`，用途 `purchase`，回答带是否库存物资）、单位换算（`Convert`）、币种小数位（`CurrencyScale`）；按依赖 ID 取 gRPC 连接（`Conn`）。
- 不负责：库存的收货与退货命令；部门投影（`orgunits`）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `upstream.go` | 上面每项一个函数与它的结果类型 |

## 依赖与被依赖

- 依赖：`core`。
- 被依赖：`orgunits`、`requisitions`、`orders`。

## 规则在哪

装配项目协议 07（资格取证，W 内提交）、协议 14（小数位）；替身怎样答在 `itest/standins_integration_test.go`。

## 测试

由各流程间接覆盖：`itest/requisitions_integration_test.go`（产品不可采购、单位不能换算被拒）。
