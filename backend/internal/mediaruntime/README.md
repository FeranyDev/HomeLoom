# HomeLoom Core Media Runtime

本目录是 HomeLoom Core 的内嵌媒体运行库。Core 直接持有版本化流状态、短期授权解析
与每摄像头 Camera Kernel 生命周期；长期凭据仍只保存在 Core 的加密存储中。

每条有效流启动一个受限的 `homeloom-camera-kernel` 子进程。Kernel 只能按需启动
随发布包提供的 FFmpeg，不能执行其他程序。HomeKit HAP 使用可达的 TCP 监听器；
MP4 预览只通过 `<runtime-dir>/<stream-id>/media.sock` Unix Socket 提供，并由 Core
已认证的 `/api/v1/media/devices/:deviceId/preview.mp4` 路由转发。公开 HAP 端口不会
暴露 `/api/stream.mp4` 或 `/api/frame.mp4`。

流配置继续使用 `(generation, revision)` 保证 replay/upsert/delete 顺序。协议范围
固定为 RTSP、ONVIF→RTSP 与 Xiaomi MISS。解析后的 source URI 只通过子进程环境变量
传入，运行时 YAML 只保存环境变量占位符。

开发验证：

```bash
./scripts/dev-env.sh sh -c 'cd backend && go test ./internal/mediaruntime/...'
```
