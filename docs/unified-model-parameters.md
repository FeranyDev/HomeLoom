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

内置目录共 36 种模型。设备族只用于组织目录；运行时契约仍由每个模型组合的 Capability 和 Property 决定。

| 设备族 | 模型 | 必须参数 | 主要可选能力 |
| --- | --- | --- | --- |
| 基础执行器 | Switch | 开关 | 使用状态、物理锁、故障 |
| 基础执行器 | Lightbulb | 开关 | 亮度、色温、色彩、颜色模式、灯效、自适应照明、故障 |
| 基础执行器 | Outlet | 开关 | 使用/过载状态、物理锁、电压、电流、功率、频率、功率因数、电量 |
| 环境测量 | Temperature Sensor | 当前温度 | 最低/最高温度、电池、防拆、有效与故障状态 |
| 环境测量 | Humidity Sensor | 当前湿度 | 最低/最高湿度、电池、防拆、有效与故障状态 |
| 环境测量 | Temperature Humidity Sensor | 当前温度、当前湿度 | 露点、绝对湿度、电池、防拆、有效与故障状态 |
| 环境测量 | Pressure Sensor | 当前气压 | 气压趋势、电池与健康状态 |
| 环境测量 | Noise Sensor | 当前声级 | 峰值声级、电池与健康状态 |
| 环境测量 | Water Level Sensor | 当前水位 | 低水位、溢水、电池与健康状态 |
| 环境测量 | Soil Moisture Sensor | 当前土壤湿度 | 电导率、土壤温度、电池与健康状态 |
| 环境测量 | Illuminance Sensor | 当前照度 | 最低/最高照度、电池与健康状态 |
| 环境测量 | Air Quality Sensor | 当前空气质量 | AQI、PM2.5、PM10、VOC、CO、CO₂、NO₂、臭氧、温湿度、健康状态 |
| 安防感知 | Contact Sensor | 接触状态 | 打开时长、触发次数、电池、防拆、健康状态 |
| 安防感知 | Motion Sensor | 活动状态 | 灵敏度、无人延迟、照度、电池、防拆、健康状态 |
| 安防感知 | Occupancy Sensor | 占用状态 | 人数、灵敏度、无人延迟、电池、防拆、健康状态 |
| 安防感知 | Leak Sensor | 漏水状态 | 检测水位、电池、防拆、健康状态 |
| 安防感知 | Smoke Sensor | 烟雾状态 | 烟雾浓度、电池、防拆、健康状态 |
| 安防感知 | Carbon Monoxide Sensor | 一氧化碳告警 | 当前/峰值浓度、电池、防拆、健康状态 |
| 安防感知 | Carbon Dioxide Sensor | 二氧化碳告警 | 当前/峰值浓度、电池、防拆、健康状态 |
| 空气与温控 | Fan | 启用、当前状态 | 模式、转速/档位、摇头、方向、物理锁、定时、故障 |
| 空气与温控 | Air Purifier | 启用、当前状态 | 模式、风速、空气质量、温湿度、定时、滤芯与故障 |
| 空气与温控 | Thermostat | 当前状态、目标模式、当前/目标温度 | 阈值、湿度、温标、风机、保持、物理锁、故障 |
| 空气与温控 | Air Conditioner v3 | 启用、运行模式、目标温度 | 当前反馈、风速、扫风、辅热、睡眠/节能、定时、湿度、滤网和故障 |
| 空气与温控 | Heater Cooler | 启用、当前状态、目标模式、当前温度 | 阈值、风速、摆风、物理锁、定时和故障 |
| 空气与温控 | Humidifier Dehumidifier | 启用、当前状态、目标模式、当前/目标湿度 | 水位、缺水、风速、物理锁、定时、滤芯和故障 |
| 空气与温控 | Water Heater | 启用、当前状态、当前/目标水温 | 模式、水量、剩余加热时间、物理锁和故障 |
| 门窗与安防 | Window Covering | 当前/目标位置、运动状态 | 障碍物、水平/垂直倾角、暂停、故障 |
| 门窗与安防 | Lock | 当前/目标锁定状态 | 门状态、卡住、最近操作、物理锁、电池、防拆、健康状态 |
| 门窗与安防 | Garage Door | 当前/目标门状态 | 障碍物、锁定状态、故障 |
| 门窗与安防 | Security System | 当前/目标布防状态 | 告警、警号、进出延迟、防拆、故障 |
| 水务 | Valve | 启用、使用状态、阀门类型 | 开度、定时、流量、累计水量、故障 |
| 水务 | Pump | 启用、当前状态 | 转速、流量、压力、定时和故障 |
| 能源 | Power Meter | 当前功率 | 电压、电流、频率、功率因数、有功/无功/视在功率、累计电量、健康状态 |
| 能源 | EV Charger | 允许充电、当前状态 | 目标电流、会话电量/时长、剩余时长、枪锁、电气参数和故障 |
| 媒体 | Speaker | 启用、音量、静音 | 播放状态、输入源、媒体信息、进度、随机与循环 |
| 清洁 | Robot Vacuum | 启用、当前状态、目标模式 | 进度、吸力、面积、时长、拖地水量、耗材、充电、电量和故障 |

内置模型目录和 Consumer 能力目录相互独立。统一模型描述 HomeLoom 内部的稳定语义基准；HomeKit、Matter 或其他 Consumer 只声明自己实际支持的模型和属性，不会因为目录新增模型而被强制实现或伪装成 HomeKit 设备。

`thermostat`、`air-conditioner` 和 `heater-cooler` 分别表示温控策略器、完整空调设备和简单冷暖执行设备。`air-conditioner` v3 保留独立启用状态、制冷/制热/除湿/送风模式和目标温度作为必须参数；当前状态和当前温度仍为可选，因为空调伴侣、红外遥控器通常无法提供真实反馈。

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

旧的 `single-property-sensor` 已从内置目录移除。单位只能描述数值，不能替代测量语义；温度、湿度、气压、噪声、水位和土壤湿度现在分别拥有明确模型和稳定路径。需要同时发布温度和湿度时使用组合模型 `temperature-humidity-sensor`。已有配置需要把 `sensor/value` 路由迁移到对应的语义路径；系统不会根据单位静默猜测并改写持久化映射。

Virtual Provider 会发布当前契约中的完整可选参数集合，用于开发和回归测试。真实 Provider 可以只发布必须参数，再根据设备能力逐项增加可选参数。
