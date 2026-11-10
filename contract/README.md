# erp-purchase 的契约

`erp/purchase` 发布的契约包：系统面的 protobuf、事件载荷的 JSON Schema、用户面的 OpenAPI。放在组件仓库的 `contract/` 子目录里单独发布（标签 `contract/v3.…`）。

依据：装配项目 [be-assembly-standard](https://github.com/brickKit/be-assembly-standard) 的协议 05（匹配依据、不可遗漏的增量事实）、05b（收货计价依据、采购退货）、07（资格）、14（单号、金额、列表）；组件的 `docs/design.md`。

## 内容

| 位置 | 是什么 |
| --- | --- |
| `proto/be/erp/purchase/v3/purchase.proto` | `MatchService.Basis`（按订单行的订购、已收、已验收，给财务的三单匹配与期末暂估）；`FactService.Read`（按事实身份读回增量事实） |
| `gen/go/be/erp/purchase/v3/` | Go 代码，包 `purchasev3`；模块 `github.com/brickKit/erp-purchase/contract/v3` |
| `python/` | Python 包 `contract-erp-purchase`，模块 `be.erp.purchase.v3` |
| `events/` | 流 `erp.purchase`：申请、订单、收货单、退货单（完整状态）；收货计价依据与它的撤回、验收事实、采购退货（增量事实） |
| `openapi.yaml` | 用户面；每个路由的操作写在 `x-operation` |

## 要点

- **数量与金额**是十进制字符串。订单行的数量用采购单位，`base_quantity` 是换算后的基本单位（库存与成本只用它）；退货按基本单位。
- **已收数量只来自库存的入库流水**：收货在库存答复之前是 `receiving`（用户面答 202），库存的流水事件或同一身份的重发把它结清。
- **增量事实**按 `fact_id` 去重，丢了用 `FactService.Read` 按身份读回（协议 05 的共同规则）。收货计价依据的 `fact_id` 是收货行身份，也是库存流水与成本的“收货行参考”。
- **能力不支持**（D75）：预付申请、供应商价格表，答 `CAPABILITY_UNSUPPORTED`。
