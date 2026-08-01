# HomeLoom 多协议摄像头接入实现计划

> 版本：v3.2
>
> 日期：2026-07-27
>
> 目标：构建范围受控、可长期维护的摄像头接入体系。当前正式输入只支持 RTSP、ONVIF→RTSP 和 Xiaomi MISS；处理只支持项目内 FFmpeg 转码；对外只实现独立 HomeKit Camera 输出。HomeLoom Core 内嵌媒体 Runtime，负责账号、设备、长期凭据、云端授权、媒体会话与策略；每路摄像头仍由受限的 HomeLoom Camera Kernel 子进程隔离执行协议数据面。
>
> 与当前项目的关系：摄像头继续使用现有 `Device → Endpoint → Capability` 统一设备模型。本文中的 Camera 是独立 Camera Provider 管理的 Device 媒体扩展，不是第二套设备主模型；厂商账号 Provider 只作为目录和凭据来源。摄像头接入后只进入设备中心，是否以及通过何种协议发布由 Target 显式决定。

---

## 1. 最终方案

采用“统一 Core 控制面 + 每流受限数据面”的架构：

```text
┌──────────────────────── HomeLoom Core ────────────────────────┐
│ 厂商账号与 Token                                                │
│ 统一设备发现、注册与摄像头媒体能力扩展                            │
│ 长期凭据加密存储                                                  │
│ 云端授权、票据刷新、配对材料管理                                  │
│ Credential Broker                                               │
│ 流配置、访问策略、审计、健康聚合                                  │
│ 内嵌媒体 Runtime：流生命周期、授权调用、健康聚合、审计              │
│ Apple Home 普通设备桥                                            │
└───────────────────────────┬────────────────────────────────────┘
                            │ 每流受限环境变量/匿名管道
                            ▼
┌──────────────── HomeLoom Camera Kernel（每路子进程）──────────────┐
│ 编译期白名单：RTSP / ONVIF→RTSP / Xiaomi MISS 输入               │
│ 预授权 MISS 握手；Kernel 不登录小米云、不持有账号 Token            │
│ 视频与音频解密、RTP 封装、受限 FFmpeg 转码                          │
│ 独立 HomeKit Camera / SRTP 输出与设备中心 MP4 预览                  │
│ 不提供通用 Web UI、动态模块、WebRTC/RTMP/HLS 或其他厂商协议          │
└─────────────────────────────────────────────────────────────────┘
```

核心原则：

1. HomeLoom 现有统一 Device 是名称、房间、在线状态和 Provider 归属的唯一权威来源。
2. HomeLoom 管理“设备是谁、谁有权访问、长期凭据在哪里”。
3. Core 内嵌媒体 Runtime 管理“本次怎么连、如何授权、如何编排媒体”。
4. 视频帧、音频帧和 RTP 包不进入 HomeLoom 事件总线。
5. 媒体 Runtime 通过 Core 的领域服务访问配置；Camera Kernel 不直接读取数据库，也不持久化业务配置和长期凭据。
6. 协议差异收敛在现有 Provider 可选接口和 Media Adapter 中。
7. 小米只是优先适配器，统一模型不能出现小米专属字段泄漏。
8. 普通设备桥故障与摄像头媒体故障应尽可能互不影响。
9. 加密存储、审计、备份恢复和子进程监督优先沿用 Matter Runtime 已验证的语义；媒体 Runtime 不引入额外进程间控制协议。
10. 厂商账号 Provider 与 Camera Provider 分离：前者提供账号、设备目录和可复用授权，后者拥有摄像头接入配置、媒体源与控制映射。
11. 发现或添加摄像头不得自动发布到任何生态；`targets` 是所有对外发布的唯一期望状态来源。
12. 同一台摄像头可以同时被多个 Target 引用，但所有 Target 共享同一逻辑 Device 和按需复用的媒体管线。

---

## 2. 支持范围与协议优先级

当前版本冻结为三类输入和一种生态输出。后续协议只保留为研究候选，不进入构建产物，
也不得通过配置动态开启。

### P0：通用基础设施

在适配任何厂商前先完成：

- 统一 Device 媒体扩展、MediaSource、Stream、Credential、Session 模型；
- Core 内嵌媒体 Runtime 与每流 Camera Kernel 生命周期；
- 动态增删流；
- Runtime 健康状态、子进程退出和错误分类；
- 凭据加密存储；
- 日志脱敏；
- 设备中心受限 MP4 预览与独立 HomeKit Camera 输出；
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

#### 3. Xiaomi MISS

优先原因：

- 当前重点需求；
- go2rtc 已有较完整实现；
- 需要验证 HomeLoom 云端授权与 Camera Kernel 本地媒体隔离架构；
- 能为其他“云端授权 + 本地 P2P”协议提供模板。

### 后续候选：本地可控厂商协议（当前版本不实现、不编译）

#### 4. Tapo / VIGI

- 长期账号或密码保存在 HomeLoom；
- nonce、AES 会话参数由 Camera Kernel 本地协商；
- 不依赖云端重连；
- 适合稳定长连接。

#### 5. HomeKit Camera 输入

- HomeLoom 保存长期配对材料；
- Camera Kernel 建立加密控制会话和每次 SRTP 会话；
- 完全本地；
- 可代理原生 HomeKit 摄像头到 HomeLoom。

#### 6. DVR-IP / Bubble / EseeCloud 本地模式

- 固定账号密码或设备密码；
- Camera Kernel 每次生成 SessionID；
- 作为廉价摄像头/NVR 的兼容补充。

### 后续候选：设备级 P2P（当前版本不实现、不编译）

#### 7. Wyze

- HomeLoom 管理 UID、ENR、MAC、账号派生材料；
- Camera Kernel 建立 P2P/DTLS 会话；
- 设备材料可缓存，但协议兼容性需要真机矩阵验证。

### 后续候选：云端信令型协议（当前版本不实现、不编译）

#### 8. Tuya
#### 9. Ring
#### 10. Nest
#### 11. Roborock Camera

共同特点：

- HomeLoom 维护 Refresh Token、client secret 或设备 key；
- 每次流会话需要云端创建 SDP/ICE/TURN 或短期票据；
- Camera Kernel 完成 WebRTC、DTLS、SRTP 媒体连接；
- 外网中断时通常无法重新建立新会话。

### 非目标

当前版本不提供第四种 Camera Driver，不支持通过配置、插件或上游模块列表启用
WebRTC、RTMP、HLS、Tapo、HomeKit Camera 输入、Wyze、Tuya、Ring 等能力。若未来重新
评估，必须先修订本计划、威胁模型和稳定性矩阵，不能直接恢复上游通用模块。

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
Camera Kernel 获取一次凭据副本
        ↓
Camera Kernel 完成本地登录和 SessionID 协商
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
Camera Kernel 连接摄像头获取 nonce
        ↓
Camera Kernel 本地派生 AES Key/IV
```

HomeLoom 不计算每包加解密密钥。

### 3.3 Pairing Identity：长期配对身份

适用：

- HomeKit Camera；
- 后续可能支持的 Matter Camera。

```text
HomeLoom 保存长期配对身份
        ↓
Camera Kernel 获取配对材料
        ↓
Camera Kernel 完成控制通道认证
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
Camera Kernel 生成临时公私钥或会话挑战
        ↓
把公开材料发送给 HomeLoom
        ↓
HomeLoom 使用厂商账号向云端申请授权
        ↓
Camera Kernel 使用授权材料连接局域网摄像头
```

关键边界：Camera Kernel 私钥不进入 HomeLoom。

### 3.5 Cloud Signaling：云端信令

适用：

- Tuya；
- Ring；
- Nest；
- Roborock。

```text
Camera Kernel 创建 SDP Offer
        ↓
HomeLoom 使用长期 Token 调用厂商云端
        ↓
返回 Answer / ICE / TURN / Session Ticket
        ↓
Camera Kernel 建立 WebRTC/DTLS/SRTP
```

长期材料只存在 HomeLoom，短期媒体会话存在 Camera Kernel。

---

## 4. 职责边界

```text
账号/目录 Provider                 独立 Camera Provider
Xiaomi MIoT Cloud ──凭据引用──┐   ┌─ Xiaomi MISS
ONVIF 局域网发现 ───设备候选──┼──▶├─ ONVIF
手动配置 ─────────连接参数────┘   ├─ RTSP
                                  └──────────┬──────────
                                             │ 统一 Camera Device
                                             ▼
                                         设备中心
                                             │ 用户显式选择
                   ┌─────────────────────────┼────────────────────────┐
                   ▼                         ▼                        ▼
          HomeKit Camera Target     Matter Camera Target      其他媒体 Target
          每摄像头独立 Accessory     首版每摄像头独立 Node      可按协议定义聚合
```

这里的“独立 Camera Provider”是 Provider 类型和产品入口独立，不是再造一套 Device
Registry。一个 Camera Provider 实例可以管理多台摄像头；每台摄像头仍只对应一个统一
`device.Device`。账号 Provider 不直接发布 Camera Device，避免 Xiaomi、Tapo、RTSP、
ONVIF 等摄像头分别形成互不兼容的产品流程。

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
- 内嵌媒体 Runtime 的流生命周期、子进程管理、健康聚合、审计；
- 运动、门铃、隐私模式、PTZ 等控制事件。
- Camera Provider 的摄像头目录及其对账号 Provider 的稳定引用；
- Target Publication，并保证没有 Target 时摄像头只在设备中心可见。

不负责：

- 视频帧和音频帧；
- RTP/RTCP；
- H.264/H.265 解码；
- FFmpeg 转码；
- DTLS/SRTP 数据面；
- 持续媒体解密。

### 4.2 Core 内嵌 Media Runtime 与 Camera Kernel

Core 内嵌 Media Runtime 负责：

- 连接 RTSP、ONVIF 或 Xiaomi MISS 摄像头；
- Xiaomi MISS 临时密钥生成与预授权材料消费；
- 按流启动、监督和回收 Camera Kernel；
- 流级重连、媒体健康检查、受限 MP4 预览与 HomeKit 发布编排。

每流 Camera Kernel 负责视频/音频解密、RTP 转换、转码与缩放，以及 HomeKit Camera/SRTP
数据面；FFmpeg 仍是受路径白名单约束的外部进程。

Camera Kernel 不负责：

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

Camera Provider 每台子设备保存 `connectionMode`，保存后会更新对应 Stream 并触发
Runtime 重建该摄像头的独立管线。兼容旧配置时默认使用 `on_demand`。

- `on_demand`：Camera Kernel 保持控制/API 监听，但只有设备中心或 HomeKit Consumer
  观看时才连接上游摄像头；最后一个 Consumer 离开后释放上游连接，资源占用最低。
- `preload`：Camera Kernel 启动后保持上游视频连接，但不提前常驻 FFmpeg H.264 转码；
  首帧比按需连接更快，适合大多数插电式摄像头。
- `always_on`：保持最终 H.264（以及启用时的 Opus）Consumer，从而让上游连接和 FFmpeg
  输出管线都维持热状态；打开最快，但持续占用摄像头会话、CPU/GPU 和网络带宽。

`preload` 和 `always_on` 会异步请求 H.264 关键帧并确认 MP4 `moof` 媒体片段，以实际
启动上游连接和转码管线；但预热属于性能优化，首帧慢或临时断流时必须降级为后续
Consumer 按需重试，不能让单路摄像头启动失败阻塞其他流、销毁 HAP/API
监听或触发 Runtime 重启风暴。设备中心预览的 Core 代理仍只会在收到首个媒体片段后
返回成功，超过 10 秒会关闭本次上游 Consumer 并返回超时；前端超过 12 秒仍未进入
可播放状态时终止加载态并提供重新连接。

macOS 的 H.264 转码以稳定性优先使用 `libx264`：已在目标环境复现 VideoToolbox 虽然
声明可用但创建压缩会话失败 `-12908`，该失败会导致 FFmpeg 在首个关键帧前退出。当前
模板保留软件 HEVC 解码，使用 `superfast + zerolatency`、Main/Level 4.0、CRF 23、
30fps、无 B 帧，关闭场景切换并将 GOP 固定为约 1 秒，在每个 IDR 重复 SPS/PPS，
同时立即刷新 RTSP 输出包。已有发布器在下次重建
时会替换旧 VideoToolbox 模板。设备中心播放器若落后
直播缓冲尾部超过 1.5 秒，会跳到尾部前约 250ms；进入 waiting/stalled 时重新启动加载
看门狗，避免网络抖动后持续播放陈旧缓冲。

HomeKit 实时流必须严格匹配 `SupportedVideoStreamConfiguration` 和
`SelectedRTPStreamConfiguration`。当前附件服务数据库与已经过 iOS 26 验证的 go2rtc
v1.9.14 保持一致：一个 Camera RTP Stream Management Service、一个 Microphone Service，
声明 H.264 Main Level 3.1/4.0 和 1080p/720p/Apple Watch 三档能力。多 Source 顺序优先
复用摄像头原生 H.264，只在源不满足 H.264/Opus Consumer 时启用 1280×720 FFmpeg 回退；
不再把所有 Consumer 强制压到共享的 299 Kbps 码流。回退编码器每个 IDR 都重复 SPS/PPS，
使新加入的 SRTP Consumer 无需等待带外参数集即可解码。Opus RTP 按 RFC 7587 固定使用
48kHz RTP clock，因此
HomeKit Consumer 不能用 Controller 选择的 16/24kHz 编码带宽去匹配 SDP clock；实际
包时长和时间戳由 `RepackToHAP` 按协商结果重排。RTP 打包采用 Controller 选择的 MTU，
RTCP Sender Report 使用 NTP 1900
纪元及 32 位秒小数，而不是 Unix 纳秒。Camera Kernel 不再声明自己无法输出的参数。后续若要
同时支持多档清晰度，应新增按 Session 的转码实例，而不是让一个固定共享码流假装满足
多个协商结果。`SetupEndpoints` 遵循 HAP-NodeJS 的标准读写流程：Controller 的 PUT
返回 204，Accessory 保存应答，Controller 再通过 GET 读取 SRTP 地址、key/salt 和 SSRC；
不得在 Controller 未请求 `r=true` 时根据 `wr` 权限强制返回 207。每次改变已配对
Accessory 的服务数据库或媒体能力都必须递增 mDNS `c#`；本轮从 5 增至 6，使 Apple Home
重新读取 `/accessories` 而无需删除配对。双栈会话按 SetupEndpoints 返回 IPv4/IPv6
地址族；当 Controller 给出不带 zone 的 IPv6 link-local RTP 地址时，从现有 HAP TCP
连接恢复网卡 zone，避免 SRTP `WriteTo(fe80::...)` 无法路由。H.264 RTP Consumer 中途
加入 FU-A 分片流时丢弃缺少起始分片的尾包，防止把残缺 IDR 误判为关键帧。

