# 容器部署

`compose.yaml` 面向 Linux 主机，前后端保持独立镜像，但共享 host network：

```bash
docker compose up --build -d
```

管理界面位于 `http://主机地址:5173`，后端 API 位于 `8090`。SQLite、WAL 和 HomeKit HAP 身份通过 `homeloom-data` 卷持久化到 `/data`。

## 为什么使用 host network

HomeKit 依赖局域网 mDNS 发现，并要求客户端能够直连每座桥配置的 HAP 端口。普通 Docker bridge 的 multicast、端口动态映射和容器地址通常无法满足这些条件。Linux 上应使用 host network，并在主机防火墙中仅允许可信局域网访问管理端口和 HAP 端口。

Docker Desktop 的 host network 行为与原生 Linux 不完全一致。macOS/Windows 开发环境建议直接运行后端完成 HomeKit 实机验证，容器主要用于 Web/API 构建检查。

## 数据与升级

- 不要删除 `homeloom-data` 卷，否则 SQLite 配置、稳定 HomeKit 身份和配对关系都会丢失；
- 升级前应停止写入并备份该卷；
- 后端启动时自动按顺序执行 SQLite migrations；
- 回滚到旧版本前必须确认旧程序能够识别当前数据库 schema；
- `docker compose down` 保留卷，`docker compose down -v` 会删除卷。

运行中可以生成 SQLite 一致性快照：

```bash
docker compose exec backend homeloom -backup /data/backups/homeloom.db
```

`-backup` 使用 SQLite `VACUUM INTO`，不会对源库执行 migrations，并将输出权限设为 `0600`。该文件包含数据库配置，但不包含 `/data/hap/...` 下的 HomeKit 密钥和配对资料。完整灾难恢复应停止 backend，同时备份整个 `homeloom-data` 卷。

恢复数据库前应保留当前卷副本，停止 backend，替换 `.db` 后移除旧的 `-wal`/`-shm` 临时文件再启动。程序会拒绝打开高于自身支持版本的数据库，不应绕过该检查强制回滚。

管理 API 使用单管理员数据库 Session 和 CSRF 防护，Compose 中的后端仅信任来自 `127.0.0.1/32`、`::1/128` 的前端 Nginx 转发头。Nginx 会覆盖 `X-Forwarded-For` 和 `X-Forwarded-Proto`；直接访问 8090 的局域网客户端不能伪造来源来绕过登录限速。

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

CI 会分别构建 `linux/amd64` 和 `linux/arm64` 的后端、前端镜像，并执行 Go 交叉编译。原生 Linux x86_64 与 ARM64 NAS 可以使用同一份 Compose；必须确认系统支持 host network、mDNS multicast 和 HAP 监听端口。Synology/QNAP 等 NAS 若通过自带反向代理提供管理页，HomeKit 后端仍需要 host network，不能只映射 8090。

OpenWrt 的存储寿命、内存、Go 二进制体积和 mDNS 防火墙差异较大，目前只视为可行性评估目标，不列为受支持部署环境。

## MQTT 开发服务

Compose 中的 `mosquitto` 是可选开发 Broker，使用 host network 监听 `1883`：

```bash
docker compose up -d mosquitto
```

其配置允许匿名连接，仅适合本机或可信隔离网络。生产环境必须改用账号、ACL 与 TLS，并在 HomeLoom 的 MQTT Provider 页面中保存对应凭据；Provider 密码会在 SQLite 中加密。
