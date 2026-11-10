# erp/purchase

采购组件的代码。用法与边界在 `BRICKKIT.md`，为什么这样设计（术语、数据、状态、规则、未决问题）在 [docs/design.md](docs/design.md)。跨组件的规则在装配项目 be-assembly-standard 的协议 05（匹配依据）、协议 05b（收货计价依据、采购退货）、协议 02（部门投影）、协议 07（资格取证）、协议 09（审批）、协议 14（单号、金额、列表）、安全 07（三个维度）。

## 代码地图

代码按业务模块分层。**先读这一节，再进目录**：每个代码目录都有自己的 `AGENTS.md`（同目录的 `CLAUDE.md` 让 Claude Code 读这个目录的文件时自动带上它），写这一层负责什么、不负责什么、每个文件、依赖谁、被谁用、测在哪；读完就知道要不要往下走。

| 目录 | 是什么 |
| --- | --- |
| [`purchase/`](purchase/AGENTS.md) | 入口：组件定义与组装（路由、订阅、后台工作都在这里登记，转给业务模块）；不写业务。`main.go` 与外壳只引用它 |
| [`internal/`](internal/AGENTS.md) | 业务：下面的表里每个模块一个包；模块之间的依赖表在它的 `AGENTS.md`。Go 的 `internal` 让别的组件编译期就引用不了（决定 0101） |
| [`itest/`](itest/AGENTS.md) | 集成测试，按业务流程分文件；它的 `AGENTS.md` 是测试索引（文件 → 流程 → 规则编号） |
| `purchase/migrations/` | 表，整个组件一套（迁移顺序是全组件的，不按模块拆） |
| `contract/` | 本组件发布的契约，单独的模块：`contract/proto/`、`contract/events/`、`contract/openapi.yaml` |
| `docs/` | 设计（规则的正文；各层的 `AGENTS.md` 只指过去，不抄） |

业务模块（`internal/` 下）：

| 模块 | 负责 | 拥有的表 |
| --- | --- | --- |
| [`orders`](internal/orders/AGENTS.md) | 采购订单与修订；**订单的生命周期**：一张表定每个状态能做什么、状态怎样变，状态唯一的写入（`Transition`）；从申请行取用 | `purchase_order`、`order_line` |
| [`requisitions`](internal/requisitions/AGENTS.md) | 采购申请；**申请的生命周期**：一张表定每个状态能做什么、状态怎样变，状态唯一的写入（`Transition`） | `requisition`、`requisition_line` |
| [`policies`](internal/policies/AGENTS.md) | 按法人的采购政策：要不要审批、超收比例 | `purchase_policy` |
| [`orgunits`](internal/orgunits/AGENTS.md) | 部门投影（协议 02）：部门路径复制到单据上 | `org_realign`（投影本身在 SDK 的表里） |

模块共用的（不属于某一个业务）：[`masterdata`](internal/masterdata/AGENTS.md)（事务外向主数据取证）、[`core`](internal/core/AGENTS.md)（名字、资源与操作的定义、错误与校验）。

**实现进度**（3.0.0 开发中，装配项目阶段计划 C2 的 P3）：采购政策、申请、订单的生命周期（到批准、修订、关闭、取消）已做；收货、验收与退货、系统面按 P3d–P3f 依次加进来（还没做的路由答“能力不支持”），每加一个模块就在上面两张表里加一行。

| 想改 | 从这里开始 | 然后 |
| --- | --- | --- |
| 订单在某个状态下能不能做某件事；加一个订单状态或命令；新需求会不会和已有规则冲突 | `docs/design.md` 的“订单的状态”（一张表与规则目录 PO01–PO12） | `internal/orders/lifecycle.go`（同一张表）与它的说明里“加一个状态或命令” |
| 申请在某个状态下能不能做某件事；加一个申请状态或命令 | `docs/design.md` 的申请一节（一张表与规则 PR01–PR06） | `internal/requisitions/lifecycle.go`（同一张表）与它的说明里“加一个状态或命令” |
| 一个维度、一个操作、列表能筛选的字段 | `internal/core/names.go` | `docs/design.md` 的命令与查询、`BRICKKIT.md` 的契约索引；路由的登记在 `purchase/module.go` |
| 加字段、加接口、改事件 | 装配项目 be-assembly-standard 规范里的“改动配方” | `docs/design.md` 的数据与命令 |

## 构建与测试

- `go test ./...`（`make test`）：单元测试：`purchase/contract_test.go`（清单的 `configSchema` 与 `events`、OpenAPI 与路由、事件载荷、合并安全）；`internal/orders/lifecycle_test.go`、`internal/requisitions/lifecycle_test.go`（生命周期表）。
- `make test-integration`（`go test -tags integration ./itest/`）：真实的 PostgreSQL 与消息系统；每个文件测哪条流程、覆盖哪些规则、要什么前提，在 [itest/AGENTS.md](itest/AGENTS.md)。
- `brickkit lint`

## 设计取舍

- **部门路径复制到单据上**（同 `erp/sales`）：SDK 的列表只按本表的列下推组织树维度，所以单据带 `department_path`，部门事件到达时改写，对齐工作项兜底；改写之前有一个窗口（投影的新鲜度）。

## 易错点

- 不要在事务里调主数据或库存：先取证（`internal/masterdata`），再开事务。
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
