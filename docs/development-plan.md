# HomeLoom 开发计划

## 1. 项目定位

HomeLoom 是一个独立运行的智能设备聚合桥，将不同平台的设备转换为统一模型，并发布到 Apple Home。

```text
Virtual / MQTT / Xiaomi / 其他平台
                 │
                 ▼
              Provider
                 │
                 ▼
     Device Registry + State Store
          + Command Router
                 │
                 ▼
               Target
                 │
                 ▼
             Apple Home
```

项目不依赖 Home Assistant。首条主线只解决以下问题：

1. 设备接入和统一建模；
2. 状态可靠同步；
3. Apple Home 命令可靠回传；
4. 设备身份及 HomeKit 配对信息永久稳定；
5. Provider 暂时失效时，Apple Home 中的附件不消失。

## 2. 范围控制

### 2.1 v0.1 范围

- Go 后台服务；
- Virtual Provider；
- MQTT Provider；
- Apple HAP Target；
- SQLite 持久化；
- YAML 配置；
- Switch、Light、Temperature Sensor、Humidity Sensor；
- 健康检查、结构化日志和基础指标；
- Docker 和 Linux ARM64 构建。

### 2.2 v0.1 非目标

- 米家接入；
- Web 管理界面；
- 多 Provider 设备合并和自动回退；
- 第三方插件系统；
- Matter、Google Home、Alexa；
- Camera、Doorbell、Lock、Security System；
- 复杂自动化和远程云管理。

这些功能只在基础链路稳定后进入后续版本。

## 3. 成功标准

v0.1 必须满足：

- Apple Home 能添加 HomeLoom Bridge；
- 虚拟设备和 MQTT 设备能稳定发布；
- 状态变化能在 2 秒内同步到 Apple Home；
- Apple Home 控制能传回 Provider，并得到真实状态确认；
- 服务重启后不需要重新配对；
- 设备改名、换房间或短暂离线时附件身份不变；
- MQTT 断开并恢复后不重复创建附件；
- 100 个模拟附件连续运行 24 小时无明显内存增长；
- amd64 和 arm64 Docker 镜像能够启动。

## 4. 核心原则

### 4.1 平台解耦

Provider 只处理平台通信和标准化，Target 只消费统一模型。二者不得直接调用。

平台原生字段，如 MIoT `siid/piid/did`、Tuya `dpId` 和 HomeKit Characteristic，只能存在于适配层或映射配置中。

### 4.2 稳定身份

以下标识生成后必须持久化：

- Provider 设备与内部 Device ID 的绑定；
- Accessory UUID；
- AID；
- Service/Characteristic IID；
- Bridge identity；
- HomeKit pairing 信息。

离线不是删除。只有显式删除并经过保留期后，才允许撤下附件。

### 4.3 状态以设备确认值为准

Apple Home 写入后可以短暂显示乐观值，但必须等待 Provider 上报或读取确认。超时状态不能永久覆盖真实状态。

### 4.4 有界并发

事件进入有界队列，并按 Device ID 分片。同一设备顺序处理，不同设备并行处理。不得为每条事件无限创建 goroutine。

### 4.5 先验证再抽象

接口以 Virtual Provider、MQTT Provider 和 Apple HAP Target 的真实需求为依据。首版不提前设计动态插件市场或通用远程 RPC。

## 5. 技术选型

| 领域 | 方案 |
|---|---|
| 主语言 | Go |
| HTTP | `echo` |
| 配置 | SQLite；YAML 仅用于进程启动参数 |
| 数据库 | SQLite + migrations + WAL |
| MQTT | Eclipse Paho `autopaho` |
| 日志 | `log/slog` |
| 指标 | Prometheus Client |
| Apple HAP | 阶段 0 PoC 后确定库和封装边界 |
| 测试 | Go testing，按需使用 Testify |
| 部署 | Docker / Docker Compose |

依赖版本需锁定。HomeKit 库、mDNS、Docker 网络和 ARM64 构建必须先做技术验证。

## 6. 目标目录结构

初期只创建实际使用的目录：

