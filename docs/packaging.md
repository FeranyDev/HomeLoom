# 单二进制与多平台打包

HomeLoom 保持 Go 后端与 React 前端的源码分层。未启用 Matter 时，Vite 静态资源仍通过 Go `embed` 打进同一个可执行文件，发布环境不需要 Nginx 或单独的前端目录。

启用 Matter 后仍是两个持续运行的进程：Go 主服务和 Matter runtime 子进程。打包脚本同时保留两种运行方式：默认 `js` 模式使用 Node.js + `matter-runtime/dist/src/cli.js`，`sea` 模式把后者制作成单文件可执行程序，因此 SEA 部署设备不需要安装 Node.js、npm 或携带 `node_modules`。这仍不是严格的单进程单二进制；sidecar 崩溃隔离和按 Target 重启能力保持不变。

普通 JS runtime 构建要求 Node.js `>=20.19.0`。SEA 构建另外要求构建机使用启用了 SEA 的 Node.js `>=25.5`。Homebrew 等发行版可能在编译时关闭 SEA，可通过 `HOMELOOM_SEA_NODE` 指向官方 Node binary。macOS 构建会自动进行 ad-hoc 签名，也可以用 `HOMELOOM_CODESIGN_IDENTITY` 指定正式签名身份。构建缓存和生成的临时配置全部写入项目 `.cache/`。

## 本机平台

版本号是必填位置参数。默认输出为 `backend/bin/homeloom`，本机普通构建默认使用 JS runtime：

```bash
./scripts/build.sh 0.1.0
```

需要无 Node.js 的 SEA 制品时显式选择 `sea` 模式：

```bash
HOMELOOM_MATTER_RUNTIME_MODE=sea \
HOMELOOM_SEA_NODE=/path/to/sea-enabled/node \
./scripts/build.sh 0.1.0
```

也可以指定输出位置：

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

默认在根目录 `dist/` 生成 Go 制品。普通 `js` 模式的输出目录如下：

- `dist/linux_amd64/homeloom`；
- `dist/linux_arm64/homeloom`；
- `dist/darwin_amd64/homeloom`；
- `dist/darwin_arm64/homeloom`；
- `dist/windows_amd64/homeloom.exe`；
- `dist/windows_arm64/homeloom.exe`；
- `SHA256SUMS`。

`js` 模式保持原来的普通运行方式，目标设备需要另外提供 Node.js 和 `matter-runtime/dist`。需要同时生成每个平台的无 Node.js SEA runtime 时：

```bash
HOMELOOM_MATTER_RUNTIME_MODE=sea \
HOMELOOM_SEA_BUILDER_NODE=/path/to/host/sea-enabled/node \
./scripts/cross-build.sh 0.1.0
```

SEA 模式会从 Node.js 官方发行站下载并校验目标平台的 Node binary，缓存到 `.cache/matter-runtime/node/v<version>/`，并将它与 Go binary 放入同一个平台目录，例如：

```text
dist/linux_amd64/homeloom
dist/linux_amd64/homeloom-matter-runtime
```

`HOMELOOM_SEA_NODE_VERSION` 默认是 `26.5.0`，必须与 `HOMELOOM_SEA_BUILDER_NODE` 的版本一致。跨平台的 Linux、Windows 和非当前平台 macOS SEA 不做本机执行检查；发布签名应在目标平台 CI 中完成。当前 macOS host 对当前平台 macOS target 会进行 ad-hoc 签名和冒烟测试。

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

普通 JS runtime 的安装、测试和构建：

```bash
./scripts/dev-env.sh sh -c 'cd matter-runtime && npm ci && npm test'
```

单独构建 SEA runtime：

```bash
HOMELOOM_SEA_NODE=/path/to/sea-enabled/node \
./scripts/dev-env.sh sh -c \
  'cd matter-runtime && npm run build:sea -- --output backend/bin/homeloom-matter-runtime'
```

Go 主服务为每个启用的 Matter Target 启动一个独立 sidecar、Unix Socket 和身份命名空间。它默认先尝试 `node matter-runtime/dist/src/cli.js`，Node.js 不可用时再从 Go 可执行文件附近发现 `homeloom-matter-runtime`。也可以通过 `HOMELOOM_MATTER_RUNTIME` 指定 SEA binary 或 JavaScript 入口；两者都缺失时，Target 错误会进入 Web 端的 `TargetInfo.Error`：

```bash
# 普通方式
HOMELOOM_MATTER_RUNTIME=/opt/homeloom/matter-runtime/dist/src/cli.js \
./backend/bin/homeloom

# SEA 方式
HOMELOOM_MATTER_RUNTIME=/opt/homeloom/homeloom-matter-runtime \
./backend/bin/homeloom
```

`@matter/main` 固定为 `0.17.7`，禁止自动漂移到 nightly。

## 容器镜像

现有统一镜像仍只包含 HomeLoom core，因此只适合 Web/API、Provider 和 HomeKit；它不会静默把 Matter Target 回退到 HomeKit。Matter 容器发布需要后续镜像显式加入对应平台的 SEA runtime 和 host network/mDNS 配置；SEA 镜像内不需要另装 Node.js。若使用普通 JS 模式，则镜像仍需显式加入 Node.js、锁定依赖和 `matter-runtime/dist`。

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
- JS 模式下六个平台实际交叉编译；
- SEA 模式下每个目标平台的 Node binary 下载、SHA-256 校验和 SEA 生成；
- 当前 host 平台的 SEA 运行时冒烟测试；
- 每个 Go 与 Matter runtime 制品的 SHA-256 校验值。

`CGO_ENABLED=0` 用于避免运行环境依赖额外的 C 动态库。SQLite 备选后端通过纯 Go 的 `github.com/ncruces/go-sqlite3/gormlite` 一并编入这六个平台的可执行文件，不要求目标设备安装 SQLite 或 C 运行库。跨平台构建成功只证明编译和资源嵌入有效；HomeKit mDNS、网络接口和防火墙行为仍需在相应操作系统上做实机验收。
