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

当前管理 API 尚未实现认证。不要直接暴露到公网；如需开放到局域网，应配合主机防火墙或可信反向代理限制来源。
