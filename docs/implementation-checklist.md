# HomeLoom 详细实施清单

更新时间：2026-08-01

状态说明：

- `[x]` 已实现并通过自动化测试；
- `[~]` 已实现，但仍需实机、网络或长期运行验证；
- `[ ]` 尚未开始或尚未完成；
- `[!]` 开始后续阶段前必须解决的风险项。

## 0. 当前基线

- [x] 初始化 Git 仓库和 `main` 分支；
- [x] 建立 `backend/`、`frontend/`、`docs/` 和 `scripts/` 分层；
- [x] 项目级 Go、Go Module 和 npm 缓存；
- [x] `.gitignore` 排除缓存、构建产物、数据库和 HAP 配对资料；
- [x] Go 单元测试；
- [x] Go race 检测；
- [x] Vitest 前端测试；
- [x] ESLint；
- [x] TypeScript 和 Vite 生产构建；
- [x] 配置 CI 自动执行上述检查；
- [x] 添加提交前快速检查脚本；
- [x] 确定版本号注入方式；
- [x] 版本号作为打包命令必填参数并通过 linker flags 注入；
- [x] 前端静态资源嵌入 Go，发布为单二进制；
- [x] Linux、macOS、Windows 的 amd64/arm64 多平台打包脚本；

基线验收命令：

```bash
./scripts/dev-env.sh sh -c 'cd backend && go test ./...'
./scripts/dev-env.sh sh -c 'cd backend && go test -race ./internal/command ./internal/eventbus ./internal/state ./internal/application'
./scripts/dev-env.sh sh -c 'cd frontend && npm run test:coverage && npm run lint && npm run build'
```

## 1. M0：HomeKit 技术验证收尾

### 1.1 已实现

- [x] Apple HAP Target；
- [x] HAP Bridge；
- [x] Switch；
- [x] Temperature Sensor；
- [x] HAP UUID 和 keypair 持久化；
- [x] Pairing 数据持久化；
- [x] 标准 HomeKit Setup URI；
- [x] PNG 配对二维码；
- [x] 二维码默认隐藏并按需获取；
- [x] 多桥配置；
- [x] 单桥独立端口；
- [x] 单桥独立 Setup ID；
- [x] 单桥独立身份目录；
- [x] 单桥设备绑定；
- [x] Target 配置拆分为桥实例与桥内虚拟设备两级；
- [x] 桥内虚拟设备使用独立稳定身份并绑定统一模型来源设备；
- [x] 桥内虚拟设备类型可独立于来源统一模型类型选择；
- [x] 单个桥内虚拟设备可按“主来源 → 辅助来源”聚合多个统一设备，手动映射优先且冲突时主来源优先；
- [x] 同一个统一设备可绑定到多个独立 HomeKit 虚拟设备；
- [x] Consumer 属性映射按桥和桥内虚拟设备隔离并支持独立转换；
- [x] 删除桥、删除虚拟设备或更换来源时清理失效 Consumer 路由；
- [x] 单桥热启动、停止和重建；
- [x] 单桥故障不停止 API 和其他桥；
- [x] 前端桥增删改入口；
- [x] 自动生成桥 ID、名称、端口、PIN 和 Setup ID。

### 1.2 实机验收

- [ ] 在同一局域网内启动 HomeLoom；
- [ ] iPhone Apple Home 能发现主桥；
- [ ] 扫描二维码完成配对；
- [ ] 手工输入 PIN 完成配对；
- [ ] Apple Home 显示虚拟开关；
- [ ] 将单属性传感器映射为温度后，Apple Home 显示温度传感器；
- [ ] Apple Home 控制开关后命令进入 `confirmed`；
- [ ] Web 控制开关后 Apple Home 状态同步；
- [ ] 连续重启三次无需重新配对；
- [ ] 修改桥名称后配对关系不丢失；
- [ ] 修改设备绑定后附件图正确更新；
- [ ] 停用桥后 mDNS 和 HAP 端口停止；
- [ ] 再次启用后恢复原配对身份；
- [ ] 两座桥同时运行并分别配对；
- [ ] 两座桥绑定不同设备且不重复发布；
- [ ] 记录 iOS 和 Home Hub 版本；
- [ ] 记录 mDNS、VLAN 和 Docker 网络限制。

### 1.3 风险整改

- [x] PIN 使用 `storage.master_key` 指定的主密钥进行 AES-256-GCM 加密存储；
- [x] 管理 API 使用数据库 Session 认证，健康检查和版本发现保持公开；
- [x] 配对资料和二维码查看受管理员 Session 权限保护；
- [x] 重新生成 PIN/Setup ID 使用 `REGENERATE {id}` 二次确认；
- [x] 删除已配对桥时默认保留身份目录；
- [x] 增加“清除配对身份”独立高风险操作并校验身份路径；
- [x] 启动前检测 HAP 端口是否被外部进程占用；
- [ ] 改善 mDNS/netlink 错误信息；
- [ ] 评估 `brutella/hap` 长期维护和并发行为。

