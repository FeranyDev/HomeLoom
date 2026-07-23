# 容器部署

`compose.yaml` 面向 Linux 主机。React 管理界面已经嵌入 Go 二进制，当前只构建一个 HomeLoom core 镜像：

```bash
HOMELOOM_VERSION=0.1.0 docker compose up --build -d
```

管理界面和后端 API 均由 `http://主机地址:8090` 提供。PostgreSQL 17 数据保存在 `postgres-data` 卷；HomeKit HAP 身份和 HomeLoom 主密钥保存在 `homeloom-data` 卷。

> Matter 发布边界：当前镜像未包含 Node.js 20+ 和 `matter-runtime`，因此容器中的 Matter Target 会明确启动失败，不会回退到 HomeKit。Matter 实机部署暂时使用宿主机双制品方式；后续专用镜像必须同时包含 sidecar，并继续使用 host network。

## 为什么使用 host network

HomeKit 和 Matter 都依赖局域网 mDNS 发现；Matter 还要求 IPv6 与控制器可直连每座桥的 UDP 端口。普通 Docker bridge 的 multicast、IPv6、动态端口映射和容器地址通常无法满足这些条件。Linux 上应使用 host network，并在主机防火墙中仅允许可信局域网访问管理端口、HAP TCP 端口和所配置的 Matter UDP 端口。

Docker Desktop、Apple Container 和原生 Linux 的 host network/mDNS 行为不同。macOS/Windows 开发环境建议直接运行双制品完成 HomeKit/Matter 实机验证，容器主要用于 Web/API 构建检查。Matter 控制器与 HomeLoom 必须位于允许 IPv6 link-local、UDP 单播和 mDNS multicast 的同一可达网络。

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
