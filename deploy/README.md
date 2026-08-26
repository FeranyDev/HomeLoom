# 容器部署

`compose.yaml` 面向 Linux 主机。React 管理界面已经嵌入 Go 二进制，当前只构建一个 HomeLoom core 镜像：

```bash
HOMELOOM_VERSION=0.1.0 docker compose up --build -d
```

管理界面和后端 API 均由 `http://主机地址:8090` 提供。PostgreSQL 17 数据保存在 `postgres-data` 卷；HomeKit HAP 身份和 HomeLoom 主密钥保存在 `homeloom-data` 卷。

## SQLite 模式

单机部署可叠加 `deploy/compose.sqlite.yaml`，以 `/data/homeloom.db` 作为数据库且不启动 PostgreSQL：

```bash
HOMELOOM_VERSION=0.1.0 docker compose -f compose.yaml -f deploy/compose.sqlite.yaml up -d backend
```

SQLite 数据库、主密钥、HomeKit HAP 身份及媒体资料都保存在 `homeloom-data` 卷；备份或迁移时必须一并保留该卷的内容。

镜像包含 Node.js、锁定版本的 `matter-runtime`、Camera Kernel 与 FFmpeg，因此 Matter Target 和摄像头发布均可在同一个容器内运行。它们仍必须使用 host network，保证 mDNS、IPv6 与动态服务端口可被局域网控制器访问。

构建环境无法直连 Go 或 npm 官方依赖源时，Dockerfile 会离线使用项目 `.cache/go-mod` 和 `.cache/npm`。执行构建前运行一次 `./scripts/check.sh` 即可预热它们；缓存只存在于构建阶段，不会写入最终运行镜像。

## 为什么使用 host network

HomeKit 和 Matter 都依赖局域网 mDNS 发现；Matter 还要求 IPv6 与控制器可直连每座桥的 UDP 端口。普通 Docker bridge 的 multicast、IPv6、动态端口映射和容器地址通常无法满足这些条件。Linux 上应使用 host network，并在主机防火墙中仅允许可信局域网访问管理端口、HAP TCP 端口和所配置的 Matter UDP 端口。

Docker Desktop、Apple Container 和原生 Linux 的 host network/mDNS 行为不同。macOS/Windows 开发环境建议直接运行双制品完成 HomeKit/Matter 实机验证，容器主要用于 Web/API 构建检查。Matter 控制器与 HomeLoom 必须位于允许 IPv6 link-local、UDP 单播和 mDNS multicast 的同一可达网络。

## MCP Agent（可选）

AI 控制使用由 Core 托管的 `homeloom-mcp-agent` 子程序，不会额外启动容器。它和 Core 只通过 `/data/mcp` 中的私有 Unix Socket 通信；Core 每次启动时生成仅本地 Core/子程序用户可读的随机 Token，Agent API 默认仅监听主机回环地址 `127.0.0.1:8091`。外部 MCP JSON-RPC 默认关闭，网页 AI 对话和自动化不受影响；仅在确实需要给外部 MCP 客户端接入时，显式设置 `HOMELOOM_MCP_HTTP_ENABLED=true`。叠加 MCP Compose 文件即可：

```bash
HOMELOOM_VERSION=0.1.0 docker compose \
  -f compose.yaml -f deploy/compose.sqlite.yaml -f deploy/compose.mcp-agent.yaml up -d --remove-orphans
```

已从双容器版本升级的实例必须使用 `--remove-orphans`，以删除旧的 `mcp-agent` 容器并释放本机回环端口。不要将 `agent.token`、AI API Key 或 `/data` 卷导出到仓库。仅启用 MCP 只读工具时无需配置 AI 服务；如需 AI Agent，在“AI → AI 服务”填写兼容 API 的地址、Key、模型和可编辑的智能体提示词即可。Key 与提示词保存到子程序的 `/data/mcp/ai-config.json`（`0600`），不写入 Core 数据库。完整的 MCP API、确认机制和权限模型见 [MCP AI Agent](mcp-ai-agent.md)。

## Intel 核显（VAAPI）

Intel 核显主机可叠加 `deploy/compose.intel-gpu.yaml`，把最小必要的 DRM render 节点交给后台容器。OpenWrt 默认会把该节点设为 root 专用，因此先创建仅供 HomeLoom 使用的组，并安装随项目提供的启动脚本以在每次开机后恢复权限：