M0 退出条件：Apple Home 实机配对、双向控制、多桥运行和三次重启验证全部通过。

## 2. M1：统一设备模型

### 2.1 模型定义

- [x] 将当前简化 `Device.State.Power/Temperature` 改为通用模型；
- [x] 定义 `Device`；
- [x] 定义 `Endpoint`；
- [x] 定义 `Capability`；
- [x] 定义 `PropertyDefinition`；
- [x] 定义 `CommandDefinition`；
- [x] 定义 `EventDefinition`；
- [x] 定义模型 schema version；
- [x] 定义所有稳定 ID 的格式和字符限制；
- [x] 定义设备可用性：online、offline、unknown；
- [x] 区分 Provider 暂时离线、PostgreSQL 持久禁用和 Provider 删除 tombstone；
- [x] 内置统一模型重整并扩充到 36 种，覆盖环境、安防、空气与温控、门窗、水务、能源、媒体和清洁设备；
- [x] 自定义统一模型支持新增、删除、PostgreSQL 持久化和占用保护；

### 2.2 类型系统

- [x] bool 属性；
- [x] int 属性；
- [x] float 属性；
- [x] string 属性；
- [x] enum 属性；
- [x] 最小值、最大值和步长；
- [x] 单位；
- [x] 读、写和通知权限；
- [x] unknown、null 和 unavailable 语义；
- [x] 属性类型或范围校验失败统一返回 `400 bad_request`；
- [x] JSON 序列化契约；
- [x] 模型表驱动测试。

### 2.3 当前模型契约

- [x] 虚拟开关统一使用 `switch/power` Capability；
- [x] 移除语义不明确的 `single-property-sensor`，温度、湿度及其他测量类型使用独立稳定路径；
- [x] 一次性升级设备 API 和前端到 schema v1；
- [x] HomeKit Target 改为读取 Capability；
- [x] 删除 Target 中对简化 State 字段的直接依赖；
- [x] 更新测试 fixtures；
- [x] 更新设备模型和 API 契约文档。

M1.1 退出条件：新增一种普通设备属性不需要修改 `Device` Go 结构体。

## 3. M1：Provider SDK 和生命周期

### 3.1 Provider 接口

- [x] 定义基础 `Provider` 生命周期接口；
- [x] 定义 Provider Manifest；
- [x] 定义 Provider 类型和版本；
- [x] 定义能力声明；
- [x] 定义可选 `Discoverer`；
- [x] 定义可选 `PropertyReader`；
- [x] 定义可选 `PropertyWriter`；
- [x] 定义可选 `CommandExecutor`；
- [x] 定义可选 `EventSubscriber`；
- [x] 定义 Provider 健康状态；
- [x] 定义初始化、启动、停止和关闭顺序；
- [x] 定义上下文取消语义；
- [x] 定义重连和退避接口。

### 3.2 Provider Manager

- [x] 从 PostgreSQL 加载 Provider 配置；
- [x] 动态创建 Provider；
- [x] 动态启用和停用；
- [x] 单 Provider 热重载；
- [x] 单 Provider 失败隔离；
- [x] 保存最近错误；
- [x] 保存连接和重连状态；
- [x] 提供 Provider 列表 API；
- [x] 提供 Provider CRUD API；
- [x] 前端 Provider 管理页面；
- [x] 凭据字段脱敏；
- [x] Provider Manager 单元测试。

### 3.3 Virtual Provider 重构

- [x] 使用正式 Provider SDK；
- [x] 程序初始化时可选为每种受支持模型各生成一个虚拟设备，配置持久化到 PostgreSQL 且幂等；
- [x] Virtual Provider 根据模型契约自动物化扩充模型的全部必需和可选属性；
- [x] 统一模型参数按必须、可选、自定义分级，并实现 Provider 发布校验与 Consumer 显式映射；
- [x] 统一模型固定使用 Endpoint / Capability / Property 三级属性地址；
- [x] 自定义统一属性 CRUD、PostgreSQL 持久化和模型目录合并；
- [x] 自定义属性完整配置类型、单位、范围、枚举及 R/W/N 权限；
- [x] 支持配置虚拟设备；
- [x] 支持动态新增和删除虚拟设备；
- [x] 支持模拟离线；
- [x] 支持模拟延迟；
- [x] 支持模拟拒绝命令；
- [x] 支持模拟乱序和重复事件；
- [x] 支持测试用 sequence；
- [x] 保持实时状态仅存内存。

退出条件：Virtual Provider 完整使用 SDK，核心不依赖其具体实现。

## 4. M1：事件系统完善

### 4.1 已实现

