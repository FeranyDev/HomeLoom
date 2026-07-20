# Xiaomi Providers

HomeLoom 将三种小米接入方式视为不同 Provider 类型，不共享名称或运行连接：

| Provider 类型 | 页面名称 | 运行方式 | 定位 |
| --- | --- | --- | --- |
| `xiaomi` | 小米中枢网关 | 局域网 mTLS + MQTT/MIPS，OAuth 官方 HTTP 云回退 | 当前首选；本地事件驱动，无法经中枢控制的设备可逐台转云 |
| `xiaomi-miot-cloud` | 小米 MIoT 云 · 第三方兼容 | 账号会话 + MIoT 云端轮询 | 参考 `al-one/hass-xiaomi-miot`，补充中枢不易适配的 Wi‑Fi 设备 |
| `xiaomi-home-cloud` | 小米官方 Cloud（独立类型预留） | 未来用于不绑定中枢的纯云实例 | 尚未注册；`xiaomi` 已复用其 OAuth 身份提供设备级官方云路径 |

同一 DID 可以出现在中枢目录和 MIoT 云目录中，但两边使用独立 Provider ID、连接和默认设备 ID 前缀。管理员应选择一个来源进行映射，HomeLoom 不会仅按 DID 或名称自动合并。

## Xiaomi Central Hub Provider

