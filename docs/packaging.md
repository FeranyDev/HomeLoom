# 单二进制与多平台打包

HomeLoom 保持 Go 后端与 React 前端的源码分层。未启用 Matter 时，Vite 静态资源仍通过 Go `embed` 打进同一个可执行文件，发布环境不需要 Nginx 或单独的前端目录。

启用 Matter 后发布形态明确为两个持续运行的制品：Go 主服务和 `matter-runtime` Node.js sidecar。当前 sidecar 要求 Node.js 20+，不属于严格的单进程单二进制发布。`scripts/build.sh` 会同时构建 Go core 与 `matter-runtime/dist/src/cli.js`；部署者必须把整个 `matter-runtime/dist`、锁定依赖和 Node.js 20+ 一起交付。若将来恢复“设备上只运行一个自包含二进制”的硬约束，必须先完成 Node SEA 的逐平台验证，或切换 ConnectedHomeIP 原生路线，不能把当前 sidecar 描述为单二进制。

## 本机平台

版本号是必填位置参数：

```bash
./scripts/build.sh 0.1.0
```

默认输出为 `backend/bin/homeloom`。也可以指定输出位置：

```bash
./scripts/build.sh 0.1.0 dist/homeloom
```

构建完成后可以直接检查 core 版本：

```bash
backend/bin/homeloom -version
```

## 多平台

```bash
./scripts/cross-build.sh 0.1.0
```

默认在根目录 `dist/` 生成：

- `linux/amd64`；
- `linux/arm64`；
- `darwin/amd64`；
- `darwin/arm64`；
- `windows/amd64`；
- `windows/arm64`；
- `SHA256SUMS`。

第二个参数可以覆盖输出目录：

```bash
./scripts/cross-build.sh 0.1.0 .cache/release-test
```

只构建部分目标时使用空格分隔的 `HOMELOOM_TARGETS`：

```bash
HOMELOOM_TARGETS='linux/amd64 linux/arm64' ./scripts/cross-build.sh 0.1.0
```

不在默认白名单中的 GOOS/GOARCH 会被拒绝，避免错误拼写生成意外制品。

## 构建信息

版本号只从第一个位置参数读取，并限制为字母、数字、点、下划线、加号和连字符。Commit 和构建时间默认从当前 Git 仓库与 UTC 时间生成，也可以覆盖：

```bash
HOMELOOM_COMMIT=abcdef123456 \
HOMELOOM_BUILD_TIME=2026-07-22T00:00:00Z \
./scripts/build.sh 0.1.0
```

这些值通过 Go linker flags 写入 `internal/buildinfo`，可通过以下入口读取：

- `homeloom -version`；
- `GET /api/v1/system/version`；
- 管理界面右上角版本标签；
- 诊断包构建信息。

## Web UI 嵌入边界

普通开发构建不携带 Web UI，继续使用 Vite `5173` 开发服务器和 API 代理。打包脚本使用 `embed_webui` 构建标签：

1. `npm run build:embed` 输出到 `backend/internal/webui/dist/`；
2. Go `embed` 将产物编译进可执行文件；
3. 后端在管理端口同时提供页面和 API；
4. `/assets/*` 使用长期 immutable 缓存；
5. `index.html` 和 SPA fallback 使用 `no-cache`；
6. SPA fallback 不会吞掉 `/api`、`/health`、`/ready` 或 `/metrics` 的 404。

嵌入目录、最终制品和所有构建缓存均被 Git 忽略。Go、Go Module 和 npm 缓存仍由 `scripts/dev-env.sh` 放在项目根目录 `.cache/`。

## Matter sidecar

安装锁定依赖并构建：

```bash
./scripts/dev-env.sh sh -c 'cd matter-runtime && npm ci && npm run build'
```

Go 主服务为每个启用的 Matter Target 启动一个独立 sidecar、Unix Socket 和身份命名空间。默认入口为 `matter-runtime/dist/src/cli.js`，部署到其他位置时必须设置绝对路径：

```bash
HOMELOOM_MATTER_RUNTIME=/opt/homeloom/matter-runtime/dist/src/cli.js \
./backend/bin/homeloom
```

所有 npm、TypeScript 和 Matter Runtime 构建缓存都进入项目 `.cache/`。`@matter/main` 固定为 `0.17.7`，禁止自动漂移到 nightly。

## 容器镜像

现有统一镜像仍只包含 HomeLoom core，因此只适合 Web/API、Provider 和 HomeKit；它不会静默把 Matter Target 回退到 HomeKit。Matter 容器发布需要后续镜像显式加入 Node.js 20+、锁定的 sidecar 文件和 host network/mDNS 配置。在此之前，生产 Matter 验收应直接运行双制品部署。

```bash
docker build \
  --file deploy/backend.Dockerfile \
  --build-arg VERSION=0.1.0 \
  --build-arg COMMIT=abcdef123456 \
  --build-arg BUILD_TIME=2026-07-22T00:00:00Z \
  -t homeloom:0.1.0 .
```

Core Compose 使用时同样要求版本号：

```bash
HOMELOOM_VERSION=0.1.0 docker compose up --build -d
```

容器内不再运行 Nginx，也不再构建独立前端镜像。管理页面和 API 都由 `8090` 提供。

## 验证

打包相关的自动验证包括：

- Web UI 文件、缓存头、安全头和 SPA fallback 单元测试；
- API 路径不会被 SPA fallback 覆盖的测试；
- 带 `embed_webui` 标签的真实 Vite 入口测试；
- 本机二进制版本注入检查；
- 六个平台实际交叉编译；
- 每个多平台制品的 SHA-256 校验值。

`CGO_ENABLED=0` 用于避免运行环境依赖额外的 C 动态库。SQLite 备选后端通过纯 Go 的 `github.com/ncruces/go-sqlite3/gormlite` 一并编入这六个平台的可执行文件，不要求目标设备安装 SQLite 或 C 运行库。跨平台构建成功只证明编译和资源嵌入有效；HomeKit mDNS、网络接口和防火墙行为仍需在相应操作系统上做实机验收。