- [x] 有界事件队列；
- [x] 8 个 Device ID 哈希分片；
- [x] 每分片 128 条容量；
- [x] 同设备事件顺序处理；
- [x] 不同设备并行处理；
- [x] 队列满错误；
- [x] 关闭后拒绝事件；
- [x] 优雅排空；
- [x] Target 独立有界事件队列；
- [x] 慢 Target 不阻塞 Core shard；
- [x] 顺序和边界单元测试；
- [x] race 检测。
- [x] 队列长度、队列满次数、吞吐和处理延迟指标；
- [x] 慢 Handler 检测和事件 trace ID；
- [x] 每秒 100 条事件压力测试；
- [x] 关闭超时测试。

### 4.2 剩余工作

- [ ] 队列容量改为可配置；
- [ ] 分片数量改为可配置；
- [ ] 事件优先级；
- [ ] 丢弃低优先级重复事件；
- [ ] 对相同属性进行事件合并；

## 5. M1：状态系统完善

### 5.1 已实现

- [x] 内存 State Store；
- [x] Provider ID；
- [x] Source；
- [x] Quality；
- [x] ObservedAt；
- [x] ReceivedAt；
- [x] Sequence；
- [x] 内部 Version；
- [x] PendingCommandID 字段；
- [x] 同 Provider sequence 比较；
- [x] 观察时间比较；
- [x] 状态质量比较；
- [x] 接收时间比较；
- [x] 状态诊断 API；
- [x] 实时状态不写 PostgreSQL；
- [x] 重启后由 Provider 重建状态；
- [x] stale TTL、定时扫描和 Provider 离线批量标记；
- [x] unknown 初始状态和乐观状态过期；
- [x] confirmed 覆盖 optimistic、Provider 时间漂移检测；
- [x] 状态变更订阅 API、单一 SSE `/api/v1/events` 和 500 设备压力测试。

### 5.2 剩余工作

- [ ] 不同 Provider 优先级；
- [ ] 属性级 Provider 优先级；
- [ ] 可解释的冲突结果；
- [ ] 可选低频 checkpoint；
- [ ] checkpoint 恢复时强制标记 stale；

退出条件：乱序、重复、延迟和断线恢复场景均有确定结果和自动化测试。

## 6. M1：命令系统完善

### 6.1 已实现

- [x] Command ID；
- [x] queued；
- [x] sent；
- [x] accepted；
- [x] confirmed；
- [x] rejected；
- [x] timeout；
- [x] 同属性新命令替代旧命令；
- [x] 期望 bool 值；
- [x] Provider 状态回报确认；
- [x] 终态保护；
- [x] 命令列表 API；
- [x] 命令详情 API；
- [x] 命令状态机单元测试；
- [x] 异步确认链路测试；
- [x] 通用 Property Value、Action/Command 参数和 Idempotency Key；
- [x] 同设备顺序队列、相同属性去重和旧命令替代；
- [x] outcome-unknown、超时语义和安全重试约束；
- [x] 命令超时、成功率、延迟和历史保留指标；
- [x] 前端命令诊断以及模拟延迟/超时集成测试。

### 6.2 剩余工作

- [ ] Provider 回退策略；

退出条件：开关写入必须经过真实状态确认，超时和未知结果对用户可见。

## 7. M1：持久化与安全

### 7.1 已实现

- [x] PostgreSQL；
- [x] GORM 持久层、`gorm.io/driver/postgres` 与 pgx 驱动；
- [x] SQLite 备选持久层、纯 Go `github.com/ncruces/go-sqlite3/gormlite`、WAL/外键/忙等待配置及无 CGO 多平台构建；
- [x] PostgreSQL 与 SQLite 共用 GORM 模型、CRUD、加密和数据库中立逻辑备份；
- [x] GORM `AutoMigrate` 当前模型同步；
- [x] 移除编号 SQL migration 和旧 schema 自动升级兼容；
- [x] foreign keys；
- [x] PostgreSQL 连接池上限、空闲回收和连接生命周期；
- [x] Target 表；
- [x] Target Device Binding 表；
- [x] 唯一端口约束；
- [x] 唯一 Setup ID 约束；
- [x] 唯一 HAP 身份目录约束；
- [x] 删除旧实时状态表；
- [x] Target CRUD 事务测试。

### 7.2 稳定身份

- [ ] Provider Device ID 到内部 Device ID 绑定表；
- [ ] Logical Device ID 表；
- [ ] Device Endpoint/Capability 身份表；
- [ ] Accessory UUID 映射表；
- [x] AID 持久化表；
- [x] IID 持久化表；
- [x] 设备改名身份不变测试；
- [x] Capability 顺序变化 IID 不变测试；
- [x] 设备离线、禁用或 Provider 删除均不删除稳定身份；
- [ ] 显式删除保留期。

### 7.3 敏感数据

