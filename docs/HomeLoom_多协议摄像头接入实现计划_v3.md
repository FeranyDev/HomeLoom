# HomeLoom 多协议摄像头接入实现计划

> 版本：v3.1
>
> 日期：2026-07-26
>
> 目标：构建面向多厂商、多协议摄像头的统一接入体系。HomeLoom 负责账号、设备、长期凭据、云端授权与策略；独立 Media Worker 负责本地握手、媒体会话、解密、转码和流输出。小米摄像头优先落地，但不作为架构中心。
>
> 与当前项目的关系：摄像头继续使用现有 `Device → Endpoint → Capability` 统一设备模型。本文中的 Camera 是 Device 的媒体扩展，不是第二套设备主模型；Provider、凭据加密、审计、备份恢复和 Runtime IPC 均优先复用现有实现。

---

## 1. 最终方案

采用“统一控制与认证平面 + 独立媒体平面”的双进程架构：

```text
┌──────────────────────── HomeLoom Core ────────────────────────┐
│ 厂商账号与 Token                                                │
│ 统一设备发现、注册与摄像头媒体能力扩展                            │
│ 长期凭据加密存储                                                  │
│ 云端授权、票据刷新、配对材料管理                                  │
│ Credential Broker                                               │
│ 流配置、访问策略、审计、健康聚合                                  │
│ Apple Home 普通设备桥                                            │
└───────────────────────────┬────────────────────────────────────┘
                            │ 本地 IPC
                            │ 只传连接所需的最小授权材料
                            ▼
┌────────────────────── Media Worker ────────────────────────────┐
│ go2rtc 媒体内核                                                  │
│ RTSP / ONVIF / Xiaomi / Tapo / HomeKit / Wyze / Tuya 等适配器    │
│ 本地协议握手与挑战应答                                            │
│ 临时密钥、共享密钥、DTLS/SRTP 会话                                │
│ 视频与音频解密、RTP 封装                                          │
│ RTSP / WebRTC / JPEG / HomeKit Camera 输出                       │
│ 必要时启动 FFmpeg 子进程                                          │
└─────────────────────────────────────────────────────────────────┘
```

核心原则：

1. HomeLoom 现有统一 Device 是名称、房间、在线状态和 Provider 归属的唯一权威来源。
2. HomeLoom 管理“设备是谁、谁有权访问、长期凭据在哪里”。
3. Media Worker 管理“本次怎么连、如何解密、如何传输媒体”。
4. 视频帧、音频帧和 RTP 包不进入 HomeLoom 事件总线。
5. Media Worker 不直接读取 HomeLoom 数据库，也不持久化业务配置和长期凭据。
6. 协议差异收敛在现有 Provider 可选接口和 Media Adapter 中。
7. 小米只是优先适配器，统一模型不能出现小米专属字段泄漏。
8. 普通设备桥故障与摄像头媒体故障应尽可能互不影响。
9. IPC、加密存储、审计、备份恢复和进程监督优先沿用 Matter Runtime 已验证的语义。

---

## 2. 支持范围与协议优先级

优先级同时考虑：用户价值、设备覆盖率、实现复杂度、离线能力、go2rtc 现有成熟度。

### P0：通用基础设施

在适配任何厂商前先完成：

- 统一 Device 媒体扩展、MediaSource、Stream、Credential、Session 模型；
- HomeLoom 与 Media Worker IPC；
- 动态增删流；
- Worker 心跳、健康状态和错误分类；
- 凭据加密存储；
- 日志脱敏；
- RTSP/WebRTC/JPEG/HomeKit Camera 统一输出；
- 按需、预加载、常驻三种流模式。

### P1：首批可用协议

#### 1. RTSP

优先原因：

- 标准接口，覆盖面最大；
- 不依赖云端；
- 适合持续录像；
- 可作为所有后续功能的基线测试源。

#### 2. ONVIF

优先原因：

- 用于局域网发现、能力探测、RTSP 地址获取和 PTZ；
- 与 RTSP 配合可快速覆盖大量摄像头。

#### 3. Xiaomi MISS / CS2 / TUTK

优先原因：

- 当前重点需求；
- go2rtc 已有较完整实现；
- 需要验证 HomeLoom 云端授权与 Worker 本地媒体分离架构；
- 能为其他“云端授权 + 本地 P2P”协议提供模板。

### P2：本地可控厂商协议

#### 4. Tapo / VIGI

- 长期账号或密码保存在 HomeLoom；
- nonce、AES 会话参数由 Worker 本地协商；
- 不依赖云端重连；
- 适合稳定长连接。

#### 5. HomeKit Camera 输入

- HomeLoom 保存长期配对材料；
- Worker 建立加密控制会话和每次 SRTP 会话；
- 完全本地；
- 可代理原生 HomeKit 摄像头到 HomeLoom。

#### 6. DVR-IP / Bubble / EseeCloud 本地模式

- 固定账号密码或设备密码；
- Worker 每次生成 SessionID；
- 作为廉价摄像头/NVR 的兼容补充。

### P3：设备级 P2P

#### 7. Wyze

- HomeLoom 管理 UID、ENR、MAC、账号派生材料；
- Worker 建立 P2P/DTLS 会话；
- 设备材料可缓存，但协议兼容性需要真机矩阵验证。

### P4：云端信令型协议

#### 8. Tuya
#### 9. Ring
#### 10. Nest
#### 11. Roborock Camera

共同特点：

- HomeLoom 维护 Refresh Token、client secret 或设备 key；
- 每次流会话需要云端创建 SDP/ICE/TURN 或短期票据；
- Worker 完成 WebRTC、DTLS、SRTP 媒体连接；
- 外网中断时通常无法重新建立新会话。

### P5：后续扩展

- Reolink 私有接口；
- Hikvision ISAPI 对讲；
- DoorBird；
- GoPro；
- 自定义 HTTP-FLV、HLS、SRT 输入；
- 用户自定义插件式 Media Adapter。

---

## 3. 协议认证模型分类

统一架构按认证行为分类，而不是按厂商写死逻辑。

### 3.1 Static Credential：固定凭据

适用：

