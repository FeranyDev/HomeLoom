# Matter 桥实施方案

> 实施状态（2026-07-24）：第一轮代码基础与第一批 12 类标准 matter.js Endpoint 已落地，包括判别配置、加密身份、稳定 Endpoint、双向 IPC、增量状态、commissioning/Fabric/reset 管理、前端与自动测试。代码侧第二轮基础能力（双实例隔离、崩溃重连、全量重放、100 Endpoint 快照、状态 burst 背压）也已覆盖。Apple Home、`chip-tool`、真实 Multi-Admin、容器网络和连续重启仍需维护者在目标局域网验收；第二批高级设备及 CSA 认证准备仍按第三轮推进，不能以本地自动测试替代。

状态图例：

- `[x]` 已实现，并有自动测试或构建验证；
- `[~]` 代码基础已完成，但仍需真实控制器、网络、容器或规模验收；
- `[ ]` 尚未实施。

## 1. 当前状态与技术路线

HomeLoom 已实现独立的 `matter` Target、Matter Consumer Catalog 和真实 matter.js Bridge Runtime。Matter 与 HomeKit 使用判别配置和独立运行时工厂；不支持的模型会被明确拒绝，不会回退到 HomeKit。

推荐采用“Go 主服务 + matter.js 独立运行时”的结构：

- Go 继续负责 PostgreSQL、统一设备模型、双段映射、设备状态、命令协调、审计和 Target 生命周期；
- Matter 协议栈由 Node.js 20+ sidecar 负责；
- 两者通过本机 Unix Domain Socket 进行异步双向通信；
- 协议一致性测试以官方 ConnectedHomeIP 和 `chip-tool` 为准；
- 第一阶段只实现已有 IP 网络上的 Matter Bridge，不引入 BLE、Wi-Fi 凭据配置或 Thread Border Router。

相比通过 Cgo 直接嵌入 ConnectedHomeIP，独立运行时可以隔离协议栈崩溃、降低 Go 构建复杂度，并允许 Matter SDK 独立升级。matter.js 的依赖版本必须固定到稳定版，不使用 nightly。

> 打包边界：当前发布形态已选择“Go 主服务 + Node.js 20+ sidecar”双制品，Go 二进制仍嵌入 React 管理界面。当前方案不满足严格的单进程单二进制约束；若未来恢复该硬约束，必须验证 Node SEA 独立制品或切换 ConnectedHomeIP 原生路线。

参考资料：

