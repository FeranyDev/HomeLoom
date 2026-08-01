# Matter Runtime 运维与安全边界

## 发布形态

Matter 模式采用 Go 主服务加 Node.js 20+ sidecar。每个 Matter Target 拥有独立进程、Unix Domain Socket、UDP 端口、discriminator 和数据库身份命名空间。sidecar 崩溃只触发该 Target 的重启与全量状态重放，不阻塞 Device Service 或其他 Target。

当前 `@matter/main` 固定为 `0.17.6`，其标准模型对应 Matter 1.4.1；管理界面的“协议版本”展示该规范版本。Go ↔ sidecar 的内部 IPC 合约另行固定为 `1.0`。开发、测试和构建统一通过 `scripts/dev-env.sh`，npm、Go、TypeScript 缓存全部写入项目 `.cache/`。

真实 driver 当前覆盖 22 类官方标准 Device Type：开关、插座、扩展彩灯、温度、湿度、接触、活动、占用、窗帘、风扇、恒温器、门锁、照度、气压、漏水、烟雾、一氧化碳、空气质量、阀门、水泵、空气净化器、扬声器。测试会直接读取 Go Matter Catalog，要求每个公开类型和 Cluster Attribute/Command 路径都能由官方 matter.js Endpoint 构造，避免管理端暴露运行时无法重放的类型。

Matter 消费端设备类型只发布当前协议中的标准 Device Type。HomeLoom 的 `temperature-humidity-sensor` 是统一模型逻辑类型，不是 Matter 标准聚合传感器，因此不会出现在 Matter 的“消费端设备类型”列表，后端也会拒绝直接保存该类型。需要导出温湿度来源时，用户可以显式创建标准的 `temperature-sensor` 或 `humidity-sensor` 消费端映射；系统不会自动拆分或隐式创建 Endpoint。

```bash
./scripts/dev-env.sh sh -c 'cd matter-runtime && npm ci && npm test'
./scripts/dev-env.sh sh -c 'cd frontend && npm test'
./scripts/dev-env.sh sh -c 'cd backend && go test ./...'
```

Go 默认从当前目录、当前目录的上一级，以及可执行文件目录附近查找 `matter-runtime/dist/src/cli.js`，因此从仓库根目录、`backend/` 或默认的 `backend/bin/` 启动均可发现 sidecar。其他部署布局必须设置 `HOMELOOM_MATTER_RUNTIME` 为绝对路径。`HOMELOOM_MATTER_ADAPTER=fake` 只允许 IPC 自动测试；真实部署必须使用 `matter-js`，且 driver 不可用时应 fail closed。

## 日志

matter.js 默认会输出大量 DEBUG 级别的 ANSI 控制台日志。sidecar 在加载 `@matter/main` 时会统一改写官方 Logger，Go 主程序再集中采集其 stdout/stderr；子程序日志不会混入主程序终端，而是在网页“系统”页展示：

- 默认级别：由 YAML `logging.child_level` 设置，项目默认 `info`
- 默认格式：JSON 行，字段为 `time` / `level` / `msg` / `component=matter-js` / `facility`
- 对 Endpoint、mDNS、Session、Exchange 等常见噪声 facility 额外压到 `WARN`（仅在默认 `NOTICE` 及以上生效）

Core 启动 sidecar 时会设置以下环境变量；直接调试 sidecar 时也可手动设置：

| 变量 | 说明 | 示例 |
| --- | --- | --- |
| `HOMELOOM_MATTER_LOG_LEVEL` | 由 `logging.child_level` 生成，优先于 `MATTER_LOG_LEVEL` | `debug` / `info` / `warn` / `error` |
| `HOMELOOM_MATTER_LOG_FORMAT` | 优先于 `MATTER_LOG_FORMAT` | `json`（默认）/ `plain` / `ansi` |
| `MATTER_LOG_LEVEL` / `MATTER_LOG_FORMAT` | matter.js 官方环境变量兼容入口 | 同上 |

排障时可在 HomeLoom YAML 中将等级临时改成 `debug`；独立启动 sidecar 时也可直接设置环境变量：

```bash
HOMELOOM_MATTER_LOG_LEVEL=debug HOMELOOM_MATTER_LOG_FORMAT=plain ./scripts/dev-env.sh sh -c 'cd backend && go run ./cmd/homeloom'
```