建议策略：

- 电池摄像头：`on_demand`；
- 普通插电式摄像头：`preload`；
- 需要最低打开延迟或接入持续消费端：`always_on`。

---

## 6. Provider 扩展设计

新增独立的 `camera` Provider 类型，但不新增一套平行的 Provider SDK 主接口。
Camera Provider 仍实现当前项目的 `provider.Provider`，按需组合 `Discoverer`、
`CommandExecutor`、`DeviceEventSubscriber`、`CredentialMaintainer` 和媒体可选接口。
它与 Xiaomi MIoT Cloud 等账号/目录 Provider 是两个独立实例，通过稳定
`credentialProviderRef` 或 `catalogProviderRef` 引用，而不是复制账号长期凭据。

Camera Provider 的设备条目使用带版本的 tagged config：

```go
type CameraEntry struct {
    ID                    string          `json:"id"`
    Name                  string          `json:"name"`
    Driver                string          `json:"driver"` // xiaomi-miss|rtsp|onvif|homekit|...
    CatalogProviderRef    string          `json:"catalogProviderRef,omitempty"`
    CredentialProviderRef string          `json:"credentialProviderRef,omitempty"`
    NativeDeviceID        string          `json:"nativeDeviceId,omitempty"`
    Connection            json.RawMessage `json:"connection"`
    Profile               CameraProfile   `json:"profile"`
    Enabled               bool            `json:"enabled"`
}
```

`Connection` 按 `Driver` 严格校验，不能成为任意 JSON 逃生舱：

- `xiaomi-miss`：引用 Xiaomi MIoT Cloud Provider、DID、局域网地址、镜头与清晰度；
- `rtsp`：地址、传输方式、认证 Credential Ref、主/辅码流；
- `onvif`：发现地址、Profile Token、PTZ/Event 能力；
- `homekit`：输入配对身份引用与 Accessory 信息；
- 其他厂商：只增加新的 driver schema 和 Media Adapter，不增加新的摄像头主模型。

添加摄像头流程统一为：

1. 在“设备来源 → 摄像头”创建或打开 Camera Provider；
2. 选择“从账号目录导入”“局域网发现”或“手动添加”；
3. 选择协议 Driver，并配置该摄像头的连接、码流、音频、双向语音和事件细节；
4. 测试连接和 Probe 成功后保存；
5. Camera Provider 发布统一 Camera Device，设备只出现在设备中心；
6. 用户前往 `targets` 显式选择是否转发以及 HomeKit、Matter或其他输出方式。

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
- 静态凭据、配对身份和云端授权由 Core 的 Credential Broker 路由到 Camera Provider
  引用的账号 Provider 或 Credential Store；
- Camera Device 的 `ProviderID` 是 Camera Provider 实例 ID，不是 Xiaomi MIoT Cloud
  账号实例 ID；
- `ProviderDeviceID` 是 Camera Provider 内的 Camera Entry ID；厂商 DID 放在协议配置中；
- Camera Provider 只引用账号级凭据，绝不复制 `accessToken`、`serviceToken`、
  `passToken` 或账号密码；
- 删除账号 Provider 前必须检查 Camera Provider 引用并阻止删除或要求用户迁移；
- Camera Provider 保存/启用不创建 Target，不分配 HAP/Matter 身份，也不自动广播 mDNS。

### 6.1 Camera 媒体与 Xiaomi 控制能力合并

小米摄像头的媒体源与 MIoT 控制源可以来自不同 Provider，但设备中心和 Target 只能看到
一个由 Camera Provider 拥有的 Camera Device。控制绑定是 Camera Entry 的显式配置，
不按名称、型号、房间或 IP 猜测：

```json
{
  "id": "living-camera",
  "name": "客厅摄像头",
  "driver": "xiaomi-miss",
  "xiaomi": {
    "credentialProviderRef": "xiaomi-miot-cloud-main",
    "did": "1178028045",
    "model": "chuangmi.camera.example",
    "localIp": "192.168.1.20"
  },
  "control": {
    "providerRef": "xiaomi-hub-main",
    "deviceId": "xiaomi-control-1178028045"
  }
}
```

契约约束：

- `control.providerRef + control.deviceId` 是首版完整绑定身份，二者都必填。厂商 DID
  继续只由被引用 Xiaomi Provider 的 `DeviceConfig` 持有，Camera 配置不复制 DID、
  SIID/PIID、Token 或凭据。修改 Xiaomi `DeviceConfig.id` 是显式身份迁移，必须同时
  更新引用，不能按名称、房间或 IP 自动改绑。
- 控制 Provider 只允许 `xiaomi` 或 `xiaomi-miot-cloud`，两者复用相同 Property/Command
  契约和失败语义；有中枢时优先绑定 `xiaomi` 以获得本地控制和云端回退。Camera
  Provider 不直接持有另一个 Provider 对象，而是通过 Core 的通用能力绑定和控制路由
  执行请求，避免生命周期循环。
- 一个已启用的 `(providerRef, deviceId)` 在全部 Camera Provider 中最多绑定一次；同一
  Camera Entry 最多一个控制源。禁止引用自身、引用另一个 Camera Provider、引用一个
  已合成 Camera Device，或形成 `catalog/credential/control` 依赖环。
- Xiaomi 控制源仍作为 Provider Manager 的内部路由设备存在，以便属性读写、Action、
  Push 和云端回退继续工作；控制专用摄像头由 Provider 显式声明为隐藏身份，即使绑定
  暂时缺失也不得生成设备中心卡片。绑定成功后其完整 MIoT Source Catalog（包括属性
  和 Action）合并到 Camera Provider 拥有的唯一 Camera Device；内部路由不删除，
  Camera Device 是唯一对用户展示和供新 Target 选择的设备。“配置映射”的来源完整
  属性接口必须返回同一个合成目录，不能退回 Camera Provider 自身的三个媒体字段。
- 首版把来源设备的非 `media` Capability 以原 endpoint/capability/property/command
  路径投影到 Camera Device；与 Camera 自身 `main/media` 冲突时保存或发现必须失败，
  不能覆盖。Raw MIoT Catalog 仍归 Xiaomi Provider，Camera 配置不复制映射定义。
  HomeKit/Matter Target 必须用显式标准映射白名单选择要发布的能力，绝不能把所有 Raw
  Capability 自动翻译成生态 Service/Cluster。
- Unified Camera v2 补齐状态灯、夜视、移动侦测、九向 PTZ 移动/停止、移动速度、
  水平/垂直绝对位置、变焦倍率以及当前/目标记忆点和记忆点数量；删除无法正确表达
  控制语义的旧 `pan/tilt/zoom` 布尔字段，不保留兼容层。具体小米型号没有对应 MIoT
  Property/Action 时不伪造能力，方向控制和记忆点命令直接来自该设备的完整 Source
  Catalog。
- Camera Device 的媒体可用性与控制可用性分别维护。视频正常而中枢离线时，Camera
  仍可预览并显示“控制不可用”；控制恢复不得重启 Camera Kernel。隐私模式开启导致
  上游停止出帧时应显示明确状态，并主动结束现有 HomeKit/Matter 实时会话。
- 属性读取、写入和 Action 的错误保持 Provider SDK 语义：找不到绑定返回 unsupported，
  控制 Provider 停止返回 unavailable，参数/枚举不匹配返回 invalid，设备拒绝写入返回
  rejected；不得静默回写乐观成功状态。
- 保存、更新或删除 Provider 前，Core 必须解析所有 Camera Entry 的
  `catalogProviderRef`、`credentialProviderRef` 和 `control.providerRef`，构建统一
  依赖图。被引用 Provider 的停用应给出影响提示并让 Camera 保留媒体能力；删除必须
  fail closed，返回具体 Camera Provider/Entry 引用，用户迁移或解除绑定后才能删除。
- 备份恢复必须先恢复账号/中枢 Provider，再恢复 Camera Provider，并重新校验绑定设备；
  引用暂不可用时保留配置但标记控制 pending，不能自动改绑到同名设备。

合并后的协议映射范围：

| Unified Camera 能力 | HomeKit Camera | Matter Camera 1.5 | 首版策略 |
| --- | --- | --- | --- |
| 隐私模式 | 可作为同一 Accessory 的命名 Switch；切流必须同步 | Camera AV Stream Management Privacy | 标准映射 |
| 夜视/红外灯 | 无专用标准，可选命名 Switch | Camera AV Stream Management NightVision | Matter 标准；HomeKit 降级 |
| 麦克风静音/音量 | 可附加 Microphone；控制器支持需实测 | Camera AV Stream Management Audio | 有真实读写能力才声明 |
| 扬声器/对讲 | 可附加 Speaker；现有媒体无 talkback 时不得声明 | Speaker feature + WebRTC audio | 当前媒体未实现 talkback，暂不发布 |
| 移动/占用 | MotionSensor | Occupancy Sensing，可结合 Zone Management | 事件标准映射 |
| 门铃 | Doorbell/Programmable Switch Event | Video Doorbell 0x0143 组合设备 | 仅真实门铃型号 |
| PTZ/预置位 | HAP 无通用 Camera PTZ 标准，保留设备中心或命名控制 | Camera AV Settings User Level Management | Matter 后续里程碑 |
| 状态灯、警笛、巡航等厂商功能 | 命名 Switch 或仅设备中心 | 无等价 Camera Cluster 时不得伪装 | 默认仅设备中心 |

HomeKit 当前独立 Camera Accessory 只安装媒体流与截图服务；Matter 当前 Camera Node 只
安装 AV Stream/WebRTC 最小集。因此上表是合并层的发布设计，不表示附加服务或 Cluster
已经实现。Target 必须逐项按真实能力启用，不能因为 Xiaomi Raw Catalog 中存在字段就
宣称协议支持。

实现必须同步覆盖以下自动化失败矩阵：

1. 缺少/非法 `providerRef` 或 `deviceId`，引用 Provider/设备不存在、停止或类型不允许；
2. Xiaomi Provider 重配后删除/重命名来源 Device 时，Camera 控制应降级 pending，
   不得按名称、房间、型号或 IP 自动改绑；