- RTSP；
- ONVIF；
- DVR-IP；
- Bubble；
- EseeCloud；
- DoorBird；
- Hikvision ISAPI。

```text
HomeLoom 保存 username/password
        ↓
Worker 获取一次凭据副本
        ↓
Worker 完成本地登录和 SessionID 协商
```

长期材料：用户名、密码。  
临时材料：Digest nonce、SessionID、RTP 通道。

### 3.2 Local Challenge：本地挑战应答

适用：

- Tapo；
- VIGI；
- 部分私有 HTTP 摄像头。

```text
HomeLoom 保存账号或密码摘要
        ↓
Worker 连接摄像头获取 nonce
        ↓
Worker 本地派生 AES Key/IV
```

HomeLoom 不计算每包加解密密钥。

### 3.3 Pairing Identity：长期配对身份

适用：

- HomeKit Camera；
- 后续可能支持的 Matter Camera。

```text
HomeLoom 保存长期配对身份
        ↓
Worker 获取配对材料
        ↓
Worker 完成控制通道认证
        ↓
每次创建新的 SRTP/媒体密钥
```

长期材料：配对 ID、公私钥、设备公钥。  
临时材料：SRTP key、salt、SSRC。

### 3.4 Cloud Authorization + Local Media：云端授权、本地媒体

适用：

- Xiaomi MISS；
- 部分 Wyze/P2P 摄像头。

```text
Worker 生成临时公私钥或会话挑战
        ↓
把公开材料发送给 HomeLoom
        ↓
HomeLoom 使用厂商账号向云端申请授权
        ↓
Worker 使用授权材料连接局域网摄像头
```

关键边界：Worker 私钥不进入 HomeLoom。

### 3.5 Cloud Signaling：云端信令

适用：

- Tuya；
- Ring；
- Nest；
- Roborock。

```text
Worker 创建 SDP Offer
        ↓
HomeLoom 使用长期 Token 调用厂商云端
        ↓
返回 Answer / ICE / TURN / Session Ticket
        ↓
Worker 建立 WebRTC/DTLS/SRTP
```

长期材料只存在 HomeLoom，短期媒体会话存在 Worker。

---

## 4. 职责边界

### 4.1 HomeLoom Core

负责：

- 厂商账号登录与登出；
- OAuth、Refresh Token、Token 刷新；
- 设备发现、同步、去重和房间归属；
- 长期用户名、密码、设备 key、配对密钥；
- 厂商云端 API；
- 云端会话票据和授权签名；
- 凭据加密存储；
- 摄像头访问策略；
- Credential Broker；
- 流配置和生命周期策略；
- Worker 握手、实例管理、健康聚合、审计；
- 运动、门铃、隐私模式、PTZ 等控制事件。

不负责：

- 视频帧和音频帧；
- RTP/RTCP；
- H.264/H.265 解码；
- FFmpeg 转码；
- DTLS/SRTP 数据面；
- 持续媒体解密。

### 4.2 Media Worker

负责：

- 连接摄像头本地 IP 或云端媒体节点；
- RTSP、CS2、TUTK、WebRTC 等媒体协议；
- 本地 challenge、nonce、SessionID；
- 临时密钥生成；
- shared key、AES、DTLS、SRTP；
- 视频和音频解密；
- RTP 转换；
- 双向语音；
- 转码与缩放；
- RTSP、WebRTC、JPEG、HomeKit Camera 输出；
- 流级重连与媒体健康检查。

不负责：

- 厂商账号交互界面；
- 长期 Token 持久化；
- 直接访问 HomeLoom 数据库；
- 摄像头业务权限判断；
- 设备命名和房间管理。

---

## 5. 统一数据模型

### 5.1 Device 与 MediaSource

摄像头首先是 HomeLoom 现有的 `device.Device`。其稳定 ID、Provider 归属、名称、家庭、房间、Availability、命令和事件继续由统一设备模型负责，不在媒体域重复保存。

统一模型新增 `camera` Device Type，并使用标准 Endpoint/Capability 表达：

```text
Device(type=camera)
└── Endpoint(main)
    ├── Capability(camera)
    │   ├── privacy
    │   └── battery
    ├── Capability(media)
    │   ├── live-stream
    │   ├── snapshot
    │   ├── microphone
    │   └── talkback
    ├── Capability(ptz)
    └── Event(motion / doorbell)
```

媒体域只保存与拉流有关的扩展：

```go
type MediaSource struct {
    DeviceID         string
    ProviderID       string
    ProviderDeviceID string
    Protocol         string
    CredentialRef    string
    Profiles         []MediaProfile
    SourceConfig     json.RawMessage
    Revision         uint64
    Enabled          bool
}
```

约束：

- `DeviceID` 同时作为 Camera ID，不另建第二套 Camera 身份；
- 名称、房间、设备 Availability 只读取 `device.Device`；
- `SourceConfig` 是按 `Protocol` 判别的严格 JSON 联合类型，不使用 `map[string]any`；
- 厂商私有字段只允许 owning Provider 与对应 Media Adapter 解码；
- `SourceConfig` 不保存明文 Token、密码、私钥和会话材料；
- 媒体连接状态与设备 Availability 分开维护，不能反向覆盖 Provider 的设备状态。

### 5.2 MediaProfile

```go
type MediaProfile struct {
    ID         string
    Name       string
    Width      int
    Height     int
    FPS        int
    VideoCodec string
    AudioCodec string
    Bitrate    int64
}
```

### 5.3 StreamSpec

```go
type StreamSpec struct {
    ID       string
    DeviceID string
    Protocol string

    CredentialRef string
    Profile        string
    Mode           StreamMode

    Audio    bool
    Talkback bool

    Options json.RawMessage
}
```

`Options` 必须按协议严格解码、拒绝未知字段并限制负载大小；跨进程契约不得直接传递任意对象。

### 5.4 StreamMode

```go
type StreamMode string

const (
    StreamOnDemand StreamMode = "on_demand"
    StreamPreload  StreamMode = "preload"
    StreamAlwaysOn StreamMode = "always_on"
)
```

默认策略：

- 插电式摄像头：`preload`；
- 电池摄像头：`on_demand`；
- 接入录像/NVR：`always_on`。

---

## 6. Provider 扩展设计

