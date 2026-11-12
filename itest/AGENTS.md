# itest/：集成测试与测试索引

采购组件对着真实的 PostgreSQL 与消息系统、经它真实的接口（用户面 HTTP、系统面 gRPC、事件）的测试（L2）。构建标签 `integration`；一条业务流程一个文件。

## 怎样跑

- `make test-integration`（`go test -count=1 -tags integration ./itest/`）。
- 环境变量：`BE_SDK_TEST_PG_ADMIN=postgres://postgres:<口令>@localhost:5432/brickkit_test_db`（口令是装配项目 `.env` 的 `POSTGRES_PASSWORD`）、`BE_SDK_TEST_NATS_URL=nats://localhost:4222`；be-sdk-go 的 `sectest` 是测试部署。
- 前提：装配项目里 `make up` 起了 `be-postgres` 与 `be-nats`；`be-postgres` 里有库 `brickkit_test_db`；测试每次开始时删掉再建自己的 schema `erp_purchase_it`，跑完留着；端口 28085（用户面）、9095（系统面）与替身的 19112–19116 要空着，被占时测试直接失败。
- **只有一个测试** `TestPurchaseEndToEnd`：它按下表的顺序跑各步；改了哪个流程就跑整个（`-run TestPurchaseEndToEnd`）。

## 文件

按运行顺序：

| 文件 | 步 | 测的流程 | 覆盖的规则 | 被测的模块 |
| --- | --- | --- | --- | --- |
| `unsupported_integration_test.go` | `notInThisVersion` | 契约里有、本版不做的路由答“能力不支持” | D75 | 入口 `purchase/module.go` |
| `policies_integration_test.go` | `policies` | 政策的起始值、按版本设置（旧版本被拒）、格式不对的值（含审批人）、看不见答 404；上海的审批人是 Bob，北京设成不要审批 | PR02、PO03 的前提 | `policies` |
| `requisitions_integration_test.go` | `requisitions` | 申请：新建（产品不可采购、单位不能换算、别的法人的部门被拒）、按版本修改、提交、提交人不能批准（`SELF_APPROVAL`）、批准再批准、驳回要原因、三种状态下取消、北京提交即批准；看不见答 404，列表按状态与单号，只看北京的人只列北京的 | PR01–PR05 | `requisitions`、`policies`、`orgunits` |
| `orders_integration_test.go` | `purchaseOrders`、`fromRequisitions` | 订单：新建（供应商暂停、币种不认识、库存物资行没仓库被拒）、按版本修改、提交、驳回要原因、提交人不能批准、修订（保留行、改单价与数量、换供应商或换产品被拒）、北京提交即批准、关闭、三种状态下取消；列表按供应商、状态与单号。申请行转订单行：取用、草稿改数量、超出被拒、全部转完关闭、取消订单退回后重开、不能用的申请行 | PO01–PO06、PO10、PO11；PR05、PR06 | `orders`、`requisitions` |
| `receipts_integration_test.go` | `receipts` | 收货：过账与收货计价依据（每基本单位）、超收比例（上海 10%、北京 0%，收货中计入）、收足即完成、库存不答复（202、关闭与取消被拒、工作项重发后过账）、答复丢失由流水事件补齐且只算一次、库存拒绝（失败、撤回计价依据）、服务行与草稿订单不能收货、收货中没有时关闭 | PO07、PO10、PO11、PO12；协议 05b 的计价依据与撤回 | `receipts`、`orders` |
| `acceptances_integration_test.go` | `acceptances` | 服务验收：按数量（金额按单价）、按金额、超收比例的两维、库存物资行与空行被拒、有验收的订单不能取消、草稿不能验收、验收足即完成且不再验收 | PO08、PO11、PO12；协议 05 的验收事实 | `acceptances`、`orders` |
| `returns_integration_test.go` | `returns`、`returnLimits` | 采购退货：已完成的订单退货后仍是已完成、超出流水的可退整笔拒绝且不建单、库存拒绝（失败、数量退回）、库存不答复（202、工作项重发）、已关闭的订单能退、别的订单的流水与草稿被拒；成本组件的可退数量（超过整笔拒绝、同一流水的行合计、重放不核对、不答复不拦） | PO09；协议 05b 的退货事实 | `returns`、`orders` |
| `system_integration_test.go` | `systemSurface`、`outbox`、`recoveryChecks` | 系统面：匹配依据（已完成又退过货的订单按流水、按行问服务行、不认识的订单、空问法、只许 `erp/finance`）、事实按身份读回；每个事件合乎 schema、每种类型都发过；整套恢复的核对只许恢复工具，审计的每种对象按版本答“还在不在” | D50、协议 05；协议 02；线协议第 14 章、P14.3 | `matchbasis`、`facts`、入口 `purchase/checks.go` |
| `approvals_integration_test.go` | `approvals` | 审批经流程内核：没配审批人被拒、任务按共享任务契约发布（候选是政策的个人）、谁能读任务、标准路由的驳回要意见、旧版本与自审被拒、驳回回草稿带原因、再提交是新实例、同一幂等键重放、旧路由；放行核对（改了修订号不放行）；取消撤回；岗位而没有员工组件时暂停、异常入口；申请的驳回 | PO03–PO05、PO08、PO11；PR02–PR04；线协议 P17.3、P17.7、P17.8、P17.10、P17.11、P17.14、P17.15 | `orders`、`requisitions`、`policies`、入口 |
| `departments_integration_test.go` | `departments`、`laterOrgEvents` | 按部门维度读订单与申请；部门移到另一部门之下，它们跟着走；不认识的动作标为不完整并补取，同一流上的法人不是部门 | 安全 07；P6.33、PJ36 | `orgunits`、`orders`、`requisitions` |

共用的：

| 文件 | 内容 |
| --- | --- |
| `endtoend_integration_test.go` | `TestPurchaseEndToEnd`（各步的顺序）；`fixture`（运行中的成员、替身、世界的 ID：上海与北京两个法人，Alice 与 Bob 两个采购员，只看北京申请的 Carol；`departments` 步里加的 Dave 只看部门 1 的树）；辅助：申请与订单的建、动作与“被状态拒绝”（各在自己的文件）；`start`（建世界、替身与 `mdm/org` 的流，迁移并运行成员）；辅助：`client.do`（新的幂等键）、`at`、`get`、`unit`、`publishOrgUnit`、`publishOrg`、`publishMovement`、`portFree` |
| `standins_integration_test.go` | 替身，各按已发布的契约经 mTLS 答：四个主数据组件（供应商一个可用、一个暂停；产品一个库存物资、12 EA 一盒，一个服务）；测试自己扮演 `mdm/org` 发部门事件；库存的 `StockService.Receive` 按收货身份幂等，每行一条流水并作为 `erp.inventory_movement.recorded.v1` 发出（测试替它的转发），可以设成拒绝、答复丢失、不可达；`Return` 按退货行身份幂等，同样可以拒绝、不可达 |

## 加测试

- 新流程：新文件 `<流程>_integration_test.go`，写一个 `func (fx *fixture) <步>()`，在 `TestPurchaseEndToEnd` 里按依赖排进顺序，在上表加一行。
- 已有流程的新情形：加在那一步的文件里，更新上表的“覆盖的规则”。
- 测试不为了通过而改（装配项目规范的铁律）。
