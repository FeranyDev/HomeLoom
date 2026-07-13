# 统一模型参数分级与角色映射

HomeLoom 的标准设备模型由 Core 中的版本化契约目录定义。Provider 是设备发布者，HomeKit、Web、自动化和后续 Target 是设备使用者；双方不再靠字段名碰运气。

## 参数等级

| 等级 | Provider / 发布者 | Consumer / 使用者 |
| --- | --- | --- |
| `required` | 必须发布正确路径和类型；缺失时整个设备快照在 Provider 边界被拒绝 | Consumer 声明支持该设备类型时必须映射；缺失时拒绝创建不完整设备 |
| `optional` | 有能力时发布，发布后必须符合标准类型 | 显式声明支持时映射；设备未发布时允许降级，不阻止接入 |
| `custom` | 未进入标准契约的属性会被保留并自动标记为自定义 | 默认不可见；Consumer 必须显式声明源路径和目标路径才能消费 |

Consumer 可以把统一模型中的可选参数提升为自己的必须参数。例如当前 HomeKit Consumer 会要求 Fan 的目标模式和转速，以及 Air Purifier 的目标模式、转速和滤芯状态，因为对应 HAP Service 的实现依赖这些值；不需要这些参数的其他 Consumer 仍可按统一模型最低必选集接入。

设备 API 的每个 `PropertyDefinition` 都包含 `parameterLevel`。完整发布者契约可通过 `GET /api/v1/device-models` 获取，其中同时给出 `must-publish`、`must-map`、`publish-if-supported`、`map-if-supported` 和自定义参数策略。

## 当前模型契约

| 模型 | 必须参数 | 可选标准参数 |
| --- | --- | --- |
| Switch | `switch.power` | — |
| Lightbulb | `switch.power` | 亮度、色温、色相、饱和度 |
| Outlet | `switch.power` | 使用状态、当前功率、累计电量 |
| Temperature Sensor | 当前温度 | 电量、低电量、防拆 |
| Humidity Sensor | 当前湿度 | 电量、低电量、防拆 |
| Contact Sensor | 接触状态 | 电量、低电量、防拆 |
| Motion Sensor | 活动状态 | 电量、低电量、防拆 |
| Fan | 启用、当前状态 | 目标模式、转速、摇头、旋转方向、物理控制锁 |
| Air Purifier | 启用、当前状态 | 目标模式、转速、摆风、控制锁、空气质量、PM2.5、VOC、滤芯状态 |
| Window Covering | 当前位置、目标位置、运动状态 | 障碍物检测 |

## 运行时边界

```text
Provider snapshot
  -> 标准路径识别和 parameterLevel 标注
  -> 必须参数、类型和值域校验
  -> Registry / State Store
  -> Consumer contract projection
       required: 必须映射
       optional: 存在才映射
       custom: 仅显式路径映射
  -> HomeKit / Web / future Target
```

HomeKit Consumer 契约位于 `backend/internal/targets/homekit/model_contract.go`。它把统一路径映射到具体 HAP Characteristic，并允许同一 Provider 设备在不同 Consumer 中选择不同的可选参数集合。

Virtual Provider 会发布当前契约中的完整可选参数集合，用于开发和回归测试。真实 Provider 可以只发布必须参数，再根据设备能力逐项增加可选参数。
