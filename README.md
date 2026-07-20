# HomeLoom

HomeLoom 是一个前后端分离的智能设备聚合桥。当前 Demo 提供虚拟设备、REST API 和管理界面，用来验证设备模型与状态更新链路。

## 目录

```text
backend/   Go + Echo API
frontend/  React + TypeScript 管理界面
docs/      项目计划与设计文档
```

## 本地运行

先用 macOS 的 Apple container 启动开发 PostgreSQL：

```bash
./scripts/apple-postgres.sh
```

默认连接为 `127.0.0.1:54329`，数据保存在 Apple container 命名卷 `homeloom-postgres-data`。

后端：

```bash
./scripts/dev-env.sh sh -c 'cd backend && go run ./cmd/homeloom'
```

首次体验时可以选择为当前支持的每种模型各生成一个虚拟设备：

```bash
./scripts/dev-env.sh sh -c 'cd backend && go run ./cmd/homeloom -init-all-virtual-models'
```

该选项会把 10 个设备的显式配置写入 PostgreSQL 中的 `virtual-main` Provider，后续启动不再需要携带该参数。重复执行不会重复生成，也不会覆盖已有的自定义设备列表。

前端：

```bash
./scripts/dev-env.sh sh -c 'cd frontend && npm install && npm run dev'
```

打开 `http://localhost:5173`。开发服务器会将 `/api`、`/health` 和 `/ready` 代理到 `http://localhost:8090`。
管理 API 默认只监听 `127.0.0.1:8090`。首次打开前端需要创建唯一管理员，之后所有管理接口、指标和 HomeKit 配对二维码都受数据库 Session 与 CSRF 保护。需要局域网访问时必须显式设置 `HOMELOOM_HTTP_ADDRESS=0.0.0.0:8090`；仍建议通过防火墙或 HTTPS 反向代理限制访问来源。

Web 端只有这个管理员账户，用于接入、桥接、映射、诊断和备份恢复，不提供普通家庭成员账户。设备的日常控制和成员共享应在 Apple Home 中完成。

新数据库会创建一个默认 HomeKit Bridge，初始监听 `51826`，配对码为 `001-02-003`。这些参数之后可在前端动态修改，并以数据库中的桥配置为准。HAP 身份和配对信息保存在 `backend/data/hap/`，该目录不会提交到版本库。

桥配置保存在 PostgreSQL，可同时运行多个 Apple HAP Bridge，并为 Matter 等其他 Target 类型预留统一入口。每个启用的 HAP 桥必须具有唯一的：

- `id`
- `address`
- `setup_id`
- `store_path`

设备绑定为空时发布全部设备；指定设备后，该桥只发布选中的设备。YAML 不再接受 Target 配置，避免数据库和文件出现两个事实来源。

前端“桥接中心”会显示所有 Target 的类型、状态、设备范围、配对码和二维码。二维码来自后端生成的标准 HomeKit Setup URI，并与桥的 Setup ID 保持一致。

后端支持 `-config configs/config.example.yaml`。YAML 只包含进程启动所需的 HTTP 地址、可信代理范围、PostgreSQL URL 和主密钥路径，也可以使用以下环境变量覆盖：

- `HOMELOOM_HTTP_ADDRESS`
- `HOMELOOM_DATABASE_URL`
- `HOMELOOM_MASTER_KEY`
- `HOMELOOM_TRUSTED_PROXIES`（逗号分隔的代理 IP/CIDR；未配置时完全忽略转发来源头）

`scripts/dev-env.sh` 会将 Go、Go module 和 npm 缓存统一放在根目录 `.cache/`，避免写入用户级缓存或触发不必要的权限请求。

## 验证

```bash
./scripts/check.sh
# CI 同级验证，额外执行 race detector
./scripts/check.sh --race
```

构建带版本信息的后端二进制和前端产物：

```bash
./scripts/build.sh
./scripts/cross-build.sh # 校验 linux/amd64 与 linux/arm64 后端构建
```

默认版本由 Git tag/commit 生成，可使用 `HOMELOOM_VERSION`、`HOMELOOM_COMMIT` 和 `HOMELOOM_BUILD_TIME` 覆盖。运行时可通过 `GET /api/v1/system/version` 查看实际后端版本。

