# HomeKit 统一模型支持矩阵

更新时间：2026-08-01

HomeLoom 只在 HAP 提供原生或语义一致的 Service 时发布设备。桥内设备选择器从 Consumer 属性目录动态读取支持范围；不支持的模型会被禁用，并在服务端再次校验。

| 统一模型 | HomeKit Service | 状态 |
|---|---|---|
| `switch` | Switch | 完整支持 |
| `lightbulb` | Lightbulb | 完整支持 |
| `outlet` | Outlet | 完整支持 |
| `temperature-sensor` | Temperature Sensor | 完整支持 |
| `humidity-sensor` | Humidity Sensor | 完整支持 |
| `temperature-humidity-sensor` | Temperature Sensor + Humidity Sensor | 完整支持 |
| `contact-sensor` | Contact Sensor | 完整支持 |
| `motion-sensor` | Motion Sensor | 完整支持 |
| `illuminance-sensor` | Light Sensor | 完整支持 |
| `occupancy-sensor` | Occupancy Sensor | 完整支持 |
| `leak-sensor` | Leak Sensor | 完整支持 |
| `smoke-sensor` | Smoke Sensor | 完整支持 |
| `carbon-monoxide-sensor` | Carbon Monoxide Sensor | 支持告警、当前浓度和峰值 |
| `carbon-dioxide-sensor` | Carbon Dioxide Sensor | 支持告警、当前浓度和峰值 |
| `air-quality-sensor` | Air Quality Sensor | 支持质量、PM2.5、PM10、VOC、CO₂、NO₂ 和臭氧 |
| `fan` | Fan v2 | 完整支持 |
| `air-purifier` | Air Purifier + Filter Maintenance + Air Quality Sensor | 完整支持 |
| `thermostat` | Thermostat | 支持模式、温度、阈值、湿度和显示单位 |
| `air-conditioner` | Heater Cooler | 支持开关、模式、温度、风速和摆风 |
| `heater-cooler` | Heater Cooler | 完整支持 |
| `humidifier-dehumidifier` | Humidifier Dehumidifier | 支持模式、目标湿度、水位和控制锁 |
| `lock` | Lock Mechanism | 完整支持 |
| `garage-door` | Garage Door Opener | 完整支持 |
| `security-system` | Security System | 完整支持 |
| `valve` | Valve | 支持通用、灌溉、淋浴和水龙头类别 |
| `speaker` | Speaker | 支持启用、音量和静音 |
| `window-covering` | Window Covering | 完整支持 |
| `robot-vacuum` | 无原生 HAP Service | 明确不支持 |
| `pressure-sensor` | 无原生 HAP Service | 明确不支持 |
| `noise-sensor` | 无原生 HAP Service | 明确不支持 |
| `water-level-sensor` | 无独立原生 HAP Service | 明确不支持 |
| `soil-moisture-sensor` | 无原生 HAP Service | 明确不支持 |
| `pump` | 无语义一致的原生 HAP Service | 明确不支持 |
| `water-heater` | 无完整语义一致的原生 HAP Service | 明确不支持 |
| `power-meter` | 无原生 HAP Service | 明确不支持 |
| `ev-charger` | 无原生 HAP Service | 明确不支持 |

## 协议语义限制

- HomeKit Heater Cooler 的目标模式只有 `auto`、`heat` 和 `cool`。空调统一模型中的 `dry`、`fan` 在 Apple Home 状态侧显示为 `auto`/`idle`，Provider 原始状态不会被改写。
- HomeKit Thermostat 没有独立的 `idle` 当前状态，统一模型的 `idle` 显示为 `off`，目标模式仍保持原值。
- HomeKit Carbon Monoxide Level 的协议范围上限为 100；更高的统一模型读数在 HomeKit 特征值侧按协议范围截断。
- HomeKit Valve 的 Set Duration 上限为 3600 秒；统一模型仍允许保存更大的设备原始范围。
- 可选属性只有在具体设备的统一模型快照或显式 Consumer 映射中存在时才创建对应 Characteristic，避免发布无数据的控件。
- Speaker Service 的展示方式取决于 Apple Home / Home Hub 版本；HomeLoom 不把扬声器伪装成 Television。
- 气压、噪声、水位、土壤湿度、电力计量、水泵、热水器和充电桩仍可被 Web、API 和后续 Matter Consumer 使用；HomeKit 不会把它们降级伪装成开关。
