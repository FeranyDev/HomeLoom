# Matter 桥实施方案

## 1. 当前状态与技术路线

HomeLoom 当前只有 `matter` Target 类型占位：运行时会明确拒绝启用，Matter Consumer 属性目录为空，也不会错误回退到 HomeKit。

推荐采用“Go 主服务 + matter.js 独立运行时”的结构：

- Go 继续负责 PostgreSQL、统一设备模型、双段映射、设备状态、命令协调、审计和 Target 生命周期；
- Matter 协议栈由 Node.js 20+ sidecar 负责；
- 两者通过本机 Unix Domain Socket 进行异步双向通信；
- 协议一致性测试以官方 ConnectedHomeIP 和 `chip-tool` 为准；
- 第一阶段只实现已有 IP 网络上的 Matter Bridge，不引入 BLE、Wi-Fi 凭据配置或 Thread Border Router。

相比通过 Cgo 直接嵌入 ConnectedHomeIP，独立运行时可以隔离协议栈崩溃、降低 Go 构建复杂度，并允许 Matter SDK 独立升级。matter.js 的依赖版本必须固定到稳定版，不使用 nightly。

> 打包边界：当前单二进制仅包含 Go 主服务和 React 管理界面。matter.js 是持续运行的 Node.js 协议栈，直接采用 sidecar 会打破“最终设备只运行一个二进制”的约束。进入 Matter Runtime 实现前必须先完成发布形态决策：验证 Node SEA 独立制品，或改用 ConnectedHomeIP 原生实现；如果最终要求严格的单进程单二进制，则不能直接沿用 Node.js sidecar 方案。

参考资料：