```bash
backend/bin/homeloom -version
```

对实际二进制执行启动、HTTP 和优雅停止烟雾测试：

```bash
HOMELOOM_SMOKE_DATABASE_URL='postgres://.../disposable_database?sslmode=disable' ./scripts/smoke.sh
```

冒烟测试要求显式提供一个可丢弃的 PostgreSQL 数据库或独立 schema，并使用临时主密钥和动态 HAP 端口；它会验证双桥并行、Target API 增删改、三次连续重启、身份稳定以及备份恢复。

容器部署、host network 与数据卷说明见 [`deploy/README.md`](deploy/README.md)。

PostgreSQL 在线一致性备份（检查连接不会在源库运行 GORM `AutoMigrate`）：

```bash
./scripts/backup.sh
```

该脚本会生成 PostgreSQL 逻辑快照 `.json` 及配套的 `.json.key` 主密钥文件，两者必须一起保管和恢复。灾难恢复 HomeKit 配对关系时，还必须备份相应的 HAP 身份目录。

停止 HomeLoom 后可以恢复备份到 `HOMELOOM_DATABASE_URL` 指定的数据库：

```bash
HOMELOOM_DATABASE_URL='postgres://homeloom:secret@127.0.0.1:54329/homeloom?sslmode=disable' ./scripts/restore.sh backups/homeloom-20260716T000000Z.json --replace
```

恢复必须显式传入 `--replace`。流程先校验快照格式、当前 GORM schema 和主密钥解密能力，再用单个 PostgreSQL 事务替换业务表并失效已恢复的浏览器 Session；操作前会在快照目录生成一份当前数据的恢复点。

## 当前 Demo 链路

```text
Web Console ──REST──┐
                    ▼
              Device Service
                    ▼
             Virtual Provider
                    │
                    └── state event ──→ HomeKit Target ──→ Apple Home
```

Web 和 Apple Home 对开关的写入都会进入同一个 Device Service。Provider 更新先进入有界分片事件队列，再写入 Device Registry 并通知 Target，避免前端与 HomeKit 各自维护一份状态，同时保证同一设备的事件顺序。

HomeKit 基础设备现覆盖 Switch、Lightbulb、Outlet、温湿度、接触、活动、Fan、Air Purifier（含 Filter Maintenance）和 Window Covering。统一 Capability 与 HAP Characteristic 对照见 [HomeKit 基础设备契约](docs/homekit-device-contracts.md)。

属性状态诊断：

```text
GET /api/v1/devices/:id/states
```

返回当前值、Provider、来源、质量、设备观察时间、服务接收时间、sequence 和内部 version。

HTTP 成功/错误包装、请求 ID 和诊断入口见 [HTTP API 约定](docs/http-api.md)。
OpenAPI 3.1 契约位于 [`docs/openapi.yaml`](docs/openapi.yaml)。

实时设备状态只保存在内存。服务重启后 State Store 为空，由各 Provider 重新发现、订阅并读取设备状态；PostgreSQL 只保存桥配置、设备绑定和稳定身份。

命令诊断：

```text
GET /api/v1/commands
GET /api/v1/commands/:id
```

设备写入先进入命令状态机。Provider 接受写入后状态为 `accepted`，只有设备后续上报与期望值一致时才变为 `confirmed`；未确认命令会进入 `timeout`。

运行指标：

```text
GET /api/v1/diagnostics
GET /metrics
```

包含 Core 事件接收/处理/丢弃、队列深度、慢 Target 丢弃、stale 状态、命令开始/确认/拒绝/超时计数。每个 Target 使用独立的 64 条有界队列，慢 Target 不会阻塞设备事件 shard。

支持资料可在系统页按需下载，也可以直接请求：

```text
GET /api/v1/system/config-export
GET /api/v1/system/diagnostic-bundle
```

两者都使用独立的脱敏模型并禁止浏览器缓存：Provider 凭据替换为占位符，桥 PIN、Setup URI 和本地身份存储路径不会进入文件。

