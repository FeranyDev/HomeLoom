# HomeLoom HTTP API 约定

管理 API 当前版本前缀为 `/api/v1`。成功响应统一使用 `data` 包装资源；部分写入同时返回 `command`。

```json
{
  "data": {}
}
```

## 管理认证

首次运行时，前端通过 `POST /api/v1/auth/setup` 创建数据库中唯一的管理员。密码要求 12–128 个字符，只保存 bcrypt 哈希。之后使用 `POST /api/v1/auth/login` 登录；Session 的随机令牌只通过 `HttpOnly`、`SameSite=Strict` Cookie 传递，数据库只保存令牌的 SHA-256 哈希，会话有效期为 24 小时且重启后仍有效。HTTPS 直连或反向代理通过 `X-Forwarded-Proto: https` 明确原始协议时，Cookie 会附带 `Secure`。

Web 管理面只有这一个管理员身份，不实现普通用户、角色或按设备授权。HomeLoom Web 用于接入、桥接、映射、诊断和运维；家庭成员的日常设备控制与共享由 Apple Home 管理。

`/health`、`/ready`、`/api/versions`、`/api/v1/system/version` 和认证初始化入口保持公开。其余 v1 管理接口、`/metrics` 和 HomeKit 配对二维码均要求登录。所有 `POST`、`PUT`、`PATCH`、`DELETE` 请求还必须把 `homeloom_csrf` Cookie 的值放入 `X-CSRF-Token` 请求头；前端 API Client 会自动完成此操作。

同一直连客户端在五分钟窗口内连续登录失败 5 次后会锁定 5 分钟。限速不读取 `X-Forwarded-For`，避免在尚未配置可信代理时被伪造来源绕过。认证响应禁止缓存，退出会立即从 SQLite 删除 Session。

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
该值也会作为写操作的 `correlationId` 写入审计事件，并附加到由该请求创建的命令历史中，因此可以从前端操作一路定位到 HTTP 日志、审计记录和 Provider 命令。服务端会去除首尾空白并将 correlation ID 限制为 128 字符。

## API 版本兼容

- `GET /api/versions` 返回当前、仍受支持和已弃用的 API 版本；
- 所有 `/api/v1/*` 响应携带 `HomeLoom-API-Version: 1`；
- v1 内允许新增可选字段、新资源和新枚举能力，不删除字段、不改变字段类型或既有状态码语义；
- 破坏性变化必须发布新的路径主版本，并在旧版本仍受支持时同时提供；
- 未识别的响应字段必须由客户端忽略，新增请求字段默认保持可选。

## 常用状态码

| 状态码 | code | 说明 |
| --- | --- | --- |
| 400 | `bad_request` | JSON 或参数不合法 |
| 404 | `not_found` | 资源不存在 |
| 408 | `request_timeout` | Provider 操作取消或超时 |
| 413 | `payload_too_large` | 上传的恢复包超过 256 MiB 限制 |
| 422 | `unprocessable_entity` | 设备不支持该属性或操作 |
| 503 | `service_unavailable` | Provider 当前不可用 |
| 500 | `internal_error` | 未预期的服务端错误 |

## 诊断入口

- `GET /health`：进程存活；
- `GET /ready`：检查必要运行依赖；SQLite 可访问时返回 200，否则返回 503 和分项原因；
- `GET /api/v1/system/version`：版本、commit、构建时间和 Go 版本；
- `GET/PUT /api/v1/system/settings`：读取或实时更新 SQLite 中的运行时设置；
- `GET /api/v1/system/config-export`：下载脱敏后的数据库配置快照；
- `GET /api/v1/system/diagnostic-bundle`：下载版本、指标、脱敏配置和最近审计事件组成的诊断包；
- `POST /api/v1/system/backup`：输入精确确认短语 `BACKUP` 后下载 SQLite 与数据库主密钥组成的 ZIP 完整备份；
- `POST /api/v1/system/restore`：上传完整备份并输入 `RESTORE`，完成完整性、Schema 和密钥校验后暂存；下次进程启动前原子替换、保留恢复前快照，并清空备份中的旧浏览器 Session；
- `GET /api/v1/diagnostics`：设备、事件、命令、订阅及 Go runtime 快照；
- `GET /metrics`：Prometheus 文本指标；
- `GET /api/v1/devices/{id}/states`：内存状态、来源和质量；
- `PUT /api/v1/devices/{id}/enabled`：持久禁用或重新启用设备；
- `GET /api/v1/commands`：命令生命周期历史。
- `GET /api/v1/audit-events?limit=200`：按时间倒序读取持久化审计事件，`limit` 范围为 1–500；
- `GET /api/v1/events/audit`：通过 SSE 实时接收 `audit` 事件。