- [x] 自动创建或加载 master key，缺失或损坏时拒绝解密；
- [x] PIN 加密存储及明文原位升级；
- [x] MQTT 密码及 Provider 嵌套敏感字段加密存储；
- [x] OAuth Token 加密存储；
- [x] 密钥文件权限检查；
- [x] 敏感字段日志过滤；
- [x] 配置导出脱敏；
- [x] 诊断包脱敏；
- [ ] 密钥轮换设计；
- [x] 数据库备份使用 PostgreSQL `REPEATABLE READ` 逻辑快照；
- [x] 打开/恢复前验证当前 GORM 模型表完整性。

## 8. M2：HomeKit 基础设备

### 8.1 已支持

- [x] Switch；
- [x] 单属性传感器映射到 Temperature Sensor；
- [x] HomeKit 温度事件实时推送；
- [x] HomeKit 离线 StatusFault 映射；

### 8.2 已补齐

- [x] Outlet；
- [x] Lightbulb；
- [x] 单属性传感器映射到 Humidity Sensor；
- [x] Temperature Humidity Sensor（组合模型提供温度/湿度双 Service）；
- [x] 单属性与温湿度传感器的可选 Battery Level / Low Battery；
- [x] Contact Sensor；
- [x] Motion Sensor；
- [x] Illuminance Sensor；
- [x] Occupancy Sensor；
- [x] Leak Sensor；
- [x] Smoke Sensor；
- [x] Carbon Monoxide Sensor；
- [x] Carbon Dioxide Sensor；
- [x] Air Quality Sensor；
- [x] Fan；
- [x] Air Purifier；
- [x] Filter Maintenance（Air Purifier 链接服务）；
- [x] Thermostat；
- [x] Air Conditioner（HomeKit HeaterCooler，支持开关、模式、温度、风速和摆风）；
- [x] Heater Cooler；
- [x] Humidifier Dehumidifier；
- [x] Lock Mechanism；
- [x] Garage Door Opener；
- [x] Security System；
- [x] Valve；
- [x] Speaker；
- [x] Window Covering。

当前 36 种内置统一模型中，27 种已登记 HomeKit Consumer 合约；机器人吸尘器以及 HAP 没有原生语义的气压、噪声、水位、土壤湿度、水泵、热水器、电力计量和充电桩明确保持不支持，不伪装成开关。详细对应关系见 `docs/homekit-model-support.md`。

每种设备必须完成：

- [x] 统一 Capability 定义；
- [x] HAP Service 映射；
- [x] 可读属性；
- [x] 可写属性及 Filter 标准重置命令；
- [x] 事件通知；
- [x] 离线 StatusFault 表现；
- [x] 单元测试；
- [x] 虚拟设备集成测试；
- [ ] Apple Home 实机记录。

M2 性能验收：

- [x] 100 个模拟附件构建测试；
- [x] 2,000 轮 HAP 属性 burst 更新稳定；
- [x] burst 更新无额外 goroutine 增长；
- [x] 重建后 AID/IID 与已有 Characteristic 身份不变。

## 9. M3：MQTT Provider

### 9.1 配置与生命周期

- [x] MQTT Provider 数据库配置；
- [x] 前端新增 MQTT Provider；
- [x] 区分 MQTT 客户端（连接外部 Broker）与 MQTT 服务端（内嵌 Broker 接受设备连接）运行模式；
- [x] MQTT Client 与 MQTT Server 提升为和小米 Provider 平级的创建入口，表单内不再二次选择运行模式；
- [x] 服务端 TCP/TLS/mTLS 监听、账户认证和设备路由生成的最小权限 ACL；
- [x] 旧 MQTT 配置缺少 `mode` 时兼容为客户端模式；
- [x] Broker 地址；
- [x] TLS/mTLS；
- [x] 用户名和密码；
- [x] Client ID；
- [x] Topic Prefix 与 QoS 移至单设备路由；
- [x] 单设备 Discovery/Availability/State/Command Topic 模板；
- [x] 设备路由严格白名单与订阅冲突校验；
- [x] 设备增删改热订阅并复用现有客户端会话或服务端监听，服务端同步热更新 ACL；
- [x] 单设备显式协议选择（当前 `homeloom-v1`），不再把自有 Payload 契约伪装成任意 MQTT 协议；
- [x] Discovery 完整来源属性进入设备级 Provider Profile，复用统一转换步骤完成枚举、范围和双向映射；
- [x] Retained 状态最大年龄配置；
- [x] Keep Alive；
- [x] 自动重连；
- [x] 指数退避；
- [x] 在线状态；
- [x] 不落库、不替换运行实例的连接测试按钮。

### 9.2 消息协议

- [x] 定义 Discovery Topic；
- [x] 定义 Device Schema；
- [x] 定义 State Topic；
- [x] 定义 Command Topic；
- [x] 定义 Availability Topic；
- [x] 定义 correlation/command ID；
- [x] 定义 schema version；
- [x] JSON Schema；
- [x] 无效消息错误处理与计数；
- [x] 重复消息 sequence 去重；
- [x] Retained Discovery 恢复；
- [x] Retained State 最大年龄策略。