“模型工具”页可以管理 PostgreSQL 中的用户 Profile，并直接预览内置或已保存版本。Profile CRUD、批量导入和删除在数据库提交后立即更新应用层快照，不需要重启：

```text
GET/POST /api/v1/mapping/profiles
POST     /api/v1/mapping/profiles/import
GET      /api/v1/mapping/profiles/export
GET/POST /api/v1/mapping/bindings
GET/PUT/DELETE /api/v1/mapping/bindings/{id}
GET      /api/v1/mapping/catalog
GET/POST /api/v1/device-models/custom-properties
PUT/DELETE /api/v1/device-models/custom-properties/{id}
POST     /api/v1/mapping/preview
```

属性路由保存在 PostgreSQL `mapping_bindings`，明确分为 `Provider → 统一模型` 和 `统一模型 → Consumer` 两段。两段路由都绑定具体 `providerId + deviceId`，同型号设备互不继承配置。两侧都可选择可逆 Profile，事件走正向转换，控制走反向转换；数值类型可在 `int` 与 `number` 间转换。统一模型始终使用 Endpoint / Capability / Property 三级路径，并按 required、optional、custom 分级。自定义属性及其类型、单位、范围、枚举和 R/W/N 权限保存在 `custom_model_properties`。

设备映射从设备中心的对应设备卡片进入。三栏关系图会锁定该设备，只展示它的 Provider 原始目录、统一模型参数、HomeKit Consumer 属性和已有路由，不允许在编辑器内切换或操作其他设备。原始目录明确展示完整性、数据来源、Spec URN，以及 Property/Action/Event 数量；米家设备通过缓存的 MIoT Spec 补齐未配置属性。顶层“模型工具”页只保留统一模型自定义属性、Profile 与转换预览。Provider 路由变化只刷新原始快照，不重启 Provider；Consumer 路由变化会保留桥的配对身份并重建附件图。完整设计见 [双段设备映射架构](docs/mapping-architecture.md)。

Provider 由 Provider Manager 聚合管理。Core 只依赖标准 Provider SDK；Manager 负责初始化、发现、事件转发以及 `Device ID → Provider ID` 写入路由，为后续同时运行 MQTT、米家和其他 Provider 保留统一边界。

Provider 配置保存在 PostgreSQL `providers` 表，启动时由 Provider Factory 按 `type` 构造实例。数据库层使用 GORM、`gorm.io/driver/postgres` 和 pgx，`AutoMigrate` 同步当前模型后由幂等初始化逻辑创建默认启用的 `virtual-main`；YAML 不包含 Provider 配置。项目不再携带编号 SQL migration，也不承诺自动升级旧版 schema。

MQTT Provider 已支持数据库/前端结构化配置、TLS/mTLS、认证、QoS、retained 恢复、自动重连、状态去重和命令确认。密码及嵌套的 token/secret/private-key 类字段使用数据库主密钥加密，API 只返回脱敏占位符。Topic 与 payload 契约见 [MQTT Provider 协议](docs/mqtt-protocol.md)。本地可用 `docker compose up -d mosquitto` 启动开发 Broker。

Xiaomi Central Hub Provider 已接入 OAuth 授权、证书申请、MQTT 5/MIPS、属性读写、Action、状态订阅和自动重连，并复用同一 OAuth 身份为中枢无法本地控制的设备提供官方 MIoT HTTP 云路径。设备可逐台选择 `auto/local/cloud`；`auto` 本地优先且失败转云，本地推送可用时不做持续轮询。OAuth 使用固定回调地址 `http://homeassistant.local:8123`，授权后按页面引导复制完整回调 URL 粘贴回 HomeLoom。另有完全独立的 `xiaomi-miot-cloud` 第三方兼容 Provider，参考 `hass-xiaomi-miot` 通过账号会话与云端轮询补充 Wi‑Fi 设备，并支持短信/邮箱验证码回填；两者不共享密码会话或 Provider 状态。凭据及 MIoT 映射均保存在 PostgreSQL Provider 配置中；使用方法和授权边界见 [Xiaomi Provider 文档](docs/xiaomi-provider.md)。
