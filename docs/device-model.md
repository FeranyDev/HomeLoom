# HomeLoom Device Model v1

HomeLoom 的设备快照使用统一的 Endpoint / Capability / Property 模型。实时状态由 Provider 在启动和重连后重新上报，SQLite 不保存设备实时值。

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
  "online": true,
  "endpoints": [{
    "id": "main",
    "name": "主端点",
    "type": "sensor",
    "capabilities": [{
      "id": "temperature",
      "type": "temperature-sensor",
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

## 属性值契约

- `bool` 使用 `bool` payload；
- `number` 使用 `number` payload，并校验 `min`、`max`；
- `string` 使用 `string` payload；
- `enum` 使用 `string` payload，值必须出现在定义的 `enum` 列表中；
- 一个值必须且只能包含一个 typed payload；
- 定义类型和值类型必须一致；
- `step` 必须大于零，`min` 不得大于 `max`。

设备可用性仍由 `online` 表示；属性的新鲜度、来源、sequence、quality 和乐观命令状态由内存 State Store 单独维护。

## 属性读取 API

Provider 可实现可选 `PropertyReader`，用于跳过列表快照直接读取属性：

```http
GET /api/v1/devices/{deviceId}/endpoints/{endpointId}/capabilities/{capabilityId}/properties/{propertyId}
```

返回 `{ "data": Property }`。设备不存在返回 404，属性不支持返回 422，Provider 离线返回 503，请求取消或超时返回 408。前端设备详情页的“从 Provider 读取”操作使用该接口。

Capability 还可以通过 `commands` 声明 Action。每个 `CommandDefinition` 使用稳定 ID，并可声明 bool、number、string 或 enum 类型的必填/可选参数。
