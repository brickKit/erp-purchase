# itest/：集成测试与测试索引

采购组件对着真实的 PostgreSQL 与消息系统、经它真实的接口（用户面 HTTP、系统面 gRPC、事件）的测试（L2）。构建标签 `integration`；一条业务流程一个文件。

## 怎样跑

- `make test-integration`（`go test -count=1 -tags integration ./itest/`）。
- 环境变量：`BE_SDK_TEST_PG_ADMIN=postgres://postgres:<口令>@localhost:5432/brickkit_test_db`（口令是装配项目 `.env` 的 `POSTGRES_PASSWORD`）、`BE_SDK_TEST_NATS_URL=nats://localhost:4222`；be-sdk-go 的 `sectest` 是测试部署。
- 前提：装配项目里 `make up` 起了 `be-postgres` 与 `be-nats`；`be-postgres` 里有库 `brickkit_test_db`；测试每次开始时删掉再建自己的 schema `erp_purchase_it`，跑完留着；端口 28085（用户面）、9095（系统面）与替身的 19112–19115 要空着，被占时测试直接失败。
- **只有一个测试** `TestPurchaseEndToEnd`：它按下表的顺序跑各步；改了哪个流程就跑整个（`-run TestPurchaseEndToEnd`）。

## 文件

按运行顺序：

| 文件 | 步 | 测的流程 | 覆盖的规则 | 被测的模块 |
| --- | --- | --- | --- | --- |
| `unsupported_integration_test.go` | `notInThisVersion` | 契约里有、本版不做的路由答“能力不支持” | D75 | 入口 `purchase/module.go` |
| `policies_integration_test.go` | `policies` | 政策的起始值、按版本设置（旧版本被拒）、格式不对的值、看不见答 404；北京设成不要审批 | PR02、PO03 的前提 | `policies` |
| `requisitions_integration_test.go` | `requisitions` | 申请：新建（产品不可采购、单位不能换算、别的法人的部门被拒）、按版本修改、提交、提交人不能批准、批准再批准、驳回要原因、三种状态下取消、北京提交即批准；看不见答 404，列表按状态与单号，只看北京的人只列北京的 | PR01–PR05 | `requisitions`、`policies`、`orgunits` |

共用的：

| 文件 | 内容 |
| --- | --- |
| `endtoend_integration_test.go` | `TestPurchaseEndToEnd`（各步的顺序）；`fixture`（运行中的成员、替身、世界的 ID：上海与北京两个法人，Alice 与 Bob 两个采购员，只看北京申请的 Carol）；`start`（建世界、替身与 `mdm/org` 的流，迁移并运行成员）；辅助：`client.do`（新的幂等键）、`at`、`get`、`unit`、`publishOrgUnit`、`publishOrg`、`portFree` |
| `standins_integration_test.go` | 替身，各按已发布的契约经 mTLS 答：四个主数据组件（供应商一个可用、一个暂停；产品一个库存物资、12 EA 一盒，一个服务）；测试自己扮演 `mdm/org` 发部门事件 |

## 加测试

- 新流程：新文件 `<流程>_integration_test.go`，写一个 `func (fx *fixture) <步>()`，在 `TestPurchaseEndToEnd` 里按依赖排进顺序，在上表加一行。
- 已有流程的新情形：加在那一步的文件里，更新上表的“覆盖的规则”。
- 测试不为了通过而改（装配项目规范的铁律）。
