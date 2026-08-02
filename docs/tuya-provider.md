# Tuya Provider

HomeLoom 以 `tuya` 类型注册 Tuya Cloud Provider。Provider 配置保存在数据库的
`providers` 表中，由 Core 启动时的 Provider Factory 创建；它不属于
`backend/configs/config.example.yaml` 的进程启动配置。

## Home Assistant 兼容扫码登录（推荐）

HomeLoom 支持 Home Assistant Tuya 集成使用的 User Code + 手机 App 扫码流程，不要求
创建 Tuya IoT Platform 开发者项目。Home Assistant 的官方说明是：在 Tuya Smart 或
Smart Life App 的“我 → 设置 → 账号与安全”中取得 User Code，然后让 App 扫描网页中的
二维码完成授权；实现使用的客户端标识和扫码接口与 Home Assistant 当前集成保持一致。
参考 [Home Assistant Tuya 集成说明](https://www.home-assistant.io/integrations/tuya/) 和
[Home Assistant 当前 config flow](https://github.com/home-assistant/core/blob/dev/homeassistant/components/tuya/config_flow.py)。

在 Web 的“新增 Provider → Tuya”中：

1. 保持登录方式为“Home Assistant 扫码（推荐）”。
2. 在 Tuya/Smart Life App 的“我 → 设置 → 账号与安全”复制 User Code，填写后点击“获取
   Tuya 扫码二维码”。
3. 使用同一个 App 扫描二维码并确认，页面会自动轮询并回填 UID、Endpoint、Terminal ID
   和访问 Token。
4. 保存并启用 Provider，然后点击“测试 Tuya 连接”确认设备目录可用。

扫码会话只保存在内存中，二维码和 User Code 不写入日志；完成后只将必要的会话字段保存在
Provider 加密配置中。Token 过期时，HomeLoom 使用同一设备共享会话刷新 Token。

对应的最小配置形态如下。真实 Token 不要写入仓库：

```json
{
  "authType": "sharing",
  "uid": "REPLACE_WITH_TUYA_UID",
  "userCode": "REPLACE_WITH_APP_USER_CODE",
  "endpoint": "https://openapi.tuyaus.com",
  "clientId": "HA_3y9q4ak7g4ephrvke",
  "terminalId": "REPLACE_WITH_TERMINAL_ID",
  "accessToken": "REPLACE_WITH_ACCESS_TOKEN",
  "refreshToken": "REPLACE_WITH_REFRESH_TOKEN",
  "requestTimeoutSeconds": 15,
  "pollIntervalSeconds": 21600
}
```

## 配置示例

可以在 Web 的 Provider 页面保存，或通过 `POST /api/v1/providers` 提交下面的
Provider 输入。示例中的值都是占位符，提交前只在受保护的管理环境中替换，不能
把真实凭据写入仓库：

```json
{
  "id": "tuya-main",
  "type": "tuya",
  "name": "Tuya Cloud",
  "enabled": true,
  "config": {
    "region": "us",
    "uid": "REPLACE_WITH_TUYA_UID",
    "accessId": "REPLACE_WITH_TUYA_ACCESS_ID",
    "accessSecret": "REPLACE_WITH_TUYA_ACCESS_SECRET",
    "accessToken": "REPLACE_WITH_TUYA_ACCESS_TOKEN",
    "refreshToken": "REPLACE_WITH_TUYA_REFRESH_TOKEN",
    "requestTimeoutSeconds": 15,
    "pollIntervalSeconds": 21600
  }
}
```

`accessSecret`、`accessToken` 和 `refreshToken` 都是敏感信息。不要将它们提交到
Git、写入日志或截图；如果凭据泄露，应立即在 Tuya IoT Platform 撤销或轮换。
`accessId` 和 `uid` 主要是账号/项目标识，但也应按内部凭据处理。保存 Provider
时，HomeLoom 使用 `storage.master_key` 保护敏感字段；Provider API、配置导出和
诊断包只返回 `********`。更新已有 Provider 时，保留接口返回的
`********` 占位符即可，不要把占位符作为新 Provider 的真实凭据提交。

手工配置 OpenAPI Token 时，必填项是 `uid`、`accessId` 和 `accessSecret`。使用上面的
Home Assistant 扫码授权时，可以先不填写 `uid`；授权成功后 HomeLoom 会自动写入 `uid`、
`endpoint`、`terminalId`、`accessToken`、`refreshToken` 和 `tokenExpiresAt`。`region` 可使用 `cn`、`eu`、
`in`、`sg` 或 `us`；省略 `baseUrl` 时会根据地区选择 Tuya OpenAPI 地址。需要显式
指定地址时，`baseUrl` 必须是 HTTPS URL。

## Tuya OpenAPI OAuth（兼容备用）

此模式使用 Tuya 官方 OAuth 2.0 授权码流程：在 Tuya IoT Platform 的云项目中
配置 OAuth 2.0/H5 授权页和回调地址，用户在 Tuya/Smart Life 的授权页中扫码或
登录并确认授权，随后 HomeLoom 用回调中的一次性 code 换取 Cloud Token。参考
[Tuya OAuth 2.0 授权流程](https://developer.tuya.com/en/docs/iot/authorization-code-page-usage?id=Kdkyz44dz6a7r)
和 [Tuya Authentication Method](https://developer.tuya.com/en/docs/iot/authentication-method?id=Ka49gbaxjygox)。

在 Web 的“新增 Provider → Tuya”中：

1. 填写同一个 Tuya 云项目的 `Access ID`、`Access Secret` 和地区。
2. 在 Tuya IoT Platform 生成/复制该项目的 OAuth H5 授权页 URL，粘贴到“OAuth H5
   授权页 URL”。不要把它替换成未经 Tuya 文档公开的 App 私有登录接口。
3. 将 OAuth 回调地址登记为当前 HomeLoom 地址加
   `/api/v1/tuya/oauth/callback`，例如
   `https://homeloom.example.com/api/v1/tuya/oauth/callback`，并把同一个地址填入表单。
4. 点击“开始 Tuya 扫码授权”，使用二维码或授权页完成登录和授权。浏览器能自动
   回传时会直接填充 UID/Token；如果浏览器阻止了弹窗或自动回传，可将回调 URL
   粘贴到表单下方，再点击“解析回调并完成授权”。
5. 保存并启用 Provider，然后使用“测试”确认项目权限和账号归属正确。

OAuth 会话 state 只在内存中保存 10 分钟且单次使用；项目 Secret 不会通过前端
回调或二维码返回。若部署在反向代理后，回调地址必须使用浏览器实际访问
HomeLoom 的协议、域名和端口，并在 Tuya 项目中登记完全一致的 URL。

## 默认运行方式

- Provider 会根据 Tuya 返回的设备规格生成设备原生目录和属性定义，不要求在配置
  中手工复制每台设备的规格。
- 对常用设备会同时发布统一语义能力：灯具、开关/插座、电能计量、风扇、窗帘、
  空调、空气净化器、热水器、温湿度、门磁、人体、照度、漏水、烟雾和空气质量。
  例如 `bright_value` 会以 `main/light/brightness` 暴露，同时保留原始的
  `main/tuya-dp/bright_value`；通过统一属性写入时会自动反向编码到对应 DP。
- 未识别的设备或 DP 不会丢失，仍会出现在 `tuya-dp` 原生目录中，可通过映射配置
  或 Quirk 继续接入。
- 默认使用 HTTP 进行初始化、控制和状态对账；未配置 `mqtt` 时仍可工作。MQTT
  只是可选的实时消息通道，断线或消息不可用时仍以 HTTP 对账为恢复路径。
- `requestTimeoutSeconds` 默认是 15 秒，`pollIntervalSeconds` 默认是 21600 秒
  （6 小时）。省略这两个字段即可使用默认值。

当前版本的 Tuya Provider 不会自行完成 `device.openHubConfig` 的临时凭据申请和
MQTT 拨号；它提供 `DecodeMQTTMessage` 与 `HandleMQTTMessage`，供宿主集成在拿到
临时凭据并订阅 `sourceTopic` 后注入实时消息。若使用该外部消息通道，必须额外提供
已授权且仍有效的 `url`、`username`、`password`、`clientId` 和 `sourceTopic`；这些
连接材料同样不得进入版本库。没有外部 MQTT 消费者时，省略 `mqtt` 即可，HTTP
轮询仍然完整支持发现、读取、控制和状态恢复。

## 接入检查

1. 在 Tuya IoT Platform 创建或选择与账号地区匹配的云项目，并授予设备目录、规格、
   状态读取和控制所需的权限。
2. 确认 `uid` 属于该项目可访问的用户/家庭，`accessId` 与 `accessSecret` 来自
   同一个项目。
3. 先用 `/api/v1/providers/test` 测试上面的配置，再保存并启用 Provider。
4. Provider 进入 `running` 后，在设备中心检查设备规格、属性和在线状态；需要映射
   到统一模型时，使用该设备的 Provider 原生目录创建映射。