HomeLoom 的 Xiaomi Provider 通过小米中枢网关的局域网 MQTT 5/MIPS 接口接入米家设备，并使用同一 OAuth 授权为无法经中枢本地控制的设备提供官方 MIoT HTTP 路径。本地部分基于现有中枢协议实现继续演进；云端路由参考 [XiaoMi/ha_xiaomi_home](https://github.com/XiaoMi/ha_xiaomi_home) 的连接分层与本地优先思路独立实现，没有复制其受限代码。

## 能力范围

- 小米账号 OAuth 2.0 授权码流程和 `state` 校验；
- 自动生成并复用 `oauthUuid` 与十进制 `virtualDid`；
- 获取账号家庭 UID；
- 生成 Ed25519 PKCS#8 私钥和中枢证书 CSR；
- 申请中枢客户端证书；
- `_miot-central._tcp.local.` mDNS 中枢发现与主中枢筛选；
- MQTT 5 双向 TLS、QoS 2、离线队列和自动重连；
- MIPS TLV 编解码和请求/响应关联；
- 网关设备列表握手；
- MIoT 属性读取、写入和 Action；
- 属性事件订阅；
- 合并中枢目录与 OAuth 账号家庭/房间目录；
- 每台设备独立选择 `auto`、`local` 或 `cloud`；
- `auto` 在中枢协议/传输失败时转入官方 `prop/get`、`prop/set`、`action` HTTP 路径；
- 有界并发初始读取；本地推送可用时事件驱动，云端、无推送或待判定设备才定时校准；
- Provider 热添加、修改、停用和重新连接。

账号密码始终由小米账号页面处理，HomeLoom 不接触也不保存账号密码。

## 配置入口

在 Web 管理页打开统一的“Provider”页面（`#/providers`）。Xiaomi 与 Virtual、MQTT 共用配置、连接、发现、发布的运行生命周期；Xiaomi 的接入流程仍拆分为“账号与中枢配置”和“子设备管理”两个独立视图，严格按以下顺序进行：

1. 填写有权使用的数值型 OAuth Client ID 和账号地区；Redirect URL 固定为 `http://homeassistant.local:8123`；
2. 点击“打开小米授权页面”，在新窗口中登录并完成授权；
3. 浏览器跳转到固定地址后，即使页面无法打开，也从地址栏复制包含 `code` 和 `state` 的完整 URL；
4. 将完整 URL 粘贴回 HomeLoom，点击“解析 URL 并完成授权”，由 HomeLoom 校验来源和 `state` 后换取 Token 与证书；
5. 证书就绪后，通过局域网发现选择中枢，或填写中枢网关 IP/主机名；
6. 测试 MQTT 5 双向 TLS 连接，保存并启用 Provider，等待运行状态变为 `running`；
7. 返回米家中枢列表，进入该中枢独立的“管理子设备”页面；
8. 子设备页面复用当前正在运行的 MQTT 连接调用 `master/proxy/getDevList`，同时使用已保存的 OAuth Token 读取账号家庭与设备目录；两份目录按 DID 合并，但不创建第二条 MQTT 连接；
9. 从设备目录中选择要接入的设备、统一模型和设备级连接策略，再生成设备映射；页面同时展示“中枢本地可控”“OAuth 云可用”“本地事件推送”能力；
10. 在高级 JSON 中按设备 MIoT Spec 核对 `siid/piid/aiid`，保存后实时应用。

新建 Xiaomi Provider 时 `devices` 默认为空数组，不再生成 DID 为空的示例设备。Provider 配置页不展示、读取或修改子设备映射，防止在 OAuth、证书或 MQTT 尚未就绪时越级发现设备。网关目录提供设备身份、名称、房间、型号和可用的 `specType`；设备加入 HomeLoom 后，Provider 根据 `specType` 或 `model` 从 MIoT Spec V2 实例目录加载完整的 Property、Action 和 Event 定义并缓存到 PostgreSQL。自动生成的旧式映射仍只覆盖统一模型必需参数，但设备中心的来源目录不再受这些已配置属性限制。

Provider 配置只写入 PostgreSQL，不生成 `auth.json`、`config.json` 或 `certs/` 目录。`accessToken`、`refreshToken` 和 `privateKey` 由 `storage.master_key` 指定的主密钥使用 AES-256-GCM 加密；管理 API 和诊断导出只返回 `********`。

### Token 与证书自动续期

- OAuth Token 在 `expires_in` 的 70% 时间点进入刷新窗口，最迟会在到期前 5 分钟刷新；服务启动、Provider 配置变化和到达刷新时间都会唤醒检查；
- 中枢客户端证书在有效期剩余 20% 时续签，提前量最多 7 天；证书无法解析时会立即尝试修复；
- 续签复用原账号 UID、`virtualDid` 和 Ed25519 私钥，申请结果必须校验公钥、证书主题和有效期后才可写入 PostgreSQL；
- 新 Token 与证书通过 Provider 热配置更新到“下一次 TLS 握手”材料，当前健康 MQTT 连接不会被主动中断，也不会创建第二条连接；
- 云端失败按 1、2、4、8、16、30 分钟指数退避。每个 Provider 独立重试，失败不会阻塞其他 Provider；
- 数据库写入成功但运行时应用暂时失败时，只重试应用已保存的新凭据，不会重复刷新 Token 或重复申请证书；
- Provider 页面只展示 Token/证书到期时间、下次检查时间和脱敏后的失败原因，不返回任何凭据值。

## 设备映射

中枢接口使用 MIoT 数值标识，HomeLoom 核心使用稳定的统一模型路径。映射保留在 Xiaomi Provider 边界内：

```json
[
  {
    "did": "1234567890",
    "id": "xiaomi-living-switch",
    "name": "客厅开关",
    "type": "switch",
    "model": "vendor.model.v1",
	"homeId": "10001",
	"home": "我的家",
    "roomId": "20001",
    "room": "客厅",
	"connectionMode": "auto",
    "properties": [
      {
        "endpointId": "main",
        "capabilityId": "switch",
        "capabilityType": "switch",
        "propertyId": "power",
        "name": "开关",
        "valueType": "bool",
        "siid": 2,
        "piid": 1,
        "writable": true,
        "notifiable": true
      }
    ],
    "actions": []
  }
]
```

枚举属性通过 `enum` 同时完成读写双向转换：

```json
{
  "propertyId": "target-state",
  "valueType": "enum",
  "siid": 2,
  "piid": 2,
  "enum": {
    "manual": 0,
    "auto": 1,
    "sleep": 2
  }
}
```

设备映射必须满足对应统一模型的必须参数。例如 `switch` 必须发布 `main/switch/power`。不满足契约的配置在写入数据库前即被拒绝。

### 原始属性目录

MIoT Spec 中尚未出现在旧式 `devices[].properties` 配置里的属性，会以稳定原始路径发布：

```text
miot-{SIID} / service-{SIID} / property-{PIID}
```

例如 SIID 2、PIID 3 对应 `miot-2/service-2/property-3`。这些属性保留 Spec 声明的值类型、读写/通知权限、单位、数值范围、步长和枚举，并可直接作为 `Provider → 统一模型` 路由的来源。Action 使用 `action-{AIID}`，Event 使用 `event-{EIID}`，全部会在设备映射页面汇总展示。

`miot_spec_cache` 表按完整实例 URN 保存原始 JSON 和获取时间。相同型号后续启动优先读取数据库，不重复访问远端。若设备没有 `specType`、型号无法在 released 实例索引中解析或网络不可用且无缓存，接口会返回 `catalog.complete=false` 和具体错误；页面显示“来源属性（不完整）”，不会再把已配置属性宣称为完整目录。

来源目录中的每个 Property 还包含内存态值标记：实际读取、写入确认或 MQTT 通知后才设置 `known=true`。前端据此展示“当前值”；设备不可用时展示“上次值”；首次读取失败则展示“当前值未知”和错误原因。MIoT Spec 缓存不保存设备状态值。

## 运行语义

- PostgreSQL 保存期望配置、OAuth 身份、Token、证书和 MIoT 映射；
- Token 与证书由后台调度器提前续期并原子写回 PostgreSQL；
- 当前属性值、在线状态和 sequence 只保存在内存；
- 启动时连接中枢、合并中枢与 OAuth 云设备列表、加载/缓存 MIoT Spec，并有界并发读取完整可读属性；
- 仅修改子设备映射时复用当前 MQTT 会话原地更新设备模型，新增属性在后台有界并发读取，不创建相同 Client ID 的第二条连接；
- 中枢地址、Client ID、TLS/OAuth 或轮询参数变化时才执行连接级替换；
- `connectionMode=auto` 时先通过中枢 MQTT 读取、写入或执行 Action，传输失败、协议错误或中枢标记不可控时使用同一 OAuth 会话转官方云端；`local` 禁止回退，`cloud` 跳过中枢；
- 属性通知实时更新内存快照，并将设备实际 `runtimeMode` 标记为 `local`；官方云端成功则标记为 `cloud`；
- 本地且具备推送能力的设备完成初始读取后不再周期轮询；云端、无本地推送或尚未判定路径的设备才执行定时校准；
- 云端 HTTP 客户端由 Provider 共享，OAuth Token 热更新不重建 MQTT 或 HTTP 客户端；
- MQTT 断线由连接管理器自动重连和恢复订阅；
- Provider 失败不会停止 API、HomeKit 或其他 Provider。

中枢 TLS 服务端证书采用小米专用信任策略：验证证书能够链接到配置中的小米自签名根 CA（并使用随 CA 包或服务端发送的中间证书）、验证整条链的有效期、要求叶证书具备 `ServerAuth` 用途。由于中枢通常通过局域网 IP 或动态主机名访问，连接不验证 DNS/IP SAN；`serverName` 仅作为可选 SNI 发送。`insecureSkipVerify` 仍保留为诊断开关，启用时会显式跳过上述全部服务端证书检查。

## 授权边界

HomeLoom 不内置、不冒充任何第三方 OAuth 应用身份。使用者必须提供自己有权使用、且已登记固定 Redirect URL `http://homeassistant.local:8123` 的 Client ID，并自行确认云接口、OAuth 应用和代码使用方式符合所获授权。小米云接口和中枢固件行为可能变化，正式部署前仍需使用目标账号、地区和实体中枢完成验收。

官方云路径只复用用户已完成的 OAuth 授权，不读取账号密码，也不与 `xiaomi-miot-cloud` 的 `serviceToken/ssecurity` 会话混用。当前实现属于功能可行性研究，调用失败会作为设备操作错误和运行指标呈现，不会尝试绕过地区、账号或设备授权限制。

MIoT Spec 只用于构造 Provider 原始目录，不会自动改变统一模型类型或 HomeKit 附件结构；是否把某个原始属性映射到统一模型仍由管理员逐设备决定。实例 JSON 从公开的 MIoT Spec V2 服务获取并持久缓存，OAuth Token、DID、证书等凭据不会发送给 Spec 服务。

## Xiaomi MIoT Cloud · 第三方兼容

`xiaomi-miot-cloud` 参考 [al-one/hass-xiaomi-miot](https://github.com/al-one/hass-xiaomi-miot) 的 Xiaomi Cloud 工作方式，为中枢接口覆盖不佳的 Wi‑Fi 设备提供补充来源。它不是小米官方 Cloud Provider，也不占用未来官方实现的 `xiaomi-home-cloud` 类型。

运行流程：

1. 使用小米账号、密码和地区建立 `xiaomiio` 会话，或导入完整的 `userId`、`ssecurity`、`serviceToken` 三元组；若账号触发风控，则保留本次登录 Cookie 并进入短信/邮箱验证步骤；
2. 通过 `home/device_list` 读取账号设备基础目录，再调用 `v2/homeroom/gethome_merged` 读取本人及共享家庭的家庭、房间层级，并按 DID 合并 `homeId/homeName/roomId/roomName`；
3. 用户在独立“管理设备”页面选择设备与统一模型，默认 ID 使用 `xiaomi-miot-` 前缀；
4. 根据型号解析并缓存 MIoT Spec，形成完整来源 Property、Action 和 Event 目录；
5. 每台设备可选择 `auto`、`local` 或 `cloud`：`auto` 使用云目录返回的私网 `localip` 与 Token 优先通过 UDP/54321 执行 LAN MIoT，失败时仅为本轮操作回退云端；
6. 可读 Property 以最多 10 项一批同步，默认每 30 秒执行；LAN 使用 `get_properties`，云端使用 `miotspec/prop/get`；
7. 写入和命令同样遵循设备连接策略，本地使用 `set_properties`/`action`，云端使用 `miotspec/prop/set`/`miotspec/action`；
8. 云会话出现认证过期时，如果保存了账号密码则在当前 Provider 内重新登录并重试一次，不创建第二个 Provider 实例。

云端设备加入时的基础模板只用于在 MIoT Spec 尚未加载前表达所选统一模型。Spec 可用后，Provider 会识别未被用户修改的自动模板，并按真实 property URN、SIID/PIID、数值范围和枚举重新生成运行时映射；显式手工映射不会被覆盖。空调伴侣等不提供当前室温/当前运行状态的设备使用 `air-conditioner` v2，其必须参数为启用、运行模式和目标温度。

设备管理页以“家庭 / 房间”展示云端位置，加入映射时同时保存家庭与房间的 ID 和名称。未分配到房间的设备仍保留家庭信息；若位置目录接口暂时不可用，基础设备目录仍可使用，不会因此清空已发现设备。

`connectionMode` 是逐设备配置：`auto` 为默认值；`local` 在缺少私网 IP、16 字节 Token 或局域网不可达时直接报告不可用；`cloud` 完全跳过局域网尝试。管理 API 只返回 `localIp` 和 `localAvailable`，云目录中的设备 Token 只保存在 Provider 运行时内存，既不写入设备映射也不返回前端。局域网目标只允许私网或链路本地 IP，避免把云端目录数据用于访问公网地址。

发布到设备中心的云设备还携带实际 `runtimeMode`：首次状态同步前为 `pending`；最近一次成功的状态同步来自 LAN MIoT 时为 `local`；使用云端轮询或 `auto` 本地失败后成功回退云端时为 `cloud`。设备卡片分别显示“等待判定”“局域网”“云端轮询”标签；这不是静态的 `connectionMode` 配置回显，而是 Provider 当前采用的状态来源。

账号密码、`ssecurity` 与 `serviceToken` 都属于敏感 Provider 配置：写入 PostgreSQL 时使用项目主密钥加密，管理 API 返回 `********`。页面会先调用登录入口；如果小米要求身份验证，HomeLoom 创建一个仅保存在内存、10 分钟过期且最多尝试 5 次的登录挑战。用户在小米验证页发送短信或邮件验证码但不在该页提交，再回到 HomeLoom 回填验证码。后端在原 Cookie 会话中完成验证，成功取得完整三元组后才允许保存并初始化 Provider。一次性验证码和账号密码不会写入挑战数据库，登录响应统一使用 `Cache-Control: no-store`。

该接口依赖未公开的兼容 API，可能随小米服务变化。它采用轮询，适合空调、净化器、灯、插座等持续状态 Wi‑Fi 设备；不适合无线开关、人体/门窗传感器等需要瞬时事件的设备。事件型设备继续优先使用中枢 MQTT。