不新增平行的 `CameraProvider` 主接口。厂商继续实现当前项目的 `provider.Provider`，并按需组合现有 `Discoverer`、`CommandExecutor`、`DeviceEventSubscriber`、`CredentialMaintainer` 等能力。

媒体接入只向现有 Provider SDK 增加小型可选接口：

```go
type MediaSourceDiscoverer interface {
    DiscoverMediaSources(ctx context.Context) ([]MediaSourceDescriptor, error)
}

type MediaAuthorizer interface {
    AcquireMediaAuthorization(
        ctx context.Context,
        req AuthorizationRequest,
    ) (*AuthorizationResponse, error)
}

type MediaSourceRefresher interface {
    RefreshMediaSource(ctx context.Context, deviceID string) (*MediaSourceDescriptor, error)
}
```

规则：

- Camera 发现同时返回或更新标准 `device.Device`；
- PTZ、隐私模式、警笛等控制继续走 `CommandExecutor`；
- Motion、Doorbell 等事件继续走 `DeviceEventSubscriber`；
- OAuth/Token/证书轮换继续走 `CredentialMaintainer`；
- 静态凭据、配对身份和云端授权由 Core 的 Credential Broker 路由到 owning Provider 或 Credential Store；
- `ProviderID` 是账号/连接实例 ID；当前阶段一个 Provider 实例对应一个厂商账号，不另建重复的账号运行时。

首批 Provider 扩展：

```text
providers/onvif
providers/xiaomi
providers/tapo
providers/homekitcamera
providers/wyze
providers/tuya
providers/ring
providers/nest
providers/roborock
```

---

## 7. Media Adapter 设计

Media Worker 内每种协议实现统一接口：

```go
type MediaAdapter interface {
    Scheme() string

    Connect(
        ctx context.Context,
        stream StreamRuntimeSpec,
        auth AuthorizationClient,
    ) (core.Producer, error)

    Probe(ctx context.Context, stream StreamRuntimeSpec) (*MediaInfo, error)
}
```

示例 Scheme：

```text
homeloom-rtsp://device-id
homeloom-xiaomi://device-id
homeloom-tapo://device-id
homeloom-homekit://device-id
homeloom-wyze://device-id
homeloom-tuya://device-id
```

流配置中只出现统一 Device ID，不出现长期 Token、密码或私钥。

---

## 8. Credential Broker

Credential Broker 是协议无关的授权入口。

### 8.1 请求

```go
type AuthorizationRequest struct {
    RequestID string
    WorkerID  string
    WorkerInstanceID string
    DeviceID  string
    Protocol  string
    Purpose   string
    Attempt   int

    ClientMaterial json.RawMessage
    SessionOffer   []byte
}
```

`ClientMaterial` 用于 Worker 提交公开的临时材料，例如：

- 小米 `client_public`；
- WebRTC SDP Offer；
- 厂商 challenge；
- Worker 支持的 codec/profile。

`ClientMaterial` 按 `Protocol` 使用严格、版本化的联合类型，不接受任意键值。不得包含：

- Worker 临时私钥；
- shared key；
- DTLS 私钥；
- 已解密媒体数据。

### 8.2 响应

```go
type AuthorizationResponse struct {
    LeaseID   string
    ExpiresAt time.Time

    Endpoint EndpointSpec
    AuthType string

    PublicMaterial json.RawMessage
    SecretMaterial json.RawMessage
    SessionAnswer  []byte

    Reusable bool
    MaxUses  int
}
```

说明：

- `SecretMaterial` 可能包含短期密码、sign、TURN credential；
- 不得包含 HomeLoom 长期 Refresh Token；
- `ExpiresAt` 是 HomeLoom 定义的租约期限，不等于厂商真实会话期限；
- Worker 连接结束后应清理内存中的会话材料；
- 响应负载必须有协议级大小限制，且不得进入通用错误对象、trace、审计详情或请求日志。

### 8.3 Lease 状态机与安全绑定

```text
claimed ──connect──> connected ──end──> ended
   │                     │
   ├──expire───────────> expired
   ├──revoke───────────> revoked
   └──fail─────────────> failed
```

要求：

- `auth.acquire` 在同一事务中创建并绑定 claimed Lease，成功响应才包含授权材料，不暴露可被其他实例抢占的 issued Lease；
- `RequestID` 在 Worker 实例范围内唯一；Core 在短期内存缓存中对重复请求返回同一结果，Core 重启导致结果不确定时返回明确错误并要求 Worker 使用新 RequestID；
- Lease 原子绑定 `WorkerID + WorkerInstanceID + DeviceID + Protocol + Purpose`；
- Lease 同时绑定 `ClientMaterial`/SDP Offer 摘要，防止授权材料被替换；
- 默认 `MaxUses=1`，Lease 创建和 use count 更新必须通过数据库事务或等价原子操作完成；
- 认证重试是否消耗 use count 由协议策略显式定义；
- Worker 重启产生新的 `WorkerInstanceID`，旧实例未使用 Lease 自动失效；
- 支持过期、主动撤销、Core 关停清理及每 Worker/Device 的速率和并发限制；
- 数据库只保存状态、摘要和时间，不保存授权响应原文。

### 8.4 连接结果上报

```go
type SessionReport struct {
    LeaseID  string
    WorkerID string
    WorkerInstanceID string
    DeviceID string

    Result    string
    ErrorCode string
    Detail    string

    ConnectedAt time.Time
    EndedAt     time.Time
}
```

标准结果：

```text
connected
network_failed
camera_offline
auth_failed
cloud_failed
protocol_failed
unsupported_codec
session_expired
cancelled
```

---

## 9. IPC 设计

### 9.1 拓扑与协议

复用当前 Matter Runtime 已验证的单连接、双向 newline-delimited JSON-RPC 2.0 模式，不再同时维护 Core HTTP Server 和 Worker HTTP Server。

```text
Media Worker
    │ 主动连接、自动重连
    ▼
HomeLoom Core Unix Socket / Named Pipe
    │
    ├── Core → Worker：握手响应、全量流重放、增量配置、运行时操作
    └── Worker → Core：授权申请、状态、事件、会话报告、身份存储
```

启动与恢复：

