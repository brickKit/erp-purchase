# erp/purchase

## 组件定位

采购：申请、采购订单与它们的执行进度。
- **申请**：部门提出的采购需要（产品、数量、需要日期）；提交、批准或驳回；批准后的申请行转成订单行。
- **订单**：一个法人向一个供应商；行是库存物资（经库存入库）或服务与非库存物资（经验收）。单价在订单上录入，订单不含税。草稿提交审批，审批人不能是提交人；法人的采购政策可以不要审批。已批准的订单修改是新修订，回到待审批。
- **收货**：经库存的系统面入库，收货行的身份是给库存与成本的收货行参考；已收数量只以库存的入库流水为准（应答丢了，按库存的流水事件补齐）；超过法人政策的超收比例整笔拒绝。
- **服务验收与采购退货**：服务行按数量或金额验收；退货引用一条入库流水，至多退回它收进的。
- **给下游的**：收货计价依据（给成本暂估）、验收与退货事实；匹配依据（给财务的三单匹配与期末暂估）。
- **数据范围**：申请、订单、收货、验收、退货带法人、部门（组织树）与负责人三个维度。

库存余额（`erp/inventory`）、供应商票据与应付（`erp/finance`）、存货成本（`erp/costing`）、付款不在这里。本版答“能力不支持”（`CAPABILITY_UNSUPPORTED`，D75）的：预付申请、供应商价格表、询价与比价、返利、税额、预算占用、框架协议、直运、寄售。

**3.0.0 开发中**：用户面的路由现在都答“能力不支持”，按装配项目阶段计划 C2 的 P3 依次实现。

## 部署前准备

- 数据库：PostgreSQL 16 及以上；schema `erp_purchase`，属主角色 `erp_purchase_owner`（只供迁移）与运行角色 `erp_purchase_rw`（只有读写数据的权限），名字来自装配项目的注册表。三者由部署方在第一次启动前建好；表由本组件的迁移建，迁移里不建角色、不授权。
- 消息系统（`BUS_URL`）与实现实例（`IMPLEMENTATION_INSTANCE`）：申请、订单、收货单、退货单以完整状态事件，收货计价依据与撤回、验收、采购退货以增量事实发布到流 `erp.purchase`（4 个分区）；订阅 `mdm/org` 的流 `mdm.org` 里的部门事件（全部动作，`mdm.org_unit.>`）。
- 部署的服务证书（`SERVICE_TLS_*`）；身份与授权（`IAM_*`、`AUTHZ_GRPC_URL`、`DEPLOYMENT_ID`）；审计（`AUDIT_GRPC_URL`）。
- 依赖 `mdm/org`、`mdm/partner`、`mdm/product`、`mdm/currency`，经它们的系统面读取；库存是可选的。
- 交易中心之一：负载高时单跑（协议 17）。

## 依赖说明

- 必需：数据库、消息系统、身份成员、授权成员、`mdm/org`、`mdm/partner`、`mdm/product`、`mdm/currency`。
- 可选：`erp/inventory`（缺时库存物资行不能收货与退货，答“依赖不可用”；只有服务行的订单照常）、审计组件。
- 主数据不可达时：新建、修改、提交答“依赖不可用”，已有数据照常读取。

## 配置指南

全部键见 `component.yaml` 的 `configSchema`（由 SDK 生成，测试核对）。

| 键 | 说明 |
| --- | --- |
| `DEPLOYMENT_ID`、`IAM_ISSUER`、`IAM_JWKS_URL`、`AUTHZ_GRPC_URL`、`SERVICE_TLS_CERT_FILE`、`SERVICE_TLS_KEY_FILE`、`SERVICE_TLS_CA_FILE`（必填） | 受保护的用户面与 mTLS 的系统面要用；部署身份、身份与授权的地址在外壳里是进程级的键 |
| `PG_HOST`、`PG_DATABASE`、`PG_USER`、`PG_PASSWORD`、`PG_SCHEMA`（必填） | 自己的数据库，密码是密钥；迁移另用 `PG_OWNER_USER`、`PG_OWNER_PASSWORD` |
| `BUS_URL`（必填，密钥）、`IMPLEMENTATION_INSTANCE`（必填） | 消息系统；实现实例是安装时生成的随机身份 |
| `AUDIT_GRPC_URL` | 审计组件的系统面；缺时审计记录留在本地，按协议 03 处理 |

依赖的地址由 BrickKit 按依赖注入（`MDM_ORG_GRPC_ENDPOINT`、`MDM_PARTNER_GRPC_ENDPOINT`、`MDM_PRODUCT_GRPC_ENDPOINT`、`MDM_CURRENCY_GRPC_ENDPOINT`，装了时还有 `ERP_INVENTORY_GRPC_ENDPOINT`），不是配置键。单号格式 `<前缀>-<法人代码>-<年>-<六位序号>`（前缀 `PR`、`PO`、`GR`、`SA`、`RT`）本版固定；要不要审批、超收比例是按法人的采购政策，经用户面维护。

## 契约索引

- **系统面**（本仓库 `contract/proto`，模块 `github.com/brickKit/erp-purchase/contract/v3`，标签 `contract/v3.…`）：`be.erp.purchase.v3.MatchService/Basis`（订单行的订购、已收、已验收，给 `erp/finance`）、`be.erp.purchase.v3.FactService/Read`（增量事实按身份补取）；操作 `erp.purchase.system.read`。
- **用户面**：`contract/openapi.yaml`。操作 `erp.purchase.requisition.read`、`erp.purchase.requisition.edit`、`erp.purchase.requisition.approve`、`erp.purchase.order.read`、`erp.purchase.order.edit`、`erp.purchase.order.approve`、`erp.purchase.receipt.read`、`erp.purchase.receipt.create`、`erp.purchase.return.read`、`erp.purchase.return.edit`、`erp.purchase.policy.read`、`erp.purchase.policy.manage`；单据带法人、部门、负责人三个维度，采购政策带法人维度。新建、收货、验收、退货要带 `Idempotency-Key`。
- **事件**（流 `erp.purchase`，schema 在 `contract/events/`）：`erp.purchase_requisition.changed.v1`、`erp.purchase_order.changed.v1`、`erp.purchase_receipt.changed.v1`、`erp.purchase_return.changed.v1`（完整状态），`erp.purchase_receipt.valuation_basis.v1`、`erp.purchase_receipt.valuation_basis_withdrawn.v1`、`erp.purchase_acceptance.recorded.v1`、`erp.purchase_return.posted.v1`（增量事实，按 `fact_id` 去重）。订阅 `mdm/org` 部门的全部事件（`mdm.org_unit.>`）。

## 外壳声明

不是外壳。可以被 Go 的核心外壳（`be/go-core`）承载。