所有 `/api/v1` 下的 POST、PUT、PATCH 和 DELETE 都记录审计事件，包括失败的操作。审计表只保存 actor、方法、模板化路由、资源 ID、状态码、结果和 correlation ID，不保存请求体或配置值，避免 Provider 凭据和 HomeKit PIN 进入日志。记录保存在 SQLite 的 `audit_events` 表中，当前自动保留最近 5000 条；写请求会在返回前同步尝试持久化，审计失败会写入结构化错误日志，但不会把已经完成的业务操作伪装成失败。SSE 订阅使用有界缓冲，慢客户端只会漏掉实时通知，不影响已经落库的历史。

`/metrics` 中的 runtime 指标包括 `homeloom_go_goroutines`、`homeloom_go_heap_alloc_bytes` 和 `homeloom_go_heap_objects`。事件指标包含入队到处理完成的平均/最大延迟及超过 100ms 的慢 Handler 计数；SQLite 指标包含配置、身份、schema、健康检查和备份操作数以及平均/最大延迟；`homeloom_homekit_pushes_total` 统计运行期间应用到 HomeKit Characteristic 的状态更新次数。命令协调指标 `homeloom_command_queue_pending` 和 `homeloom_command_queue_max_pending` 分别表示当前等待 Provider 执行槽的命令数及进程生命周期内的最大等待数。

属性写入和 Action 按 Device ID 串行进入 Provider，不同设备仍可并行执行。等待执行槽时会响应 HTTP 请求取消或 deadline，取消的排队请求不会创建命令记录，也不会到达 Provider。

同一属性已有执行中或待确认命令时，相同 typed value 不会再次调用 Provider，而是返回原命令并增加 `coalescedRequests`；对应累计指标为 `homeloom_commands_coalesced_total`。后写不同值会把旧命令切换为 `superseded/outcome=unknown`，通过 context 协作取消仍在执行的 Provider 调用，并在设备串行槽释放后执行新值。旧 HTTP 请求返回 409；终态保护会阻止迟到的 Provider 返回把旧命令改回 accepted/rejected。Provider 如果不响应 context，物理设备仍可能短暂执行旧值，因此新值始终会继续发送而不是假定取消已经生效。

属性写入会在创建命令和调用 Provider 之前按模型校验 typed payload、`min`、`max` 与整数约束。类型不匹配、包含多个 payload 或越界统一返回 `400 bad_request`；不存在或不可写的属性返回 422。校验失败不会产生虚假的命令历史。

命令确认超时由 `system_settings.command_timeout_seconds` 保存，允许范围为 1–300 秒，默认 5 秒；历史上限由 `command_history_limit` 保存，允许 100–10000 条，默认 1000 条。两项设置通过同一事务保存并实时应用。新超时只用于之后创建的命令；降低历史上限会立即清理最旧的终态记录，但不删除执行中的命令。

HomeKit 桥提供两个独立高风险入口：`POST /api/v1/targets/{id}/pairing/regenerate` 要求 `REGENERATE {id}`，只更换 PIN 与 Setup ID 并保留既有控制器身份；`DELETE /api/v1/targets/{id}/pairing-identity` 要求 `CLEAR {id}`，会先停止对应桥、拒绝符号链接或越界身份路径、清除 HAP 密钥及控制器配对文件，再按原配置重建桥。删除普通 Target 配置仍默认保留身份目录。

Provider 上报时间与接收时间相差超过 5 分钟时，Core 会记录时钟漂移指标，并将 State Store 的 `observedAt` 钳制为接收时间，避免错误的未来时间戳长期阻止正常状态合并。原始设备快照时间仍保留用于排查。

带正数 `sequence` 的 Provider 快照按设备进行单调比较。重复或倒退事件不会覆盖设备列表或属性状态，并计入 `homeloom_provider_events_ignored_total`。Virtual Provider 的 simulation API 支持 `sequence` 和 1–10 次的 `repeat`，管理页可直接触发重复与旧序列事件。

设备临时离线仅来自 Provider availability，不写数据库；人工禁用写入 `device_preferences`，实时标记状态 unavailable 并阻止读写及后续 Provider 事件复活；设备从 Provider 配置中删除后保留 `removed` tombstone 和稳定身份。重新出现相同设备 ID 时可恢复原身份。State 的 `known/available/unavailableReason` 区分“从未有值”和“最后值暂时不可用”；前者输出 `value:null`，后者保留 typed value。属性写入不接受 `null`。

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

命令的传输状态和执行结果分开表达：`confirmed` 对应 `outcome=succeeded`，`rejected` 对应 `outcome=failed`，`timeout` 或已发送后被替代对应 `outcome=unknown`。命令中的 `correlationId` 对应创建它的 HTTP `X-Request-Id`；它用于追踪请求而不是确认设备状态。超时只说明在期限内没有获得可靠确认，不表示设备一定没有执行。由于普通 Provider 状态事件缺少可靠的 command ID，后续恰好出现相同属性值也不会擅自改写该命令的未知结果。

动作请求建议携带最长 128 字符的 `Idempotency-Key` header。在命令历史保留期内，相同设备、Capability、Command、参数和 key 只执行一次；重复请求返回原命令。同一作用域的 key 如果被复用为不同参数，返回 409 `conflict`。

