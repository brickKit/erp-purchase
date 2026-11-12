# erp/purchase

采购组件的代码。用法与边界在 `BRICKKIT.md`，为什么这样设计（术语、数据、状态、规则、未决问题）在 [docs/design.md](docs/design.md)。跨组件的规则在装配项目 be-assembly-standard 的协议 05（匹配依据）、协议 05b（收货计价依据、采购退货）、协议 02（部门投影）、协议 07（资格取证）、协议 09（审批）、协议 14（单号、金额、列表）、安全 07（三个维度）。

## 代码地图

代码按业务模块分层。**先读这一节，再进目录**：每个代码目录都有自己的 `AGENTS.md`（同目录的 `CLAUDE.md` 让 Claude Code 读这个目录的文件时自动带上它），写这一层负责什么、不负责什么、每个文件、依赖谁、被谁用、测在哪；读完就知道要不要往下走。

| 目录 | 是什么 |
| --- | --- |
| [`purchase/`](purchase/AGENTS.md) | 入口：组件定义与组装（路由、gRPC、订阅、后台工作、恢复核对都在这里登记，转给业务模块）；不写业务。`main.go` 与外壳只引用它 |
| [`internal/`](internal/AGENTS.md) | 业务：下面的表里每个模块一个包；模块之间的依赖表在它的 `AGENTS.md`。Go 的 `internal` 让别的组件编译期就引用不了（决定 0101） |
| [`itest/`](itest/AGENTS.md) | 集成测试，按业务流程分文件；它的 `AGENTS.md` 是测试索引（文件 → 流程 → 规则编号） |
| `purchase/migrations/` | 表，整个组件一套（迁移顺序是全组件的，不按模块拆） |
| `cmd/seed/` | 种子：角色与两个采购账号的范围、各法人的政策、各种状态的申请与订单（含收货、验收、退货）（`make seed`） |
| `contract/` | 本组件发布的契约，单独的模块：`contract/proto/`、`contract/events/`、`contract/openapi.yaml` |
| `docs/` | 设计（规则的正文；各层的 `AGENTS.md` 只指过去，不抄） |

业务模块（`internal/` 下）：

| 模块 | 负责 | 拥有的表 |
| --- | --- | --- |
| [`requisitions`](internal/requisitions/AGENTS.md) | 采购申请；**申请的生命周期**：一张表定每个状态能做什么、状态怎样变，状态唯一的写入（`Transition`） | `requisition`、`requisition_line` |
| [`orders`](internal/orders/AGENTS.md) | 采购订单与修订；**订单的生命周期**：一张表定每个状态能做什么、状态怎样变，状态唯一的写入（`Transition`）；从申请行取用 | `purchase_order`、`order_line` |
| [`receipts`](internal/receipts/AGENTS.md) | 收货：经库存入库（同步一次、工作项重发），收货计价依据与撤回；**已收数量只来自库存的入库流水**（答复与流水事件按流水身份去重） | `receipt`、`receipt_line`、`receipt_movement` |
| [`acceptances`](internal/acceptances/AGENTS.md) | 服务验收：按数量或金额，验收事实 | `acceptance`、`acceptance_line` |
| [`returns`](internal/returns/AGENTS.md) | 采购退货：引用收货流水，先扣可退再逐行经库存出库（工作项重发），退货事实 | `purchase_return`、`return_line` |
| [`policies`](internal/policies/AGENTS.md) | 按法人的采购政策：要不要审批、审批人、超收比例 | `purchase_policy` |
| [`orgunits`](internal/orgunits/AGENTS.md) | 部门投影（协议 02）：部门路径复制到单据上 | `org_realign`（投影本身在 SDK 的表里） |
| [`matchbasis`](internal/matchbasis/AGENTS.md) | 系统面：匹配依据（订购、已收按流水、已验收），只给 `erp/finance` | —（只读） |
| [`facts`](internal/facts/AGENTS.md) | 系统面：增量事实按身份读回，任一组件 | —（读 `purchase_fact`） |

模块共用的（不属于某一个业务）：[`masterdata`](internal/masterdata/AGENTS.md)（事务外向主数据取证）、[`core`](internal/core/AGENTS.md)（名字、资源与操作的定义、错误与校验、增量事实 `Fact`；表 `purchase_fact` 留着每个增量事实给 `FactService.Read`）。

**实现进度**（3.0.0 开发中，装配项目阶段计划 C2）：用户面、系统面与恢复核对都已实现（预付申请与价格表按设计答“能力不支持”）；发布随 P5。