3. 控制源引用自身、Camera→Camera、跨 Camera Entry 重复 `(providerRef,deviceId)` 和多级
   `catalog/credential/control` 循环；
4. 来源包含 `media` 或路径与 `main/media` 冲突、重复 endpoint/capability/property/
   command、写只读属性、Action 缺参/多参/参数类型错误；
5. 本地中枢成功、本地失败回退云端、强制 local 不回退、Provider 停止、设备离线、
   超时、取消、写入被拒绝，以及失败后不得留下乐观值；
6. Push/轮询事件只更新合成 Camera 一次，乱序旧值和重复事件不倒退状态，解绑后旧
   订阅不得继续更新 Camera；
7. 隐藏 Source 不破坏内部 Property/Command 路由，设备中心和新 Target 不展示 Source，
   旧 Target/Binding 的迁移或保留行为确定且可回滚；
8. 停用被引用 Provider 只降级控制状态；删除被引用 Provider 被拒绝且错误列出全部
   引用；解绑后可删除；持久化失败时运行态和配置均回滚；
9. 视频在线/控制离线、视频离线/控制在线、隐私模式主动结束流、控制恢复不重启
   Camera Kernel；
10. HomeKit/Matter 只声明已实现且有真实 source mapping 的能力，未知 Xiaomi 功能不
    自动成为 Switch/Cluster；Target 删除或重建不会删除 Xiaomi 控制设备和账号凭据；
11. 备份恢复顺序颠倒、引用 Provider 暂缺、来源 Device ID 变化、旧配置没有 `control` 字段时均
    fail safe，且日志/API/导出不泄露 Token、密码或 Action 原始敏感参数。

首批 Camera Driver：

```text
camera/drivers/rtsp
camera/drivers/onvif
camera/drivers/xiaomi
camera/drivers/tapo
camera/drivers/homekit
camera/drivers/wyze
camera/drivers/tuya
camera/drivers/ring
camera/drivers/nest
camera/drivers/roborock
```

---

## 7. Media Adapter 设计

Core 内嵌 Media Runtime 内每种协议实现统一接口：

```go
type MediaAdapter interface {
    Scheme() string

    Connect(
        ctx context.Context,
        stream StreamRuntimeSpec,
    auth AuthorizationService,
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
    DeviceID  string
    Protocol  string
    Purpose   string
    Attempt   int

    ClientMaterial json.RawMessage
    SessionOffer   []byte
}
```

`ClientMaterial` 用于 Runtime 为 Camera Kernel 生成公开的临时材料，例如：

- 小米 `client_public`；
- WebRTC SDP Offer；
- 厂商 challenge；
- Camera Kernel 支持的 codec/profile。

`ClientMaterial` 按 `Protocol` 使用严格、版本化的联合类型，不接受任意键值。不得包含：

- Camera Kernel 临时私钥；
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
- Camera Kernel 退出后 Runtime 应清理内存中的会话材料；
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
- `RequestID` 在 Runtime 进程内唯一；Core 在短期内存缓存中对重复请求返回同一结果，Core 重启导致结果不确定时返回明确错误并要求新的 RequestID；
- Lease 原子绑定 `DeviceID + Protocol + Purpose + KernelInstanceID`；
- Lease 同时绑定 `ClientMaterial`/SDP Offer 摘要，防止授权材料被替换；
- 默认 `MaxUses=1`，Lease 创建和 use count 更新必须通过数据库事务或等价原子操作完成；
- 认证重试是否消耗 use count 由协议策略显式定义；
- Core 重启或 Kernel 退出后，未使用 Lease 自动失效；
- 支持过期、主动撤销、Core 关停清理及每 Device 的速率和并发限制；
- 数据库只保存状态、摘要和时间，不保存授权响应原文。

### 8.4 连接结果上报

```go
type SessionReport struct {
    LeaseID  string
    KernelInstanceID string
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

## 9. 内嵌 Runtime 边界

Media Runtime 与 Core 在同一进程、同一领域事务边界内运行；不维护 Unix Socket、Named Pipe、
TCP 监听器、握手、replay 或进程间 JSON-RPC。流的期望状态直接由 Media Service 传给
Runtime，并以 generation/revision 驱动按流启动、停止、重启、probe 和 snapshot。

```text
Media Service → Embedded Runtime → Camera Kernel（每流子进程）
                 │                         │
                 ├─ 授权、状态、审计、存储  └─ 仅短期会话材料、媒体数据面
                 └─ 退出/资源监督
```

Kernel 启动只接收当前流所需的最小短期材料；优先使用受限环境变量或匿名管道，绝不通过
命令行、可被其他用户读取的临时文件或网络接口传递秘密。KernelInstanceID 用于将 Lease、
状态和退出事件绑定到特定子进程。所有调用都有 context 超时、有界队列和明确背压错误。

---

## 10. 典型协议流程

### 10.1 RTSP

```text
Runtime 读取 camera 配置
        ↓
HomeLoom 返回 host、path、短期用户名密码副本
        ↓
Camera Kernel 执行 RTSP OPTIONS/DESCRIBE/SETUP/PLAY
        ↓
持续处理 RTP
```

用户名密码可长期保存，但 Runtime/Camera Kernel 只在内存中持有副本。

### 10.2 ONVIF + RTSP

```text
HomeLoom 扫描 ONVIF
        ↓
读取设备信息、Profile、RTSP URI、PTZ 能力
        ↓
注册 Camera 和 StreamSpec
        ↓
Camera Kernel 使用 RTSP 拉流
```

ONVIF 控制命令由 HomeLoom Provider 执行，媒体由 Runtime/Camera Kernel 处理。

### 10.3 Xiaomi MISS

```text
Camera Kernel 生成 client_public/client_private
        ↓
Runtime 将 client_public 发送给 HomeLoom 授权服务
        ↓
HomeLoom 使用小米账号调用 miss_get_vendor
        ↓
返回 device_public、sign、vendor、uid、endpoint
        ↓
Camera Kernel 使用 client_private 计算 shared key
        ↓
Camera Kernel 通过 CS2/TUTK 本地连接摄像头
        ↓
解密和处理媒体
```

要求：

- `client_private` 不离开 Camera Kernel；
- 小米账号 Token 不离开 HomeLoom；
- 第一版每个新上游会话重新申请授权；
- 后续可做“缓存优先、失败刷新”。

### 10.4 Tapo / VIGI

```text
HomeLoom 返回设备账号或密码摘要
        ↓
Camera Kernel 连接摄像头获取 nonce
        ↓
Camera Kernel 本地派生 AES Key/IV
        ↓
Camera Kernel 解密 MPEG-TS/媒体数据
```

nonce 和 AES 密钥不回传 HomeLoom。

### 10.5 HomeKit Camera 输入

```text
HomeLoom 返回配对 ID、控制器私钥、设备公钥
        ↓
Camera Kernel 建立 HomeKit 加密控制通道
        ↓
Camera Kernel 为本次媒体会话生成 SRTP key/salt
        ↓
摄像头发送 SRTP
        ↓
Camera Kernel 转为内部 RTP
```

配对材料长期保存在 HomeLoom，Camera Kernel 按需获取。

### 10.6 Wyze

```text
HomeLoom 返回 UID、ENR、MAC、model 等设备材料
        ↓
Camera Kernel 建立 P2P/DTLS 会话
        ↓
Camera Kernel 生成临时媒体密钥并取流
```

设备材料可以缓存，DTLS 会话不可跨连接复用。

### 10.7 Tuya / Ring / Nest / Roborock

```text
Camera Kernel 创建 SDP Offer
        ↓
HomeLoom 使用长期账号材料调用云端
        ↓
返回 SDP Answer、ICE、TURN、Session Ticket
        ↓
Camera Kernel 建立 WebRTC/DTLS/SRTP
```

HomeLoom 负责 Token 刷新，Camera Kernel 不接触 Refresh Token。

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
│   └── mediaruntime/
│       ├── manager.go        # 内嵌流生命周期与 Kernel 监督
│       ├── authorization.go  # Core 内部授权调用
│       └── publisher.go
└── persistence/gormstore/
    ├── media.go
    └── media_storage.go
```

约束：

- 不创建独立 `camera` 主领域包复制 Device Service；
- Provider 只能依赖现有统一设备模型和通用媒体契约，不允许互相引用；
- 凭据 AEAD、主密钥加载、日志脱敏、审计和备份恢复扩展现有模块，不另建第二套实现；
- Media Runtime 复用 Target Manager/Matter Runtime 的生命周期和测试模式，但它是 Core 内嵌组件，不伪装成普通 Target，也不启动独立控制进程。

---

## 12. Core Media Runtime 与 Camera Kernel 目录结构

实现范围收敛为 HomeLoom 自有的最小 Camera Kernel。固定版本 go2rtc 只作为 MIT
许可证下的协议实现来源，不再作为外部通用服务下载或运行。源码纳入仓库，由
`camera-kernel/main.go` 在编译期固定能力白名单：

```text
camera-kernel/
├── main.go                 # 唯一编译期能力白名单
├── internal/
│   ├── rtsp/
│   ├── onvif/              # 仅输入解析，不提供 ONVIF Server
│   ├── xiaomi/             # 仅预授权 MISS，不登录云端、不回退 Legacy
│   ├── ffmpeg/
│   ├── homekit/            # 仅 Camera 输出
│   ├── srtp/
│   ├── streams/
│   └── mp4/                # 仅设备中心预览端点
└── pkg/                    # 上述能力的最小协议依赖
```

正式支持范围固定为：

| 能力 | 范围 |
| --- | --- |
| 输入 | RTSP；ONVIF 发现/Profile 解析后转 RTSP；预授权 Xiaomi MISS |
| 处理 | 受路径白名单约束的项目内 FFmpeg，首要用途为 H.265/HEVC → H.264 |
| 输出 | 每 Camera 独立 HomeKit Accessory、HAP、SRTP；设备中心受限 MP4 预览 |
| 明确排除 | HomeKit Camera 输入、RTMP、WebRTC、HLS、DVR、隧道、通用 Web UI/API、其他云摄像头和厂商协议 |

稳定性边界：

- Media Runtime 内嵌 Core；每个 HomeKit Camera 继续使用独立 Camera Kernel 子进程，单路故障不拖垮 Core 或其他流；
- 子进程只能运行 HomeLoom 构建的 `homeloom-camera-kernel`，版本不匹配即拒绝；
- 通用静态 Web UI 不注册，HTTP 只开放 HomeKit 配对和设备中心预览白名单；
- RTSP 服务只监听 loopback；HAP/SRTP 仅开放确定的每 Camera 端口；
- Xiaomi Kernel 不持有账号 Token，只接受 Core Runtime 生成的内存预授权 MISS Source；
- 密钥、签名和 Source URI 只经受限子进程环境变量或匿名管道传递，不落盘；
- 新协议不得通过配置动态开启，必须修改编译期白名单、Provider 契约、威胁模型和测试。

---

## 13. 动态流管理

HomeLoom 为唯一配置源；内嵌 Runtime 不长期保存业务配置。

启动流程：

```text
Media Service 读取期望流状态
   ↓
Core 内嵌 Runtime 创建/更新/删除本地流
   ↓
按流启动或回收 Camera Kernel
   ↓
Runtime 将结果写入状态与审计
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

- `Generation` 标识 Core 配置纪元，内嵌 Runtime 不接受旧纪元；
- `Revision` 是 Core 生成的全局单调版本；
- 重复 mutation 必须幂等；
- revision 缺号、乱序或 generation 改变时，Runtime 从持久化期望状态重新加载；
- Core 重启时不恢复旧增量队列，只应用最新完整状态；
- start/stop 等临时动作不改变期望配置；
- 多消费者观看同一逻辑流时复用上游连接。

流名称使用稳定内部 ID：

```text
camera_<uuid>
```

摄像头名称变化不应改变流 URL 和 Apple Home 身份。

---

## 14. 数据库设计

不新增重复的 `cameras` 主表。一个 `providers(type=camera)` 记录代表一个摄像头接入
实例，其 config 保存带版本的 Camera Entry；摄像头的名称、房间、Availability 等仍
来自统一 Device，媒体表只保存媒体扩展。Xiaomi MIoT Cloud 等账号 Provider 继续保存
账号连接，Camera Entry 只保存其稳定引用。

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

Core 根据当前 Device 和 Camera Kernel 实例绑定 namespace；Runtime 不能指定任意命名空间。HomeKit 输出配对、Accessory 私钥和配置版本通过 Core 存储服务持久化。

### 14.5 media_runtime_state

运行状态驻留内存；只持久化恢复所需的流期望状态、Kernel 最近退出摘要和审计记录，不引入
独立实例、心跳或多运行时分配表。

### 14.6 media_auth_leases

只保存元数据和摘要，不保存原始私钥：

```text
id
kernel_instance_id
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
kernel_instance_id
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