### 9.3 集成测试

- [x] Compose 启动 Mosquitto 开发服务；
- [x] 已配置路由的 retained Discovery 创建设备（Provider 单元测试）；
- [x] MQTT 状态同步至 Registry（跨层 race 测试）；
- [ ] MQTT 状态同步至 Apple Home；
- [ ] Apple Home 命令发布 MQTT；
- [x] MQTT 回报确认命令（跨层 race 测试）；
- [x] Broker 重启恢复（测试内真实 MQTT 5 TCP Broker 原端口重启）；
- [x] 内嵌 MQTT 服务端的真实客户端认证、设备发布与命令下发集成测试；
- [x] 网络中断恢复（Paho 自动重连、重新订阅和恢复命令发布）；
- [x] 不重复创建设备（remote sequence 去重）；
- [x] 不恢复过期实时状态；
- [x] Linux ARM64 Go 交叉构建与容器构建。

M3 退出条件：MQTT 双向链路、断线恢复和命令确认全部通过。

## 10. M4：Mapping Engine

### 10.1 Profile 模型

- [x] Provider Profile（原始属性到统一模型的独立路由、转换和反向写入）；
- [~] Capability Profile（可用于精确属性运行时绑定，Capability 自动生成规则待实现）；
- [x] Target Profile（统一模型到 Consumer 的独立路由、投影、转换和反向写入）；
- [x] Profile ID 和版本；
- [x] Profile schema；
- [x] Profile validator；
- [x] 内置 Profile；
- [x] 用户自定义；
- [x] 数据库存储；
- [x] 导入和导出。

### 10.2 转换能力

- [x] bool 转换；
- [x] 数值范围转换；
- [x] 数值分段到枚举、数值阈值到布尔及显式反向代表值；
- [x] 布尔与枚举双向转换；
- [x] 区间线性映射、取整、数字文本解析和格式化；
- [x] 数值缩放；
- [x] 枚举转换；
- [x] 单位转换；
- [x] 默认值；
- [ ] 缺失属性；
- [ ] 多属性组合；
- [x] 写入反向转换；
- [x] 转换错误解释。

### 10.3 工具

- [x] 映射预览 API；
- [x] 原始 Provider 属性目录（绕过统一模型投影读取 Provider 快照，离线时回退缓存目录）；
- [x] 统一模型结果目录（内置参数与数据库自定义参数合并）；
- [~] HAP Consumer 完整属性目录和运行时投影已实现，逐值结果预览待补充；
- [x] 前端 Profile 管理；
- [x] 前端 Provider → 统一模型 → Consumer 三栏可视化路由管理；
- [x] 三栏路由编辑器移动到对应设备卡片，锁定具体 Provider/设备且不聚合展示其他设备路由；
- [x] 来源属性同步展示当前值、上次值或未知状态，并保留观察时间和读取错误；
- [x] 设备详情仅展示统一模型标准/已登记自定义属性，Provider 完整原始属性只在设备映射入口展示；
- [x] 两段映射分别选择转换 Profile、启停和删除；
- [x] 逐设备展示 Provider → 统一模型的有效恒等默认映射，并允许可视化编辑为数据库覆盖；已有覆盖支持重新载入修改；
- [x] Provider 与 Consumer 两段路由均按具体 Provider/设备隔离，同型号设备互不继承映射；
- [x] 前端统一模型自定义属性三级编辑入口；
- [x] 顶层统一模型页面完整展示所有设备模型、三级属性、参数分级及 Provider/Consumer 规则，并将 Profile 工具收纳为次级入口；
- [x] 映射调试页面；
- [x] Provider 路由热重载（刷新原始快照，不重启 Provider）；
- [x] Consumer 路由热应用（保留 HomeKit 配对身份并重建附件图）；
- [x] 运行时映射命中和失败诊断计数；
- [x] 表驱动转换测试。

退出条件：新增普通 MQTT 设备类型不需要修改 Go 代码。

## 11. Web 管理界面

### 11.1 已实现

- [x] 基础导航；
- [x] 设备卡片；
- [x] 虚拟开关控制；
- [x] Target 列表；
- [x] Target 新增；
- [x] Target 编辑；
- [x] Target 删除；
- [x] Target 设备绑定；
- [x] 配对码展示；
- [x] 二维码按需展示；
- [x] Target 状态展示；
- [x] 基础响应式布局。

### 11.2 功能状态

- [x] Router 和独立页面路由；
- [x] 系统状态页；
- [x] Provider 管理页；
- [x] 设备列表筛选；
- [x] 设备详情页；
- [x] Capability 树；
- [x] 状态来源和质量展示；
- [x] 命令历史页面；
- [~] 实时日志（审计事件已支持 PostgreSQL 历史和 SSE，进程运行日志流待实现）；
- [x] Mapping 预览；
- [x] Profile 管理；
- [x] 完整备份下载、恢复包校验暂存和下次启动前原子应用；
- [x] 错误边界；
- [x] Toast/通知系统；
- [x] Provider/Target 表单服务端字段错误定位；
- [x] Loading 和空状态统一组件；
- [~] 无障碍检查（已补键盘焦点、跳转主要内容、当前导航、状态与错误播报；正式 WCAG 审计待完成）；
- [x] API Client 统一错误模型；
- [x] WebSocket/SSE 实时更新；
- [x] 前端覆盖率报告（HTML、JSON summary 和终端文本）。