`@matter/main` 0.17.6 的在线 factory reset 会在进程内重建 `ServerNode`，但该版本可能保留旧一代 shared mDNS 引用。sidecar 在 reset RPC 成功返回后会主动正常退出，由 Go Target supervisor 启动全新进程并执行握手与全量重放；这是身份轮换的一部分，不应被监控系统当作整套 HomeLoom 服务故障。

## IPC 与状态恢复

Go 与 sidecar 使用 Unix Socket 上的 newline-delimited JSON-RPC 2.0：

- 连接先执行 `runtime.handshake`，协议版本固定为 `1.0`；
- 握手后必须执行 `state.replay`，发送桥配置和完整设备快照；
- 增量属性、可达性、commissioning、Fabric 与 factory reset 使用独立 method；
- 属性写入与 Cluster Command 反向进入 Go 的 Consumer 映射和现有命令串行队列；
- 请求包含 ID、超时和有界队列；队列满时明确返回背压错误；
- 重连不重放旧增量，而是重新握手并发送最新全量状态。

Matter Fabric、NOC、密钥和计数器通过反向 `storage.*` RPC 保存到 PostgreSQL。Go 将连接固定到当前 Target 命名空间，sidecar 不能指定任意 namespace。值使用主密钥 AEAD 加密；主密钥缺失时拒绝加载已有身份。配置导出和诊断包不包含 passcode、私钥、证书或 Fabric 凭据，逻辑备份只包含加密后的身份。

## 网络要求

- IPv6 必须启用，不能屏蔽接口的 link-local 地址；
- 允许同一局域网的 UDP 单播到每座桥配置的 Matter 端口；
- 允许 mDNS/Bonjour multicast：IPv4 `224.0.0.251:5353` 与 IPv6 `ff02::fb:5353`；
- VLAN 间 commissioning 需要支持 mDNS reflector 且正确转发 IPv6/UDP，单纯 DNS 记录不足；
- 两座桥必须使用不同 UDP 端口、discriminator 和身份命名空间；
- 容器必须验证 host network、IPv6 和 multicast；仅映射管理端口 `8090` 无法完成 Matter 发现。

排障顺序：

1. 检查桥卡片的 runtime 状态、网络接口、协议版本和 UDP 端口；
2. 检查 sidecar 日志是否完成 handshake 与 `state.replay`；
3. 用 `dns-sd`/`avahi-browse` 确认 Matter commissioning 广播可见；
4. 检查宿主机 IPv6 地址、UDP 防火墙和 VLAN ACL；
5. 确认目标的 discriminator、端口和 identity namespace 未与另一座桥复用；
6. sidecar 重启后确认 Endpoint ID 未变化，且 Fabric 数量由加密身份恢复。

## Commissioning 与危险操作

二维码和手工配对码只在 commissioning window 打开时返回。加入首个 Fabric 后默认隐藏。打开窗口必须输入 `OPEN COMMISSIONING <targetId>`；删除 Fabric 必须输入 `DELETE FABRIC <targetId> <fabricId>`；恢复出厂必须输入 `FACTORY RESET <targetId>`。桥卡片只接收 Fabric 的 `id/label` 摘要，不返回 Fabric 凭据、密钥或证书。配置、commissioning、Fabric 删除、identity KV 和 factory reset 都进入审计日志，日志不得包含凭据值。

已分配 Endpoint 的 Device Type 不允许被普通目标保存流程静默修改。管理端会先保存其余字段，再要求输入 `CHANGE ENDPOINT TYPE <targetId> <consumerDeviceId> <deviceType>`，由独立确认接口同步更新 Endpoint identity、目标配置并重启运行时；不愿承担控制器重新发现风险时，应删除后新建消费端设备。

开发参数使用测试 Vendor ID/Product ID，并在前端标为“测试设备 · 未认证”。matter.js 或 ConnectedHomeIP 本身不代表 CSA 认证；正式发布仍需 CSA 成员资格、设备证明凭据和产品认证。

## 验收边界

日常 CI 覆盖 IPC 合约、背压、断线恢复、双实例隔离、当前已开放的 22 种标准 Matter Device Type、100 Endpoint Go 快照、Endpoint 稳定身份、数据库恢复和前端操作。以下项目需要维护者在真实局域网执行：

- Apple Home 首次 commissioning、读写与订阅；
- `chip-tool` commissioning/read/write/subscribe；
- 第二 Fabric 的 Multi-Admin；
- 连续三次重启后的 Fabric/Endpoint 稳定性；
- host、Docker/Apple Container 与目标 VLAN 的 IPv6/mDNS 差异；
- factory reset 后旧 Fabric 无法继续访问。