Runtime 与 Camera Kernel 只在内存保存：

- client_private；
- shared key；
- Tapo AES key/IV；
- RTSP SessionID；
- DTLS/SRTP key；
- SDP/ICE/TURN 临时会话；
- Xiaomi sign 和设备公钥副本。

### 15.3 短期复用

允许 Runtime 在同一 Core 进程内对短期材料进行有限缓存：

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
Runtime 在协议层快速重连
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
network_failed       → Runtime/Camera Kernel 网络层
camera_offline       → 摄像头状态
local_auth_failed    → Runtime/Camera Kernel 本地认证
cloud_auth_failed    → HomeLoom Provider
credential_expired   → HomeLoom 凭据刷新
unsupported_codec    → Camera Kernel 媒体能力
runtime_unavailable  → Runtime 或 Camera Kernel 生命周期
```

HomeLoom UI 应展示具体层级，而不是统一显示“摄像头不可用”。

### 16.4 Core 或 Kernel 暂时不可用

- Core 退出会受控关闭 Kernel，启动后从期望状态恢复；
- Kernel 退出只影响对应流，Runtime 使用内存中已有会话材料按退避重启；
- Runtime 不允许从本地文件读取长期 Token 作为旁路；
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

Core 内嵌 Media Runtime：

- 可访问摄像头 VLAN；
- 可开放媒体服务端口；
- 不可直接访问数据库；
- 可按协议限制是否访问公网。

对 Xiaomi、RTSP、Tapo 等本地媒体协议，可禁止 Camera Kernel 访问厂商账号 API。

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

### 17.4 Camera Kernel HTTP/RTSP 边界

- 不提供通用 Web UI、动态配置、WebSocket 或管理 API；
- HTTP 只注册 HomeKit Pair Setup/Verify 与设备中心 MP4 预览白名单；
- RTSP 只监听 localhost，不提供用户配置的 LAN 开放开关；
- FFmpeg/exec 只接受 HomeLoom 构建时确定的二进制绝对路径；
- HomeKit 摄像头端口和 mDNS 单独开放。

---

## 18. Target 输出与摄像头子分页

摄像头输出必须进入现有桥接中心的独立子分页，不能混入普通 Target 的设备映射表。

```text
桥接中心 / Targets
├── 普通设备
│   ├── Apple Home Bridge
│   ├── Matter Bridge
│   └── 其他 Consumer
├── HomeKit 摄像头
│   └── 每行一台摄像头、一个独立 HAP Accessory
├── Matter 摄像头
│   └── 首版每行一台摄像头、一个独立 Matter Node
└── 其他摄像头
    └── RTSP restream / WebRTC / NVR / 厂商输出
```

HomeKit 摄像头子分页每行至少展示：

- Camera Device、来源 Driver、在线状态和设备中心预览入口；
- 发布开关、发布名称、主/辅码流、音频、双向语音和事件映射；
- 独立 HAP 地址、PIN、Setup ID、配对状态、配对二维码和重置身份操作；
- Source、Transcode、Publisher 三段健康状态和最近错误；
- “取消发布”只停止 Target，不删除 Camera Device、MediaSource 或设备中心预览。

Target 期望状态建议统一为：

```go
type CameraPublication struct {
    ID             string          `json:"id"`
    TargetType     string          `json:"targetType"` // homekit-camera|matter-camera|...
    CameraDeviceID string          `json:"cameraDeviceId"`
    Enabled        bool            `json:"enabled"`
    StreamPolicy   json.RawMessage `json:"streamPolicy"`
    TargetConfig   json.RawMessage `json:"targetConfig"`
}
```

`CameraPublication` 属于 Target 域；`StreamSpec` 属于共享媒体运行域。Target Manager 根据
Publication 派生/引用 Stream；`on_demand` 在最后一个 Consumer 停止后释放上游，
`preload` 保持原始视频连接，`always_on` 保持最终转码输出。
禁止把 `StreamSpec.Options.publisher` 当作用户配置或 Target 的替代物，也禁止发现
Camera 时自动创建 Apple Home Publisher。当前 Xiaomi 纵切由 `homekit-camera` Target
生命周期把它投影成 Runtime 的内部期望状态（`apple-home`/`none`）；删除或停用 Target
会恢复为 `none`，但不会删除 Camera Device、MediaSource 或设备中心预览。

### 18.1 Apple Home 输出

第一版使用 Core 内嵌 Media Runtime/HomeLoom Camera Kernel 输出摄像头 HomeKit 服务：

```text
HomeLoom 普通设备桥
├── 灯
├── 开关
├── 窗帘
└── 传感器

Core Media Runtime HomeKit Camera
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

摄像头发布器必须是独立进程和独立 HAP Accessory，不能合并到现有普通设备
`apple-hap` Bridge。当前实现由每个 Stream 独占一个 `homeloom-camera-kernel` 子进程，并在权限为
`0700` 的 Stream Runtime 目录中保存 `0600` 的 HomeKit identity 与 pairings。后续发布
加固仍需通过 Core 存储服务把这些状态迁移到 `media_runtime_kv`，以纳入 Core 备份、恢复
和主密钥轮换；迁移时不得改变已发布的
Accessory 身份。

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

### 18.2 Matter Camera 输出

Matter 1.5 正式引入 Camera，媒体使用 WebRTC；Matter 1.5.1 增强多路音视频传输。
Matter 数据模型允许一个 Node 包含多个 Endpoint，Bridge 也允许用 Bridged Node 和
动态 Endpoint 表示多个外部设备。因此协议层面可以把 Camera Endpoint 合并到一个
Matter Bridge。

HomeLoom 首版仍采用“每台 Matter Camera 一个独立 Matter Node/Target”：

- 摄像头的 WebRTC 会话、访问控制、网络带宽和错误域与普通属性 Endpoint 显著不同；
- 可独立配网、撤销 Fabric、升级和诊断，不影响现有普通 Matter Bridge；
- 不同 Controller 对 Matter 1.5 Camera 和 bridged camera 的支持节奏不一致；
- Camera 认证、设备类型和媒体能力可以独立演进。

后续在以下条件全部满足后，增加“合并到 Matter Bridge”高级选项：

1. 使用 Matter 1.5.1 或更高实现并通过 Camera 相关一致性测试；
2. 目标 Controller 明确支持 bridged Camera Endpoint；
3. 动态 Endpoint、ACL、Fabric、多路 WebRTC 和重启持久化测试通过；
4. 单 Camera 故障不会重启整个 Matter Bridge；
5. UI 明确提示合并后共享 commissioning identity 和故障域。

“Matter Camera 可合并”与“Apple Home 能显示 Matter Camera”是两个问题。HomeLoom
不得因为 Controller 支持普通 Matter 设备就推断其支持 Matter Camera；Apple Home
场景在确认其 Controller 支持前继续使用独立 HAP Camera Target。

规范依据：

- CSA Matter 1.5 Camera：
  https://csa-iot.org/newsroom/matter-1-5-introduces-cameras-closures-and-enhanced-energy-management-capabilities/
- CSA Matter 1.5.1 Camera：
  https://csa-iot.org/newsroom/matter-1-5-1-enhancing-camera-performance-and-expanding-device-flexibility/

#### 18.2.1 实现基线和参考项目

Matter Camera 不复用现有 Matter Bridge 的 Aggregator Endpoint。新增
`matter-camera` Target，继续复用 Matter Runtime 的进程监督、commissioning、Fabric
存储和诊断能力，但每个 Target 创建一个独立 Node，Camera Device Type `0x0142`
直接挂载到该 Node。

实现以 CSA 官方 connectedhomeip `v1.5.1.0` 为行为参考：

- Camera App：
  https://github.com/project-chip/connectedhomeip/tree/v1.5.1.0/examples/camera-app
- Camera Controller：
  https://github.com/project-chip/connectedhomeip/tree/v1.5.1.0/examples/camera-controller
- Camera AV Stream Management Server：
  https://github.com/project-chip/connectedhomeip/tree/v1.5.1.0/src/app/clusters/camera-av-stream-management-server
- WebRTC Transport Provider Server：
  https://github.com/project-chip/connectedhomeip/tree/v1.5.1.0/src/app/clusters/webrtc-transport-provider-server

项目当前固定的 matter.js `0.17.6` 已包含 Matter 1.5 Camera、Camera AV Stream
Management、WebRTC Transport Provider/Requestor 数据模型，但其默认 Provider 和
AV Stream Management Server 只是生成的空行为，不提供媒体资源分配、SDP、ICE 或 RTP
实现。因此 matter.js 只作为首选控制面；只有在以下能力测试全部通过后才继续使用：

1. Camera `0x0142` 和必需 Cluster 可注册到独立 Node；
2. Provider 可向 Requestor 发送异步 `ProvideAnswer` 和 ICE 命令；
3. SDP/ICE 大消息、TCP 传输和重启后的 Fabric/ACL 恢复通过；
4. 自定义 AV Stream 分配行为可替换空 Server 且不修改生成代码。

任一能力无法稳定满足时，Matter Camera Target 改用 connectedhomeip `v1.5.1.0`
独立 sidecar；普通 Matter Bridge 继续使用 matter.js，两者不共享故障域。

#### 18.2.2 首版能力和明确非目标

首版固定能力为：

- 一台 Camera 对应一个独立 Matter Node/Target；
- Camera AV Stream Management `0x0551`：Video、Audio、Snapshot；
- WebRTC Transport Provider `0x0553` 和 Requestor `0x0554`；
- H.264 视频、Opus 音频和 JPEG Snapshot；
- 仅局域网 host ICE、单并发直播会话；
- 会话所有权绑定 Fabric、Peer Node、Peer Endpoint 和 Camera Target；
- 媒体源由 Core 按 Device ID 解析，Matter Runtime 不接触 RTSP/MISS 长期凭据。

首版不实现 Push AV、录像、Zone、PTZ、TURN、SFrame 和多并发会话。Matter Camera
Device Type 的完整实现要求 Video、Audio、Snapshot；源摄像头没有真实音频时，不得用
静音轨伪装完整兼容，应拒绝发布并在 UI 中指出缺失能力。

#### 18.2.3 分期实现与进度

| 阶段 | 状态 | 交付与验收 |
| --- | --- | --- |
| MC-0 规范/SDK 能力审计 | 已完成 | 固定 connectedhomeip `v1.5.1.0` 参考；确认 matter.js 只有模型、默认 Camera Server 无媒体行为；Apple Home 不作为首个验收 Controller |
| MC-1 WebRTC 会话控制契约 | 已完成 | 已实现可注入媒体后端的新会话 `ProvideOffer(null) → ProvideAnswer`、已有会话 re-offer、双向 trickle ICE、EndSession、并发上限、未知/跨 Fabric 会话拒绝及失败清理；单元测试覆盖关闭竞态 |
| MC-2 独立 Target/Node | 已完成 | 已新增 `matter-camera` Target 与 `nodeKind=camera` IPC；一 Target 绑定一 Camera Device；Camera 0x0142 直接挂 Node，复用 commissioning/storage/watchdog，普通 Matter Bridge 不受影响 |
| MC-3 AV 资源与 Snapshot | 已完成（自动化） | 已实现 H.264/Opus/JPEG 能力表、优先级、Allocate/Modify/Deallocate、Fabric/Peer 所有权和资源上限；受限 `frame.jpeg` 返回真实 JPEG 并限制尺寸、并发、时间和内存 |
| MC-4 WebRTC 媒体面 | 已完成（自动化） | 已在 Camera Kernel 内用 Pion 实现 H.264/Opus RTP、SDP、host ICE、RTCP drain、关键帧/协商超时及清理；Provider 先返回 OfferResponse，再异步回调 Requestor Answer/ICE；没有引入通用 WebRTC Web UI/WS |
| MC-5 一致性与真机 | 自动化完成，外部验证待执行 | Core↔sidecar↔Camera Kernel 私有 RPC、Matter/Pion 会话 ID 隔离、CurrentSessions、错误清理及构建/单测均已完成；仍须使用 `chip-camera-controller` 执行 `TC_AVSM_*`、`TC_WEBRTC_*`、`TC_WEBRTCP_*`，并完成故障注入、重启恢复和 24 小时单会话回归后，方可移除实验性标识 |
| MC-6 Controller 兼容性 | 验证准备完成，尚未执行 | 已固定 connectedhomeip `v1.5.1.0` Camera Controller 的 commissioning、AV allocation、WebRTC LiveView、JPEG Snapshot 和资源清理步骤，并增加本机工具预检入口；当前本机无 `chip-camera-controller`，所有结果保持 `NOT RUN`。Android/其他 Matter 1.5 Controller 后续建立矩阵；Apple Home 仅作实验探测，未验证前继续使用 HAP Camera |