```text
Worker 连接 Core
  → runtime.handshake
  → streams.replay（最新完整期望状态）
  → streams.upsert/delete（连续增量）
  ↔ auth.acquire/report
  ↔ status.update/event.publish
```

Core 不重放断线期间的历史增量。Worker 重连后总是获取最新完整状态，再继续接收增量。

### 9.2 传输优先级

1. Linux/macOS：Unix Domain Socket；
2. Windows：Named Pipe；
3. 开发环境：仅监听 `127.0.0.1` 的 TCP；
4. 禁止通过局域网明文接口分发凭据。

建议：

```text
/run/homeloom/media.sock
```

权限：

```text
owner: homeloom
 group: homeloom-media
  mode: 0660
```

### 9.3 第一版 RPC

协议版本从 `1.0` 开始，HomeLoom 和 Worker 使用同一组 golden test vectors：

```text
runtime.handshake
runtime.ping
runtime.status

streams.replay
streams.upsert
streams.delete
streams.operation

auth.acquire
auth.report

device.status
device.event

storage.get
storage.put
storage.delete
storage.list
```

其中：

- `streams.*` 只处理期望流配置；
- `streams.operation` 处理 `start/stop/restart/probe/snapshot` 等临时动作；
- `storage.*` 只允许访问由 Core 绑定的媒体 Runtime 命名空间，Worker 不能指定任意数据库 scope；
- 请求有超时、有界队列和明确背压错误；
- 未完成 handshake 或协议版本不兼容时 fail closed。

### 9.4 身份校验

Unix 系统使用：

- Unix Socket 文件权限；
- Linux 使用 `SO_PEERCRED`，macOS 使用 `getpeereid`/平台等价能力校验 UID/GID；
- Core 启动时生成短期 bootstrap token，通过受限文件描述符、权限为 `0600` 的临时文件或进程监督器注入；
- handshake 后绑定逻辑 `WorkerID` 和本次进程唯一 `WorkerInstanceID`；
- Lease 与 Worker 实例绑定；
- 单连接 JSON-RPC 不要求每条消息重复携带 bearer token。

Windows 使用：

- Named Pipe ACL；
- Worker 实例 ID；
- 本机短期注册 Token。

开发环境 loopback TCP 必须使用随机短期 bootstrap token；禁止仅凭 `127.0.0.1` 视为可信。

---

## 10. 典型协议流程

### 10.1 RTSP

```text
Worker 请求 camera 配置
        ↓
HomeLoom 返回 host、path、短期用户名密码副本
        ↓
Worker 执行 RTSP OPTIONS/DESCRIBE/SETUP/PLAY
        ↓
持续处理 RTP
```

用户名密码可长期保存，但 Worker 只在内存中持有副本。

### 10.2 ONVIF + RTSP

```text
HomeLoom 扫描 ONVIF
        ↓
读取设备信息、Profile、RTSP URI、PTZ 能力
        ↓
注册 Camera 和 StreamSpec
        ↓
Worker 使用 RTSP 拉流
```

ONVIF 控制命令由 HomeLoom Provider 执行，媒体由 Worker 处理。

### 10.3 Xiaomi MISS

```text
Worker 生成 client_public/client_private
        ↓
Worker 将 client_public 发送给 HomeLoom
        ↓
HomeLoom 使用小米账号调用 miss_get_vendor
        ↓
返回 device_public、sign、vendor、uid、endpoint
        ↓
Worker 使用 client_private 计算 shared key
        ↓
Worker 通过 CS2/TUTK 本地连接摄像头
        ↓
解密和处理媒体
```

要求：

- `client_private` 不离开 Worker；
- 小米账号 Token 不离开 HomeLoom；
- 第一版每个新上游会话重新申请授权；
- 后续可做“缓存优先、失败刷新”。

### 10.4 Tapo / VIGI

```text
HomeLoom 返回设备账号或密码摘要
        ↓
Worker 连接摄像头获取 nonce
        ↓
Worker 本地派生 AES Key/IV
        ↓
Worker 解密 MPEG-TS/媒体数据
```

nonce 和 AES 密钥不回传 HomeLoom。

### 10.5 HomeKit Camera 输入

```text
HomeLoom 返回配对 ID、控制器私钥、设备公钥
        ↓
Worker 建立 HomeKit 加密控制通道
        ↓
Worker 为本次媒体会话生成 SRTP key/salt
        ↓
摄像头发送 SRTP
        ↓
Worker 转为内部 RTP
```

配对材料长期保存在 HomeLoom，Worker 按需获取。

### 10.6 Wyze

```text
HomeLoom 返回 UID、ENR、MAC、model 等设备材料
        ↓
Worker 建立 P2P/DTLS 会话
        ↓
Worker 生成临时媒体密钥并取流
```

设备材料可以缓存，DTLS 会话不可跨连接复用。

### 10.7 Tuya / Ring / Nest / Roborock

```text
Worker 创建 SDP Offer
        ↓
HomeLoom 使用长期账号材料调用云端
        ↓
返回 SDP Answer、ICE、TURN、Session Ticket
        ↓
Worker 建立 WebRTC/DTLS/SRTP
```

HomeLoom 负责 Token 刷新，Worker 不接触 Refresh Token。

---

## 11. HomeLoom 目录结构

```text
backend/internal/
├── domain/
│   ├── device/             # 现有统一模型，增加 camera 类型/契约
│   └── media/              # MediaSource、Stream、Authorization 领域类型
├── provider/               # 现有 Provider SDK，增加媒体可选接口
├── providers/
│   ├── onvif/
│   ├── xiaomi/             # 复用现有账号、发现、OAuth、凭据维护
│   ├── tapo/
│   └── ...
├── application/
│   ├── media_service.go
│   ├── media_auth_service.go
│   └── media_storage_service.go
├── runtime/
│   └── mediaworkermanager/
│       ├── manager.go
│       ├── contract.go
│       ├── transport_unix.go
│       └── transport_windows.go
└── persistence/gormstore/
    ├── media.go
    └── media_storage.go
```

约束：

