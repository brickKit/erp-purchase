# purchase/：入口与组装

组件对外的一个包：`main.go` 与外壳只引用它（`purchase.Definition()`、`purchase.New`）。这里只**组装**：把 `internal/` 下业务模块的函数登记成 SDK 要的路由、gRPC 方法、订阅、后台工作与恢复核对；业务一行也不写。

## 负责与不负责

- 负责：组件的身份与版本、迁移、把哪个路由交给哪个模块、哪个后台工作交给哪个模块。
- 不负责：业务规则（去 [internal/AGENTS.md](../internal/AGENTS.md) 找模块）；资源、操作、维度、事件类型的名字（`internal/core/names.go`）；集成测试（`itest/`）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `definition.go` | `Version`、端口、迁移；`Definition()` 把身份、流、资源与操作（`core.Definitions()`）收成组件定义 |
| `module.go` | `Module` 与 `New`（建流程内核：两种流程、钩子按单据类型转给 `orders`、`requisitions`，装了 `mdm/employee` 时目录是员工投影；`FlowKernel` 交给宿主登记标准路由，`Start` 运行员工投影）；`Routes`（用户面的每条路由 → 模块的处理函数与操作；预付申请与价格表答“能力不支持”）、`Services`（系统面：匹配依据只许 `erp/finance`，事实读取任一组件）、`Subscriptions`（部门事件 → `orgunits`，库存的流水 → `receipts`，员工的流 → 内核的员工投影）、`Jobs`（每个后台工作 → 模块；内核的重新解析与心跳）；`handler`、`job`、`received` 把“先收 `*core.Base`”的模块函数适配成 SDK 的签名 |
| `services.go` | gRPC 方法转给模块：`Basis` → `matchbasis`，`Read` → `facts` |
| `checks.go` | 整套恢复的核对（线协议第 14 章）：审计的每种对象按版本答“还在不在”（收货与验收同一种资源，验收的版本恒为 1；政策以法人为身份） |
| `contract_test.go` | 单元测试：清单的 `configSchema` 与 `events`、OpenAPI 与路由一一对应、事件载荷合乎 schema、合并安全 |
| `migrations/` | 表，整个组件一套 |

## 依赖与被依赖

- 依赖：`internal/` 下除 `masterdata` 以外的全部模块（它只被模块用）。
- 被依赖：`main.go`、外壳、`itest/`。
- 模块不能反过来引用这里（会成环）：模块需要的名字放 `internal/core`。

## 加东西

- 新路由：处理函数写在它所属的模块里，在 `module.go` 的 `Routes` 登记（替换掉 `todo`），操作的名字在 `internal/core/names.go`，OpenAPI 改 `contract/openapi.yaml`（`TestTheRoutesAreTheOpenAPIDocument` 核对两边）。
- 新后台工作：函数写在模块里，在 `Jobs` 登记；工作名作为模块的导出常量（例：`orgunits.JobRepairOrgUnits`）。

## 测试

`go test ./purchase/`。