```text
HomeLoom/
├── cmd/
│   └── homeloom/
├── internal/
│   ├── config/
│   ├── runtime/
│   ├── device/
│   ├── registry/
│   ├── state/
│   ├── command/
│   ├── eventbus/
│   ├── persistence/
│   ├── mapping/
│   ├── api/
│   └── diagnostics/
├── providers/
│   ├── virtual/
│   └── mqtt/
├── targets/
│   └── homekit/
├── profiles/
├── migrations/
├── configs/
├── testdata/
├── Dockerfile
├── compose.yaml
└── go.mod
```

米家、Web、Matter 和插件目录在开始对应里程碑时再创建。

## 7. 核心模型

### 7.1 设备层级

```text
Device
└── Endpoint
    └── Capability
        ├── Property
        ├── Command
        └── Event
```

一个物理设备默认对应一个逻辑附件。例如空气净化器的净化、空气质量、滤芯、温湿度服务应组合在同一附件中。

### 7.2 属性定义

属性不得只使用无约束的 `any`。每个 Property Definition 至少包含：

- 稳定 ID；
- 数据类型：bool、int、float、string、enum；
- 读、写、通知权限；
- 单位；
- 最小值、最大值、步长；
- 枚举集合；
- stale TTL；
- schema version。

属性运行状态包含：

```go
type StateValue struct {
    Value            Value
    SourceProviderID string
    Source           StateSource
    ObservedAt       time.Time
    ReceivedAt       time.Time
    Sequence         uint64
    Version          uint64
    Quality          StateQuality
    PendingCommandID string
}
```

状态质量：`confirmed`、`reported`、`polled`、`optimistic`、`stale`、`unknown`。

### 7.3 状态归并

状态归并必须实现为可独立测试的确定性函数，依次考虑：

1. 同一 Provider 内可比较的 sequence；
2. 设备实际产生时间；
3. 状态质量；
4. 接收时间；
5. 配置的 Provider 优先级。

不同 Provider 的 sequence 不得直接比较。设备时钟异常时退回接收时间。乐观状态必须有过期时间。

## 8. 接口边界

### 8.1 Provider

基础 Provider 只负责生命周期和能力声明：

```go
type Provider interface {
    Manifest() Manifest
    Initialize(context.Context, Runtime) error
    Capabilities() ProviderCapabilities
    Close(context.Context) error
}
```

按能力使用可选接口：

```text
Discoverer
PropertyReader
PropertyWriter
CommandExecutor
EventSubscriber
```

这样不要求所有平台伪造其不支持的认证、发现或订阅能力。

### 8.2 Target

Target 采用声明式协调思路：

- 接收当前期望设备图；
- 与持久化身份和 Target 当前状态对账；
- 创建或更新服务；
- 对离线设备只更新可用性；
- 仅对显式删除且超过保留期的设备执行移除；
- 失败后可安全重试。

### 8.3 Command

统一命令至少包含：

- Command ID 和幂等键；
- Device/Endpoint/Capability/Property；
- 目标值或动作参数；
- 创建时间和截止时间；
- 是否允许重试或回退；
- 期望状态断言。

状态流：

```text
queued → sent → accepted → confirmed
                     ├── rejected
                     ├── timeout
                     └── outcome-unknown
```

超时不自动等于执行失败；非幂等命令不得自动发送到第二个 Provider。

## 9. 持久化设计

首版至少包含：

```text
schema_migrations
providers
devices
device_endpoints
device_capabilities
property_states
pending_commands
mapping_profiles
mapping_bindings
hap_bridges
hap_accessories
hap_instances
hap_pairings
system_settings
```

要求：

- 所有 schema 通过 migration 管理；
- 写操作使用明确事务边界；
- 启用 WAL 并设置 busy timeout；
- JSON 字段带 schema version；
- 时间统一使用 UTC 和明确精度；
- 外键和唯一约束必须开启；
- pairing、Token 和私钥加密存储；
- 备份使用 SQLite 一致性备份机制，不直接复制活动数据库文件。

## 10. 配置与安全

默认管理 API 只监听 `127.0.0.1`。开放至局域网必须显式配置，并启用认证。

