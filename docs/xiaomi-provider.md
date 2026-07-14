# Xiaomi Central Hub Provider

HomeLoom 的 Xiaomi Provider 通过小米中枢网关的局域网 MQTT 5/MIPS 接口接入米家设备。实现依据根目录 `xiaomi-central-hub-client-oauth-tls-fix-source/` 的 MIT 许可参考代码，并适配到 HomeLoom 的 Provider SDK、内存状态系统和 SQLite 配置体系。

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
- 有界并发初始读取和定时轮询校准；
- Provider 热添加、修改、停用和重新连接。

账号密码始终由小米账号页面处理，HomeLoom 不接触也不保存账号密码。

## 配置入口

在 Web 管理页打开顶层“米家”页面（`#/xiaomi`）。接入流程拆分为“账号与中枢配置”和“子设备管理”两个独立页面，严格按以下顺序进行：

1. 填写有权使用的数值型 OAuth Client ID 和账号地区；Redirect URL 固定为 `http://homeassistant.local:8123`；
2. 点击“打开小米授权页面”，在新窗口中登录并完成授权；
3. 浏览器跳转到固定地址后，即使页面无法打开，也从地址栏复制包含 `code` 和 `state` 的完整 URL；
4. 将完整 URL 粘贴回 HomeLoom，点击“解析 URL 并完成授权”，由 HomeLoom 校验来源和 `state` 后换取 Token 与证书；
5. 证书就绪后，通过局域网发现选择中枢，或填写中枢网关 IP/主机名；
6. 测试 MQTT 5 双向 TLS 连接，保存并启用 Provider，等待运行状态变为 `running`；
7. 返回米家中枢列表，进入该中枢独立的“管理子设备”页面；
8. 子设备页面复用当前正在运行的 MQTT 连接调用 `master/proxy/getDevList`，不会根据表单配置另开临时连接；
9. 从设备目录中选择要接入的设备和统一模型，再生成设备映射；
10. 在高级 JSON 中按设备 MIoT Spec 核对 `siid/piid/aiid`，保存后实时应用。

新建 Xiaomi Provider 时 `devices` 默认为空数组，不再生成 DID 为空的示例设备。Provider 配置页不展示、读取或修改子设备映射，防止在 OAuth、证书或 MQTT 尚未就绪时越级发现设备。网关目录只提供设备身份、名称、房间和型号等元数据；自动生成的映射只包含对应统一模型的必需参数模板，SIID/PIID 仍需以具体型号的 MIoT Spec 为准。

Provider 配置只写入 SQLite，不生成 `auth.json`、`config.json` 或 `certs/` 目录。`accessToken`、`refreshToken` 和 `privateKey` 由数据库旁主密钥使用 AES-256-GCM 加密；管理 API 和诊断导出只返回 `********`。

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
    "room": "客厅",
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

## 运行语义

- SQLite 保存期望配置、OAuth 身份、Token、证书和 MIoT 映射；
- 当前属性值、在线状态和 sequence 只保存在内存；
- 启动时连接中枢、获取设备列表并并发读取映射属性；
- 仅修改子设备映射时复用当前 MQTT 会话原地更新设备模型，新增属性在后台有界并发读取，不创建相同 Client ID 的第二条连接；
- 中枢地址、Client ID、TLS/OAuth 或轮询参数变化时才执行连接级替换；
- 属性通知实时更新内存快照；
- 轮询用于事件丢失后的校准；
- MQTT 断线由连接管理器自动重连和恢复订阅；
- Provider 失败不会停止 API、HomeKit 或其他 Provider。

中枢 TLS 服务端证书采用小米专用信任策略：验证证书能够链接到配置中的小米自签名根 CA（并使用随 CA 包或服务端发送的中间证书）、验证整条链的有效期、要求叶证书具备 `ServerAuth` 用途。由于中枢通常通过局域网 IP 或动态主机名访问，连接不验证 DNS/IP SAN；`serverName` 仅作为可选 SNI 发送。`insecureSkipVerify` 仍保留为诊断开关，启用时会显式跳过上述全部服务端证书检查。

## 授权边界

HomeLoom 不内置、不冒充任何第三方 OAuth 应用身份。使用者必须提供自己有权使用、且已登记固定 Redirect URL `http://homeassistant.local:8123` 的 Client ID，并自行确认云接口、OAuth 应用和代码使用方式符合所获授权。小米云接口和中枢固件行为可能变化，正式部署前仍需使用目标账号、地区和实体中枢完成验收。

当前 MIoT Spec 不从云端自动下载；映射应依据设备对应 Spec 明确配置。这样可以先保证发布到 HomeKit 的模型契约稳定，也避免固件或 Spec 变化静默改变附件类型。自动 Spec 获取、缓存与映射建议仍作为后续增强项。
