# attachments：订单的附件

订单的合同与发票扫描件：本组件是属主，按订单与类别的字段判断用户的权限，再替用户经系统面向 `infra/attachment` 申请上传会话、列表、下载与解除引用；订单结束时命令它解除引用或设置保留起算（装配项目协议 12、D68：属主命令，附件组件从不回调）。

## 负责与不负责

- 负责：路由 `/api/purchase/orders/{id}/attachments`（`Upload`、`List`）、`…/{aid}/download`（`Download`）、`…/{aid}`（`Remove`）；工作项 `core.JobOrderEnded` 的处理（`Ended`）。
- 不负责：附件的类别声明（`core.OrderAttachments`，在订单的资源定义里）；排工作项（`orders.Transition` 在订单进入取消、完成、关闭的同一事务里排）；附件的记录、对象、凭证与清除（`infra/attachment`）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `attachments.go` | `Upload`、`List`、`Download`、`Remove`、`Ended`；`readable`（发票扫描件要 `core.OpOrderReadInvoice`） |

## 依赖与被依赖

- 依赖：`core`、`orders`（读订单、订单的事实）；契约 `infra-attachment/contract`（`AttachmentService`）。
- 被依赖：入口登记路由与工作项。

## 规则在哪

`docs/design.md` 的“附件”。

## 测试

`itest/attachments_integration_test.go`（`attachments`：附件组件的替身记下被问了什么）。