| 想改 | 从这里开始 | 然后 |
| --- | --- | --- |
| 订单在某个状态下能不能做某件事；加一个订单状态或命令；新需求会不会和已有规则冲突 | `docs/design.md` 的“订单的状态”（一张表与规则目录 PO01–PO12） | `internal/orders/lifecycle.go`（同一张表）与它的说明里“加一个状态或命令” |
| 申请在某个状态下能不能做某件事；加一个申请状态或命令 | `docs/design.md` 的申请一节（一张表与规则 PR01–PR06） | `internal/requisitions/lifecycle.go`（同一张表）与它的说明里“加一个状态或命令” |
| 审批：谁批、自审怎样拦、待办里的任务、放行 | `docs/design.md` 的 PO03–PO08、PR02–PR06 与“规则要点”的审批人；线协议第 17 章 | `internal/orders/approval.go`、`internal/requisitions/approval.go`、入口 `purchase/module.go` 的 `New`；SDK 的 `flow` 包 |
| 收货、验收、退货怎样走；库存不答复或答复丢了会怎样 | `docs/design.md` 的“收货、验收与退货” | `internal/receipts`、`internal/acceptances`、`internal/returns` 的说明；`itest/receipts_integration_test.go`、`itest/returns_integration_test.go` |
| 给财务、成本的东西（匹配依据、收货计价依据、验收与退货事实） | 契约 `contract/proto`、`contract/events` | `internal/matchbasis`、`internal/facts`、`core.Fact`（每个增量事实都经它） |
| 一个维度、一个操作、列表能筛选的字段 | `internal/core/names.go` | `docs/design.md` 的命令与查询、`BRICKKIT.md` 的契约索引；路由的登记在 `purchase/module.go` |
| 加字段、加接口、改事件 | 装配项目 be-assembly-standard 规范里的“改动配方” | `docs/design.md` 的数据与命令 |

## 构建与测试

- `go test ./...`（`make test`）：单元测试：`purchase/contract_test.go`（清单的 `configSchema` 与 `events`、OpenAPI 与路由、事件载荷、合并安全）；`internal/orders/lifecycle_test.go`、`internal/requisitions/lifecycle_test.go`（生命周期表）。
- `make test-integration`（`go test -tags integration ./itest/`）：真实的 PostgreSQL 与消息系统；每个文件测哪条流程、覆盖哪些规则、要什么前提，在 [itest/AGENTS.md](itest/AGENTS.md)。
- `brickkit lint`

## 设计取舍

- **生命周期是一张表**（同 `erp/sales`）：申请与订单各一张，代码与 `docs/design.md` 逐格相同（单元测试比较），每条规则都有集成测试写明；状态只经各自的 `Transition` 写。通用的表与三项核对在 `core/lifecycle.go`。
- **对库存的命令**（收货、退货）：先在事务里写好单据与工作项，提交之后同步调一次（3 秒），库存的领域错误算拒绝，其余算结果不明，留给工作项按同一身份重发（库存按身份幂等）。
- **收货数量以库存为准**：收货单上的数量是请求；已收只在 `receipts` 的 `record` 里按库存的流水加，答复与流水事件谁先到都一样，按流水身份只算一次。所以库存答复丢了、进程在调用之后崩了，结果都由入库事实补齐（验证故事）。
- **部门路径复制到单据上**（同 `erp/sales`）：SDK 的列表只按本表的列下推组织树维度，所以单据带 `department_path`，部门事件到达时改写，对齐工作项兜底；改写之前有一个窗口（投影的新鲜度）。

## 易错点

- 不要在事务里调主数据或库存：先取证（`internal/masterdata`），再开事务；调库存的命令在事务提交之后，结果不明就留给工作项按同一身份重发。
- 不要在别处加订单行的已收：只经 `receipts` 的 `record`（按流水身份去重）。
- 不要用浮点算金额与数量：乘法经 `core.Times`（`apd`），比较与加减在 SQL 里做。
- 带 `department_path` 的新表要加进 `core.DepartmentTables`，否则部门移动时它的副本不改写。

## 改代码前自查

- 改了契约（`contract/`）：proto 改后 `make -C contract gen`，发布 `contract/v…`，再改这里的依赖；OpenAPI 与路由由测试核对。
- 改了版本号吗？每次改动都升版本。

<!-- brickkit:managed:begin lang=zh -->
## BrickKit

这是一个 BrickKit 组件：平台只读 `component.yaml`。它依赖的规则：

- `configSchema` 的键就是代码读的环境变量名。不能用保留名：`COMPONENT_ID`、`COMPONENT_VERSION`、`PORT`、`BRICKKIT_SERVED_MEMBERS`、`BRICKKIT_SERVED_MEMBERS_CONFIG`，以及任何 `*_ENDPOINT`。
- 依赖写精确版本。依赖的地址以 `<ID>_ENDPOINT` 注入；缺席的可选依赖根本没有这个变量，读的时候要带兜底。
- `/healthz` 只查本进程，不查依赖。迁移命令用同一个镜像跑，遇到不认识的参数必须直接失败；`local.migrationCommand` 从源码跑同一条迁移，给 `mode: local` 用。
- `BRICKKIT.md` 会随版本进入每个使用它的项目，在那里是脱离仓库单独读的：跟代码一起改，不放相对链接。
- 发版：改 `metadata.version`，提交、推送，`brickkit release`。`brickkit lint` 会检查清单和这些文档——在项目里、在这个目录下跑，只查这个组件（`--all` 查整个项目）。
- 完整规则在 `brickkit-component` 技能里（装了技能的项目或仓库根目录下的 `.claude/skills/brickkit-component/SKILL.md`；`brickkit skills update` 会装上）；参数问 `brickkit <命令> --help`；BrickKit 自己的文档用 `brickkit docs` 看。
<!-- brickkit:managed:end -->