### 11.3 管理认证

- [x] 首次启动管理员初始化；
- [x] 登录；
- [x] PostgreSQL Session（24 小时，数据库仅存令牌哈希）；
- [x] CSRF 防护；
- [x] 登录限速；
- [x] 备份、整库恢复、配对参数再生成和配对身份清理使用精确短语二次确认；
- [x] 审计日志；
- [x] 可信代理 IP/CIDR 配置、转发链解析和 Secure Cookie 判断；
- [x] 默认仅监听 `127.0.0.1`。

## 12. API 和可观测性

### 12.1 已实现

- [x] `/health`；
- [x] `/ready`；
- [x] 运行时版本 API；
- [x] Device API；
- [x] Property Write API；
- [x] State Diagnostics API；
- [x] Command API；
- [x] Target CRUD API；
- [x] Pairing QR API；
- [x] 结构化 HTTP 日志。

### 12.2 指标和契约状态

- [x] `/metrics`；
- [x] Provider 在线状态；
- [x] Provider 重连次数；
- [x] 在线/离线设备数量；
- [x] 状态事件计数；
- [x] 队列长度；
- [x] 丢弃事件数；
- [x] 命令成功率；
- [x] 命令延迟；
- [x] 命令超时数；
- [x] HomeKit 推送数；
- [x] PostgreSQL 操作数、平均和最大延迟；
- [x] goroutine 数；
- [~] 内存指标（CPU 指标待长期采样）；
- [x] Request ID；
- [x] Trace/Correlation ID；
- [x] 统一错误响应结构；
- [x] OpenAPI 3.1 文档；
- [x] API 版本兼容策略。

## 13. 测试完善

### 13.1 当前状态

- [x] 后端整体测试；
- [x] 核心 race 检测；
- [x] Event Queue 边界测试；
- [x] State Merge 测试；
- [x] Command State Machine 测试；
- [x] GORM AutoMigrate、约束、事务和 CRUD 测试；
- [x] HTTP API 测试；
- [x] Target 配置逻辑测试；
- [x] 前端二维码交互测试；
- [x] 前端设备绑定表单测试；
- [x] 后端覆盖率报告纳入回归基线（具体数值以当前 CI 报告为准）。

### 13.2 测试状态

- [x] TargetManager 启用桥真实生命周期测试（进程级冒烟测试启动真实 HAP 服务）；
- [x] HAP Server 端口占用测试；
- [x] 多桥并发测试；
- [x] HAP 身份三次重启测试（Setup URI 和身份目录保持稳定）；
- [x] API Target CRUD 集成测试；
- [x] Target 保存失败回滚测试；
- [x] Dispatcher 关闭超时测试；
- [x] Command 多次覆盖测试；
- [x] State stale/optimistic 测试；
- [x] 500 设备基准测试；
- [x] 每秒 100 事件基准测试；
- [x] 20 并发命令测试；
- [ ] 24 小时稳定性测试；
- [x] 前端 API 错误测试；
- [x] 前端 Target/Provider 删除确认测试；
- [x] 前端 Target 编辑回填测试；
- [x] 前端覆盖率阈值；
- [~] 全应用登录、路由、退出和未授权回退集成测试已完成；真实浏览器 E2E 待补。

## 14. 部署

- [x] 多阶段 Dockerfile；
- [x] 前后端单二进制统一镜像；
- [x] Compose 示例；
- [x] 数据卷；
- [x] host network HomeKit 示例；
- [x] bridge network 限制说明；
- [x] CI 构建 amd64/arm64 统一镜像；
- [x] Go 交叉构建 Linux、macOS、Windows 的 amd64/arm64 单二进制；
- [ ] Linux ARM64 实机；
- [~] NAS 部署说明已补充，真实 Synology/QNAP 验收待完成；
- [ ] OpenWrt 可行性验证；
- [x] 进程级优雅停止烟雾验证；
- [x] 容器健康检查；
- [x] 数据库备份脚本；
- [x] 当前 GORM schema、备份和离线回滚文档。

## 15. 后续版本

### v0.2：米家 Provider

