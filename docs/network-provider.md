# 网络设备监测与 Wake-on-LAN Provider

`network` Provider 用 TCP 端口或 ICMP echo 探测维护局域网设备的电源状态，并可在“开启”操作中向有线或无线网卡发送 Wake-on-LAN（WOL）魔术包。它运行在 HomeLoom 主程序内；ICMP 使用容器内的标准 `ping` 工具，不要求配置 raw socket。

## 创建 Provider

在管理端新建 Provider 时选择类型 `network`。Provider 的 `config` 可配置全局默认值，并在每台设备上覆盖：

```json
{
	"probeMethod": "tcp",
	"probeIntervalSeconds": 30,
  "probeTimeoutSeconds": 3,
  "onlineThreshold": 1,
  "offlineThreshold": 2,
  "wolBroadcastAddress": "255.255.255.255",
  "wolPort": 9,
  "devices": [
    {
      "id": "living-room-nas",
      "name": "客厅 NAS",
      "host": "192.168.1.20",
      "probePort": 443,
      "mac": "AA:BB:CC:DD:EE:FF"
    }
  ]
}
```

`id` 必须是 Provider 内稳定且唯一的小写 ID。`host` 可以是固定 IP 或本地 DNS 名称；建议为长期休眠设备分配固定 DHCP 租约。`probeMethod` 默认为 `tcp`；选择 `icmp` 时无需填写 `probePort`。TCP 的 `probePort` 应选择设备正常运行时稳定监听的端口，例如 NAS 的 HTTPS/SSH 或电脑的远程管理端口。

## 配置字段

| 字段 | 作用 | 默认值 |
| --- | --- | --- |
| `probeMethod` | 全局探测方式：`tcp` 或 `icmp`；每台设备可单独覆盖 | `tcp` |
| `probeIntervalSeconds` | 全局探测周期（1–3600 秒） | `30` |
| `probeTimeoutSeconds` | 单次 TCP 建连或 ICMP 回显超时（1–120 秒） | `3` |
| `onlineThreshold` | 连续探测成功多少次后转为已开启（1–100） | `1` |
| `offlineThreshold` | 连续探测失败多少次后转为已关闭（1–100） | `1` |
| `wolBroadcastAddress` | WOL UDP 广播地址 | `255.255.255.255` |
| `wolPort` | WOL UDP 端口 | `9` |
| `wolInterface` | 可选的本地网卡名或本地 IP，用于选择 WOL 出口 | 空 |

以上字段都可以在单台设备内以同名字段覆盖全局设置。`mac` 对只做状态监测的设备是可选的；要执行开启/唤醒则必须填写 MAC 地址。MAC 支持常见的冒号、连字符或连续十六进制写法。

## 状态与唤醒行为

- 新配置的设备初始显示为已关闭但仍可管理；达到开启或关闭阈值后才改变电源状态，避免 Wi-Fi 短暂波动造成抖动。
- 对有 MAC 的设备，将 `switch/power` 设为 `true` 会发送魔术包，不会乐观地将设备设为已开启；下一次 TCP 或 ICMP 探测成功后才会更新为已开启。
- 设备必须在 BIOS/UEFI、操作系统及网卡中启用 WOL；深度休眠、无线网卡和不同 VLAN 的广播策略常会影响实际可唤醒性。
- 容器部署建议使用 host network，确保 UDP 广播可到达家庭局域网；仍应以实际网络环境测试唤醒。

该 Provider 只发布其独立配置的网络设备。若只是为既有 Camera、米家或 Sonoff 设备补充可达性，不要再创建同一实体的第二张设备卡片。

## 统一模型与目标平台

网络设备在 HomeLoom 内部使用独立的 `network-device` 统一模型，而不是接触传感器。TCP 或 ICMP 探测成功表示 `main/switch/power=true`（已开启），达到失败阈值表示 `false`（已关闭）；它不会将本地监测 Provider 或该设备标记为离线。MAC 已配置时，向同一 `main/switch/power` 写入 `true` 就是“开启”操作，并会发送 Wake-on-LAN；写入 `false` 不会尝试远程关机。管理端始终显示为“网络设备 / 电源状态”。

目标平台没有统一的网络电源 Device Type 时才在其适配层降级：Matter 使用官方 On/Off Plug-in Unit Endpoint，HomeKit 使用 Switch。目标端的开启动作会写入 `switch/power=true` 并触发 WOL；这种降级不会改变 Provider 的统一模型。

早期已将网络 Provider 设备映射为 `contact-sensor` 的目标设备，需要在目标设备管理器中重新选择 `network-device` 并重新保存映射；这属于显式的模型迁移，避免悄悄改变已配对的目标端点类型。新版在 HomeKit 和 Matter 目标侧均显示为开关，开启动作会触发 WOL。
