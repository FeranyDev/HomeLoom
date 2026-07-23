# HomeLoom Device Model v1

HomeLoom 的设备快照使用统一的 Endpoint / Capability / Property 模型。实时状态由 Provider 在启动和重连后重新上报，PostgreSQL 不保存设备实时值。

标准属性按 `required`、`optional` 和 `custom` 分级，并分别约束 Provider 发布和 Consumer 映射。完整规则与 36 种内置模型目录见 [统一模型参数分级与角色映射](unified-model-parameters.md)。

## 版本和兼容性

- 每个设备快照必须包含 `"schemaVersion": 1`。
- Core 在 Provider 发现、热加载、事件上报和写入返回时校验快照。
- 不支持的版本或无效快照会隔离对应 Provider，不进入 Registry、State Store 或 Target。
- v1 不再提供旧的 `state.power`、`state.temperature` 字段；所有读写统一使用属性路径。

属性路径由四段稳定 ID 构成：

```text
deviceId / endpointId / capabilityId / propertyId
```

## 稳定 ID

设备、Provider、Endpoint、Capability、Property、Command、Command Parameter 和 Event ID 使用相同规则：

- 仅允许小写 ASCII 字母、数字、点、下划线和连字符；
- 必须以字母或数字开始、结束；
- 长度为 1–64 个字符；
- 同一父级内不可重复；
- ID 是持久身份，显示名称变化时不得跟随修改。

合法示例：`living-room.light-1`、`main`、`current-temperature`。

## JSON 示例

```json
{
  "schemaVersion": 1,
  "id": "living-room-temperature",
  "providerId": "virtual-main",
  "name": "客厅温度",
  "type": "temperature-sensor",
  "availability": "online",
  "online": true,
  "endpoints": [{
    "id": "main",
    "name": "主端点",
    "type": "sensor",
    "capabilities": [{
        "id": "temperature",
        "type": "temperature",
      "properties": [{
        "definition": {
          "id": "current-temperature",
          "name": "当前温度",
          "type": "number",
          "unit": "celsius",
          "readable": true,
          "writable": false,
          "notifiable": true,
          "staleAfterSeconds": 30
        },
        "value": { "type": "number", "number": 23.6 }
      }]
    }]
  }],
  "lastUpdateAt": "2026-07-13T02:00:00Z"
}
```

温度和湿度使用不同的模型与稳定路径：`temperature-sensor` 发布 `main/temperature/current-temperature`，`humidity-sensor` 发布 `main/humidity/current-humidity`。统一模型不再通过单位猜测测量语义；同一设备同时发布温度和湿度时使用 `temperature-humidity-sensor`。

## 属性值契约

- `bool` 使用 `bool` payload；
- `int` 使用 JSON 整数 `int` payload，范围约束和步长也必须是整数；
- `number` 使用 `number` payload，并校验 `min`、`max`；
- `string` 使用 `string` payload；
- `enum` 使用 `string` payload，值必须出现在定义的 `enum` 列表中；
- 一个值必须且只能包含一个 typed payload；
- 写入请求中的 `null` 不是 typed value，统一返回 `400 bad_request`；
- 定义类型和值类型必须一致；
- `step` 必须大于零，`min` 不得大于 `max`。

设备可用性由 `availability` 表示：`online` 代表 Provider 明确确认设备可通信，`offline` 代表 Provider 暂时确认不可通信，`unknown` 代表尚未获得或暂时无法判断。`disabled=true` 是用户持久化到 PostgreSQL 的管理意图，Provider 后续事件不能重新将其置为在线；`removed=true` 是 Provider 配置删除设备后保留的 tombstone，用于保留内部及 HomeKit 稳定身份。恢复同一 ID 的 Provider 设备会清除 tombstone。schema v1 暂时保留 `online` 布尔字段作为兼容投影，只有 `availability=online` 且设备未禁用、未删除时才为 `true`。

State API 使用三个正交字段表达值语义：

- `known=false` 表示从未获得可信观察，`quality=unknown` 且 JSON `value=null`；Provider 模板里的默认值不会被冒充为设备状态；
- `known=true, available=false` 表示保留了最后值但当前不可用，`quality=stale`，`value` 仍是原 typed value；
- `known=true, available=true` 表示 typed value 当前可用；
- `unavailableReason` 解释原因：`never-reported`、`device-offline`、`availability-unknown`、`disabled`、`removed`、`expired`、`command-unconfirmed` 或通用 `stale`；
- Provider 事件状态携带 Core 生成的 `traceId`；命令乐观状态复用 HTTP correlation ID；
- `bool`、`int`、`number`、`string` 和 `enum` 都使用同一状态契约。

因此 `null` 只作为 State API 的“没有历史值”输出，不是可写属性值。设备离线、禁用或删除不会清除最后值；恢复在线并收到 Provider 快照后会重新变为 available。

Provider 可以为快照提供设备级 `sequence`。同一 Provider、同一设备的 sequence 必须单调递增；重复或倒退的在线快照会在更新 Registry 和 State Store 前整体丢弃。离线事件会重置 sequence epoch，使 Provider 热重启后从较小序列重新开始仍能恢复。

## 属性读取 API

Provider 可实现可选 `PropertyReader`，用于跳过列表快照直接读取属性：

```http
GET /api/v1/devices/{deviceId}/endpoints/{endpointId}/capabilities/{capabilityId}/properties/{propertyId}
```

返回 `{ "data": Property }`。设备不存在返回 404，属性不支持返回 422，Provider 离线返回 503，请求取消或超时返回 408。前端设备详情页的“从 Provider 读取”操作使用该接口。

Capability 还可以通过 `commands` 声明 Action。每个 `CommandDefinition` 使用稳定 ID，并可声明 bool、int、number、string 或 enum 类型的必填/可选参数。`idempotent` 字段声明 Provider 动作本身是否可安全重放，例如 `set-power` 是幂等动作，而 `toggle` 不是。该声明与 HTTP `Idempotency-Key` 的请求去重互补，不能据此推断未确认命令已经失败。