- [~] 已记录接口授权边界和 MIT 参考实现来源，正式发布前仍需确认实际 OAuth Client ID/云接口授权；
- [x] OAuth 授权码登录、state 校验、固定 `http://homeassistant.local:8123` 回调地址和完整回调 URL 手工回填引导；
- [x] 米家功能并入 Provider 页面，保留接入引导、中枢发现和运行状态面板；
- [x] Virtual、MQTT、Xiaomi 统一为配置、连接、发现、发布的单列运行视图；
- [x] OAuth/证书、中枢 MQTT 连接与子设备管理分阶段；仅在 Provider 运行后复用现有 MQTT 连接拉取设备目录，并在独立页面生成统一模型映射；
- [x] 子设备映射原地热应用并保留 MQTT 会话，避免相同虚拟 DID 并行登录被中枢拒绝；
- [x] 中枢 Provider 复用现有 OAuth 身份增加官方 MIoT HTTP 云路由，按 DID 合并中枢与账号家庭/房间目录；
- [x] 中枢设备逐台支持 `auto/local/cloud`，`auto` 本地优先并在传输/协议失败或中枢不可控时回退官方云端，显式 `local` 不回退；
- [x] 中枢设备目录与管理页展示本地控制、OAuth 云和事件推送能力，已映射设备可原地修改连接策略；
- [x] 中枢与官方云推送设备改为初始读取后事件驱动；健康推送链路只对不支持 `notify` 的属性执行定时补偿；
- [x] 官方云与中枢共享单个 Provider 生命周期，Token 热更新不创建第二条 MQTT/HTTP 连接，并暴露本地请求、失败、云请求和转云指标；
- [x] 按 `docs/xiaomi-cloud-mips-plan.md` 增加官方云 MQTT/MIPS 长连接，复用现有 `xiaomi` Provider 生命周期，不创建第二个 Provider；
- [x] 进程内 Fake Broker 覆盖 MQTT v5 握手、消息投递、增量订阅、断线重连、订阅拒绝重试，以及旧 Token 认证失败后的热更新恢复；
- [x] 官方云属性和在线状态已改为 MQTT 实时推送；瞬时事件通过独立 Provider → Manager → DeviceService → SSE `device-event` 通道投递，不写入状态仓库；HTTP 已收敛为目录、初始化、控制、重连对账和不可通知属性补偿；
- [x] 官方云初始与补偿属性读取改为有界并发批量请求，移除共享短超时下的逐属性全量轮询；
- [x] 中枢与官方云双推送按属性时间、来源优先级和值摘要归并去重，旧 HTTP 响应不得覆盖更新的 MQTT 推送；
- [x] 状态来源已区分中枢实时、官方云实时和官方云校准，并将 HTTP 快照标记为 `polled`；TTL 跟随通知能力与补偿周期动态计算，统一模型显式 TTL 优先，并具备断线宽限与属性失败指数退避；
- [x] 完整 MIoT 来源属性仅在初始化、推送或主动读取时更新；周期补偿由映射兴趣热同步驱动，只读取默认模型映射和已启用的手工 Provider 映射，移除映射后无需重连即可停止对应补偿；
- [x] 已处理中枢 `devListChange` 并防抖刷新当前目录/Spec；官方云目录以独立低频任务校准，不阻塞属性归并；
- [x] Provider、子设备和设备中心已展示中枢实时、官方云实时和官方云校准；官方云 MQTT 额外展示断线原因、预计重试时间和最近成功时间，且不返回凭据；
- [~] Token 获取、加密持久化、70% 有效期自动刷新和指数退避已实现；主动吊销仍待完成；
- [x] 中枢客户端证书按有效期提前续签，复用 UID、Virtual DID 与 Ed25519 私钥，校验新证书后写回 PostgreSQL 并无断连热应用；
- [x] 中枢 OAuth 已获取家庭 UID；MIoT 云通过合并家庭目录按 DID 获取本人/共享家庭名称与房间名称，并作为设备级映射元数据保存；
- [x] MIoT 云设备支持逐设备 `auto/local/cloud`；`auto` 使用云目录私网 IP/Token 优先执行 LAN MIoT，失败自动回退云端，Token 不经管理 API 暴露；
- [x] MIoT 云发布设备记录实际 `runtimeMode`，设备中心以“局域网 / 云端轮询 / 等待判定”标签展示最近成功的状态同步来源；
- [~] mDNS 中枢发现、设备列表握手和配置设备发现已实现，未映射设备的自动导入仍待完成；
- [x] MIoT Spec V2 实例获取、型号索引解析、PostgreSQL 缓存和完整性错误状态；
- [x] 有界并发属性读取；
- [x] 属性写入；
- [x] Action；
- [x] 属性状态订阅；
- [x] 轮询校准；
- [x] Token 过期前自动恢复，运行时应用失败时复用已持久化的新 Token 重试而不重复换发；
- [x] MQTT 自动重连、离线队列和恢复订阅；
- [x] 首批内置转换 Profile；
- [x] 新增与中枢平行的 `xiaomi-miot-cloud` 第三方兼容 Provider，通过账号设备目录补充 Wi‑Fi 设备；
- [x] MIoT 云端 `prop/get` 分批轮询、`prop/set`、`action`、认证过期单次重登和独立运行指标；
- [x] 中枢与第三方云使用不同 Provider 类型、设备默认 ID 前缀和设备管理接口，不按 DID 自动合并；
- [x] 前端明确标注“第三方兼容”，并为未来官方 `xiaomi-home-cloud` 保留独立命名；
- [x] 第三方云账号密码/导入会话已实现；短信/邮箱二次验证使用 10 分钟内存挑战复用原登录 Cookie，页面引导用户在小米端发送验证码并回填完成登录；
- [ ] 实机测试记录。