- 不创建独立 `camera` 主领域包复制 Device Service；
- Provider 只能依赖现有统一设备模型和通用媒体契约，不允许互相引用；
- 凭据 AEAD、主密钥加载、日志脱敏、审计和备份恢复扩展现有模块，不另建第二套实现；
- Media Worker supervisor 复用 Target Manager/Matter Runtime 的生命周期和测试模式，但 Worker 是共享媒体进程，不伪装成普通 Target。

---

## 12. Media Worker 目录结构

建议维护 HomeLoom 专用 go2rtc fork，但把修改集中在独立目录：

```text
internal/homeloom/
├── init.go
├── config.go
├── ipc_client.go
├── worker_register.go
├── stream_sync.go
├── auth_client.go
├── session_cache.go
├── status_report.go
├── redact.go
└── adapters/
    ├── rtsp.go
    ├── xiaomi.go
    ├── tapo.go
    ├── homekit.go
    ├── wyze.go
    ├── tuya.go
    └── generic.go
```

尽量不修改 go2rtc 原有协议包。必须修改时：

- 优先新增结构化构造函数；
- 保留原有 URL 构造入口；
- 不把 HomeLoom IPC 逻辑写入 `pkg/xiaomi`、`pkg/tapo` 等纯协议包；
- HomeLoom Adapter 只负责组装 Options 和调用原协议客户端。

示例：

```go
type XiaomiConnectOptions struct {
    Host string
    UID  string
    Vendor string

    ClientPublic  []byte
    ClientPrivate []byte
    DevicePublic  []byte
    Sign          []byte
}

func DialXiaomi(ctx context.Context, opts XiaomiConnectOptions) (core.Producer, error)
```

---

## 13. 动态流管理

HomeLoom 为唯一配置源，Worker 不长期保存业务配置。

启动流程：

```text
Worker 启动
   ↓
连接 Core 并完成 handshake
   ↓
HomeLoom 返回期望流列表
   ↓
Worker 创建/更新/删除本地流
   ↓
Worker 上报结果
```

全量状态：

```go
type StreamReplay struct {
    Generation uint64
    Revision   uint64
    Streams    []StreamSpec
}
```

配置增量：

```go
type StreamMutation struct {
    Generation uint64
    Revision   uint64
    Action     string // upsert | delete
    Spec       StreamSpec
}
```

运行时临时操作使用独立消息：

```go
type StreamOperation struct {
    RequestID string
    StreamID  string
    Action    string // start | stop | restart | probe | snapshot
}
```

规则：

- `Generation` 标识 Core 配置纪元，Worker 不接受旧纪元；
- `Revision` 是 Core 生成的全局单调版本；
- 重复 mutation 必须幂等；
- revision 缺号、乱序或 generation 改变时，Worker 请求完整 replay；
- Worker 重连时不恢复旧增量队列，只应用最新完整状态；
- start/stop 等临时动作不改变期望配置；
- 多消费者观看同一逻辑流时复用上游连接。

流名称使用稳定内部 ID：

```text
camera_<uuid>
```

摄像头名称变化不应改变流 URL 和 Apple Home 身份。

---

## 14. 数据库设计

不新增重复的 `provider_accounts` 和 `cameras` 主表。当前阶段一个 `providers` 记录代表一个账号/连接实例，摄像头的名称、房间、Availability 等来自统一 Device；媒体表只保存媒体扩展。

### 14.1 media_sources

```text
device_id                 PK
provider_id
provider_device_id
protocol
credential_ref
profiles_json
source_config_json
revision
enabled
created_at
updated_at
```

`source_config_json` 不保存明文 Token、密码、私钥或会话材料。

### 14.2 media_credentials

```text
id
device_id
credential_type
credential_blob_encrypted
key_version
version
status
created_at
updated_at
```

`credential_type`：

```text
static_password
homekit_input_pairing_identity
homekit_output_accessory_identity
device_secret
vendor_device_material
```

云端 Refresh Token 等账号级凭据继续保存在现有 Provider 配置，并由 `CredentialMaintainer` 维护；不复制到设备凭据表。

### 14.3 media_streams

```text
id
device_id
protocol
profile
mode
audio_enabled
talkback_enabled
options_json
revision
enabled
created_at
updated_at
```

### 14.4 media_runtime_kv

为 HomeKit Camera 输出等需要稳定身份的 Runtime 提供反向存储：

```text
namespace
key
value_encrypted
sensitive
updated_at
```

Core 根据当前 Worker 和 Device 绑定 namespace；Worker 不能指定任意命名空间。HomeKit 输出配对、Accessory 私钥和配置版本通过 `storage.*` RPC 持久化。

### 14.5 media_workers（可选持久化）

```text
id
instance_name
version
host
platform
instance_id
capabilities_json
last_heartbeat_at
status
```

第一版 Worker 在线状态可仅驻留内存；只有需要历史、集群调度或多 Worker 分配时才持久化该表。

### 14.6 media_auth_leases

只保存元数据和摘要，不保存原始私钥：

```text
id
worker_id
worker_instance_id
device_id
protocol
purpose
status
expires_at
request_id
request_material_hash
max_uses
use_count
created_at
claimed_at
used_at
ended_at
```

### 14.7 media_auth_audit

```text
id
worker_id
device_id
provider
action
result
error_code
remote_identity
created_at
```

### 14.8 迁移、备份与密钥要求

新增媒体表必须同时进入：

- GORM `currentModels`；
- SQLite/PostgreSQL 迁移和约束测试；
- 逻辑备份数据结构与读取流程；
- restore 删除/插入顺序；
- restore candidate 校验；
- master key 缺失检测；
- 导出、诊断包和日志脱敏测试。

现有 `enc:v1` AES-GCM 可用于第一版，但“主密钥版本与轮换”是新增能力：密文必须记录 key version，轮换需支持旧 key 读取、新 key 写入、批量重加密和可验证回滚。

---

## 15. 凭据和缓存策略

### 15.1 长期凭据

只在 HomeLoom 加密存储：

- 用户名和密码；
- OAuth Refresh Token；
- client secret；
- HomeKit 配对私钥；
- Xiaomi 账号 Token；
- Roborock DevKey；
- Wyze ENR 等设备秘密。

### 15.2 会话材料

Worker 只在内存保存：

