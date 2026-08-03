# Sonoff/eWeLink Provider

HomeLoom 的 `sonoff` Provider 面向原厂 eWeLink/Sonoff 设备，采用统一 Provider SDK 发布设备快照，并按 `auto`、`local`、`cloud` 选择传输：`auto` 先尝试 LAN，LAN 失败后回退到已配置的云端 REST 客户端。

## 初版支持范围

- 单路、双路和多路开关；
- B05/L3 类灯具的开关、亮度、色温和 HSV 参数；
- iFan 类风扇的开关和档位；
- POW/POWR3/DUALR3 类设备的电压、电流、功率和电量；
- TH/THR、SNZB 温湿度与常见安防传感器；
- DualR3/窗帘类设备的位置；
- 未知 UIID 参数保留在 `sonoff-raw` 原生能力中，不会静默丢弃。

LAN 协议使用 `_ewelink._tcp.local.` 的设备信息、`/zeroconf/*` HTTP 接口，以及非 DIY 设备的 `MD5(devicekey)` → AES-128-CBC/PKCS#7 编解码。普通设备需要先从云端或受保护配置取得 `deviceKey`；DIY 设备可使用明文 LAN 模式。

创建 Provider 时可直接输入 eWeLink 邮箱/手机号、密码和国家区号，点击“登录 eWeLink 账号”。HomeLoom 按 [AlexxIT/SonoffLAN](https://github.com/AlexxIT/SonoffLAN) 的 v2 登录方式调用 `/v2/user/login`，自动取得区域、Access Token 和设备 `apikey`（用于 LAN 的 `deviceKey`），登录结果回填到 Provider 配置后再由用户保存。已保存账号凭据可在 Token 失效时触发后端重新登录；也可只保存已有 Access Token。

云端设备目录使用 `/v2/device/thing`，状态写入使用 `/v2/device/thing/status`；WebSocket 实时监督仍未纳入这一初版，状态由发现、读取和刷新流程提供。

## 配置示例

Provider 的 `config` JSON 可以按下面方式配置。实际保存时 `password`、`accessToken`、`deviceKey` 等敏感字段由 Core 的 Provider secret codec 加密，管理 API 只返回脱敏占位符。

```json
{
  "mode": "auto",
  "region": "auto",
  "requestTimeoutSeconds": 10,
  "refreshIntervalSeconds": 60,
  "cloud": {
    "endpoint": "",
    "username": "name@example.com",
    "password": "replace-me",
    "countryCode": "+86",
    "accessToken": ""
  },
  "devices": [
    {
      "id": "living-switch",
      "deviceId": "1000abcdef",
      "name": "客厅开关",
      "uiid": 1,
      "deviceKey": "replace-me",
      "host": "192.168.1.31",
      "port": 8081,
      "params": {"switch": "off"}
    }
  ]
}
```

## 传输和状态原则

- `local` 模式不允许没有 LAN 地址或 `deviceKey`（DIY 除外）；
- `cloud` 模式不把旧的功率、电压、电流值伪装为实时值；
- LAN 与云端状态合并时保留最新来源和时间；
- 设备缺少参数时保留未知状态，不使用默认值冒充真实状态；
- `deviceKey` 不进入错误、诊断、日志或未加密的 Provider 快照。

新增 UIID 时优先补充 `catalog` 映射和 golden fixture，再补特殊命令；传输层不应耦合 HomeKit/Matter。