```yaml
server:
  address: 127.0.0.1:8090

storage:
  database: /data/homeloom.db
```

Provider、Target、设备绑定、映射和运行时开关统一存入 SQLite，由管理 API 修改。YAML 不得覆盖这些数据库配置。

安全要求：

- 凭据、Token、pairing 信息和私钥不得记录到日志；
- 配置导出和诊断包默认脱敏；
- 数据目录和密钥文件限制权限；
- API 返回值不得包含敏感配置；
- 所有日志字段经过敏感字段过滤；
- Web 登录使用数据库 Session、登录限速、CSRF 防护和审计日志；反向代理来源仅在显式配置可信代理后使用。

## 11. 开发里程碑

每个里程碑必须独立可运行、可测试、可验收。上一阶段的退出条件未满足时，不进入下一阶段。

### M0：技术验证

目标：消除 HomeKit 路线中的最大不确定性。

工作内容：

- 创建最小 Go 项目；
- 验证 HAP Bridge 和 mDNS；
- 发布一个虚拟开关和温度传感器；
- 验证 Apple Home 双向控制；
- 验证 pairing、UUID、AID、IID 持久化；
- 验证 Docker host/network 模式；
- 验证 Linux ARM64 构建和启动。

退出条件：

- Apple Home 成功添加 Bridge；
- 连续重启三次无需重新配对；
- 改名后附件和自动化关系不丢失；
- ARM64 环境能正常启动和广播。

### M1：核心最小闭环

目标：建立 Virtual Provider → Core → HomeKit Target 的完整链路。

工作内容：

- 统一设备和属性类型；
- Runtime 生命周期；
- Registry；
- 有界事件总线和按设备分片 Dispatcher；
- State Store 和状态归并函数；
- 基础 Command Router；
- SQLite migrations；
- YAML 配置校验；
- 优雅关闭。

退出条件：

- Virtual Provider 可动态创建和更新设备；
- Apple Home 命令可返回 Provider；
- 状态乱序测试结果确定且可解释；
- 重启后恢复设备身份和主要状态；
- `go test ./...` 通过。

### M2：HomeKit 基础设备

目标：稳定支持首批设备类型。

按顺序实现：

1. Switch；
2. Light；
3. Temperature Sensor；
4. Humidity Sensor。

工作内容：

- Target Profile；
- Service/Characteristic 映射；
- 离线和 stale 状态；
- 设备更新与显式删除策略；
- 100 个附件压力测试。

退出条件：

- 四类设备在 Apple Home 正确显示和控制；
- 重启、改名和短暂离线均不改变附件身份；
- 高频状态事件不会导致无界内存或 goroutine 增长。

### M3：MQTT Provider

目标：用真实外部协议验证 Provider 边界。

工作内容：

- 连接、认证、自动重连；
- Topic 订阅和命令发布；
- Availability、QoS、Retained Message；
- 设备发现消息 schema；
- 状态恢复和去重；
- MQTT 集成测试环境和 Compose 示例。

退出条件：

- MQTT 设备能自动创建且不重复；
- MQTT → Apple Home 状态同步正常；
- Apple Home → MQTT 命令及确认正常；
- Broker 重启后自动恢复连接和设备状态。

### M4：映射引擎

目标：普通设备映射不再需要修改 Go 代码。

工作内容：

- Provider Profile；
- Capability Profile；
- Target Profile；
- bool、数值、枚举和单位转换；
- Profile schema、版本和校验器；
- 用户覆盖；
- 映射预览和诊断输出。

退出条件：

- 新增普通 MQTT 设备映射无需重新编译；
- 错误 Profile 在加载时给出定位明确的错误；
- 映射转换具有完整表驱动测试。

完成 M0—M4 后发布 v0.1.0。

### M5：米家 Provider 原型

开始前提：v0.1.0 已稳定运行，米家接口的授权、许可及长期可用性已完成评估。

工作内容：

- 账号认证与 Token 生命周期；
- 家庭、房间和设备发现；
- MIoT Spec 获取和缓存；
- 批量读取、属性写入、Action；
- 事件订阅、轮询校准和断线恢复；
- 首批 Switch、Outlet、Light 和温湿度设备。