- client_private；
- shared key；
- Tapo AES key/IV；
- RTSP SessionID；
- DTLS/SRTP key；
- SDP/ICE/TURN 临时会话；
- Xiaomi sign 和设备公钥副本。

### 15.3 短期复用

允许 Worker 在同一进程内对短期材料进行有限缓存：

```text
网络瞬断
→ 优先复用仍有效的会话授权
→ 认证失败则立即向 HomeLoom 获取新授权
```

默认：

- 静态账号：可重复读取；
- 小米授权：第一版不跨进程持久化；
- TURN/云端票据：严格按到期时间；
- HomeKit 配对身份：可重复获取；
- 媒体会话密钥：断线即丢弃。

---

## 16. 重连与故障策略

### 16.1 分层重连

```text
媒体读超时
   ↓
Worker 在协议层快速重连
   ↓
若认证失败，向 HomeLoom 申请新授权
   ↓
若长期凭据失效，HomeLoom 刷新 Token
   ↓
仍失败则进入退避和告警
```

### 16.2 退避策略

建议：

```text
1s, 1s, 2s, 5s, 10s, 30s, 60s
```

加入 10%～20% 随机抖动，防止多个摄像头同时重连。

### 16.3 错误归属

```text
network_failed       → Worker 网络层
camera_offline       → 摄像头状态
local_auth_failed    → Worker 本地认证
cloud_auth_failed    → HomeLoom Provider
credential_expired   → HomeLoom 凭据刷新
unsupported_codec    → Worker 媒体能力
worker_unavailable   → Worker 生命周期
```

HomeLoom UI 应展示具体层级，而不是统一显示“摄像头不可用”。

### 16.4 HomeLoom 暂时不可用

- 已建立流继续工作；
- Worker 使用内存中已有会话材料；
- 需要新授权时进入退避；
- HomeLoom 恢复后自动重新申请；
- Worker 不允许从本地文件读取长期 Token 作为旁路。

### 16.5 Worker 暂时不可用

- 普通 HomeLoom 设备继续工作；
- HomeLoom 将摄像头标记为 `media_worker_offline`；
- Worker 恢复后进行全量流配置同步；
- Apple Home 摄像头身份配置不删除。

---

## 17. 安全要求

### 17.1 加密存储

- 使用主密钥加密数据库中的 credential blob；
- 支持主密钥版本和轮换；
- 主密钥来自环境变量、系统密钥环或独立 secrets 文件；
- 禁止把凭据写入普通 YAML 配置。

### 17.2 最小权限

HomeLoom：

- 可访问厂商云端；
- 可访问数据库；
- 不需要访问摄像头媒体端口。

Media Worker：

- 可访问摄像头 VLAN；
- 可开放媒体服务端口；
- 不可直接访问数据库；
- 可按协议限制是否访问公网。

对 Xiaomi、RTSP、Tapo 等本地媒体协议，可禁止 Worker 访问厂商账号 API。

### 17.3 日志脱敏

永不打印：

```text
password
token
refresh_token
client_secret
client_private
device_private
sign
shared_key
srtp_key
turn_password
完整 SDP 中的敏感字段
```

错误日志仅记录：

- camera ID；
- provider；
- protocol；
- lease ID；
- 标准错误码；
- 已脱敏 endpoint。

### 17.4 go2rtc API

- API 默认不监听 LAN；
- 优先只通过 Unix Socket 或 loopback 使用；
- RTSP 默认监听 localhost，按用户配置开放；
- 禁用不需要的 `exec` 等危险功能；
- HomeKit 摄像头端口和 mDNS 单独开放。

---

## 18. Apple Home 输出

第一版仍使用 Media Worker/go2rtc 输出摄像头 HomeKit 服务：

```text
HomeLoom 普通设备桥
├── 灯
├── 开关
├── 窗帘
└── 传感器

Media Worker HomeKit Camera
├── 摄像头视频
├── 快照
├── 麦克风
└── 双向语音
```

HomeLoom 管理并稳定保存：

- Camera Accessory ID；
- PIN；
- Device ID；
- Device Private Key；
- 配对状态；
- 配置版本。

这些状态通过 Worker → Core 的 `storage.*` 反向 RPC 写入 `media_runtime_kv`，不能只保存在 Worker 本地文件。Worker 只能访问 Core 为当前 Camera 绑定的身份命名空间。

必须区分三类材料：

- HomeKit Camera 输入的控制器配对身份；
- HomeLoom Camera 输出的 Accessory 身份；
- Apple Home Controller 对输出 Camera 的配对记录。

底层从 Xiaomi 切换到 RTSP 或其他协议时，不改变 Apple Home 中的逻辑摄像头身份。

第一阶段不承诺 HKSV，目标是：

- 实时视频；
- 快照；
- 音频；
- 双向语音；
- 在线状态；
- 运动和门铃事件映射。

---

## 19. 开发阶段

### 阶段 0：架构契约与上游基线

任务：

- 固定 go2rtc 上游版本；
- 建立 HomeLoom Media Worker fork；
- 建立自动同步上游流程；
- 明确 Camera 是现有 Device 的媒体扩展；
- 定义版本化 MediaSource、Stream、Authorization、Status、Storage 契约；
- 明确单连接双向 JSON-RPC 拓扑；
- 建立协议能力表和真机清单。

验收：

- HomeLoom 和 Worker 可独立编译；
- 契约包有版本号；
- golden vectors 可被 Core 和 Worker 同时验证；
- 空 Worker 可完成握手、全量 replay、断线重连和错误版本拒绝；
- RTSP 测试流可由原始 go2rtc 正常播放。

### 阶段 1：RTSP 最小纵切与通用 Worker

任务：

- Worker 握手、实例身份和心跳；
- Unix Socket/Named Pipe 双向 JSON-RPC；
- 全量和增量流同步；
- 健康状态上报；
- Credential Broker 通用接口；
- 日志脱敏；
- 手动创建 RTSP Camera Device 和 MediaSource；
- RTSP 静态凭据加密存储；
- RTSP 拉流、WebRTC/JPEG 输出；
- Worker supervisor 和退出清理。

验收：