### v0.3：Sonoff/eWeLink Provider 初版

- [x] 注册 `sonoff` Provider 并接入统一 Provider 生命周期、设备发现、属性读写和命令接口；
- [x] 支持 `auto/local/cloud` 三种传输模式，LAN 优先、云端 REST 兜底；
- [x] 实现 `_ewelink._tcp.local.` TXT 数据拼接、DIY 明文和 `MD5(devicekey)` → AES-128-CBC/PKCS#7 编解码；
- [x] 支持单路、多路开关、灯具、风扇、窗帘、功率计量、温湿度、门窗和运动等常用 UIID 能力；
- [x] 未知原始参数保留在 `sonoff-raw` 原生能力中；
- [x] 云端 REST 客户端支持家庭/设备目录、状态命令、Token 注入和敏感信息错误脱敏；
- [x] `deviceKey` 纳入 Provider 配置加密、API 脱敏和日志脱敏；
- [x] 为配置、能力映射、LAN/云端客户端和双通道路由补充单元测试；
- [x] Provider 创建流程支持 eWeLink 账号密码登录、区域端点发现、Access Token 回填和保存后 Token 失效自动重新登录；
- [~] LAN mDNS 自动发现、云端 WebSocket 实时推送和实机兼容性待后续完成；
- [ ] Sonoff 真实设备、跨 VLAN mDNS、断网恢复和长时间功率数据实机记录。

### v0.4：Logical Device 和多 Provider 路由

- [ ] Logical Device；
- [ ] Provider Binding；
- [ ] 手动设备链接；
- [ ] 自动匹配候选；
- [ ] 禁止仅按名称自动合并；
- [ ] 属性级路由；
- [ ] 命令级路由；
- [ ] Provider 优先级；
- [ ] 安全回退；
- [ ] 状态冲突解释；
- [ ] 解绑流程；
- [ ] 前端设备链接页面。

### v0.5+：隔离和扩展

- [ ] Provider 独立进程；
- [ ] RPC 协议；
- [ ] API 版本协商；
- [ ] 心跳；
- [ ] 崩溃重启；
- [ ] 日志转发；
- [ ] 资源限制；
- [~] Matter Target 基础架构（详细方案见 [`matter-target-plan.md`](matter-target-plan.md)）：
  - [x] HomeKit/Matter 判别配置、加密 passcode 和类型不可变；
  - [x] Node.js 20+ sidecar 工程与固定 `@matter/main` 版本；
  - [x] Unix Socket JSON-RPC 握手、超时、有界队列、背压、重连与全量重放；
  - [x] Go 提供的加密 Matter KV、Target 命名空间隔离和身份审计；
  - [x] 稳定 Endpoint、tombstone、并发与耗尽测试；
  - [x] Matter Consumer Catalog 与第一批统一模型映射元数据；
  - [x] Matter 专属前端表单、状态、commissioning/Fabric/factory reset 操作；
  - [x] OpenAPI、双段映射、打包和网络排障文档；
  - [x] matter.js 真实 Bridge driver、官方 Endpoint 与 Catalog 对照测试（第一批 12 类 + 第二批 10 类 + 电视，共 23 类）；
  - [ ] matter.js Controller、Apple Home 与 `chip-tool` 真实网络验收；
  - [ ] Apple Home、`chip-tool`、Multi-Admin、三次重启和容器网络实机记录；
  - [ ] 剩余高级设备（空调、车库门、扫地机、能耗/EVSE 等）与正式 CSA 认证准备。
- [ ] Zigbee2MQTT/Tuya/ESPHome 评估。

## 16. 推荐执行顺序

下一批建议严格按以下顺序推进：

1. `[!]` Apple Home 实机配对、双向控制、多桥和三次重启验收；
2. Docker、ARM64、mDNS 与数据卷部署验收；
3. 进程运行日志流与正式无障碍审计；
4. MQTT Broker 中断恢复与 MQTT ↔ HomeKit 实机链路验收；
5. Provider 优先级、逻辑设备聚合和剩余 Mapping 场景；
6. Matter Target 与 Zigbee2MQTT/Tuya/ESPHome 接入评估。

每完成一个步骤都应：

- 更新本清单；
- 增加或更新单元测试；
- 运行 race 检测；
- 运行前端测试、Lint 和构建；
- 更新 API/架构文档；
