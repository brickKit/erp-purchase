# facts：增量事实按身份读取（系统面）

`FactService.Read`：本组件发布过的增量事实（收货计价依据与撤回、验收、采购退货）按事实身份读回，给漏掉了事件的消费方（协议 05 的共同规则）。每个事实由 `core.Fact` 在发布它的同一个事务里存进 `purchase_fact`。

## 负责与不负责

- 负责：按问的顺序答，至多 500；不认识的身份答空类型。
- 不负责：事实怎样产生（各业务模块经 `core.Fact`）；谁能调（入口：任一组件）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `facts.go` | `Read` |

## 依赖与被依赖

- 依赖：`core`；读表 `purchase_fact`。
- 被依赖：入口的 `services.go`。

## 规则在哪

契约的 `FactService`；装配项目协议 05 的共同规则（事实身份、按身份补取）。

## 测试

`itest/system_integration_test.go`（`systemSurface`：以 `erp/costing` 读一个收货计价依据与一个不认识的身份）。
