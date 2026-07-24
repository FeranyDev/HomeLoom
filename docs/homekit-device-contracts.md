# HomeKit 基础设备契约

HomeKit Target 只读取统一 Device Model，不依赖具体 Provider。Virtual、MQTT 和后续 Provider 只要上报相同 Capability，即可复用以下 HAP 映射。

表中基础状态属于 `required`；亮度、空气质量、控制锁等增强能力属于 `optional`。HomeKit 通过独立 Consumer 契约选择映射，可选参数缺失时降级，自定义参数只有显式路径绑定后才进入 Target。

| Device Type | Capability | Property / Command | 类型与方向 | HomeKit 映射 |
| --- | --- | --- | --- | --- |
| `switch` / `lightbulb` / `outlet` | `switch` | `power` | bool，R/W/N | On；Outlet 同步 Outlet In Use |
| `temperature-sensor` | `temperature` | `current-temperature` | number，R/N | Current Temperature |
| `humidity-sensor` | `humidity` | `current-humidity` | number，R/N | Current Relative Humidity |
| `temperature-humidity-sensor` | `temperature` | `current-temperature` | number，R/N | Current Temperature |
| `temperature-humidity-sensor` | `humidity` | `current-humidity` | number，R/N | Current Relative Humidity |
| `contact-sensor` | `contact` | `contact-detected` | bool，R/N | Contact Sensor State |
| `motion-sensor` | `motion` | `motion-detected` | bool，R/N | Motion Detected |
| `fan` | `fan` | `active` | bool，R/W/N | Active |
| `fan` | `fan` | `current-state` | enum `inactive/idle/blowing-air`，R/N | Current Fan State |
| `fan` | `fan` | `target-state` | enum `manual/auto`，R/W/N | Target Fan State |
| `fan` | `fan` | `rotation-speed` | number 0–100，R/W/N | Rotation Speed |
| `air-purifier` | `air-purifier` | `active` | bool，R/W/N | Active |
| `air-purifier` | `air-purifier` | `current-state` | enum `inactive/idle/purifying-air`，R/N | Current Air Purifier State |
| `air-purifier` | `air-purifier` | `target-state` | enum `manual/auto`，可选 R/W/N | Target Air Purifier State |
| `air-purifier` | `air-purifier` | `rotation-speed` | number 0–100，可选 R/W/N | Rotation Speed |
| `air-purifier` | `filter` | `life-level` | number 0–100，可选 R/N | Filter Life Level |
| `air-purifier` | `filter` | `change-indication` | bool，可选 R/N | Filter Change Indication |
| `air-purifier` | `filter` | `reset-filter` | 可选幂等 Command | Reset Filter Indication |
| `window-covering` | `window-covering` | `current-position` | int 0–100，R/N | Current Position |
| `window-covering` | `window-covering` | `target-position` | int 0–100，R/W/N | Target Position |
| `window-covering` | `window-covering` | `position-state` | enum `decreasing/increasing/stopped`，R/N | Position State |

增强映射还包括 Lightbulb 的亮度/色温/色相/饱和度，Outlet In Use，温度、湿度、温湿度、接触与活动传感器的 Battery Service，接触与活动传感器的防拆状态，Fan 的摇头/方向/控制锁，Air Purifier 的摆风/控制锁及链接 Air Quality Sensor（空气质量、PM2.5、VOC），以及 Window Covering 的 Obstruction Detected。温度和湿度使用明确的模型路径，不再由单位猜测语义；温湿度组合模型在同一附件中提供两个独立 HAP Service。

`R/W/N` 分别表示 readable、writable、notifiable。Virtual Provider 会即时完成位置变化，并把窗帘状态归并为 `stopped`；真实 Provider 可以依次上报移动状态和最终位置。

每个 HAP Service 都包含 Status Fault。设备为 `offline` 或 `unknown` 时只更新故障状态，不用缓存值伪装实时数据；恢复在线后重新推送完整属性。空气净化器的 Filter Maintenance / Air Quality Sensor 仅在统一模型提供对应属性时作为链接服务发布，拥有独立故障状态和稳定 IID。