## Mapping 预览

`POST /api/v1/mapping/preview` 接受带 `schemaVersion`、稳定 ID、版本、Profile kind、输入/输出 typed value 类型和转换流水线的临时 Profile。预览是无状态操作，不保存 Profile，也不写设备。当前转换包括：

- `invert`：布尔反转；
- `scale`：数值缩放和偏移，反向按逆公式执行；
- `clamp`：范围裁剪，因为信息丢失而明确拒绝反向执行；
- `enum`：一对一枚举映射，重复目标值会在校验阶段拒绝；
- `unit`：摄氏、华氏、开尔文以及比例、百分比转换。

响应包含最终 typed value 和每一步的输入、输出及 transform 索引。错误 Profile 使用统一字段错误结构返回，字段路径例如 `profile.transforms.0.factor`，便于前端准确定位。反向预览逆序运行 transform，用于提前验证 Target 控制写回 Provider 的可逆性。

Profile 管理入口为 `GET/POST /api/v1/mapping/profiles` 和 `GET/PUT/DELETE /api/v1/mapping/profiles/{id}`。用户 Profile 保存到 SQLite `mapping_profiles`，应用层在数据库成功提交后原子替换内存快照；后续预览只需提交 `profileId`，无需重启即可解析最新版本。更新必须把 `version` 增加到当前版本之上，避免旧页面覆盖新配置。

HomeLoom 随程序提供 provider、capability 和 target 三类内置示例 Profile。内置 ID 只读且不能被用户配置覆盖。`POST /api/v1/mapping/profiles/import` 会先验证完整批次，再使用单个 SQLite 事务写入，任何一项错误都不会产生部分导入。`GET /api/v1/mapping/profiles/export` 只导出用户 Profile，生成的文件可以直接重新导入；全局脱敏配置导出和诊断包则包含内置与用户 Profile，便于还原排障上下文。

运行时属性绑定入口为 `GET/POST /api/v1/mapping/bindings` 和 `GET/PUT/DELETE /api/v1/mapping/bindings/{id}`。绑定存储于 SQLite `mapping_bindings`，精确指定 Provider、设备、Endpoint、Capability 和 Property 路径；ID 可以省略并由后端生成。启用后，Provider 快照和读取结果执行 `forward` 转换，属性控制执行 `reverse` 转换。保存、启停或删除会同步重新发现并处理当前 Provider 快照，不重启 Provider 或 Target。首版运行时绑定要求 Profile 不是 target kind、输入输出类型一致且每一步可逆；因此 `clamp` 不能绑定到真实读写链路。正在被绑定引用的用户 Profile 不能删除，也不能更新为不兼容流水线。

诊断响应的 `mappingApplied` 和 `mappingErrors` 分别统计运行时转换命中与失败。转换失败的 Provider 事件不会覆盖内存中的上一份有效设备状态；错误配置仍保存在数据库中，便于管理员修正而不会触发服务重启循环。全局脱敏配置导出和 SQLite 备份均包含属性绑定。

## Provider 敏感配置

Provider 配置仍以完整值保存在 SQLite 中，但管理 API 会递归识别 password、secret、token、API key、private key 和 credential 类字段，并以 `********` 返回。编辑时保留该占位符会沿用数据库中的原值；输入新值会替换原值。新建 Provider 时不能把占位符当作真实密钥提交。数组对象优先按稳定 `id` 恢复密钥，避免配置重排后发生错配。

配置导出使用独立 DTO，不复用包含运行时配对资料的 Target 页面响应。它保留排障所需的 Target ID、类型、名称、地址、Setup ID 和设备绑定，但始终排除 PIN、Setup URI 与本地身份存储路径；Provider 配置沿用递归凭据脱敏。诊断包只在此安全快照之上增加构建信息、当前聚合指标和不含请求体的审计元数据。两个下载响应均携带 `Cache-Control: no-store` 与 `Content-Disposition: attachment`。

进程结构化日志统一经过敏感属性过滤器。敏感键直接替换为 `********`，错误文本或 URL 中常见的 `token=...`、`api_key=...`、`password=...` 等赋值也会在输出前清理。调用方仍不应把完整请求体作为无语义字符串写入日志。

HomeKit PIN 在 SQLite 中使用 AES-256-GCM 加密，主密钥自动保存为数据库旁的 `<database>.key` 文件并强制使用 `0600` 权限。已有明文 PIN 会在升级后的首次启动自动加密；数据库包含密文但密钥缺失时服务会拒绝启动，避免静默生成错误密钥。Web 完整备份把数据库和配套密钥封装为一个 ZIP；该文件可解密 Provider 凭据与桥 PIN，必须按敏感文件保管。HAP 控制器配对目录不在 SQLite 备份中。

SQLite 主数据库文件在打开后强制设置为 `0600`。HomeKit 身份目录由 HomeLoom 自己实现的安全 Store 管理：目录为 `0700`、身份与配对文件为 `0600`，启动时会修复已有权限并拒绝身份目录中的符号链接。
