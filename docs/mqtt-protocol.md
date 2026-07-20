# HomeLoom MQTT Provider 协议 v1

MQTT Provider 把显式配置的 MQTT 设备转换成 HomeLoom Device Model。Provider 支持两种运行模式：客户端模式由 HomeLoom 连接外部 Broker，服务端模式由 HomeLoom 内嵌 Broker 并接受设备连接。连接配置和设备路由均保存在 PostgreSQL；实时设备状态、远端 sequence 和连接状态只保存在内存，服务重启后由 retained discovery、availability 和新状态重新建立。

在管理界面中，“MQTT Client（客户端）”和“MQTT Server（服务端）”是与小米中枢、MIoT 云平级的 Provider 类型入口。用户创建时直接选择连接方向，表单内不再提供第二次运行模式切换；后端仍以统一的 MQTT Provider 实现承载生命周期，并把入口转换为固定的 `mode` 配置。

## Provider 配置

### 客户端模式（client）

```json
{
  "mode": "client",
  "brokerUrl": "mqtt://127.0.0.1:1883",
  "username": "homeloom",
  "password": "change-me",
  "clientId": "homeloom-mqtt-main",
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
  },
  "devices": []
}
```

HomeLoom 作为 MQTT 客户端，支持 `mqtt`、`tls`/`mqtts`、`ws` 和 `wss`。Broker URL 中禁止嵌入凭据。Provider 可以在没有设备时先建立连接；此时不会订阅任何设备 Topic。旧配置未提供 `mode` 时按 `client` 处理。

### 服务端模式（server）

```json
{
  "mode": "server",
  "listenAddress": "127.0.0.1:1883",
  "username": "device",
  "password": "change-me",
  "retainedStateMaxAgeSeconds": 300,
  "tls": {
    "certFile": "",
    "keyFile": "",
    "caFile": ""
  },
  "devices": []
}
```

HomeLoom 在 `listenAddress` 启动内嵌 MQTT TCP Broker，外部设备主动连接。默认只监听 `127.0.0.1:1883`；需要局域网设备访问时可改为 `0.0.0.0:1883`，并应同时设置账户和 TLS。`certFile`/`keyFile` 是服务端证书；设置 `caFile` 后启用 mTLS，并要求客户端提供由该 CA 签发的证书。服务端模式不接受 `brokerUrl`、`serverName` 和 `insecureSkipVerify`。

服务端 ACL 由设备路由自动生成：设备只能向该路由的 Discovery、Availability 和 State Topic 发布，只能订阅对应 Command Topic，其他 Topic 默认拒绝。没有设备路由时 Broker 仍监听，但拒绝全部业务 Topic。服务端模式目前只提供 TCP/TLS 监听，不提供 WebSocket 监听。

密码、token、secret 和 private key 类配置在 PostgreSQL 中由数据库主密钥加密，列表和诊断接口只返回 `********`。

## 设备配置

Provider 运行后，从“管理设备”逐台配置路由：

```json
{
  "id": "living-room-light",
  "topicPrefix": "homeloom/living-room",
  "protocol": "homeloom-v1",
  "qos": 1,
  "topics": {
    "discovery": "homeloom/living-room/discovery/living-room-light",
    "availability": "homeloom/living-room/availability/living-room-light",
    "state": "homeloom/living-room/state/living-room-light/{endpointId}/{capabilityId}/{propertyId}",
    "command": "homeloom/living-room/command/living-room-light/{endpointId}/{capabilityId}/{operationId}"
  }
}
```

`topicPrefix`、QoS 和四类 Topic 均为设备级配置。`topics` 留空时按 Prefix 和设备 ID 生成以上默认值；State 与 Command 模板允许调整静态层级，但必须保留完整占位符。订阅模板不能互相重叠，Command 也不能与任何入站订阅重叠。

新增、修改和移除设备时，客户端模式在已有 Broker 会话上执行 Subscribe/Unsubscribe；服务端模式在已有监听器上更新内联订阅和 ACL。两种模式都不会为设备创建第二条连接，也不会因设备路由变化重连或重启监听。被移除的设备会向 Core 发布一次 `removed` 快照。只有连接级字段（例如运行模式、Broker URL、监听地址或 TLS）变化时才替换 Provider 运行实例。

## Topic

设设备 `topicPrefix=homeloom/living-room`、`id=living-room-light`：

| 方向 | Topic | Retained |
| --- | --- | --- |
| 设备 → HomeLoom | `homeloom/living-room/discovery/living-room-light` | 建议是 |
| 设备 → HomeLoom | `homeloom/living-room/availability/living-room-light` | 建议是 |
| 设备 → HomeLoom | `homeloom/living-room/state/living-room-light/{endpointId}/{capabilityId}/{propertyId}` | 可选 |
| HomeLoom → 设备 | `homeloom/living-room/command/living-room-light/{endpointId}/{capabilityId}/{operationId}` | 否 |

所有路径 ID 必须匹配 `[a-z0-9](?:[a-z0-9._-]{0,62}[a-z0-9])?`。发布空的 retained discovery payload 表示设备当前结构被撤销，但设备路由仍保留在数据库，下一次 Discovery 可以重新发布。未配置设备的消息不会进入设备中心。

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

## 外部本地 Broker（客户端模式）

根目录 Compose 包含可选的 Mosquitto 开发服务：

```bash
docker compose up -d mosquitto
```

`deploy/mosquitto.conf` 为本机/可信局域网 Demo 开启匿名访问，不应直接用于公网或生产环境；生产部署应配置账户、ACL 和 TLS。

客户端模式自动化测试使用测试专用的最小 MQTT 5 TCP Broker，在同一端口执行关闭和重启，验证 Paho 自动重连、重新订阅、设备不重复创建以及恢复后的命令发布。服务端模式测试启动真实内嵌 Broker，并用独立 Paho 客户端验证认证、Discovery 上报、命令订阅和发布。测试不依赖本机 Docker 或外部 Broker。
