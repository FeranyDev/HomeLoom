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
- `GET /ready`：检查必要运行依赖；SQLite 可访问时返回 200，否则返回 503 和分项原因；
- `GET /api/v1/system/version`：版本、commit、构建时间和 Go 版本；
- `GET/PUT /api/v1/system/settings`：读取或实时更新 SQLite 中的运行时设置；
- `GET /api/v1/diagnostics`：设备、事件、命令、订阅及 Go runtime 快照；
- `GET /metrics`：Prometheus 文本指标；
- `GET /api/v1/devices/{id}/states`：内存状态、来源和质量；
- `GET /api/v1/commands`：命令生命周期历史。

`/metrics` 中的 runtime 指标包括 `homeloom_go_goroutines`、`homeloom_go_heap_alloc_bytes` 和 `homeloom_go_heap_objects`。事件指标包含入队到处理完成的平均/最大延迟及超过 100ms 的慢 Handler 计数；SQLite 指标包含配置、身份、schema、健康检查和备份操作数以及平均/最大延迟；`homeloom_homekit_pushes_total` 统计运行期间应用到 HomeKit Characteristic 的状态更新次数。命令协调指标 `homeloom_command_queue_pending` 和 `homeloom_command_queue_max_pending` 分别表示当前等待 Provider 执行槽的命令数及进程生命周期内的最大等待数。

属性写入和 Action 按 Device ID 串行进入 Provider，不同设备仍可并行执行。等待执行槽时会响应 HTTP 请求取消或 deadline，取消的请求不会创建命令记录，也不会到达 Provider。

属性写入会在创建命令和调用 Provider 之前按模型校验 typed payload、`min`、`max` 与整数约束。类型不匹配、包含多个 payload 或越界统一返回 `400 bad_request`；不存在或不可写的属性返回 422。校验失败不会产生虚假的命令历史。

命令确认超时由 `system_settings.command_timeout_seconds` 保存，允许范围为 1–300 秒，默认 5 秒；历史上限由 `command_history_limit` 保存，允许 100–10000 条，默认 1000 条。两项设置通过同一事务保存并实时应用。新超时只用于之后创建的命令；降低历史上限会立即清理最旧的终态记录，但不删除执行中的命令。

Provider 上报时间与接收时间相差超过 5 分钟时，Core 会记录时钟漂移指标，并将 State Store 的 `observedAt` 钳制为接收时间，避免错误的未来时间戳长期阻止正常状态合并。原始设备快照时间仍保留用于排查。

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

Core 会根据 Capability 中的 `CommandDefinition` 校验必填参数、未声明参数和 typed payload。动作会记入统一命令历史，`kind` 为 `action`。命令定义通过 `idempotent` 明确声明是否可安全重放；Core 当前不会自动重试 Provider 调用，非幂等动作必须由上层使用同一个 `Idempotency-Key` 重放原 HTTP 请求。

命令的传输状态和执行结果分开表达：`confirmed` 对应 `outcome=succeeded`，`rejected` 对应 `outcome=failed`，`timeout` 或已发送后被替代对应 `outcome=unknown`。超时只说明在期限内没有获得可靠确认，不表示设备一定没有执行。由于普通 Provider 状态事件缺少可靠的 command correlation ID，后续恰好出现相同属性值也不会擅自改写该命令的未知结果。

动作请求建议携带最长 128 字符的 `Idempotency-Key` header。在命令历史保留期内，相同设备、Capability、Command、参数和 key 只执行一次；重复请求返回原命令。同一作用域的 key 如果被复用为不同参数，返回 409 `conflict`。

## Provider 敏感配置

Provider 配置仍以完整值保存在 SQLite 中，但管理 API 会递归识别 password、secret、token、API key、private key 和 credential 类字段，并以 `********` 返回。编辑时保留该占位符会沿用数据库中的原值；输入新值会替换原值。新建 Provider 时不能把占位符当作真实密钥提交。数组对象优先按稳定 `id` 恢复密钥，避免配置重排后发生错配。

HomeKit PIN 在 SQLite 中使用 AES-256-GCM 加密，主密钥自动保存为数据库旁的 `<database>.key` 文件并强制使用 `0600` 权限。已有明文 PIN 会在升级后的首次启动自动加密；数据库包含密文但密钥缺失时服务会拒绝启动，避免静默生成错误密钥。数据库备份会同时生成同名 `.key` 配套文件，两者必须一起保管和恢复。

SQLite 主数据库文件在打开后强制设置为 `0600`。HomeKit 身份目录由 HomeLoom 自己实现的安全 Store 管理：目录为 `0700`、身份与配对文件为 `0600`，启动时会修复已有权限并拒绝身份目录中的符号链接。
