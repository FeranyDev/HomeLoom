# HomeLoom 详细实施清单

更新时间：2026-07-13

状态说明：

- `[x]` 已实现并通过自动化测试；
- `[~]` 已实现，但仍需实机、网络或长期运行验证；
- `[ ]` 尚未开始或尚未完成；
- `[!]` 开始后续阶段前必须解决的风险项。

## 0. 当前基线

- [x] 初始化 Git 仓库和 `main` 分支；
- [x] 创建首次提交 `c8402c9`；
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
- [x] 确定版本号注入方式。

基线验收命令：

```bash
./scripts/dev-env.sh sh -c 'cd backend && go test ./...'
./scripts/dev-env.sh sh -c 'cd backend && go test -race ./internal/command ./internal/eventbus ./internal/state ./internal/application'
./scripts/dev-env.sh sh -c 'cd frontend && npm test && npm run lint && npm run build'
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
- [ ] Apple Home 显示温度传感器；
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

- [x] PIN 使用数据库旁主密钥进行 AES-256-GCM 加密存储；
- [!] 管理 API 未认证，不应在公网或不可信局域网开放；
- [ ] 配对资料查看增加管理权限检查；
- [ ] 重新生成 PIN/Setup ID 增加二次确认；
- [ ] 删除已配对桥时默认保留身份目录；
- [ ] 增加“清除配对身份”独立高风险操作；
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
- [x] 区分 Provider 暂时离线、SQLite 持久禁用和 Provider 删除 tombstone；

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

### 2.3 兼容迁移

- [x] 将虚拟开关迁移为 `switch/power` Capability；
- [x] 将温度传感器迁移为 `temperature/current-temperature`；
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

- [x] 从 SQLite 加载 Provider 配置；
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
- [x] 程序初始化时可选为每种受支持模型各生成一个虚拟设备，配置持久化到 SQLite 且幂等；
- [x] 统一模型参数按必须、可选、自定义分级，并实现 Provider 发布校验与 Consumer 显式映射；
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

### 4.2 待完善

- [ ] 队列容量改为可配置；
- [ ] 分片数量改为可配置；
- [ ] 事件优先级；
- [ ] 丢弃低优先级重复事件；
- [ ] 对相同属性进行事件合并；
- [x] 队列长度指标；
- [x] 队列满次数指标；
- [x] 事件吞吐指标；
- [x] 事件处理延迟指标；
- [x] 慢 Handler 检测；
- [x] 事件 trace ID；
- [x] 压力测试每秒 100 条事件；
- [x] 关闭超时测试。

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
- [x] 实时状态不写 SQLite；
- [x] 重启后由 Provider 重建状态。

### 5.2 待完善

- [x] stale TTL；
- [x] 定时 stale 扫描；
- [x] unknown 初始状态；
- [x] Provider 离线批量标记 stale；
- [x] 乐观状态；
- [x] 乐观状态到期；
- [x] confirmed 状态覆盖 optimistic；
- [x] Provider 时间漂移检测；
- [ ] 不同 Provider 优先级；
- [ ] 属性级 Provider 优先级；
- [ ] 可解释的冲突结果；
- [x] 状态变更订阅 API；
- [x] WebSocket/SSE 实时推送；
- [ ] 可选低频 checkpoint；
- [ ] checkpoint 恢复时强制标记 stale；
- [x] 500 设备状态压力测试。

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
- [x] 异步确认链路测试。

### 6.2 待完善

- [x] 通用 Property Value，不限于 bool；
- [x] Action/Command 参数；
- [x] Action Idempotency Key；
- [x] 同设备命令顺序队列；
- [x] 相同属性命令去重；
- [x] 后写命令取消旧命令；
- [x] outcome-unknown；
- [x] timeout 不自动视为执行失败；
- [x] 安全重试声明；
- [x] 非幂等命令禁止自动重试；
- [ ] Provider 回退策略；
- [x] 命令超时配置；
- [x] 命令成功率和延迟指标；
- [x] 命令历史保留策略；
- [x] 前端命令诊断页面及状态、设备、动作和错误筛选；
- [x] 模拟延迟和超时集成测试。

退出条件：开关写入必须经过真实状态确认，超时和未知结果对用户可见。

## 7. M1：持久化与安全

### 7.1 已实现

- [x] SQLite；
- [x] migration runner；
- [x] WAL；
- [x] foreign keys；
- [x] busy timeout；
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
- [!] OAuth Token 加密存储；
- [x] 密钥文件权限检查；
- [x] 敏感字段日志过滤；
- [x] 配置导出脱敏；
- [x] 诊断包脱敏；
- [ ] 密钥轮换设计；
- [x] 数据库备份使用 SQLite `VACUUM INTO` 一致性 API；
- [x] 打开/恢复前拒绝高于程序支持版本的数据库。

## 8. M2：HomeKit 基础设备

### 8.1 已支持

- [x] Switch；
- [x] Temperature Sensor。

- [x] HomeKit 温度事件实时推送；
- [x] HomeKit 离线 StatusFault 映射；

### 8.2 已补齐

- [x] Outlet；
- [x] Lightbulb；
- [x] Humidity Sensor；
- [x] Contact Sensor；
- [x] Motion Sensor；
- [x] Fan；
- [x] Air Purifier；
- [x] Filter Maintenance（Air Purifier 链接服务）；
- [x] Window Covering。

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
- [x] Broker 地址；
- [x] TLS/mTLS；
- [x] 用户名和密码；
- [x] Client ID；
- [x] Topic prefix；
- [x] QoS；
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
- [x] 自动发现设备（Provider 单元测试）；
- [x] MQTT 状态同步至 Registry（跨层 race 测试）；
- [ ] MQTT 状态同步至 Apple Home；
- [ ] Apple Home 命令发布 MQTT；
- [x] MQTT 回报确认命令（跨层 race 测试）；
- [ ] Broker 重启恢复；
- [ ] 网络中断恢复；
- [x] 不重复创建设备（remote sequence 去重）；
- [x] 不恢复过期实时状态；
- [ ] ARM64 构建。

M3 退出条件：MQTT 双向链路、断线恢复和命令确认全部通过。

## 10. M4：Mapping Engine

### 10.1 Profile 模型

- [x] Provider Profile（模型、内置样例、数据库管理和精确属性运行时绑定）；
- [~] Capability Profile（可用于精确属性运行时绑定，Capability 自动生成规则待实现）；
- [~] Target Profile（模型、内置样例和管理已完成，Target 发布绑定待实现）；
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
- [ ] 原始 Provider 数据预览；
- [ ] Capability 结果预览；
- [ ] HAP Target 结果预览；
- [x] 前端 Profile 管理；
- [x] 前端设备属性绑定管理；
- [x] 映射调试页面；
- [x] Profile 与属性绑定热重载（刷新 Provider 原始快照，不重启 Provider/Target）；
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

### 11.2 待实现

- [x] Router 和独立页面路由；
- [x] 系统状态页；
- [x] Provider 管理页；
- [x] 设备列表筛选；
- [x] 设备详情页；
- [x] Capability 树；
- [x] 状态来源和质量展示；
- [x] 命令历史页面；
- [~] 实时日志（审计事件已支持 SQLite 历史和 SSE，进程运行日志流待实现）；
- [x] Mapping 预览；
- [x] Profile 管理；
- [ ] 备份恢复；
- [x] 错误边界；
- [x] Toast/通知系统；
- [x] Provider/Target 表单服务端字段错误定位；
- [x] Loading 和空状态统一组件；
- [~] 无障碍检查（已补键盘焦点、跳转主要内容、当前导航、状态与错误播报；正式 WCAG 审计待完成）；
- [x] API Client 统一错误模型；
- [x] WebSocket/SSE 实时更新；
- [ ] 前端覆盖率报告。

### 11.3 管理认证

- [!] 首次启动管理员初始化；
- [!] 登录；
- [!] Session 或 Token；
- [!] CSRF 防护；
- [!] 登录限速；
- [!] 敏感操作二次确认；
- [x] 审计日志；
- [ ] 可信代理配置；
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

### 12.2 待实现

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
- [x] SQLite 操作数、平均和最大延迟；
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
- [x] SQLite migration 和 CRUD 测试；
- [x] HTTP API 测试；
- [x] Target 配置逻辑测试；
- [x] 前端二维码交互测试；
- [x] 前端设备绑定表单测试；
- [x] 后端覆盖率约 64%。

### 13.2 待补测试

- [ ] TargetManager 启用桥真实生命周期测试；
- [x] HAP Server 端口占用测试；
- [ ] 多桥并发测试；
- [ ] HAP 身份三次重启测试；
- [ ] API Target CRUD 集成测试；
- [ ] Target 保存失败回滚测试；
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
- [ ] 前端覆盖率阈值；
- [ ] E2E 浏览器测试。

## 14. 部署

- [x] 多阶段 Dockerfile；
- [x] 前端静态文件镜像；
- [x] 后端镜像；
- [x] Compose 示例；
- [x] 数据卷；
- [x] host network HomeKit 示例；
- [x] bridge network 限制说明；
- [ ] amd64 镜像；
- [ ] arm64 镜像；
- [ ] Linux ARM64 实机；
- [ ] NAS 部署文档；
- [ ] OpenWrt 可行性验证；
- [x] 进程级优雅停止烟雾验证；
- [x] 容器健康检查；
- [x] 数据库备份脚本；
- [x] 版本升级、回滚和 migration 文档。

## 15. 后续版本

### v0.2：米家 Provider

- [ ] 接口授权和许可评估；
- [ ] 账号认证；
- [ ] Token 生命周期；
- [ ] 家庭和房间；
- [ ] 设备发现；
- [ ] MIoT Spec 获取和缓存；
- [ ] 批量属性读取；
- [ ] 属性写入；
- [ ] Action；
- [ ] 状态订阅；
- [x] 轮询校准；
- [ ] Token 过期恢复；
- [ ] 网络恢复；
- [x] 首批内置转换 Profile；
- [ ] 实机测试记录。

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

- [ ] 第二 Provider；
- [ ] Provider 独立进程；
- [ ] RPC 协议；
- [ ] API 版本协商；
- [ ] 心跳；
- [ ] 崩溃重启；
- [ ] 日志转发；
- [ ] 资源限制；
- [ ] Matter Target；
- [ ] Zigbee2MQTT/Tuya/ESPHome 评估。

## 16. 推荐执行顺序

下一批建议严格按以下顺序推进：

1. `[!]` PIN 和管理 API 安全边界；
2. 统一 `Endpoint/Capability/Property` 模型；
3. 将 Virtual Provider 和 HomeKit Target 迁移到统一模型；
4. Provider SDK 和 Provider Manager；
5. stale、unknown 和 optimistic 状态生命周期；
6. 通用命令值、幂等和同设备顺序；
7. `/metrics` 和基础可观测性；
8. Docker、ARM64 和 HomeKit 实机验收；
9. MQTT Provider；
10. Mapping Engine。

每完成一个步骤都应：

- 更新本清单；
- 增加或更新单元测试；
- 运行 race 检测；
- 运行前端测试、Lint 和构建；
- 更新 API/架构文档；
- 创建独立 Git 提交。