MC-1 的代码是 SDK 无关的控制契约，不代表已经能创建或配对 Matter Camera。当前
“Matter 摄像头”分页可以开放实验性创建和配置入口，用于推动 MC-2 至 MC-6 联调；
但在 MC-5/MC-6 通过前必须持续显示 Controller 兼容性和 Apple Home 限制，不能把
二维码可见、mDNS 可发现或 Target 已创建描述为摄像头可用或正式发布。

#### 18.2.4 安全和稳定性约束

- SDP、ICE candidate、Node/Fabric 标识设置长度与数量上限；日志只记录会话摘要；
- 会话上限首版固定为 1，建立、Answer 回调、ICE、媒体首帧和闲置均有独立超时；
- Target 停用、Fabric 撤销、Peer 断开和 Runtime 重启都必须幂等释放编码器、UDP
  socket、AV Stream allocation 和会话表项；
- Provider 只接收 Core 签发的短期本机媒体入口，不接收或持久化 Camera 源凭据；
- Camera 媒体故障不得重启普通 Matter Bridge，也不得占用已删除 Target 的 Node
  identity 或端口。

#### 18.2.5 MC-6 验证口径

可重复验证命令、证据要求和 Controller 矩阵见
[`Matter_Camera_MC6_验证清单.md`](Matter_Camera_MC6_验证清单.md)。执行前使用
`scripts/check-matter-camera-controller.sh` 检查 connectedhomeip `v1.5.1.0`
`chip-camera-controller`。工具不存在时脚本返回 `NOT RUN` 和非零状态，不得将单元测试、
Matter 二维码可见或 mDNS 被发现替代为真机 Camera 验收。

MC-5/MC-6 的发布门槛至少包括：

1. commissioning 后 Fabric 可跨 Target 重启恢复；
2. H.264/Opus/JPEG allocation、deallocation 与超限拒绝均有 Controller 证据；
3. WebRTC 在 10 秒内收到 H.264 IDR并连续播放，Opus 使用 48000 Hz RTP clock；
4. `CaptureSnapshotResponse.ImageData` 可独立解码为请求尺寸的 JPEG；
5. EndSession、断网、Provider 重启和 Target 停用均能有界释放会话与媒体资源；
6. 24 小时回归及 `TC_AVSM_*`、`TC_WEBRTC_*`、`TC_WEBRTCP_*` 通过。

兼容矩阵只允许 `PASS`、`FAIL`、`BLOCKED`、`NOT RUN`。`PASS` 必须附版本、日志和媒体
证据。Apple Home 当前没有官方 Matter Camera 支持承诺，不能作为 Matter Camera
发布门槛；面向 Apple Home 的实时视频继续使用独立 HAP Camera Target。

Apple 官方当前的 Matter 设备类型列表和 Matter Framework 文档没有承诺 Matter Camera。
因此 Apple Home 不能作为 Matter Camera 首版发布标准；Apple Home 实时视频继续使用
独立 HAP Camera Target。

---

## 19. 开发阶段

### 当前实现进度（更新至 2026-07-29）

状态说明：`已完成` 表示代码和单元测试已落地；`进行中` 表示已有可运行骨架但尚未达到阶段验收；`未开始` 表示还没有产品实现。

| 里程碑 | 状态 | 当前结果 | 下一验收缺口 |
| --- | --- | --- | --- |
| 统一 Device 媒体扩展 | 已完成 | 新增 `camera` Device Type、Camera 模型契约和 Virtual Camera；普通 HomeKit Bridge 显式排除 Camera | 后续按真实 Provider 补 Camera 发现映射 |
| 媒体领域与 Provider 契约 | 已完成 | 已实现版本化 MediaSource/Stream/Auth/Session、严格校验、Provider 媒体可选接口 | 协议 Options 需随真实 Adapter 增加具体 tagged schema |
| Core 内嵌 Media Runtime | 进行中（三输入发布链已完成） | Core 进程内管理流、授权、健康与审计；不再有独立控制二进制或共享媒体进程。每 Stream 使用独立 Camera Kernel 子进程；Kernel 以 go2rtc v1.9.14 MIT 源码为基础，编译期只初始化 RTSP、ONVIF 输入、预授权 Xiaomi MISS、FFmpeg、HomeKit/SRTP 和受限 MP4 预览。Publisher 启动前会校验项目内 FFmpeg 存在且可执行，避免监听器健康但首次消费必然失败。每 Stream 同时输出独立的脱敏 JSON Debug 日志，覆盖 Core 生命周期、Kernel、输入协议、HomeKit/SRTP 和 FFmpeg，并以 8 MiB × 4 份轮转 | 补跨平台 Kernel 进程管理、ONVIF 局域网发现、三类输入真机矩阵和 24/72 小时稳定性回归 |
| 媒体持久化 | 已完成 | 六张媒体表、单行 `media_config_state`、事务版本递增/CAS、CRUD、AEAD、master-key fail closed、SQLite/PostgreSQL schema、备份恢复与并发测试已实现 | 主密钥轮换仍属于阶段 8 的发布加固项 |
| RTSP 最小纵切 | 进行中（代码链路完成） | Camera Provider 支持手动 RTSP endpoint/basic auth；Core Runtime 在内存组装认证 URI 并启动独立 Camera Kernel；FFmpeg H.264 转码、MP4 预览和 HomeKit 输出复用同一管线 | 补真实 RTSP 摄像头端到端和断线恢复测试 |
| ONVIF | 进行中（手动接入完成） | Camera Provider 已提供独立 ONVIF Driver、Profile 和 Digest 凭据；Core Runtime 在内存中为 Camera Kernel 生成 `onvif://` 输入 | 补 WS-Discovery、Profile 自动枚举、PTZ 和多品牌真机测试 |
| Xiaomi Camera | 进行中（真机已取流） | MIoT Cloud 密码登录捕获 `userId + passToken`，账号 Token 只留在 Core；Core Runtime 按摄像头在内存复用 X25519 公私钥并签发单次 Lease。完整 MISS URI 仅经受限子进程环境变量传给 Camera Kernel；Kernel 只接受预授权 MISS，不登录云端、不回退 Xiaomi Legacy。2026-07-29 真机复验确认 MISS 长连接建立并在补齐 FFmpeg 后恢复 H.264 输出 | 补更多 MISS/CS2/TUTK 型号、断网/签名失效自动刷新和 24 小时验证 |
| Camera + Xiaomi 控制合并 | 核心链路已完成，协议附加控制未完成 | Camera Entry 已实现 `control.providerRef + control.deviceId`；Provider Manager 将 Xiaomi 摄像头完整 Source Catalog 的非媒体属性和 Action 投影到唯一 Camera Device，并精确委托属性读写、命令和事件。所有 Xiaomi 中枢摄像头身份都由 Provider 显式标记为隐藏，即使绑定暂缺或配置中残留旧媒体字段也不生成第二张设备卡片，内部路由仍保留。Unified Camera 已升级为 v2，加入状态灯、夜视、移动侦测、九向 PTZ、速度、绝对位置、变焦倍率和记忆点位字段，旧 `pan/tilt/zoom` 布尔字段已删除。控制源离线不下线媒体，恢复、配置热更新和 Provider 重连无需重启 Camera Kernel。Provider 保存/删除已保护控制与凭据引用；管理页可选择、保留或解除暂不可用的 Xiaomi 控制源，设备详情复用统一属性/命令控件。已通过 DeviceService 集成回归验证：控制 Push 不会把媒体 `online/offline` 回退为 `unknown`，控制源离线只把对应 Capability 标记为 `control-provider-offline`，恢复后由新投影恢复可用状态。Camera/ProviderManager/Application/State 定向测试、ProviderManager race/vet、前端全量测试和生产构建已通过 | HomeKit 附加 Service、Matter Privacy/NightVision/Occupancy/PTZ 可选 Cluster 尚未实现，完成协议映射和目标 Controller 真机验证前不得标记为协议控制可用 |
| 设备中心媒体诊断 | 已完成（首版） | Camera 详情页提供登录保护的持续 fragmented MP4 实时预览、加载状态和重连；Core 只请求浏览器兼容的 H.264 视频轨道，避免 MP4/Opus 支持差异阻止整个 `<video>` 播放，并只按 Device ID 代理对应的本机 Camera Kernel 端口，不向浏览器暴露 Token、Publisher 地址或上游错误原文；Core 收到首个 `moof` 后才回写在线并向浏览器返回成功，10 秒无关键帧会取消上游 Consumer 和回写离线；前端挂载后显式执行 `load/play`，重试或关闭时取消旧流，首次播放使用 12 秒看门狗，播放后的短暂 waiting/stalled 只显示追赶直播状态而不误报首帧超时；2026-07-29 真机浏览器复验为 H.264 1280×720、`readyState=3`、播放时间持续增长且无解码错误 | 后续补结构化 Probe/错误分类、码流参数、延迟和会话统计 |
| Apple Home Camera | 进行中（HomePod 远程直播已通过；本地 TUN 路由修正版待重启复验） | 摄像头不进入普通 Bridge；`homekit-camera` Target 每次只选择一台 Camera Device，并由 Core Runtime 驱动对应 Kernel 的独立 HAP/mDNS/PIN/DeviceID/private/pairings。附件按 Scrypted 暴露 8 个 RTP Stream Management + Microphone，`c#` 已升至 10；8 个 RTP Service 现在分别保存 SetupEndpoints 应答、Consumer、StreamingStatus 和生命周期，不再共享全局会话指针；原生 H.264 优先、FFmpeg 兼容回退。已移植 go2rtc 的 IPv6 link-local 与 H.264 FU-A 修复，并进一步按 Scrypted/Homebridge 对齐完整直播会话：SetupEndpoints 回显 Controller SRTP 材料；每个 Session 为视频、音频分别创建同地址族的独立 UDP Socket；普通 LAN/HomePod 路径绑定 SetupEndpoints 通告的具体本机地址，当 Controller 地址命中 Publisher 主机自身的 VPN/TUN 接口时改用逐 Session 通配 Socket，让内核选择 Controller-facing 源接口；视频 MTU 上限 1200、UDP 缓冲 1 MiB；Selected Stream `START` 立即完成 HAP 应答，RTCP Return Path 检查、上游连接和 FFmpeg 启动转到异步流程；首个 RTP 前发送 RTCP Sender Report；媒体 Consumer 不再拥有或主动关闭 HAP 控制连接，HAP 连接关闭或 RTCP 30 秒空闲只回收对应媒体资源，同一 Stream Slot 的活动会话才返回 Busy，其他 Available Slot 可并行准备。每次 START 都按 Controller 选择的宽、高、帧率、最大码率、H.264 Profile/Level 和 Opus 采样率/包时长/码率创建独立 FFmpeg 管线；视频 VBV Buffer 为目标码率的 2 倍，关键帧间隔不超过 4 秒，SRTP 鉴权标签计入协商 MTU，首个 Sender Report 使用首个 RTP 包的时间戳。Opus 能力按 Scrypted/HAP-NodeJS 的实际线格式合并为单一 Codec Parameter Block，其中携带 8/16/24 kHz 组合；`SetupEndpoints` 和 `SelectedRTPStreamConfiguration` 使用显式 Error/Suspend 初值，保证其 `value` 出现在 `/accessories`。HAP PUT 已区分 Characteristic Value Write 与 `ev` 事件订阅，StreamingStatus 状态变化会通过加密 HAP EVENT 推送，连接结束时清理订阅。私有诊断保留最近一次会话的协商媒体参数、Setup/Start/RTP/RTCP/写错误、SPS/PPS/IDR/STAP-A 计数，并解析 Apple Receiver Report 的 SSRC、最高序号、丢包、抖动及 PLI/FIR/NACK，不含地址、Session ID 或密钥。停用 Target 仅停止发布，删除 Target 清除独立 HAP 身份 | 重启本地 TUN 路由修正版并分别完成 HomePod 远程、本机 Home 和独立 iPhone 局域网连续直播复验；补 identity/pairings 加密存储、HKSV、事件/门铃、断网恢复和 24 小时回归 |
| Camera Provider 产品边界 | 进行中（限定三种 Driver） | 新增独立 `camera` Provider；Provider 首次保存并运行后启用 Core 内嵌媒体 Runtime。摄像头连接详情通过独立“管理摄像头”页面维护 RTSP、ONVIF、Xiaomi MISS 子设备；后端严格拒绝第四种 Driver | 补 ONVIF 局域网发现，以及账号停用/删除的依赖提示和跨账号重新绑定 UI |
| Camera Targets 子分页 | 进行中（HomeKit 配对纵切已完成） | 桥接中心已拆分普通设备、HomeKit 摄像头、Matter 摄像头、其他摄像头四个子分页；HomeKit 页可从 Camera Provider 设备中选择一台摄像头创建独立 `homekit-camera` Target，展示来源 Provider、设备在线状态、实际 HAP 地址、手动配对码和配对状态，且可直接打开设备中心实时预览；同时提供停用、删除及清除独立身份；后端校验一 Target 一 Camera，Target Manager 驱动 Runtime 发布/回收；前后端边界、生命周期单测及 Xiaomi 真机配对和截图已验证，实时流兼容修正版待重启复验；Matter 页已开放带实验性警告的创建、配置、二维码/状态、停用和删除入口 | 补 Source/Transcode/Publisher 分段健康状态和码流/音频策略；Matter 页在 MC-5/MC-6 通过前不得移除实验性警告，其他摄像头分页仍只展示能力说明 |
| Matter Camera | 功能实现完成，实验性验证阶段 | 独立 Target/Node、Camera 0x0142、AV Stream 资源、JPEG Snapshot、Pion H.264/Opus WebRTC、Requestor Answer/ICE 回调、会话/资源清理和实验性 UI 均已接通并通过自动化测试；创建入口可用于 Controller 联调 | 本机缺少 `chip-camera-controller`，commissioning、五分钟 LiveView、真机 Snapshot、一致性用例、故障注入和 24 小时回归仍为 `NOT RUN`；完成对应矩阵前不能对目标平台标记正式可用 |

