# HomeLoom MQTT Provider 协议 v1

MQTT Provider 把 Broker 上的设备转换成 HomeLoom Device Model。配置保存在 PostgreSQL；实时设备状态、远端 sequence 和连接状态只保存在内存，服务重启后由 retained discovery、availability 和新状态重新建立。

## Provider 配置

```json
{
  "brokerUrl": "mqtt://127.0.0.1:1883",
  "username": "homeloom",
  "password": "change-me",
  "clientId": "homeloom-mqtt-main",
  "topicPrefix": "homeloom",
  "qos": 1,
  "keepAliveSeconds": 30,
  "connectTimeoutSeconds": 10,
  "sessionExpirySeconds": 86400,
  "retainedStateMaxAgeSeconds": 300,
  "tls": {
    "caFile": "",
    "certFile": "",
    "keyFile": "",
    "serverName": "",
    "insecureSkipVerify": false
  }
}
```

支持 `mqtt`、`tls`/`mqtts`、`ws` 和 `wss`。Broker URL 中禁止嵌入凭据。密码、token、secret 和 private key 类配置在 PostgreSQL 中由数据库主密钥加密，列表和诊断接口只返回 `********`。

## Topic

设 `topicPrefix=homeloom`：

| 方向 | Topic | Retained |
| --- | --- | --- |
| 设备 → HomeLoom | `homeloom/discovery/{deviceId}` | 建议是 |
| 设备 → HomeLoom | `homeloom/availability/{deviceId}` | 建议是 |
| 设备 → HomeLoom | `homeloom/state/{deviceId}/{endpointId}/{capabilityId}/{propertyId}` | 可选 |
| HomeLoom → 设备 | `homeloom/command/{deviceId}/{endpointId}/{capabilityId}/{operationId}` | 否 |

所有路径 ID 必须匹配 `[a-z0-9](?:[a-z0-9._-]{0,62}[a-z0-9])?`。发布空的 retained discovery payload 表示显式删除设备。

## Discovery

Discovery payload 是 Device Model v1。`providerId`、内部 `sequence` 和最终 availability 由 MQTT Provider 管理。

```json
{
  "schemaVersion": 1,
  "id": "hall-switch",
  "providerId": "mqtt-main",
  "name": "玄关开关",
  "type": "switch",
  "availability": "online",
  "online": true,
  "lastUpdateAt": "2026-07-13T08:00:00Z",
  "endpoints": [{
    "id": "main",
    "name": "主端点",
    "type": "switch",
    "capabilities": [{
      "id": "switch",
      "type": "switch",
      "properties": [{
        "definition": {"id": "power", "name": "开关", "type": "bool", "readable": true, "writable": true, "notifiable": true},
        "value": {"type": "bool", "bool": false}
      }]
    }]
  }]
}
```

Retained discovery 只恢复结构，并把设备置为 `unknown`；它不能单独证明设备在线。重复 discovery 会保留已上报的兼容属性值，不重复创建设备身份。

## Availability 与 State

```json
{"schemaVersion":1,"availability":"online","sequence":17,"observedAt":"2026-07-13T08:00:01Z"}
```

```json
{"schemaVersion":1,"value":{"type":"bool","bool":true},"sequence":42,"observedAt":"2026-07-13T08:00:02Z","correlationId":"optional-device-correlation"}
```

`sequence` 必须为同一 availability 或属性流内单调递增的正整数。重复或倒退的消息会被忽略。Retained state/availability 超过 `retainedStateMaxAgeSeconds` 会被忽略；非 retained 状态表示设备在线。Broker 连接断开时，相关设备统一转为 `unknown`。

## Command 与确认

属性写入：

```json
{"schemaVersion":1,"kind":"property","correlationId":"e4a1...","value":{"type":"bool","bool":true},"createdAt":"2026-07-13T08:00:03Z"}
```

动作调用：

```json
{"schemaVersion":1,"kind":"action","correlationId":"a921...","idempotencyKey":"request-001","parameters":{"value":{"type":"bool","bool":false}},"createdAt":"2026-07-13T08:00:03Z"}
```

`operationId` 对属性命令是 property ID，对动作命令是 command ID。命令发布成功只代表 `accepted`；设备必须随后在对应 state topic 回报期望值，Core 才将命令推进到 `confirmed`。命令永不 retained。

机器可读契约见 [`schemas/mqtt-protocol.schema.json`](schemas/mqtt-protocol.schema.json)。

## 本地 Broker

根目录 Compose 包含可选的 Mosquitto 开发服务：

```bash
docker compose up -d mosquitto
```

`deploy/mosquitto.conf` 为本机/可信局域网 Demo 开启匿名访问，不应直接用于公网或生产环境；生产部署应配置账户、ACL 和 TLS。

自动化测试使用测试专用的最小 MQTT 5 TCP Broker，在同一端口执行关闭和重启，验证 Paho 自动重连、重新订阅、设备不重复创建以及恢复后的命令发布。该测试不依赖本机 Docker 或外部 Broker。
