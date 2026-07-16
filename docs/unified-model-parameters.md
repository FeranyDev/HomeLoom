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
| Single Property Sensor | `sensor.value` 单一数值；单位与范围由设备实例提供 | 电量、低电量 |
| Temperature Humidity Sensor | 当前温度、当前湿度 | 电量、低电量 |
| Contact Sensor | 接触状态 | 电量、低电量、防拆 |
| Motion Sensor | 活动状态 | 电量、低电量、防拆 |
| Fan | 启用、当前状态 | 目标模式、转速、摇头、旋转方向、物理控制锁 |
| Air Purifier | 启用、当前状态 | 目标模式、转速、摆风、控制锁、空气质量、PM2.5、VOC、滤芯状态 |
| Window Covering | 当前位置、目标位置、运动状态 | 障碍物检测 |
| Illuminance Sensor | 当前照度 | 电量、低电量 |
| Occupancy Sensor | 占用状态 | 电量、低电量、防拆 |
| Leak Sensor | 漏水状态 | 电量、低电量、防拆 |
| Smoke Sensor | 烟雾状态 | 电量、低电量、防拆 |
| Carbon Monoxide Sensor | 一氧化碳告警 | 当前浓度、峰值浓度、电量、低电量、防拆 |
| Carbon Dioxide Sensor | 二氧化碳告警 | 当前浓度、峰值浓度、电量、低电量 |
| Air Quality Sensor | 当前空气质量 | PM2.5、PM10、VOC、CO₂、NO₂、臭氧浓度 |
| Thermostat | 当前状态、目标模式、当前温度、目标温度 | 制热/制冷阈值、当前湿度、显示温标 |
| Air Conditioner v2 | 启用、运行模式、目标温度 | 当前状态、当前温度、风速档位/百分比、上下/左右扫风、导风方向、辅热、睡眠模式、湿度、温标、故障和滤网状态 |
| Heater Cooler | 启用、当前状态、目标模式、当前温度 | 制热/制冷阈值、风速、摆风、控制锁 |
| Humidifier Dehumidifier | 启用、当前状态、目标模式、当前湿度、目标湿度 | 水位、控制锁 |
| Lock | 当前锁定状态、目标锁定状态 | 卡住状态、电量、低电量、防拆 |
| Garage Door | 当前门状态、目标门状态 | 障碍物检测 |
| Security System | 当前布防状态、目标布防状态 | 告警类型、防拆 |
| Valve | 启用、使用状态、阀门类型 | 设定时长、剩余时长 |
| Speaker | 启用、音量、静音 | 当前/目标媒体状态、输入源 |
| Robot Vacuum | 启用、当前状态、目标模式 | 清洁进度、吸力、故障、充电与电量状态 |

内置模型目录和 Consumer 能力目录相互独立。统一模型描述 HomeLoom 内部的稳定语义基准；HomeKit、Matter 或其他 Consumer 只声明自己实际支持的模型和属性，不会因为目录新增模型而被强制实现或伪装成 HomeKit 设备。

`thermostat`、`air-conditioner` 和 `heater-cooler` 分别表示温控策略器、完整空调设备和简单冷暖执行设备。`air-conditioner` v2 保留独立启用状态、制冷/制热/除湿/送风模式和目标温度作为必须参数；当前状态和当前温度调整为可选，因为空调伴侣、红外遥控器通常无法提供真实室温或设备运行反馈。具备传感器的完整空调仍可映射这些可选参数。

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

HomeKit Consumer 契约位于 `backend/internal/mapping/consumer_catalog.go`。它把统一路径映射到具体 HAP Characteristic，并允许同一 Provider 设备在不同 Consumer 中选择不同的可选参数集合。

`single-property-sensor` 不固化“温度”或“湿度”语义。Provider 统一发布 `main/sensor/value`，并可在属性定义中给出 `celsius`、`percent` 等单位；桥内每台虚拟设备再独立把这个字段绑定到 `TemperatureSensor.CurrentTemperature`、`HumiditySensor.CurrentRelativeHumidity` 或后续 Consumer 支持的其他目标。需要同时发布温度和湿度时使用 `temperature-humidity-sensor`，它保留两个明确的必需字段。

Virtual Provider 会发布当前契约中的完整可选参数集合，用于开发和回归测试。真实 Provider 可以只发布必须参数，再根据设备能力逐项增加可选参数。