本轮以“Xiaomi Provider Camera → MediaSource/Stream 持久化 → Core 内嵌授权服务 → 独立
Camera Kernel → Apple Home Camera”链路打通最小纵切。正式控制链路现已迁移为“Camera Provider →
Device Center → Camera Target”，不保留发现即发布行为。`chuangmi.camera.079ac1` 真机已经完成授权、
CS2 局域网连接、关键帧读取，并曾完成旧纵切的 Apple Home 配对验证；该配对现已移除，
只有用户在 HomeKit 摄像头子分页创建并启用 Target 时才会再次发布。当前关键帧实测为
`video/mp4; codecs="hvc1.1.6.L153.B0"`。当前已按 go2rtc 推荐的多 Source 方式增加
`ffmpeg:<stream>#video=h264`，并验证输出为持续的
`video/mp4; codecs="avc1.640029,opus"`。macOS 上不启用该型号会失败的 VideoToolbox
HEVC 硬件解码；后续又复现 VideoToolbox H.264 创建压缩会话失败 `-12908`，现统一采用
软件 HEVC 解码 + 低延迟 `libx264` H.264 编码，优先保证 Runtime、设备中心和 HomeKit
共享管线稳定。2026-07-29 针对“Apple Home 仅周期刷新预览图”的进一步诊断确认
Controller 已完成 SetupEndpoints/SelectedRTP 且 Camera Kernel 持续发送 SRTP；先前尝试
把共享输出强制为 Baseline/3.1、299 Kbps 并增加第二个 RTP Service，既没有恢复直播，
又让画质明显低于原版 go2rtc。复核 go2rtc Issue/PR 后确认：iOS 26 的已发布修复只要求
Microphone Service；当前上游经典 HomeKit 路径仍使用单 RTP Service、Main 3.1/4.0。
因此本轮恢复上游附件数据库和 Source 顺序，优先原生 H.264，以 CRF 回退替代全局
299 Kbps 限流；同时移植 PR #2305 的 IPv6 link-local SRTP zone 修复和 PR #2380 的
FU-A 中途加入同步修复，保留 Opus 48kHz RTP clock、标准 SetupEndpoints 两步交换、
NTP RTCP Sender Report、协商 MTU 和每 IDR 重复 SPS/PPS。随后继续对照 Scrypted
`camera-streaming.ts`、`camera-streaming-ffmpeg.ts` 和 Homebridge Camera FFmpeg：
两者都会在 SetupEndpoints 应答中回显 Controller 提供的 SRTP key/salt，并使用该材料
加密发往 Controller 的媒体，而不是为应答另生成一套 Accessory key/salt；Scrypted 还把
视频 MTU 收敛到 1200、为 UDP Socket 配置 1 MiB 读写缓冲区。Camera Kernel 已同步这些
线上实现中更充分验证过的语义。首次真机复验表现为打开直播不再持续转圈，但仍可能提示
“摄像头不可用”，因此继续按 Scrypted 的完整生命周期修正：视频、音频改用逐 Session
独立 UDP Socket；Selected Stream `START` 不再同步等待 Stream/FFmpeg 启动，而是先完成
HAP 应答，再在后台等待最多一秒的初始 RTCP 并启动媒体；首个 RTP 前立即发送 RTCP
Sender Report；媒体 Consumer 与 HAP Transport 解耦，结束媒体不会抢先关闭控制连接；
控制连接关闭、Controller 30 秒不再发送 RTCP 时主动回收会话，避免
`StreamingStatus=InUse` 永久残留；新的 SetupEndpoints 只对真实活动会话返回 Busy。
Opus 能力同步为 8/16/24 kHz，RTP timestamp 使用 Controller 选择的采样率，40/60 ms
多帧包按 RFC 6716 只编码前 `M-1` 个帧长度。真机抓取进一步确认：Apple Home 打开实时
页面期间 HAP TCP 已连接，但没有创建动态 UDP Socket，Setup/START/RTP/RTCP 均为零。
这把问题定位在媒体能力协商之前。2026-07-29 的安全阶段日志进一步确认 Apple 已成功
完成 Pair Verify、读取 `/accessories`，并持续请求 Snapshot，但从未写入 SetupEndpoints
或 Selected Stream。复查 Scrypted 与 HAP-NodeJS 的实际序列化后确认，Scrypted 虽配置
六组 Opus 采样率/码率选项，但 HAP-NodeJS 的兼容层会按 Codec Type 合并为一个 Parameter
Block，并保留重复的 8/16/24 kHz 项。另一个兼容性问题是
`SetupEndpoints` 和 `SelectedRTPStreamConfiguration` 的空字符串会被
`hap.Character` 的 `json:",omitempty"` 从 `/accessories` 完全省略；HAP-NodeJS 则分别
初始化为 Error 和 Suspend TLV。现已恢复 HAP-NodeJS 的合并 Opus 线格式、加入显式 RTP 控制初值，并补齐
Main Level 3.2、960×540、640×360 等选择和严格布尔类型的 Microphone Mute。
首次 `c#=9` 真机复验确认新数据库被读取，但 Apple 仍未进入 SetupEndpoints。继续对照后
发现 Scrypted 明确发布 8 个 RTP Stream Management，而 HAP-NodeJS 注明非 HKSV 摄像头
规范最低需要 2 个；HomeLoom 此前只有 1 个。同时旧 HAP PUT 解析器忽略 `ev` 字段，把
事件订阅当成空 Value Write，StreamingStatus 也只在内存改值而不发送 HAP EVENT。现已按
Scrypted 提供 8 个独立 RTP 管理服务，完整实现订阅、退订、断开清理和状态事件推送，并将
Accessory `c#` 升至 10。Camera Kernel 同时增加仅在私有 API 白名单开放的
`/api/homekit/session`：只返回 `prepared/answered/started/streaming`、协商宽高/帧率/码率、
音视频 RTP 包数/字节数/写错误及 RTCP Datagram/成功/解密失败计数；活动会话结束后仍保留最近一次
计数用于排查，但不返回地址、Session ID、key 或 salt。直播 START 的媒体生产也已由
固定 1280×720 共享回退流改为逐 Session 管线：保留 Camera Provider 主流的预连接，
每次再以本机 RTSP 读取该主流，并把 Controller 选择的 `width/height/framerate/max_bitrate`
传给独立 FFmpeg H.264/Opus 输出。这样协商 1920×1080、1280×720 或 320×240 时，
实际 H.264 SPS、帧率和码率约束与 HAP 应答一致；STOP、HAP 断开或 RTCP 超时会同时
释放该次转码进程。`c#=10` 真机日志已经进一步确认控制面和网络面完全闭环：Apple
成功完成 SetupEndpoints → Selected Stream START，初始 RTCP Return Path 验证成功；
一次会话中 Camera Kernel 向 Controller 发送 9721 个视频 RTP 包、约 1.37 MiB，写错误为
0，并成功解密 60 个 Controller RTCP 包，最后由 Apple 主动发送 END。因此剩余问题不再是
配对、Characteristic、端口、防火墙或 SRTP 密钥，而是 Controller 收到媒体后没有接受
编码参数。该次日志同时暴露出实际 FFmpeg 仍固定为 Main/4.0、Opus 16 kHz，而 Apple 选择了
24 kHz Opus；299 Kbps 的 VBV Buffer 也只有 299 Kbits，持续产生 underflow。

现已按 Controller 选择值逐 Session 传递 H.264 Profile/Level、Opus 8/16/24 kHz、Packet
Time 和音频码率；视频 VBV Buffer 改为目标码率的 2 倍，并采用 Scrypted 的不超过 4 秒
GOP。会话诊断同时记录实际发出的 SPS/PPS/IDR 数量和最大加密 Datagram 大小，下一次
真机复验可以直接区分“有 RTP”与“有完整可解码关键帧”。随后真机日志确认一次 30 秒会话
已发出 8 组 SPS、8 组 PPS 和 80 个 IDR Slice，且最大加密 Datagram 为 1200 字节，但
Apple 仍不呈现画面。继续逐字节对照 Scrypted 的 HomeKit H.264 Repacketizer 后发现：
通用 go2rtc Payloader 按 RFC 6184 把 SPS/PPS STAP-A 的 NRI 设为聚合 NAL 的最大值，
头字节为 `0x78`；Scrypted 明确记录 Apple Home 不接受这种 NRI 聚合，并在 HomeKit 路径
强制使用 `F=0/NRI=0` 的 `0x18`。现仅在 HomeKit 输出路径清除此 STAP-A 上三位，不改变
RTSP、预览、Matter 或通用 H.264 包化行为。HomeKit 的 MTU 表示加密 UDP Datagram 上限，因此 H.264 Payloader 会预留
AES_CM_128_HMAC_SHA1_80 的 10 字节鉴权标签。首个 RTCP Sender Report 仍先于首个 RTP
发送，但 RTP Timestamp 已预置为首包时间戳，避免用 0 建立错误的 NTP/RTP 时钟映射。
替换修正版后，最新真机日志确认 `0x18` STAP-A 已生效，但 Apple 的表现没有改变：每次会话
仍发出约 9000 个视频包、8 组 SPS/PPS、80 个 IDR Slice，并收到约 60 个可成功解密的
Controller RTCP Datagram。继续对照 Scrypted 当前实现发现一个尚未对齐的网络细节：
Scrypted 将每个视频和音频 UDP Socket 绑定到 SetupEndpoints 的 `sourceAddress`，HomeLoom
此前仍绑定 `0.0.0.0`。在本次 Controller 地址为 `198.19.0.1` 的 HomeHub/VPN 路由中，
通配绑定可以收到 RTCP，但内核为出站 RTP 选择的源地址仍可能与 SetupEndpoints 通告地址
不同，Apple 会静默丢弃这种媒体。现已把逐 Session Socket 改为具体通告地址绑定；IPv6
link-local 绑定会保留 HAP TCP 接口 Zone，而线上的 SetupEndpoints 地址仍按规范省略 Zone。
同时 SRTCP 不再只统计“解密成功”，还会解析 Compound RTCP：日志和私有状态接口记录本地
Video SSRC、Apple Report Block 指向的 SSRC、匹配报告数、Fraction/Cumulative Lost、
Extended Highest Sequence、Jitter、Last SR 及 PLI/FIR/NACK。STAP-A 总数与 `NRI=0`
数量也会单独输出。这样下一次真机复验可以直接确认 Apple 是否接受了实际 RTP 包，而不是
仅凭周期 RTCP 推断。生成参数已由项目内 FFmpeg 实际执行验证；相关 HomeKit/SRTP 单元测试、
race 和 vet 已通过，待替换 Camera Kernel 后完成 Apple Home 真机连续直播复验。