退出条件：

- Provider 可被独立禁用且不影响核心；
- Token 过期和网络中断可恢复；
- 原生 `siid/piid/did` 不进入核心模型；
- 实机测试记录完整。

### M6：管理 API 和 Web UI

目标：无需手改 YAML 即可完成日常配置和诊断。

优先页面：

- 系统状态；
- Provider/Target 管理；
- 设备列表和详情；
- 实时状态来源；
- 命令测试；
- 映射预览；
- HomeKit 配对；
- 日志和脱敏诊断包；
- 备份与恢复。

### M7：Logical Device 与多 Provider 路由

目标：同一物理设备可绑定多个来源而不重复发布。

首版只支持用户手动链接。自动匹配只生成候选，不自动合并。

路由按属性和命令配置，并区分安全重试、不可重试和 outcome unknown。完成状态冲突解释、Provider 回退和解绑流程后发布 v0.4.0。

### M8：扩展与隔离

在两个以上 Provider 验证核心模型后，再考虑：

- Provider 独立进程；
- RPC 协议和版本协商；
- 崩溃恢复和资源限制；
- Zigbee2MQTT、Tuya 或 ESPHome；
- Matter Target。

第三方插件市场不属于 v1.0 必需项。

## 12. 测试策略

### 单元测试

- 状态归并和 stale 计算；
- 数值、枚举和单位转换；
- 命令状态机、超时和幂等；
- 稳定 UUID/AID/IID 分配；
- Profile 匹配和校验；
- 配置校验。

### 集成测试

```text
Virtual Provider → Core → HomeKit Target
MQTT Provider → Core → HomeKit Target
SQLite restart/recovery
```

### 故障测试

- 网络和 MQTT 断开；
- 重复、乱序、延迟事件；
- Provider 无响应或崩溃；
- 数据库 busy 和异常退出；
- 设备离线；
- Apple Home 高频控制；
- 乐观状态确认超时。

### 性能基线

v0.1 基线：

- 100 台设备；
- 500 个属性；
- 每秒 100 条状态事件；
- 20 个并发命令；
- 连续运行 24 小时无明显内存增长。

v1.0 目标再提升到 500 台设备、2000 个能力和 7 天稳定运行。

## 13. 可观测性

接口：

```text
GET /health   进程是否存活
GET /ready    数据库及必要 Target 是否就绪
GET /metrics  Prometheus 指标
```

核心指标：

- Provider 连接状态和重连次数；
- 在线、离线、stale 设备数量；
- 事件吞吐、队列长度和丢弃量；
- 命令成功率、延迟、超时和 unknown outcome；
- HomeKit 推送和失败次数；
- SQLite 延迟和错误数；
- goroutine、内存和 CPU。

## 14. 版本路线

| 版本 | 内容 |
|---|---|
| v0.0.x | M0 技术验证，不保证数据兼容 |
| v0.1.0 | M1—M4：核心、HomeKit、MQTT、映射引擎 |
| v0.2.0 | M5：米家 Provider 原型和基础设备 |
| v0.3.0 | M6：管理 API 和 Web UI |
| v0.4.0 | M7：Logical Device 和多 Provider 路由 |
| v0.5.0 | 第二 Provider、进程隔离、稳定性增强 |
| v1.0.0 | 核心接口、数据迁移、部署与文档稳定 |

## 15. 近期执行清单

下一步只执行 M0：

1. 初始化 Git 和 Go module；
2. 建立最小 `cmd/homeloom`；
3. 选择并锁定 HAP 库版本；
4. 发布虚拟开关；
5. 完成 Apple Home 配对；
6. 将 pairing 和附件身份写入 SQLite；
7. 验证三次重启和设备改名；
8. 验证 Docker 与 ARM64；
9. 记录 PoC 结论和未解决风险；
10. M0 验收通过后再开始核心抽象。

项目的第一优先级不是平台数量，而是稳定身份、可靠状态和可恢复运行。只有这三点得到验证，后续 Provider、Web UI 和 Matter 扩展才有可靠基础。