```bash
addgroup homeloom-render
install -m 755 deploy/openwrt-homeloom-render.init /etc/init.d/homeloom-render
/etc/init.d/homeloom-render enable
/etc/init.d/homeloom-render start
HOMELOOM_RENDER_GID="$(awk -F: '$1 == "homeloom-render" { print $3 }' /etc/group)" \
  docker compose -f compose.yaml -f deploy/compose.intel-gpu.yaml up -d backend
```

镜像包含 Intel Media Driver、VAAPI 工具和支持 VAAPI 的 FFmpeg。摄像头链会先尝试 `hardware=vaapi`，不可用时再回退到软件编码。可在运行后以 `docker compose exec backend vainfo --display drm --device /dev/dri/renderD128` 和 `ffmpeg -hwaccels` 验证驱动与编解码能力。

## 数据与升级

- 不要删除 `postgres-data`，否则 PostgreSQL 中的 Provider、Target、映射和管理员配置都会丢失；
- 不要删除 `homeloom-data`，否则加密主密钥、稳定 HomeKit 身份和配对关系都会丢失；
- 升级前应同时备份两个卷；
- 后端启动时通过 GORM `AutoMigrate` 同步当前模型，不执行旧版编号 migration；
- 回滚到旧版本前必须确认旧程序能够识别当前数据库 schema；
- `docker compose down` 保留卷，`docker compose down -v` 会删除卷。

运行中可以生成 PostgreSQL 一致性快照：

```bash
docker compose exec backend homeloom -backup /data/backups/homeloom.json
```

`-backup` 在 `REPEATABLE READ` 只读事务中生成逻辑 JSON 快照，并同时输出 `.json.key`，不会对源库运行 `AutoMigrate`。快照包含数据库配置，但不包含 `/data/hap/...` 下的 HomeKit 身份和配对资料。

恢复数据库前应备份两个数据卷并停止 backend，然后运行 `docker compose run --rm backend -restore /data/backups/homeloom.json -restore-replace`。恢复会先验证 snapshot schema 和主密钥，再以事务替换表数据并保留恢复前逻辑快照。

管理 API 使用单管理员数据库 Session 和 CSRF 防护。内嵌管理页面与 API 同源，不再需要内部 Nginx 转发。只有外部 HTTPS 反向代理的精确地址应该配置到 `HOMELOOM_TRUSTED_PROXIES`；直接访问 8090 的局域网客户端不能通过伪造转发头绕过登录限速。

生产 HTTPS 代理应同时代理静态前端和 `/api`，覆盖而不是透传客户端提交的转发头，并将代理自身的精确 IP/CIDR 写入 `HOMELOOM_TRUSTED_PROXIES`。未配置可信代理时，HomeLoom 只使用 TCP 直连地址，且不会依据 `X-Forwarded-Proto` 设置 Secure Cookie。不要为了省事信任整个局域网网段。

示例环境变量：

```text
HOMELOOM_TRUSTED_PROXIES=127.0.0.1/32,10.20.0.8/32
```

外层代理必须传递：

```nginx
proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
proxy_set_header X-Forwarded-Proto $scheme;
```

当可信代理声明原始协议为 HTTPS 时，Session 和 CSRF Cookie 会自动带上 `Secure`。

## 架构与 NAS

CI 会分别构建 `linux/amd64` 和 `linux/arm64` 的统一镜像，并实际交叉编译 Linux、macOS、Windows 的 amd64/arm64 单二进制。原生 Linux x86_64 与 ARM64 NAS 可以使用同一份 Compose；必须确认系统支持 host network、mDNS multicast 和 HAP 监听端口。Synology/QNAP 等 NAS 若通过自带反向代理提供管理页，HomeKit 服务仍需要 host network，不能只映射 8090。

OpenWrt 的存储寿命、内存、Go 二进制体积和 mDNS 防火墙差异较大，目前只视为可行性评估目标，不列为受支持部署环境。

## MQTT 开发服务

Compose 中的 `mosquitto` 是可选开发 Broker，使用 host network 监听 `1883`：

```bash
docker compose up -d mosquitto
```

其配置允许匿名连接，仅适合本机或可信隔离网络。生产环境必须改用账号、ACL 与 TLS，并在 HomeLoom 的 MQTT Provider 页面中保存对应凭据；Provider 密码会在 PostgreSQL 中加密。