2026-07-30 重启后的新日志进一步暴露出独立的会话管理错误。首个 Stream Slot 已完成
Setup/START 并持续发送媒体，但 Apple Receiver Report 的 `matched_reports` 持续增长时，
`last_sequence=0`、`last_sender_report=0`，随后 Apple 选择另一个显示为 Available 的
RTP Stream Management Service 重试。Camera Kernel 虽然发布了 8 个 Service，内部却仍用
单一全局 Consumer，因此错误返回 Busy；更严重的是紧接着的 SetupEndpoints GET 又从旧
Consumer 重新生成 Success，应答中带回旧会话端口和密钥。Apple 随后用新 Session ID 发送
START，HomeLoom 将其误报为媒体参数无效并停止旧流。现已将会话表改为以 RTP Service IID
为键：每个 Slot 独立持有 Consumer 和 SetupEndpoints 线值；不同 Available Slot 可以并行
准备/直播，同一 Slot Busy 后的 GET 保留该次 Busy，新 Session ID 的错误 START 不会影响
其他会话，END、HAP 断开和媒体结束也只释放所属 Slot。结构化日志新增 `stream_slot`，
用于把 Setup、START、RTCP 和结束统计关联到同一 Service。双 Slot、Busy GET、Session ID
隔离和单 Slot 清理不影响相邻 StreamingStatus 的回归测试均已落地。

同日后续真机日志已把媒体兼容与本地网络路由分离。HomePod 路径的 Controller 为
`192.168.101.114`，Apple Receiver Report 的最高接收序号持续增长到 `15017`，同时出现
PLI/FIR，用户已确认经 HomePod 远程可以查看实时画面；这证明 HAP、SRTP、H.264、动态
FFmpeg 和多 Stream Slot 主链路已经闭环。本机 Home 路径则通过
`192.168.101.197 → 192.168.101.197` 的 HAP TCP 连接发起，但 SetupEndpoints 把 Controller
媒体地址报告为 `198.19.0.1`；该地址同时属于 Publisher 主机的 `utun8`。旧实现把媒体 Socket
强制绑定到通告的物理地址 `192.168.101.197`，虽然能够收到和解密 Controller RTCP，也能
持续无错误写出 RTP，但 Apple 的 Receiver Report 始终保持 `last_sequence=0`。这说明
发往本机 TUN 目的地址的 RTP 被固定源接口送入了错误路径，而非编码或密钥回退。

修正版在每次 SetupEndpoints 时检查 Controller 地址是否属于本机任一网络接口：普通
HomePod、独立 iPhone 和真实远端地址继续绑定通告的物理接口；仅当 Controller 地址命中
本机 VPN/TUN/loopback 时，为该 Session 创建显式通配的独立视频/音频 UDP Socket，让操作
系统选择 Controller-facing 源接口，同时 SetupEndpoints 仍向 Apple 通告可达的物理 LAN
地址。日志新增 `media_bind=advertised-address|wildcard-local-controller`、
`initial_rtcp_wait_ms` 和 `rtcp_before_media`，最近会话状态也保留 `mediaBindMode`。鉴于
当前 FFmpeg 首帧启动约需 2.4 秒，而 Controller RTCP 通常在约 0.5 秒到达，媒体启动前的
串行 RTCP 等待由最多 1 秒缩短为 250 ms，既给 UDP Return Path 留出准备窗口，也减少本来
叠加在转码启动前的固定延迟。接口地址匹配、通配 Socket 不继承具体基础地址、本地
Controller 选择策略以及既有 HomeKit/SRTP 回归测试均已落地；等待用户重启新 Kernel 后，
以 Receiver Report 的 `last_sequence > 0` 作为本机直连直播通过条件。

2026-07-30 关闭 VPN 后的复核进一步收窄了问题：日志中的
`192.168.101.197:52454 → 192.168.101.197:60272 broken pipe` 属于只读取附件和截图的
HAP 短连接；直播 Setup/START 使用另一条连接，因此该错误不是直播失败根因。两次本地
直播的 Receiver Report 已分别增长到 `last_sequence=13467/18479`，匹配 SSRC、零丢包且
RTP 写入无错，证明 LAN SRTP 往返正常；但 Controller 分别发送了 67/101 次 FIR，而四秒
GOP 只产生约 5/9 个关键帧。修正版只在 Controller 是本机另一个 TUN/loopback 地址时使用
通配媒体 Socket；Controller 与通告地址相同（本次均为 `192.168.101.197`）时恢复精确 LAN
绑定，稳定 SRTP 源地址与端口元组。HomeKit 动态 FFmpeg 会话改为固定一秒 IDR 周期，并
显式设置相同的 `g/keyint_min` 与关闭场景切换，缩短 Apple 解码器在 FIR 后取得完整
SPS/PPS+IDR 的等待。`EOF/EPIPE/ECONNRESET` 等由 Controller 主动结束的 HAP 连接降为
Debug 的 `peer closed`，真实解密、协议和处理失败仍保留 Warn。

为便于继续真机定位，Camera Publisher 已启用逐 Stream 文件诊断：
`<media.runtime_dir>/<stream_id>/camera.log`。该文件同时接收 Core 的启动、监听、预热、
停止和退出事件，以及 Camera Kernel 的 RTSP/ONVIF/Xiaomi MISS、Stream、HomeKit/SRTP
和 FFmpeg 日志；Kernel 以 JSON Debug 级别输出，FFmpeg 自身限制为 Info，避免 Trace
级包数据长期淹没关键会话事件。单文件上限 8 MiB，保留 `camera.log.1` 至
`camera.log.4` 四份历史，目录保持 `0700`、所有日志保持 `0600`。Camera Kernel 在写出前
对环境传入的完整源地址、账号 Token、HomeKit PIN 和附件私钥统一替换为 `***`；Core
不会记录子进程环境变量。日志卷故障只停止后续日志写入，不会中断正在运行的视频流。
首次日志真机复核发现 FFmpeg 每半秒输出的编码进度占据绝大多数记录，而 HomeKit 的附件
读取和 Characteristic 访问仍停留在包含原始 TLV 的 Trace 日志，无法安全地直接打开。
现已关闭 FFmpeg 周期进度但保留启动信息、警告和错误，并增加不含 Characteristic Value、
Session ID、SRTP key/salt 的结构化 HAP 阶段事件：Pair Setup/Verify、加密连接关闭原因、
附件数据库读取、Characteristic 名称和读写方向、SetupEndpoints、Selected Stream 命令、
Snapshot、RTCP 检查及 RTP 统计。日志同时暴露并修复了 preview-only 启动仍加载磁盘中
HomeKit 配置的问题：Camera Kernel 当前会初始化所有编译进二进制的模块，`app.modules`
不能单独阻止顶层 `homekit:` 配置被加载。因此 preview-only 配置会先把旧 YAML pairing
迁入 `0600` sidecar，再彻底移除运行时 `homekit:`、HAP 路由和 mDNS；再次启用 Target
时从 sidecar 恢复 pairing 和身份，避免未注入 PIN 的占位符错误及一次无效 HAP 启动。

独立 Camera Provider 迁移已开始落地：Provider Manager 只接受
`Manifest.Type=camera` 的媒体源，MIoT Cloud 不再成为媒体 owner；旧纵切生成的
`{"publisher":"apple-home","independent":true}` Stream 会迁移为
`{"publisher":"none"}`。Core 内嵌 Runtime 只有在 Stream 明确指定
`publisher=apple-home` 时才写入 HomeKit 配置并激活配对路由和身份环境变量；默认 Stream
仅服务设备中心预览。首次运行态验证暴露了“复用完整 CloudProvider 会启动第二套设备
轮询”的错误边界，现已改为只复用无轮询的 MISS 授权客户端。最终修正版已完成真机
重启复验：Core Runtime 对瞬态 `stream update rejected` 有限重试，并重新启动受影响的
Camera Kernel；go2rtc 预览在 20 秒内持续返回约 29 KB MP4 数据。

2026-07-29 使用设备中心真机排查发现一次确定性的部署缺件：Camera Kernel 已与
`chuangmi.camera.079ac1` 建立 MISS 长连接，但项目运行目录缺少 `backend/bin/ffmpeg`，
因此每次创建 H.264 Consumer 都返回 `fork/exec .../ffmpeg: no such file or directory`。
补齐固定版本、校验和保护的 FFmpeg 并重建独立 Camera Target 后，设备中心实时预览恢复为
1280×720 连续播放，设备状态由离线回写为在线；Runtime 现增加 Publisher 启动前 FFmpeg
可执行文件校验，缺件会在应用 Stream 时明确失败，不再留下“Provider 已连接、媒体必然失败”
的假健康状态。排查时 HomeKit 摄像头分页为空，确认原 Target 已删除；已重新创建独立
`homekit-camera` Target，后续验收从重新配对及 Apple Home 实时 SRTP 播放继续。

本轮同时修复了历史映射占用：启动时会物理删除已不存在 Provider 或已从权威
`devices`/`cameras` 配置移除的 Provider/Consumer Binding，Provider 保存和删除时也会
即时执行同范围清理，并同步释放内存唯一性索引。`devices: null`/`cameras: null` 不视为
权威空列表，避免异常配置误删。Matter Endpoint identity tombstone 不属于普通映射占用，
继续保留到 Factory Reset；当前数据库实测清理 9 条孤儿 Binding，并保留 2 条 Matter
tombstone。

### 19.1 Xiaomi Token 复用边界

- 现有 Xiaomi Home OAuth `accessToken/refreshToken` 不能直接作为摄像头内核 Token；
- MIoT 的 `serviceToken + ssecurity` 不能直接替代 Camera Provider 获取 MISS
  签名所需的完整账号会话；
  `passToken`；
- 可以复用同一次小米账号密码登录：登录结果同时保存 `userId`、MIoT 会话字段和
  `passToken`，之后 Core 从加密 Provider 配置签发短期、单次媒体 Lease；
- `passToken` 只在 Core 解密内存中使用，不进入 Camera Kernel、Stream、URI、argv、日志或
  YAML；Runtime 只向 Kernel 提供本次 MISS 握手需要的签名与设备公钥；
- 若已有 Provider 只有 `serviceToken`，需要重新执行一次账号密码登录以补获
  `passToken`。

### 19.2 当前 Xiaomi → Apple Home 使用步骤

1. 执行 `scripts/build.sh <version>`；脚本构建 Core 与 `homeloom-camera-kernel`，并下载带固定
   SHA-256 校验的项目内 FFmpeg。下载缓存位于项目 `.cache/ffmpeg-static`。
2. Camera 媒体能力由 Core 内嵌 Runtime 提供；发布包仅需 Core、`homeloom-camera-kernel` 和 FFmpeg。
   `hap_host` 必须可被 Apple 家庭中枢或 iPhone 从局域网访问，不能设为 loopback。
3. 在 Xiaomi MIoT Cloud Provider 中用账号密码重新登录并保存，确认会话包含
   `passToken`。该字段在管理 API 和页面中只显示为脱敏值。
4. 当前纵切版本在 MIoT Cloud 的“管理设备”中选择 Camera，并自动生成
   `protocol=xiaomi-miss`、`subtype=hd` 和媒体 Profile。目标架构会把这一步迁移到
   “设备来源 → 摄像头 → 从 Xiaomi 目录导入”，MIoT Provider 本身不再发布 Camera。
5. 当前纵切版本会自动创建独立 Apple Home Stream；目标架构会迁移为
   “桥接中心 → HomeKit 摄像头 → 新建发布”。迁移完成前不要把自动发布行为作为正式
   产品契约。