- HomeLoom 可以动态创建和删除 RTSP 流；
- 浏览器可通过 WebRTC 查看，JPEG 快照可用；
- Worker 重启后自动恢复流；
- HomeLoom 重启不终止已有媒体连接；
- 旧 generation、乱序 revision、重复 mutation 均有契约测试；
- 日志、审计和 trace 通过 secret canary 检查。

### 阶段 2：ONVIF

任务：

- ONVIF 发现；
- Profile、RTSP URI、PTZ 能力；
- 持续运行测试。

验收：

- 至少两种品牌 ONVIF 摄像头可自动发现；
- 24 小时持续拉流；
- 摄像头和 Worker 重启后自动恢复。

### 阶段 3：Xiaomi 优先适配

任务：

- 复用当前 Xiaomi Provider 的账号、OAuth、设备发现和凭据维护；
- Camera Device 与 MediaSource 映射；
- MISS `client_public` 授权流程；
- CS2 UDP/TCP；
- TUTK；
- Legacy 入口；
- 音频和双向语音；
- 授权失败刷新；
- 凭据不进入 URL 和日志。

验收：

- 至少两种现代 MISS 型号；
- 至少一种 CS2 TCP/UDP 真机；
- 断线后自动重新授权和连接；
- HomeLoom 不持有 Worker 临时私钥；
- Worker 不持有小米长期账号 Token。

### 阶段 4：Apple Home 摄像头输出

任务：

- 稳定 Camera Accessory 身份；
- `storage.*` 反向身份与配对存储；
- H.264/Opus 能力检测；
- 必要时 FFmpeg 转码；
- 快照、实时视频、双向语音；
- 运动和门铃事件映射。

验收：

- HomeLoom/Worker 重启后无需重新配对；
- 备份恢复后保持配对和 Accessory 身份；
- 切换底层源不改变 Apple Home 摄像头身份；
- 多客户端观看时复用上游连接。

### 阶段 5：Tapo/VIGI 与 HomeKit Camera 输入

任务：

- Tapo/VIGI 本地挑战应答；
- HomeKit 长期配对材料管理；
- SRTP 会话；
- 对讲能力适配；
- 本地断网重连测试。

验收：

- Tapo/VIGI 在无外网时可断线重连；
- HomeKit Camera 可代理到 HomeLoom 输出；
- 临时 AES/SRTP 密钥不持久化。

### 阶段 6：Wyze 与其他本地私有协议

任务：

- Wyze 设备材料模型；
- P2P/DTLS；
- DVR-IP/Bubble/EseeCloud；
- 真机兼容矩阵；
- 并发和重连限制。

验收：

- 每类至少一台设备稳定运行；
- 协议失败不影响其他摄像头或普通设备桥。

### 阶段 7：云端 WebRTC 厂商

按顺序：

1. Tuya；
2. Roborock；
3. Ring；
4. Nest。

任务：

- Refresh Token 生命周期；
- SDP/ICE/TURN 会话；
- 会话续期；
- 云端 API 限流；
- 外网中断状态提示。

验收：

- 长期 Token 不进入 Worker；
- 会话过期自动更新；
- 云端失败有明确错误分类。

### 阶段 8：稳定性、安全与发布

任务：

- 72 小时持续运行；
- 多摄像头并发；
- 内存和 goroutine 泄漏检测；
- 凭据轮换；
- IPC 权限测试；
- Worker 沙盒/低权限运行；
- 安装、升级和回滚。

验收：

- 单摄像头故障不影响其他流；
- Worker 崩溃不影响 HomeLoom 普通设备；
- 日志和配置无敏感信息；
- 版本升级可保留摄像头和 HomeKit 配对数据。

---

## 20. 测试计划

### 20.1 单元测试

- Camera Device 与 MediaSource 映射；
- Provider 字段映射；
- Credential 加解密；
- Lease 状态机、原子 claim、过期和撤销；
- 错误分类；
- 流配置 Generation/Revision；
- 日志脱敏；
- 协议 Options 结构化转换；
- SQLite/PostgreSQL 模型约束与迁移。

### 20.2 契约测试

HomeLoom 和 Worker 使用同一组测试向量：

- Authorization 请求/响应；
- Runtime handshake 和 Worker 实例绑定；
- 心跳；
- StreamReplay/StreamMutation/StreamOperation；
- SessionReport；
- Storage RPC 命名空间绑定；
- 版本不兼容、未知字段、超大负载和背压处理。

### 20.3 集成测试

使用模拟 Provider 和虚拟摄像头：

- RTSP test server；
- ONVIF mock；
- Xiaomi cloud mock；
- Tapo nonce mock；
- WebRTC signaling mock；
- HomeLoom/Worker 重启和网络中断；
- Revision 重复、缺号、乱序和旧 Generation；
- Lease 并发 claim、重放和超时；
- SQLite/PostgreSQL 备份、恢复及回滚；
- 错误主密钥和主密钥丢失；
- 日志、trace、审计和诊断包 secret canary；
- Worker 卡死、FFmpeg 崩溃与孤儿进程清理；
- HomeKit 输出身份和配对跨重启、备份恢复保持稳定。

### 20.4 真机矩阵

至少记录：

```text
厂商
型号
固件版本
协议
视频编码
音频编码
主码流/子码流
双向语音
长连接时间
断线重连
是否依赖云端
并发流限制
```

### 20.5 稳定性

- 单流 72 小时；
- 4、8、16 路并发；
- 反复启停 1000 次；
- 摄像头断电重启；
- Wi-Fi 抖动和丢包；
- HomeLoom 重启；
- Worker 重启；
- 厂商云端不可用；
- Token 过期；
- FFmpeg 崩溃和自动恢复。

---

## 21. 可观测性

### 指标

```text
homeloom_camera_total
homeloom_stream_active
homeloom_stream_consumers
homeloom_stream_reconnect_total
homeloom_stream_last_frame_seconds
homeloom_auth_acquire_total
homeloom_auth_failure_total
homeloom_auth_latency_seconds
homeloom_worker_heartbeat_age_seconds
homeloom_ffmpeg_process_total
homeloom_stream_bitrate_bytes
```

标签控制在低基数：

```text
provider
protocol
result
worker
```

不要用完整 Device ID 作为全局指标标签，可在日志和状态接口中查询。

