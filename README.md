# HomeLoom

HomeLoom 是一个前后端分离的智能设备聚合桥。当前 Demo 提供虚拟设备、REST API 和管理界面，用来验证设备模型与状态更新链路。

## 目录

```text
backend/   Go + Echo API
frontend/  React + TypeScript 管理界面
docs/      项目计划与设计文档
```

## 本地运行

后端：

```bash
./scripts/dev-env.sh sh -c 'cd backend && go run ./cmd/homeloom'
```

前端：

```bash
./scripts/dev-env.sh sh -c 'cd frontend && npm install && npm run dev'
```

打开 `http://localhost:5173`。开发服务器会将 `/api`、`/health` 和 `/ready` 代理到 `http://localhost:8090`。

HomeKit Bridge 同时监听 `51826`，配对码为 `001-02-003`。HAP 身份和配对信息保存在 `backend/data/hap/`，该目录不会提交到版本库。

桥配置保存在 SQLite，可同时运行多个 Apple HAP Bridge，并为 Matter 等其他 Target 类型预留统一入口。每个启用的 HAP 桥必须具有唯一的：

- `id`
- `address`
- `setup_id`
- `store_path`

设备绑定为空时发布全部设备；指定设备后，该桥只发布选中的设备。YAML 不再接受 Target 配置，避免数据库和文件出现两个事实来源。

前端“桥接中心”会显示所有 Target 的类型、状态、设备范围、配对码和二维码。二维码来自后端生成的标准 HomeKit Setup URI，并与桥的 Setup ID 保持一致。

后端支持 `-config configs/config.example.yaml`。YAML 只包含 HTTP 地址和数据库路径，也可以使用以下环境变量覆盖：

- `HOMELOOM_HTTP_ADDRESS`
- `HOMELOOM_DATABASE`

`scripts/dev-env.sh` 会将 Go、Go module 和 npm 缓存统一放在根目录 `.cache/`，避免写入用户级缓存或触发不必要的权限请求。

## 验证

```bash
./scripts/dev-env.sh sh -c 'cd backend && go test ./...'
./scripts/dev-env.sh sh -c 'cd frontend && npm run lint && npm run build'
```

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

属性状态诊断：

```text
GET /api/v1/devices/:id/states
```

返回当前值、Provider、来源、质量、设备观察时间、服务接收时间、sequence 和内部 version。

实时设备状态只保存在内存。服务重启后 State Store 为空，由各 Provider 重新发现、订阅并读取设备状态；SQLite 只保存桥配置、设备绑定和稳定身份。

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