- [matter.js](https://github.com/matter-js/matter.js)
- [ConnectedHomeIP](https://github.com/project-chip/connectedhomeip)
- [Connectivity Standards Alliance](https://csa-iot.org/all-solutions/matter/)

## 2. 技术验证

- [ ] 建立独立的 `matter-runtime/` TypeScript 工程；
- [ ] 明确 Matter Runtime 是否允许第二个自包含制品；若不允许，先切换为原生协议栈路线；
- [ ] 固定稳定版 `@matter/main` 和 Node.js 最低版本；
- [ ] 验证同一局域网内 on-network commissioning；
- [ ] 验证 Apple Home、matter.js Controller 和 `chip-tool` 三种控制端；
- [ ] 确认 IPv6、UDP、mDNS/Bonjour 在 Apple Container、Docker 和宿主机上的网络要求；
- [ ] 验证两个 Matter 桥进程同时运行且端口、discriminator、身份完全隔离；
- [ ] 记录选定 SDK 版本对应的 Matter 规范版本和已知兼容性问题。

## 3. Target 配置模型重构

当前 `target.Config` 中的 `Address`、`Pin` 和 `SetupID` 实际偏向 HomeKit，需要拆分协议专属配置。

- [ ] 保留 Target 公共字段：ID、名称、类型、启用状态、虚拟设备；
- [ ] 建立 `HomeKitConfig` 和 `MatterConfig` 两种协议专属配置；
- [ ] Matter 配置支持网络接口；
- [ ] Matter 配置支持 UDP 监听端口，允许留空自动分配；
- [ ] Matter 配置支持 discriminator，允许留空自动生成；
- [ ] Matter 配置支持 commissioning passcode，自动生成并加密；
- [ ] Matter 配置支持 Vendor ID、Product ID；
- [ ] Matter 配置支持产品名、序列号；
- [ ] Matter 配置支持 commissioning window 默认时长；
- [ ] 前后端改为 Target 类型判别联合，杜绝 Matter 复用 HomeKit 字段；
- [ ] Target 类型创建后禁止修改；
- [ ] 所有普通配置通过 GORM 保存到 PostgreSQL；
- [ ] 增加对应的后端和前端单元测试。

## 4. Matter 运行时边界

- [ ] Go `TargetManager` 支持 `apple-hap` 与 `matter` 两种运行时工厂；
- [ ] Go 负责启动、停止、重启和监控 Matter sidecar；
- [ ] 使用 Unix Domain Socket + JSON-RPC 建立双向通信；
- [ ] Go → Matter 支持应用桥配置；
- [ ] Go → Matter 支持发送全量设备快照；
- [ ] Go → Matter 支持发送属性增量和可用性变化；
- [ ] Go → Matter 支持打开、关闭 commissioning window；
- [ ] Go → Matter 支持删除 Fabric 和恢复出厂身份；
- [ ] Matter → Go 支持属性写入；
- [ ] Matter → Go 支持 Cluster Command；
- [ ] Matter → Go 支持 commissioning 状态与 Fabric 增删事件；
- [ ] Matter → Go 支持运行错误和诊断指标；
- [ ] IPC 加入请求 ID、超时、有界队列和背压；
- [ ] IPC 加入断线重连、版本握手和全量状态重放；
- [ ] sidecar 崩溃不得阻塞 Device Service 或其他 Target；
- [ ] IPC 协议建立独立的契约测试。

## 5. 身份与数据库持久化

- [ ] 新增 Matter 运行时 KV 存储表，由 Go 提供存储 RPC；
- [ ] Fabric、NOC、密钥、计数器等敏感状态加密保存；
- [ ] 每个 Matter Target 使用独立存储命名空间；
- [ ] 防止一个 Target 读取另一个 Target 的身份数据；
- [ ] 主密钥缺失时拒绝加载已有 Matter 身份；
- [ ] 配置导出与诊断包排除 passcode、私钥、证书和 Fabric 凭据；
- [ ] 完整备份包含加密后的 Matter 身份数据；
- [ ] 支持独立的“清除 Matter 身份并恢复未配网状态”；
- [ ] 身份读取、写入、轮换和清理进入审计日志。

## 6. 稳定 Endpoint 身份

Matter 桥不能简单按数组序号生成 Endpoint。删除设备后也不能立即把旧 Endpoint ID 分配给另一台设备。

- [ ] Endpoint `0` 固定为 Root Node；
- [ ] Endpoint `1` 固定为 Aggregator；
- [ ] 桥内虚拟设备从 Endpoint `2` 开始分配；
- [ ] 新建 `matter_endpoint_identities` 表；
- [ ] 保存 target ID、consumer device ID、endpoint ID、设备类型和 tombstone；
- [ ] 对 target ID + endpoint ID 建立复合唯一索引；
- [ ] 删除设备后保留 Endpoint tombstone，不立即复用编号；
- [ ] 重启、改名、来源设备重连后 Endpoint ID 不变；
- [ ] 同一统一设备绑定到多个 Matter 虚拟设备时分别分配稳定 Endpoint；
- [ ] 已 commissioning 的设备变更 Matter Device Type 时要求显式确认或创建新虚拟设备；
- [ ] 增加分配并发、重启稳定性、删除恢复和编号耗尽测试。

## 7. Matter Consumer 属性目录

- [ ] 在 Consumer Catalog 中正式注册 `matter`；
- [ ] 使用 `Cluster.Attribute` 和 `Cluster.Command` 作为 Consumer 路径；
- [ ] 每项声明数据类型、范围、枚举和单位；
- [ ] 每项声明读取、写入、订阅和命令方向；
- [ ] 继续使用设备级“统一模型 → Matter”映射；
- [ ] 支持 Consumer Profile 正反向转换；
- [ ] 同一统一模型属性允许映射到多个 Matter 属性；
- [ ] 不支持的模型明确标记，不回退到 HomeKit；
- [ ] 前端同时显示中文字段名和原始 Matter Cluster/Attribute 名称；
- [ ] Consumer Catalog 和默认映射同步增加单元测试。

## 8. 第一批设备模型

第一批目标是形成可以被 Apple Home 或 `chip-tool` 添加，并支持实时读写和订阅的基础 Demo。

- [ ] 开关 → On/Off Cluster；
- [ ] 插座 → On/Off Plug-in Unit；
- [ ] 灯 → On/Off、Level Control、Color Control；
- [ ] 单属性温度传感器 → Temperature Measurement；
- [ ] 单属性湿度传感器 → Relative Humidity Measurement；
- [ ] 温湿度组合传感器；
- [ ] 门磁/接触传感器；
- [ ] 人体存在/占用传感器；
- [ ] 窗帘 → Window Covering；
- [ ] 风扇 → Fan Control；
- [ ] 恒温器 → Thermostat；
- [ ] 门锁 → Door Lock。

每种设备必须同时完成：

- [ ] Matter Device Type 与必需 Cluster；
- [ ] 统一模型属性映射；
- [ ] 可读属性；
- [ ] 可写属性和 Command；
- [ ] 状态订阅；
- [ ] 离线/不可达状态；
- [ ] 单元测试；
- [ ] Matter Runtime 集成测试。

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

## 10. 状态与控制链路

- [ ] Core 状态变化实时推送到 Matter Attribute；
- [ ] Matter 写入通过 Consumer 映射反向解析到统一模型；
- [ ] 统一模型写入继续通过 Provider 映射写回真实设备；
- [ ] Action 映射成 Matter Cluster Command；
- [ ] Provider 离线映射到 Bridged Device Basic Information 的可达状态；
- [ ] 未知值与暂时不可用值保持区分；
- [ ] sidecar 重启后执行一次全量状态同步；
- [ ] 防止 Matter 回写产生状态回环；
- [ ] 同设备命令继续使用现有串行队列、超时、合并和 superseded 机制；
- [ ] 不同设备的事件和命令保持异步并行；
- [ ] 属性 burst 不得无界扩张 IPC 队列。

## 11. Commissioning 与多 Fabric

Matter 不能只用 HomeKit 的 `paired: boolean` 表达运行状态。

- [ ] Target 增加 `commissioningState`；
- [ ] Target 增加 `commissioningWindowOpen`；
- [ ] Target 增加 `fabricCount`；
- [ ] Target 增加 `endpointCount`；
- [ ] 未加入 Fabric 时展示二维码和手工配对码；
- [ ] 加入首个 Fabric 后默认隐藏 passcode 和二维码；
- [ ] 支持临时打开 commissioning window；
- [ ] 支持关闭 commissioning window；
- [ ] 展示 Fabric 数量和安全标签，不展示密钥信息；
- [ ] 支持删除单个 Fabric；
- [ ] 支持完整 factory reset；
- [ ] 多 Fabric 共享同一组桥内设备状态；
- [ ] Fabric 权限、会话和订阅由 Matter Runtime 管理；
- [ ] commissioning、Fabric 删除和 factory reset 进入审计日志。

## 12. 桥接中心前端

- [ ] Matter 桥不再显示“运行时未实现”；
- [ ] 新建 Matter 桥专属配置表单；
- [ ] 默认自动生成 discriminator、passcode、Vendor/Product 测试参数；
- [ ] 桥卡片展示运行状态；
- [ ] 桥卡片展示 commissioning 状态；
- [ ] 桥卡片展示 Fabric 数和 Endpoint 数；
- [ ] 桥卡片展示网络接口和协议版本；
- [ ] 二维码默认隐藏，仅 commissioning window 打开时允许展示；
- [ ] 明确标注“测试设备/未认证”状态；
- [ ] 桥内虚拟设备继续复用家庭、房间和设备来源筛选；
- [ ] Matter 属性映射页面使用 Cluster → Attribute/Command 可视化结构；
- [ ] 高风险操作使用精确短语确认；
- [ ] HomeKit 与 Matter 卡片只展示各自协议有意义的配置和操作。

## 13. 测试与验收

### 13.1 自动测试

- [ ] Matter Consumer Catalog 单元测试；
- [ ] Endpoint 分配、tombstone 和重启稳定性测试；
- [ ] Go ↔ Matter IPC 合约测试；
- [ ] 属性双向转换测试；
- [ ] Command 回写测试；
- [ ] sidecar 崩溃与自动恢复测试；
- [ ] 数据库身份恢复测试；
- [ ] 两座 Matter 桥并行测试；
- [ ] 多 Fabric 测试；
- [ ] 100 个动态 Endpoint 构建测试；
- [ ] 状态 burst 和有界队列测试；
- [ ] matter.js Controller 自动化集成测试；
- [ ] 官方 `chip-tool` commissioning/read/write/subscribe 测试。

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

- [ ] Node.js 依赖和构建缓存放入项目 `.cache`；
- [ ] 更新 `scripts/dev-env.sh`，统一 Go、前端和 Matter Runtime 缓存路径；
- [ ] 开发脚本支持同时启动 Go、前端和 Matter Runtime；
- [ ] Apple Container/Docker 增加 IPv6、UDP、mDNS 和 host-network 说明；
- [ ] 增加 Matter 端口、防火墙和 VLAN 排障文档；
- [ ] 更新 OpenAPI；
- [ ] 更新双段映射架构文档；
- [ ] 更新配置导出、备份和恢复文档；
- [ ] 更新项目实施清单；
- [ ] 明确测试 Vendor/Product 与正式 CSA 认证的区别。

## 15. 推荐实施顺序

### 第一轮：最小 Matter Demo

1. Target 配置模型重构；
2. matter.js sidecar 和 IPC；
3. Matter 数据库存储；
4. Endpoint 稳定身份；
5. Matter Consumer Catalog；
6. Switch、Light、Temperature Sensor；
7. commissioning API 和桥接中心页面；
8. matter.js Controller 与 `chip-tool` 集成测试。

退出条件：Matter 桥可以加入一个 Fabric，三种基础设备可以实时读取、订阅和双向控制，服务重启后 Fabric 与 Endpoint 身份保持不变。

### 第二轮：完整基础桥

1. 扩充第一批设备模型；
2. 多 Fabric 和 commissioning window；
3. sidecar 故障恢复与全量重放；
4. 100 Endpoint 和状态 burst 测试；
5. 完善前端映射、诊断和高风险操作。

退出条件：两座桥可并行运行，多 Fabric 独立管理，Provider 离线和 Runtime 重启均可恢复。

### 第三轮：高级设备与发布准备

1. 第二批设备模型；
2. 生态差异兼容；
3. 完整 `chip-tool` 回归；
4. Apple Home、Google Home 等实机记录；
5. 安全、部署和认证准备。

## 16. 认证边界

matter.js 和 ConnectedHomeIP 都只是实现协议的 SDK，不会自动使 HomeLoom Matter Bridge 获得认证。开发阶段应使用明确标识的测试 Vendor ID、Product ID 和测试凭据；如果未来作为正式 Matter 产品发布，仍需满足 CSA 成员资格、设备证明凭据和产品认证要求。
