# 日志规范

HomeLoom 的 Go 进程统一使用 `go.uber.org/zap`。Backend 日志直接写入启动终端，Camera Kernel
日志由 Backend 采集并在管理端展示；Matter sidecar 的 JSON 日志在采集时映射到同一模型。

个别第三方接口（当前为 MQTT Broker）公开 API 仍要求 `log/slog`。这类调用只能通过
`platform/logging.SlogAdapter` 进入 Zap，禁止在业务代码中创建第二套日志输出链路。

## 结构化字段

每条日志使用单行 JSON，公共字段如下：

| 字段 | 说明 |
| --- | --- |
| `time` | RFC 3339 时间，包含时区和毫秒以上精度 |
| `level` | `debug`、`info`、`warn`、`error` |
| `msg` | 简短、稳定的事件描述，不包含模块前缀 |
| `component` | 进程组件：`backend`、`camera-kernel`、`matter-js` |
| `module` | 进程内模块，例如 `http`、`homekit`、`ffmpeg`、`rtsp` |
| `instance` | 多实例运行单元，例如 Target ID 或 Stream ID |
| `error` | 错误文本，仅在失败事件中出现 |

业务上下文使用稳定的 snake_case 字段，例如 `request_id`、`provider_id`、`target_id`、
`stream_id`。消息中禁止使用 `[homekit]`、`[ffmpeg]` 等前缀，来源必须由字段表达。

## 等级

- `debug`：协议帧、重试细节和高频内部状态；生产环境默认关闭。
- `info`：启动、停止、连接建立、配对和媒体会话等正常生命周期。
- `warn`：可恢复失败、降级、无效输入和资源压力。
- `error`：当前操作失败或运行单元退出，需要运维关注。

Backend 使用 `-log-level` 设置等级；Camera Kernel、HomeKit、FFmpeg 等子程序模块使用 YAML
中的 `logging.child_level`。两者仅接受 `debug`、`info`、`warn`、`error`。

## 安全

结构化字段和错误文本必须通过统一脱敏写入链路。禁止记录 Provider 凭据、账号密码、Token、
HomeKit PIN、Setup URI、SRTP 密钥、附件私钥以及完整带凭据源地址。新增日志调用时必须使用
独立字段表达上下文，并为凭据或地址相关场景补充脱敏测试。