6. 可在“设备中心 → 摄像头详情”打开实时预览排查源流。若设备中心有画面但 Apple Home
   黑屏，优先检查源编码：HAP 仅协商 H.264，HEVC/H.265 源必须先转码。

运行网络要求：Core 能访问小米账号服务；小米云授权只在 Core 完成，Camera Kernel
只访问摄像头局域网 IP；mDNS、对应 HAP 端口段和 SRTP 端口段不能被主机防火墙阻断。摄像头换成
RTSP/ONVIF 输入时应复用同一 Stream/Accessory 身份，不应把 Camera 合入普通设备桥。

### 阶段 0：架构契约与上游基线

任务：

- 固定 Camera Kernel 所引用的 go2rtc MIT 上游版本；
- 建立 HomeLoom Camera Kernel 源码、编译期能力白名单和许可证清单；
- 建立自动同步上游流程；
- 明确 Camera 是现有 Device 的媒体扩展；
- 定义版本化 MediaSource、Stream、Authorization、Status、Storage 契约；
- 明确 Core 内嵌 Runtime 与每流 Kernel 的最小材料边界；
- 建立协议能力表和真机清单。

验收：

- HomeLoom Core 与 Camera Kernel 可独立编译；
- 契约包有版本号；
- Runtime 生命周期、generation/revision 和 KernelInstanceID 可由单元测试验证；
- RTSP 测试流可由 HomeLoom Camera Kernel 正常播放；
- 构建产物不再下载或依赖外部通用 go2rtc 二进制。

### 阶段 1：RTSP 最小纵切与内嵌 Runtime

任务：

- Runtime 生命周期、Kernel 实例身份和健康状态；
- generation/revision 驱动的全量配置恢复；
- Credential Broker 通用接口；
- 日志脱敏；
- 手动创建 RTSP Camera Device 和 MediaSource；
- RTSP 静态凭据加密存储；
- RTSP 拉流、受限 MP4 预览和 HomeKit 输出；
- Kernel supervisor 和退出清理。

验收：

- HomeLoom 可以动态创建和删除 RTSP 流；
- 浏览器可通过受限 fragmented MP4 查看；
- Core 重启后按期望状态自动恢复流；
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
- 摄像头、Kernel 或 Core 重启后自动恢复。

### 阶段 3：独立 Camera Provider 与 Xiaomi 优先适配

任务：

- 实现独立 Camera Provider 和带版本的 Camera Entry；
- 引用当前 Xiaomi Provider 的账号、OAuth、设备目录和凭据维护，不复制 Token；
- 支持从 Xiaomi 目录导入 Camera Entry，Camera Device 归属 Camera Provider；
- Camera Device 与 MediaSource 映射；
- MISS `client_public` 授权流程；
- MISS 当前实现所需的本地传输；
- 音频读取；
- 授权失败刷新；
- 完整 source URI 只存在 Runtime/子进程环境，不落盘、不进入日志。

验收：

- 至少两种现代 MISS 型号；
- 至少两种 MISS 真机；
- 断线后自动重新授权和连接；
- Core 不持有 Camera Kernel 临时私钥；
- Camera Kernel 不持有小米长期账号 Token。

### 阶段 4：Apple Home 摄像头输出

任务：

- 稳定 Camera Accessory 身份；
- Core 加密身份与配对存储；
- H.264/Opus 能力检测；
- 必要时 FFmpeg 转码；
- 快照、实时视频、双向语音；
- 运动和门铃事件映射。

验收：

- HomeLoom 或 Camera Kernel 重启后无需重新配对；
- 备份恢复后保持配对和 Accessory 身份；
- 切换底层源不改变 Apple Home 摄像头身份；
- 多客户端观看时复用上游连接。

### 阶段 5：稳定性封版

任务：

- RTSP、ONVIF、Xiaomi MISS 三类输入的断线、超时和进程退出回归；
- FFmpeg 进程数量、资源上限和僵尸进程检查；
- HomeKit 多客户端、重配对、重启恢复和网络切换测试；
- 24/72 小时内存、句柄、端口和 CPU 稳定性测试；
- 证明构建依赖图不存在第四种输入协议和通用媒体服务器模块。

验收：

- 单台摄像头故障不会结束其他摄像头、普通 HomeKit Bridge 或 Core；
- Core 重启后只恢复当前 Camera Provider 的有效 Stream；
- 配置取消映射后子进程、端口和运行记录均释放；
- 依赖图白名单、日志 secret canary 和真机稳定性矩阵通过。

### 阶段 8：稳定性、安全与发布

任务：

- 72 小时持续运行；
- 多摄像头并发；
- 内存和 goroutine 泄漏检测；
- 凭据轮换；
- Kernel 启动材料与文件权限测试；
- Camera Kernel 沙盒/低权限运行；
- 安装、升级和回滚。

验收：

- 单摄像头故障不影响其他流；
- Camera Kernel 崩溃不影响 HomeLoom 普通设备或其他流；
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

Core Media Service 与内嵌 Runtime 使用同一组测试向量：

- Authorization 请求/响应；
- KernelInstanceID 与 Lease 绑定；
- Runtime 健康状态与退出清理；
- Stream generation/revision 与 operation；
- SessionReport；
- Storage 命名空间绑定；
- 版本不兼容、未知字段、超大负载和背压处理。

### 20.3 集成测试

使用模拟 Provider 和虚拟摄像头：

- RTSP test server；
- ONVIF mock；
- Xiaomi cloud mock；
- Tapo nonce mock；
- WebRTC signaling mock；
- HomeLoom/Camera Kernel 重启和网络中断；
- Revision 重复、缺号、乱序和旧 Generation；
- Lease 并发 claim、重放和超时；
- SQLite/PostgreSQL 备份、恢复及回滚；
- 错误主密钥和主密钥丢失；
- 日志、trace、审计和诊断包 secret canary；
- Camera Kernel 卡死、FFmpeg 崩溃与孤儿进程清理；
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
- Camera Kernel 重启；
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
homeloom_media_runtime_kernel_exit_total
homeloom_ffmpeg_process_total
homeloom_stream_bitrate_bytes
```

标签控制在低基数：

```text
provider
protocol
result
kernel
```

不要用完整 Device ID 作为全局指标标签，可在日志和状态接口中查询。

### 状态接口

```text
GET /api/v1/devices?type=camera
GET /api/v1/devices/{id}
GET /api/v1/devices/{id}/media
GET /api/v1/devices/{id}/streams
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
- HomeLoom 专属 Runtime 编排不提交到协议核心；
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

1. HomeLoom 内嵌 Media Runtime 运行；
2. Runtime 可按期望状态恢复流并监督 Kernel；
3. RTSP 可手动接入；
4. RTSP 输入、受限 MP4 预览和 HomeKit 输出可用；
5. 长期凭据只在 HomeLoom 加密持久化；
6. Camera Kernel 退出后自动恢复受影响的流；
7. HomeLoom 重启不终止已建立媒体流；
8. 单摄像头错误不影响普通设备桥；
9. Generation/Revision、Lease 和日志脱敏契约测试通过。

### 23.2 首个可发布版本

首个发布版本在 RTSP 纵切基础上交付 ONVIF、Xiaomi 和 Apple Home Camera：

1. HomeLoom 内嵌 Media Runtime 运行；
2. 通用 Credential Broker 可工作；
3. RTSP 可手动接入；
4. ONVIF 可发现并生成 RTSP 流；
5. Xiaomi MISS 至少两种型号可用；
6. 设备中心受限 fragmented MP4 预览可用；
7. Apple Home 可查看实时视频；
8. HomeLoom 重启不终止已建立媒体流；
9. Camera Kernel 退出后自动恢复受影响的流；
10. 长期凭据只保存在 HomeLoom；
11. Camera Kernel 临时私钥和会话密钥不持久化；
12. 单摄像头错误不影响普通设备桥；
13. 24 小时持续运行无明显内存增长；
14. 构建和运行时只能启用 RTSP、ONVIF、Xiaomi MISS 三类输入；
15. 备份恢复可保留 MediaSource、凭据、流配置和 HomeKit 输出身份；
16. SQLite/PostgreSQL、Kernel 启动材料权限、Lease 并发和 secret canary 测试通过。

---

## 24. 推荐实际开发顺序

```text
第 1 步：冻结 Device 媒体扩展、MediaSource、Stream、Auth、Storage 契约
第 2 步：固定 go2rtc fork/commit，建立 Core Runtime/Kernel golden vectors
第 3 步：实现内嵌 Runtime、期望状态恢复和 Kernel supervisor
第 4 步：完成 RTSP 静态凭据、拉流和受限 MP4 预览的最小纵切
第 5 步：补齐 Revision、Lease、备份恢复、日志脱敏和故障注入测试
第 6 步：接入 ONVIF 发现、Profile 和 PTZ
第 7 步：实现独立 Camera Provider、Camera Entry tagged schema 与账号 Provider 引用
第 8 步：把 Xiaomi Camera 从 MIoT Cloud 设备发布迁移为 Camera Provider 目录导入
第 9 步：实现 Target CameraPublication，移除“发现即自动发布”
第 10 步：实现 HomeKit 摄像头子分页、独立 Target 及已有配对身份无损迁移
第 11 步：实现预授权 Xiaomi MISS 并完成真机回归
第 12 步：冻结 Camera Kernel 依赖图，删除通用 Web UI 和未使用运行入口
第 13 步：完成三类输入、FFmpeg 和 HomeKit 输出的 24/72 小时稳定性验证
第 14 步：完善安全、安装、升级和回滚
第 15 步：完成 Matter Camera MC-0/MC-1 规范、SDK 能力与会话控制契约
第 16 步：实现独立 matter-camera Target/Node 和 Camera Endpoint
第 17 步：实现 AV Stream 分配、JPEG Snapshot 和受限 WebRTC 媒体面
第 18 步：通过 chip-camera-controller、Camera 一致性测试和 24 小时回归
第 19 步：建立 Controller 兼容矩阵；Apple Home Matter Camera 仅在实测支持后开放
```

前五步构成首个可演示版本，前九步构成首个 HAP Camera 可发布版本。Matter Camera
在第 18 步完成后才进入实验发布，在第 19 步完成目标 Controller 验证后才对对应平台
标记可用。

---

## 25. 最终交付物

### HomeLoom Core

- 现有 Device Registry 的 Camera 类型与媒体扩展；
- 现有 Provider SDK 的媒体可选接口；
- 扩展现有 AEAD Credential Store；
- 通用 Credential Broker；
- 内嵌 Media Runtime 与 Kernel 生命周期管理；
- 动态流配置；
- 摄像头状态和审计 API；
- 媒体 Runtime 反向身份存储；
- Apple Home 普通设备与摄像头配置管理。

### Core 内嵌 Media Runtime

- 直接调用 HomeLoom 领域服务；
- generation/revision 驱动的 Stream 生命周期；
- 通用 Authorization Service；
- RTSP 媒体入口（ONVIF 发现和控制保留在 Core Provider）；
- RTSP、ONVIF、Xiaomi MISS Adapter；
- FFmpeg 转码、受限 MP4 预览和 HomeKit Camera 输出；
- Kernel 状态收集和日志脱敏。

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
├── Xiaomi 账号目录与授权
├── 长期身份与凭据
├── 独立 Camera Provider 与设备发现/控制
├── Xiaomi MISS 云端预授权
├── Target Publication、策略、审计和配置
└── 内嵌 Runtime 与 Kernel 生命周期

Core 内嵌 Media Runtime
├── 临时授权、Kernel 启动和生命周期监督
├── RTSP、ONVIF 与 Xiaomi MISS 的流编排
└── 标准媒体输出的健康、审计与访问控制

Camera Kernel（每流子进程）
├── 摄像头网络连接、媒体解密和封装
├── 外部 FFmpeg 转码和双向语音
└── HomeKit/SRTP 与受限 MP4 数据面
```

小米摄像头是首个重点验证对象，但 Xiaomi MIoT Cloud 不是摄像头模块。同一 Camera
Provider 当前只允许 RTSP、ONVIF 和 Xiaomi MISS 三种 Driver；Camera Kernel 只提供
这三种输入、FFmpeg 转码、受限 MP4 预览以及 HomeKit Camera 输出。任何第四种协议都
不属于当前实现范围，不能通过配置动态启用。任何对外发布仍必须经过 Camera Target，
不得在发现阶段隐式发布。