- [matter.js](https://github.com/matter-js/matter.js)
- [ConnectedHomeIP](https://github.com/project-chip/connectedhomeip)
- [Connectivity Standards Alliance](https://csa-iot.org/all-solutions/matter/)

## 2. 技术验证

- [x] 建立独立的 `matter-runtime/` TypeScript 工程；
- [x] 发布形态采用 Go 主服务与 Node.js sidecar 双制品，并在打包文档中声明边界；
- [x] 固定 `@matter/main@0.17.6` 和 Node.js 20+；
- [ ] 验证同一局域网内 on-network commissioning；
- [ ] 验证 Apple Home、matter.js Controller 和 `chip-tool` 三种控制端；
- [~] 已记录 IPv6、UDP、mDNS/Bonjour 在宿主机和容器中的要求，待 Apple Container、Docker 与目标 VLAN 实测；
- [~] 已有双实例 IPC、端口、discriminator 和身份隔离测试，待两座真实网络桥并行验收；
- [x] 记录 `@matter/main@0.17.6`、Matter 1.4.1 和 factory reset shared mDNS 引用等已知边界。

## 3. Target 配置模型重构

当前 `target.Config` 中的 `Address`、`Pin` 和 `SetupID` 实际偏向 HomeKit，需要拆分协议专属配置。

- [x] 保留 Target 公共字段：ID、名称、类型、启用状态、虚拟设备；
- [x] 建立 `HomeKitConfig` 和 `MatterConfig` 两种协议专属配置；
- [x] Matter 配置支持网络接口；
- [x] Matter 配置支持 UDP 监听端口，允许留空自动分配，并在进程生命周期内保持稳定；
- [x] Matter 配置支持 discriminator，允许留空自动生成；
- [x] Matter 配置支持 commissioning passcode，自动生成并加密；
- [x] Matter 配置支持 Vendor ID、Product ID；
- [x] Matter 配置支持产品名、序列号；
- [x] Matter 配置支持 commissioning window 默认时长；
- [x] 前后端使用 Target 类型判别联合，Matter 不复用 HomeKit 字段；
- [x] Target 类型创建后禁止修改；
- [x] 所有普通配置通过 GORM 保存到 PostgreSQL；
- [x] 后端和前端单元测试覆盖默认值、校验、迁移、保密字段与类型不可变。

## 4. Matter 运行时边界

- [x] Go `TargetManager` 支持 `apple-hap` 与 `matter` 两种运行时工厂；
- [x] Go 负责启动、停止、重启和监控 Matter sidecar；
- [x] 使用 Unix Domain Socket + newline-delimited JSON-RPC 2.0 建立双向通信；
- [x] Go → Matter 支持应用桥配置；
- [x] Go → Matter 支持发送全量设备快照；
- [x] Go → Matter 支持发送属性增量和可用性变化；
- [x] Go → Matter 支持打开、关闭 commissioning window；
- [x] Go → Matter 支持删除 Fabric 和恢复出厂身份；
- [x] Matter → Go 支持属性写入；
- [x] Matter → Go 支持 Cluster Command；
- [x] Matter → Go 支持 commissioning 状态与 Fabric 增删事件；
- [x] Matter → Go 支持运行错误和诊断指标；
- [x] IPC 加入请求 ID、超时、有界队列和背压；
- [x] IPC 加入断线重连、版本握手和全量状态重放；
- [x] sidecar 崩溃只重启当前 Target，不阻塞 Device Service 或其他 Target；
- [x] IPC 协议具有独立契约、断线恢复、隔离和 burst 测试。

## 5. 身份与数据库持久化

- [x] 新增 Matter 运行时 KV 存储表，由 Go 提供存储 RPC；
- [x] Fabric、NOC、密钥、计数器等敏感状态使用主密钥 AEAD 加密保存；
- [x] 每个 Matter Target 使用独立存储命名空间；
- [x] 防止一个 Target 读取另一个 Target 的身份数据；
- [x] 主密钥缺失时拒绝加载已有 Matter 身份；
- [x] 配置导出与诊断信息排除 passcode、私钥、证书和 Fabric 凭据；
- [x] 完整备份包含加密后的 Matter 身份数据；
- [x] factory reset 清除旧身份并恢复未配网状态，随后由 supervisor 重启 sidecar；
- [x] 身份读取、写入、轮换和清理进入审计日志。

## 6. 稳定 Endpoint 身份

Matter 桥不能简单按数组序号生成 Endpoint。删除设备后也不能立即把旧 Endpoint ID 分配给另一台设备。

- [x] Endpoint `0` 固定为 Root Node；
- [x] Endpoint `1` 固定为 Aggregator；
- [x] 桥内虚拟设备从 Endpoint `2` 开始分配，最大为 `0xFFFE`；
- [x] 新建 `matter_endpoint_identities` 表；
- [x] 保存 target ID、consumer device ID、endpoint ID、设备类型和 tombstone；
- [x] 对 target ID + endpoint ID 建立复合唯一索引；
- [x] 删除设备后保留 Endpoint tombstone，不立即复用编号；
- [x] 重启、改名、来源设备重连后 Endpoint ID 不变；
- [x] 同一统一设备绑定到多个 Matter 虚拟设备时分别分配稳定 Endpoint；
- [x] Matter 消费端设备自动 ID 包含标准 Device Type 后缀，避免删除映射后以另一类型重建时撞上旧 tombstone；
- [x] Device Type 变更要求输入 `CHANGE ENDPOINT TYPE <targetId> <consumerDeviceId> <deviceType>`，也可选择创建新虚拟设备；
- [x] 分配并发、重启稳定性、删除恢复和编号耗尽测试已覆盖。

## 7. Matter Consumer 属性目录

- [x] 在 Consumer Catalog 中正式注册 `matter`；
- [x] 使用 `Cluster.Attribute` 和 `Cluster.Command` 作为 Consumer 路径；
- [x] 每项声明数据类型、范围、枚举和单位；
- [x] 每项声明读取、写入、订阅和命令方向；
- [x] 继续使用设备级“统一模型 → Matter”映射；
- [x] 复用 Consumer Profile 正反向转换与 Provider 回写链路；
- [x] 同一统一模型属性允许映射到多个 Matter 属性；
- [x] 不支持的模型明确标记，不回退到 HomeKit；
- [x] “消费端设备类型”只列出当前协议的标准 Device Type，非标准逻辑聚合类型不进入 Catalog；
- [x] 前端同时显示中文字段名和原始 Matter Cluster/Attribute 名称；
- [x] Catalog 单元测试及 Catalog ↔ driver 类型/路径精确对照测试已覆盖。

## 8. 第一批设备模型

第一批目标是形成可以被 Apple Home 或 `chip-tool` 添加，并支持实时读写和订阅的基础 Demo。

- [x] 开关 → On/Off Cluster；
- [x] 插座 → On/Off Plug-in Unit；
- [x] 灯 → On/Off、Level Control、Color Control；
- [x] 单属性温度传感器 → Temperature Measurement；
- [x] 单属性湿度传感器 → Relative Humidity Measurement；
- [x] 门磁/接触传感器 → Boolean State；
- [x] 活动/占用传感器 → Occupancy Sensing（PIR）；
- [x] 窗帘 → Window Covering（Lift + Position Aware Lift）；
- [x] 风扇 → Fan Control + On/Off；
- [x] 恒温器 → Thermostat + Thermostat UI Configuration；
- [x] 门锁 → Door Lock（Door Position Sensor）。

HomeLoom 的温湿度组合传感器属于统一模型逻辑类型，Matter 没有对应的标准聚合 Device Type，因此禁止将其直接选为 Matter 消费端设备类型，也不自动拆分 Endpoint。用户需要显式创建标准的 Temperature Sensor 或 Humidity Sensor 消费端映射。

每种设备必须同时完成：

- [x] 使用官方 Matter Device Type 与必需 Cluster；
- [x] 统一模型属性映射；
- [x] 可读属性与数值/枚举/bitmap 换算；
- [x] 可写属性和 Cluster Command 回写；
- [x] 属性变更由 matter.js Interaction/Subscription 机制发布；
- [x] 离线状态映射到 Bridged Device Basic Information `Reachable`；
- [x] 单元测试；
- [x] 官方 `ServerNode + Aggregator + Endpoint` 集成测试。

## 9. 第二批设备模型

- [ ] 空调/Room Air Conditioner；
- [ ] 空气质量、PM2.5、VOC、CO、CO₂；
- [ ] 烟雾、漏水等安全传感器；
- [ ] 阀门、水泵；
- [ ] 车库门；
- [ ] 空气净化器与滤芯状态；
- [ ] 机器人吸尘器；
- [ ] 能耗、电压、电流、功率属性，根据 Matter 规范版本和生态兼容性分级接入；
- [ ] 媒体与音箱最后处理，避免第一阶段引入完整媒体控制模型。

机器人吸尘器需要重新按 Matter 原生设备模型评估，不能沿用 HomeKit Consumer 的“不支持”结论。

第二批类型在 Catalog 与前端中保持未开放，必须遵循“统一模型映射 → 官方 Device Type/Cluster → driver 构造测试 → 控制器验收”的顺序逐类启用，禁止先暴露目录再让 `state.replay` 失败。

## 10. 状态与控制链路

- [x] Core 状态变化通过增量 IPC 推送到 Matter Attribute；
- [x] Matter 写入通过 Consumer 映射反向解析到统一模型；
- [x] 统一模型写入继续通过 Provider 映射写回真实设备；
- [x] Action 映射成 Matter Cluster Command；
- [x] Provider 离线映射到 Bridged Device Basic Information 的可达状态；
- [~] 已区分设备不可达与保留的最后已知属性；属性级 unknown/null 策略仍需随第二批 nullable Cluster 完善；
- [x] sidecar 重启后执行一次握手和全量状态同步；
- [x] host replay 抑制避免 Matter 回写形成状态回环；
- [x] 同设备命令继续使用现有串行队列、超时、合并和 superseded 机制；
- [x] 不同设备的事件和命令保持异步并行；
- [x] 属性 burst 使用有界 IPC 队列并在过载时返回明确背压错误。

## 11. Commissioning 与多 Fabric

Matter 不能只用 HomeKit 的 `paired: boolean` 表达运行状态。

- [x] Target 增加 `commissioningState`；
- [x] Target 增加 `commissioningWindowOpen` 和过期时间；
- [x] Target 增加 `fabricCount` 与安全的 Fabric `id/label` 摘要；
- [x] Target 增加 `endpointCount`；
- [x] 未加入 Fabric 且窗口打开时展示二维码和手工配对码；
- [x] 加入首个 Fabric 且窗口关闭后隐藏 passcode 和二维码；
- [x] 支持临时打开 commissioning window，并要求精确短语确认；
- [x] 支持关闭 commissioning window；
- [x] 展示 Fabric 数量和安全标签，不展示密钥、NOC 或证书；
- [x] 支持删除单个 Fabric；
- [x] 支持完整 factory reset，并在成功返回后重启 sidecar 释放旧 mDNS 资源；
- [x] 多 Fabric 由同一 ServerNode 共享桥内设备状态；
- [x] Fabric 权限、会话和订阅由 matter.js Runtime 管理；
- [x] commissioning、Fabric 删除和 factory reset 进入审计日志。

## 12. 桥接中心前端

- [x] Matter 桥使用真实运行时，不再显示“运行时未实现”；
- [x] 新建 Matter 桥专属配置表单；
- [x] 默认自动生成 discriminator、passcode、Vendor/Product 测试参数；
- [x] 桥卡片展示运行状态；
- [x] 桥卡片展示 commissioning 状态；
- [x] 桥卡片展示 Fabric 数和 Endpoint 数；
- [x] 桥卡片展示实际 UDP 端口、网络接口和 Matter 1.4.1；
- [x] 二维码默认隐藏，仅 commissioning window 打开时允许展示；
- [x] 明确标注“测试设备/未认证”状态；
- [x] 桥内虚拟设备继续复用家庭、房间和设备来源筛选；
- [x] Matter 属性映射页面使用 Cluster → Attribute/Command 可视化结构；
- [x] commissioning、Fabric 删除、Endpoint 类型变更和 factory reset 使用后端校验的精确短语；
- [x] HomeKit 与 Matter 卡片只展示各自协议有意义的配置和操作。

## 13. 测试与验收

### 13.1 自动测试

- [x] Matter Consumer Catalog 单元测试及 driver 精确对照测试；
- [x] Endpoint 分配、tombstone 和重启稳定性测试；
- [x] Go ↔ Matter IPC 合约测试；
- [x] 第一批 12 类标准 Device Type 的属性双向转换与官方 Endpoint 构造测试；
- [x] Window Covering、Fan、Thermostat、Door Lock 等 Command/可写属性回写测试；
- [x] sidecar 断线、崩溃重启与全量恢复测试；
- [x] 数据库加密身份、备份和重启恢复测试；
- [~] 两座 sidecar 的 IPC/身份隔离已自动测试，待两座真实网络桥并行验收；
- [~] Fabric 增删、摘要和 reset 生命周期已自动测试，真实 Multi-Admin 待验收；
- [~] Go 已覆盖 100 个稳定 Endpoint 快照；100 个官方 Endpoint 的控制器订阅压力测试待真实环境执行；
- [x] 状态 burst 和有界队列测试；
- [ ] matter.js Controller 自动化集成测试；
- [ ] 官方 `chip-tool` commissioning/read/write/subscribe 测试。

当前回归基线：

- Go 后端全量测试和 Matter 相关包 `-race` 通过；
- Matter Runtime TypeScript 类型检查与 13/13 测试通过，Catalog ↔ driver 精确覆盖 12 类标准 Device Type；
- 前端 33 个测试文件、172/172 测试和生产构建通过；既有 lint 与嵌入构建基线保持通过；
- OpenAPI YAML 解析与 `git diff --check` 通过。

### 13.2 实机验收

- [ ] Apple Home 首次 commissioning；
- [ ] Apple Home 双向控制与实时状态；
- [ ] Google Home 或另一 Fabric 的 Multi-Admin；
- [ ] 连续重启三次不丢失 Fabric 和 Endpoint 身份；
- [ ] Matter Runtime 中断后自动恢复；
- [ ] 删除 Fabric 后可重新打开 commissioning window；
- [ ] factory reset 后旧 Fabric 无法继续访问。

实机验收由项目维护者执行，不作为日常自动测试的阻塞条件。

## 14. 部署、缓存与文档

- [x] Node.js 依赖保持项目本地，npm 下载缓存、Go 构建缓存和临时构建产物统一放入项目 `.cache`；
- [x] 更新 `scripts/dev-env.sh`，统一 Go、前端和 Matter Runtime 缓存路径；
- [~] 启用 Matter Target 后由 Go 自动拉起并监管 Matter Runtime；前端开发服务器仍需单独启动，尚未提供统一的三进程开发入口；
- [x] Apple Container/Docker 文档补充 IPv6、UDP、mDNS 和 host-network 约束；
- [x] 增加 Matter 端口、防火墙、VLAN、mDNS 和容器网络排障文档；
- [x] 更新 OpenAPI；
- [x] 更新双段映射架构文档；
- [x] 更新配置导出、备份和恢复文档；
- [x] 更新项目实施清单；
- [x] 明确测试 Vendor/Product 与正式 CSA 认证的区别。

## 15. 模块难度与 5.6 子任务分配

模型选择原则：

- `gpt-5.6-sol`：用于协议语义、安全身份、跨进程恢复、官方 Cluster/Device Type 和复杂集成测试等高耦合任务；
- `gpt-5.6-terra`：用于边界明确的 API、前端、文档、构建和机械性测试补齐；
- `medium`：已有清晰接口和验收标准的局部任务；
- `high`：涉及多层联调、状态机或异常路径；
- `xhigh`：涉及协议正确性、安全边界或会影响持久身份的架构决策。

| 子任务 | 范围与主要所有权 | 难度 | 模型 | 推理强度 | 状态与验收 |
| --- | --- | --- | --- | --- | --- |
| A. Target 配置与 IPC 合约 | Go 配置判别联合、sidecar 生命周期、JSON-RPC 合约、OpenAPI | 高 | `gpt-5.6-sol` | `high` | `[x]` Go 测试、IPC 契约和 OpenAPI 校验通过 |
| B. 身份、Endpoint 与恢复 | 加密 KV、Target 隔离、稳定 Endpoint、tombstone、factory reset、全量重放 | 极高 | `gpt-5.6-sol` | `xhigh` | `[x]` 身份/并发/重启/race 测试通过；实机连续重启归入 H |
| C. 第一批官方 Matter 驱动 | `matter-runtime` 的 12 类标准 Device Type、Cluster、属性、Command 与回写 | 极高 | `gpt-5.6-sol` | `xhigh` | `[x]` 类型检查和 12/12 官方 Endpoint 测试通过 |
| D. Consumer Catalog 与映射 | Cluster 路径、正反向转换、默认映射、Catalog ↔ driver 对照 | 中高 | `gpt-5.6-terra` | `high` | `[x]` Catalog、映射和精确对照测试通过 |
| E. 管理 API 与桥接中心 | commissioning/Fabric/reset API、Matter 表单、状态卡片、映射与确认交互 | 中高 | `gpt-5.6-terra` | `high` | `[x]` Go 与前端测试、lint、构建通过 |
| F. 并发、隔离与故障注入 | 双 Target、sidecar 崩溃、背压、burst、100 Endpoint、审计安全 | 高 | `gpt-5.6-sol` | `high` | `[~]` 自动测试完成；真实双桥和 Controller 压力测试归入 H |
| G. 工程化与文档 | 项目缓存、开发环境、容器网络、打包、备份、排障与实施文档 | 中 | `gpt-5.6-terra` | `medium` | `[~]` 文档和缓存完成；统一三进程开发入口待补 |
| H. Controller 与实机验收 | matter.js Controller、`chip-tool`、Apple Home、Multi-Admin、重启与容器网络矩阵 | 高 | `gpt-5.6-sol` | `high` | `[ ]` 下一优先级；必须在目标局域网执行 |
| I. 第二批高级设备 | 空气质量、能耗、阀门、泵、告警、媒体和机器人吸尘器等 | 极高 | `gpt-5.6-sol` | `xhigh` | `[ ]` 按设备逐类派发，不允许一次开放全部 Catalog |
| J. 发布与认证准备 | 生产身份、设备证明链、CSA 决策、互操作性和发布审查 | 极高 | `gpt-5.6-sol` | `high` | `[ ]` 仅在确定正式产品化后启动 |

子任务并行与合并规则：

1. A、B 负责合同与持久身份，相关接口变更必须先合并，C、D、E 不得各自复制协议结构；
2. C、D、E 可在合同冻结后并行，但每新增一种 Device Type，必须由 C 提供正式驱动、D 提供映射、E 确认管理端可配置；
3. F 独立执行故障注入和回归，不在测试中修改生产协议行为；发现缺陷后回派给对应所有者；
4. G 可与代码任务并行，只记录已验证的命令、端口和限制；
5. H 需要真实 Controller、局域网和容器环境，不能用 mock 结果关闭验收项；
6. I 每个设备类型拆成独立子任务，完成“驱动 + 映射 + 单测 + Controller 验收”后才允许合并；
7. J 不与开发测试 Vendor/Product ID 混用，生产凭据不得进入仓库、日志或诊断包。

并发上限建议为 3 个编码子任务加 1 个独立验证子任务；涉及同一文件的工作应串行，避免协议合约、Catalog 和驱动同时产生冲突。

## 16. 推荐实施顺序

### 第一轮：最小 Matter Demo（代码完成，实机退出条件待验收）

- [x] Target 配置模型重构；
- [x] matter.js sidecar 和 IPC；
- [x] Matter 数据库存储；
- [x] Endpoint 稳定身份；
- [x] Matter Consumer Catalog；
- [x] Switch、Light、Temperature Sensor；
- [x] commissioning API 和桥接中心页面；
- [ ] matter.js Controller 与 `chip-tool` 集成测试。

退出条件：[~] 代码已具备加入 Fabric、基础设备实时读写/订阅以及身份持久化能力；仍需用独立 Controller 完成首次配网、双向控制和连续三次重启验收。

### 第二轮：完整基础桥（代码基础完成，真实双桥与 Multi-Admin 待验收）

- [x] 扩充并验证第一批 12 类标准 Matter 设备模型；
- [x] 多 Fabric 管理 API 和 commissioning window 生命周期；
- [x] sidecar 故障恢复与全量重放；
- [~] 100 Endpoint 稳定身份和状态 burst 已自动测试，真实 Controller 订阅压力测试待执行；
- [x] 完善前端映射、诊断和高风险操作。

退出条件：[~] 双 Target 的进程、IPC、数据库和身份隔离已自动验证；仍需两座真实网络桥并行运行、Multi-Admin 和 Runtime 故障恢复实机验收。

### 第三轮：高级设备与发布准备（未开始）

- [ ] 第二批设备模型；
- [ ] 生态差异兼容；
- [ ] 完整 `chip-tool` 回归；
- [ ] Apple Home、Google Home 等实机记录；
- [ ] 安全、部署和认证准备。

### 下一步执行顺序

1. 增加 matter.js Controller 自动化套件，覆盖 commissioning、read、write、subscribe、remove Fabric 和 factory reset；
2. 增加官方 `chip-tool` 回归，固化命令、预期结果和失败日志；
3. 使用 Apple Home 与另一生态完成 Multi-Admin，并执行连续三次服务重启；
4. 在 Apple Container/Docker、host-network、跨 VLAN 三种环境完成网络矩阵验收；
5. 按“一个正式驱动 + 属性/命令测试 + Controller 验收”的门禁逐个接入第二批设备；
6. 发布前单独决策 CSA 认证、生产 Vendor/Product ID 和设备证明凭据。

## 17. 认证边界

matter.js 和 ConnectedHomeIP 都只是实现协议的 SDK，不会自动使 HomeLoom Matter Bridge 获得认证。开发阶段应使用明确标识的测试 Vendor ID、Product ID 和测试凭据；如果未来作为正式 Matter 产品发布，仍需满足 CSA 成员资格、设备证明凭据和产品认证要求。

- [x] UI 和文档明确标识测试 Vendor ID、Product ID 与未认证状态；
- [ ] 确定 HomeLoom 是否以正式 Matter 产品发布；
- [ ] 如正式发布，完成 CSA 成员资格与产品标识申请；
- [ ] 准备 DAC、PAI、PAA/DCL 等生产设备证明链路；
- [ ] 完成正式认证、互操作性测试和发布审查。

在上述发布项完成前，当前实现仅作为开发和测试用 Matter Bridge，不应宣称为已认证产品。

## 18. 实现与验证入口

- 运行时、部署和排障：[matter-runtime.md](matter-runtime.md)
- Go ↔ Matter IPC 与双段映射：[mapping-architecture.md](mapping-architecture.md)
- API 合约：[openapi.yaml](openapi.yaml)
- 打包边界：[packaging.md](packaging.md)
- 项目级交付清单：[implementation-checklist.md](implementation-checklist.md)
- Go Target 实现：[`backend/internal/targets/matter`](../backend/internal/targets/matter)
- matter.js 正式驱动：[`matter-runtime/src/runtime/matter-js-driver.ts`](../matter-runtime/src/runtime/matter-js-driver.ts)
- 第一批设备回归：[`matter-runtime/test/matter-first-batch.test.ts`](../matter-runtime/test/matter-first-batch.test.ts)
- 桥接中心前端：[`frontend/src/components/TargetCard.tsx`](../frontend/src/components/TargetCard.tsx)
