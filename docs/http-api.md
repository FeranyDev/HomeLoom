# HomeLoom HTTP API 约定

管理 API 当前版本前缀为 `/api/v1`。成功响应统一使用 `data` 包装资源；部分写入同时返回 `command`。

```json
{
  "data": {}
}
```

## 错误响应

所有由 HTTP API 返回的错误使用同一结构：

```json
{
  "code": "not_found",
  "message": "device not found",
  "requestId": "01J...",
  "fields": {
    "name": "required"
  }
}
```

- `code` 是适合程序判断的稳定错误类别；
- `message` 是可展示信息；
- `requestId` 与响应头 `X-Request-Id`、结构化日志一致；
- `fields` 仅在服务端能够定位具体表单字段时返回；
- 500 错误不向客户端暴露内部错误内容。

客户端可以传入 `X-Request-Id` 以关联一次跨服务调用；未传入时由 HomeLoom 自动生成。

## 常用状态码

| 状态码 | code | 说明 |
| --- | --- | --- |
| 400 | `bad_request` | JSON 或参数不合法 |
| 404 | `not_found` | 资源不存在 |
| 408 | `request_timeout` | Provider 操作取消或超时 |
| 422 | `unprocessable_entity` | 设备不支持该属性或操作 |
| 503 | `service_unavailable` | Provider 当前不可用 |
| 500 | `internal_error` | 未预期的服务端错误 |

## 诊断入口

- `GET /health`：进程存活；
- `GET /ready`：进程已完成启动；
- `GET /api/v1/system/version`：版本、commit、构建时间和 Go 版本；
- `GET /api/v1/diagnostics`：设备、事件、命令、订阅及 Go runtime 快照；
- `GET /metrics`：Prometheus 文本指标；
- `GET /api/v1/devices/{id}/states`：内存状态、来源和质量；
- `GET /api/v1/commands`：命令生命周期历史。

`/metrics` 中的 runtime 指标包括 `homeloom_go_goroutines`、`homeloom_go_heap_alloc_bytes` 和 `homeloom_go_heap_objects`。

## Action / Command

Capability 可以声明带 typed parameters 的命令，执行入口为：

```http
POST /api/v1/devices/{deviceId}/endpoints/{endpointId}/capabilities/{capabilityId}/commands/{commandId}
Content-Type: application/json

{
  "parameters": {
    "value": { "type": "bool", "bool": true }
  }
}
```

Core 会根据 Capability 中的 `CommandDefinition` 校验必填参数、未声明参数和 typed payload。动作会记入统一命令历史，`kind` 为 `action`。动作默认不自动重试，避免对非幂等操作产生重复副作用。