### 状态接口

```text
GET /api/v1/devices?type=camera
GET /api/v1/devices/{id}
GET /api/v1/devices/{id}/media
GET /api/v1/devices/{id}/streams
GET /api/v1/media/workers
GET /api/v1/media/health
```

摄像头详情应区分：

- 设备在线状态；
- 云端账号状态；
- 媒体连接状态；
- Apple Home 发布状态；
- 最近错误和恢复时间。

---

## 22. 上游维护策略

### 原则

- 不把 HomeLoom 业务逻辑散落到 go2rtc 各协议包；
- HomeLoom 改动集中在 `internal/homeloom`；
- 对上游提交通用改进，例如结构化连接 Options、日志脱敏、可注入 Credential Provider；
- HomeLoom 专属 IPC 不提交到协议核心；
- 每次升级运行全协议契约测试和真机回归。

### 分支

```text
upstream/master
homeloom/upstream-sync
homeloom/main
feature/<protocol>
```

### 版本固定

每个 HomeLoom 版本固定：

- go2rtc commit；
- FFmpeg 最低版本；
- 协议契约版本；
- 数据库 schema 版本。

---

## 23. 里程碑完成标准

### 23.1 首个可演示版本：RTSP 纵切

1. HomeLoom 与 Media Worker 独立运行；
2. Worker 可握手、全量 replay、增量同步和自动重连；
3. RTSP 可手动接入；
4. RTSP、WebRTC、JPEG 输出可用；
5. 长期凭据只在 HomeLoom 加密持久化；
6. Worker 重启后自动恢复流；
7. HomeLoom 重启不终止已建立媒体流；
8. 单摄像头错误不影响普通设备桥；
9. Generation/Revision、Lease 和日志脱敏契约测试通过。

### 23.2 首个可发布版本

首个发布版本在 RTSP 纵切基础上交付 ONVIF、Xiaomi 和 Apple Home Camera：

1. HomeLoom 与 Media Worker 独立运行；
2. 通用 Credential Broker 可工作；
3. RTSP 可手动接入；
4. ONVIF 可发现并生成 RTSP 流；
5. Xiaomi MISS/CS2 至少两种型号可用；
6. RTSP、WebRTC、JPEG 输出可用；
7. Apple Home 可查看实时视频；
8. HomeLoom 重启不终止已建立媒体流；
9. Worker 重启后自动同步和恢复流；
10. 长期凭据只保存在 HomeLoom；
11. Worker 临时私钥和会话密钥不持久化；
12. 单摄像头错误不影响普通设备桥；
13. 24 小时持续运行无明显内存增长；
14. 方案可继续添加 Tapo、HomeKit、Wyze 和云端 WebRTC Provider，而无需新增第二套设备主模型；
15. 备份恢复可保留 MediaSource、凭据、流配置和 HomeKit 输出身份；
16. SQLite/PostgreSQL、IPC 权限、Lease 并发和 secret canary 测试通过。

---

## 24. 推荐实际开发顺序

```text
第 1 步：冻结 Device 媒体扩展、MediaSource、Stream、Auth、Storage 契约
第 2 步：固定 go2rtc fork/commit，建立 Core/Worker golden vectors
第 3 步：实现双向 JSON-RPC、握手、全量 replay 和 Worker supervisor
第 4 步：完成 RTSP 静态凭据、拉流、WebRTC/JPEG 输出的最小纵切
第 5 步：补齐 Revision、Lease、备份恢复、日志脱敏和故障注入测试
第 6 步：接入 ONVIF 发现、Profile 和 PTZ
第 7 步：复用现有 Xiaomi Provider，实现结构化授权材料和 Credential Broker
第 8 步：实现 Xiaomi MISS/CS2/TUTK
第 9 步：接入 Apple Home Camera 输出及反向身份存储
第 10 步：实现 Tapo/VIGI
第 11 步：实现 HomeKit Camera 输入
第 12 步：实现 Wyze 和本地私有协议
第 13 步：实现 Tuya/Roborock/Ring/Nest
第 14 步：完善安全、稳定性和安装升级
```

前五步构成首个可演示版本，前九步构成首个可发布版本。

---

## 25. 最终交付物

### HomeLoom Core

- 现有 Device Registry 的 Camera 类型与媒体扩展；
- 现有 Provider SDK 的媒体可选接口；
- 扩展现有 AEAD Credential Store；
- 通用 Credential Broker；
- Media Worker 管理；
- 动态流配置；
- 摄像头状态和审计 API；
- 媒体 Runtime 反向身份存储；
- Apple Home 普通设备与摄像头配置管理。

### Media Worker

- HomeLoom 双向 JSON-RPC Client；
- Handshake、Replay 与 Stream Mutation；
- 通用 Authorization Client；
- RTSP 媒体入口（ONVIF 发现和控制保留在 Core Provider）；
- Xiaomi Adapter；
- 后续 Tapo/HomeKit/Wyze/Cloud WebRTC Adapter；
- RTSP/WebRTC/JPEG/HomeKit Camera 输出；
- 状态上报和日志脱敏。

### 测试与运维

- 协议契约测试；
- 模拟 Provider；
- 真机兼容矩阵；
- Docker/Compose；
- Linux systemd 服务；
- Windows 服务方案；
- 升级、迁移和回滚文档；
- 安全检查清单；
- 72 小时稳定性测试报告。

---

## 26. 架构决策总结

最终边界应保持为：

```text
HomeLoom
├── 多厂商账号
├── 长期身份与凭据
├── 设备发现和控制
├── 云端授权和信令
├── 策略、审计和配置
└── Worker 生命周期

Media Worker
├── 临时密钥和本地挑战
├── 摄像头网络连接
├── 私有媒体协议
├── 媒体解密和封装
├── 转码和双向语音
└── 标准媒体输出
```

小米摄像头是首个重点验证对象，但同一套架构必须同时适用于 RTSP/ONVIF、Tapo/VIGI、HomeKit Camera、Wyze、Tuya、Ring、Nest 和 Roborock。任何新协议都应通过新增 Provider Adapter 和 Media Adapter 接入，而不是修改 HomeLoom Core 的通用设备模型。
