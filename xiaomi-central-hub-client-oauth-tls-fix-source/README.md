# Xiaomi Central Hub Local Client

一个独立的 Go 命令行客户端，用于完成小米账号 OAuth 授权、申请中枢网关客户端证书，并通过局域网 MQTT/MIPS 与支持本地 MIoT Pub/Sub 的中枢网关通信。

## 已实现

- 小米账号 OAuth 2.0 授权码登录；
- 本地 HTTP 回调和 `state` 校验；
- access token / refresh token 获取与刷新；
- 获取账号 UID；
- 生成并持久化 OAuth 身份和 `virtual_did`；
- Ed25519 私钥与 PKCS#8 PEM；
- 生成中枢证书所需 CSR；
- 请求中枢网关客户端证书；
- 自动生成 CA、客户端证书和本地 MQTT 配置；
- 通过 `_miot-central._tcp.local.` 发现中枢网关；
- MQTT 5.0 + 双向 TLS；
- MIPS 二进制 TLV 封包与解包；
- 获取网关设备列表；
- 读取、写入 MIoT 属性；
- 调用 MIoT action；
- 订阅属性变化和设备事件；
- MQTT QoS 2、Keepalive 和连接异常检测。

项目只使用 Go 标准库。

## 许可与使用边界

程序不会内置或冒充任何第三方 OAuth 应用身份。运行 `login` 时必须通过 `--client-id` 或环境变量 `XIAOMI_OAUTH_CLIENT_ID` 提供一个你有权使用、并已登记对应 redirect URL 的数值型 Client ID。

小米官方 Home Assistant 集成的源代码及相关云 API 许可证限定为“用于 Home Assistant 的非商业用途”，并明确没有授权将该 Licensed Work 用于其他应用或服务。个人非商业并不自动等于获得任意独立应用使用权。请自行确认 Client ID、云接口和代码使用方式符合你获得的授权。

OAuth 登录页面由小米账号站点处理，本程序不接触或保存账号密码。`auth.json` 中包含 access token 和 refresh token，`certs/client.key` 是客户端私钥，二者都必须视为敏感凭据。

## 环境要求

- Go 1.23 或更新版本；
- 一个有权使用的 OAuth Client ID 和与之匹配的 redirect URL；
- 中枢网关与客户端处于同一局域网；
- 局域网允许 UDP 5353 组播；
- 中枢网关固件支持并启用了本地 MQTT/MIPS；
- 账号地区与 `--region` 一致。

## 编译

```bash
go test ./...
go build -o xiaomi-hub ./cmd/xiaomi-hub
```

Windows：

```powershell
go build -o xiaomi-hub.exe ./cmd/xiaomi-hub
```

## 1. OAuth 登录并申请证书

最基本的命令：

```bash
./xiaomi-hub login \
  --client-id YOUR_AUTHORIZED_OAUTH_CLIENT_ID \
  --region cn \
  --redirect-url http://homeassistant.local:8123 \
  --listen :8123
```

也可以通过环境变量传入 Client ID：

```bash
export XIAOMI_OAUTH_CLIENT_ID=YOUR_AUTHORIZED_OAUTH_CLIENT_ID
./xiaomi-hub login --region cn
```

程序会：

1. 生成并持久化一个 32 位十六进制 OAuth UUID；
2. 形成 OAuth `device_id`：`ha.<oauth_uuid>`；
3. 生成独立的十进制 `virtual_did`，用于中枢 MQTT 身份；
4. 启动本地 HTTP 回调服务；
5. 打开小米账号授权页面；
6. 使用授权码换取 token；
7. 调用家庭信息接口获取账号 UID；
8. 生成 Ed25519 私钥；
9. 生成主题为 `C=CN, O=Mijia Device, CN=mips.<uid>.<sha1(virtual_did)>.2` 的 CSR；
10. 申请中枢客户端证书；
11. 写出 `auth.json`、`certs/` 和 `config.json`。

默认输出：

```text
auth.json
config.json
certs/ca.pem
certs/client.crt
certs/client.key
```

### redirect URL 注意事项

`--redirect-url` 必须是该 OAuth Client ID 允许的地址。程序会监听 redirect URL 的路径，例如：

```bash
./xiaomi-hub login \
  --client-id 1234567890 \
  --redirect-url http://homeassistant.local:8123/oauth/callback \
  --listen :8123
```

此时回调路径为 `/oauth/callback`。

`homeassistant.local` 必须在执行授权的浏览器所在设备上解析到运行本程序的主机。不能解析时，应使用你的 OAuth 应用已登记且能够回到本机的地址。仅修改 hosts 并不能绕过 OAuth 服务端对 redirect URL 的登记校验。

无图形环境：

```bash
./xiaomi-hub login \
  --client-id YOUR_CLIENT_ID \
  --no-browser
```

程序会打印授权 URL，可复制到浏览器打开。

### 身份参数

通常不要手动指定：

```text
--oauth-uuid
--virtual-did
```

它们会写进 `auth.json` 并在后续登录时复用。更换 `virtual_did` 后，旧证书不再匹配，需要重新申请证书。

## 2. 刷新 OAuth token

```bash
./xiaomi-hub refresh --auth-file auth.json
```

程序会在 `expires_in` 的 70% 时间点标记为应刷新，但不会在后台自动运行。`renew-cert` 在到达刷新时间时会先刷新 token。

## 3. 重新申请中枢证书

```bash
./xiaomi-hub renew-cert \
  --auth-file auth.json \
  --cert-dir certs \
  --config config.json
```

强制先刷新 token：

```bash
./xiaomi-hub renew-cert --refresh-token
```

该命令复用原来的 UID、`virtual_did` 和 Ed25519 私钥，从而保持 MQTT 身份稳定。

## 4. 发现网关

```bash
./xiaomi-hub discover --timeout 12s
```

典型输出：

```json
[
  {
    "instance": "...",
    "host_name": "...local",
    "addresses": ["192.168.100.50"],
    "port": 8883,
    "did": "1234567890",
    "group_id": "...",
    "role": 1,
    "mqtt_enabled": true
  }
]
```

程序会在所有已启用且支持 IPv4 多播的网卡上发送 DNS-SD 浏览请求，并在收到 PTR 后继续主动查询 SRV、TXT 和主机地址。多网卡、VPN 或虚拟网卡环境建议打开诊断：

```bash
./xiaomi-hub discover --timeout 15s --debug
```

也可以只使用连接家庭局域网的网卡：

```bash
# macOS 常见为 en0；Linux 常见为 eth0、enp3s0 或 br-lan
./xiaomi-hub discover --timeout 15s --debug --interface en0
```

优先选择 `role: 1` 且 `mqtt_enabled: true` 的主中枢，把地址写入登录流程生成的 `config.json`：

```json
{
  "host": "192.168.100.50",
  "port": 8883,
  "client_id": "由 login 写入的 virtual_did",
  "ca_file": "certs/ca.pem",
  "cert_file": "certs/client.crt",
  "key_file": "certs/client.key",
  "server_name": "",
  "verify_server_name": false,
  "insecure_skip_verify": false,
  "request_timeout_seconds": 10
}
```

`client_id` 必须是 `virtual_did`，不是 OAuth Client ID，也不是 `ha.<oauth_uuid>`。

## 5. 获取设备列表

```bash
./xiaomi-hub devices --config config.json
```

## 6. 读取属性

```bash
./xiaomi-hub get \
  --config config.json \
  --did 1234567890 \
  --siid 2 \
  --piid 1
```

## 7. 写入属性

布尔值：

```bash
./xiaomi-hub set \
  --config config.json \
  --did 1234567890 \
  --siid 2 \
  --piid 1 \
  --value true
```

整数：

```bash
./xiaomi-hub set --config config.json --did 1234567890 --siid 2 --piid 2 --value 2
```

字符串必须保留 JSON 引号：

```bash
./xiaomi-hub set --config config.json --did 1234567890 --siid 2 --piid 3 --value '"auto"'
```

## 8. 调用动作

```bash
./xiaomi-hub action \
  --config config.json \
  --did 1234567890 \
  --siid 4 \
  --aiid 1 \
  --in '[]'
```

带参数：

```bash
./xiaomi-hub action --config config.json --did 1234567890 --siid 4 --aiid 1 --in '[1,true]'
```

## 9. 订阅属性和事件

订阅设备全部属性和事件：

```bash
./xiaomi-hub listen \
  --config config.json \
  --did 1234567890 \
  --properties \
  --events
```

仅订阅一个属性：

```bash
./xiaomi-hub listen \
  --config config.json \
  --did 1234567890 \
  --properties=true \
  --events=false \
  --siid 2 \
  --piid 1
```

## 文件安全

不要提交以下文件：

```text
auth.json
config.json
certs/client.key
certs/client.crt
```

建议权限：

```bash
chmod 600 auth.json config.json certs/client.key
chmod 644 certs/client.crt certs/ca.pem
```

删除授权材料：

```bash
rm -f auth.json config.json
rm -rf certs
```

这只删除本机保存的材料，不一定会撤销云端授权。云端撤销需要通过对应小米账号或 OAuth 应用提供的授权管理入口完成。

## 协议结构

控制消息通过 MQTT QoS 2 发送。MQTT Payload 不是裸 JSON，而是 TLV：

```text
uint32_le data_length
uint8     field_type
bytes     field_data
```

主要字段：

```text
0 = message ID，4 字节 uint32_le
1 = reply topic，NUL 结尾字符串
2 = JSON payload，NUL 结尾字符串
3 = from，NUL 结尾字符串
```

回复 Topic 使用：

```text
<virtual_did>/reply
```

属性和事件的精确订阅后缀使用：

```text
<siid>.<piid>
<siid>.<eiid>
```

## 故障排查

### OAuth 页面提示 redirect URL 无效

Client ID 没有登记该 URL。必须使用被允许的 redirect URL，不能由客户端单方面绕过。

### 浏览器授权后程序一直等待

检查：

- redirect URL 主机是否解析到运行程序的设备；
- `--listen` 端口是否与 redirect URL 端口一致；
- 防火墙是否允许浏览器访问该端口；
- redirect URL 路径是否被反向代理改写。

### 获取 UID 失败

证书主题需要账号 UID。当前实现从账号拥有的家庭 `homelist` 中读取 UID。确认账号至少拥有一个家庭，而不仅仅是接受了别人单独分享的设备。

### 证书请求返回 401

access token 无效或过期。运行：

```bash
./xiaomi-hub refresh
./xiaomi-hub renew-cert
```

### TLS 主机名校验失败

`verify_server_name` 默认保持为 `false`。小米中枢常见的服务端证书没有 DNS/IP SAN，因此客户端会跳过名称匹配，但仍使用 `ca_file` 验证完整证书链和 ServerAuth 用途。只有确认网关证书包含有效 SAN 时，才设置 `verify_server_name: true`。`insecure_skip_verify: true` 会连证书链也一起跳过，只能临时用于诊断。

### discover 没有结果

先执行：

```bash
./xiaomi-hub discover --timeout 15s --debug
```

根据最后一行判断：

- `收到 0 个 mDNS 数据包`：本机没有收到 UDP 5353，多数是选错网卡、Docker/虚拟机未使用 host/桥接网络、VPN 抢占路由、VLAN 未转发 mDNS、AP/访客网络隔离或防火墙拦截；
- 有数据包但 `PTR=0`：局域网 mDNS 正常，但没有设备发布 `_miot-central._tcp.local.`，检查中枢型号和固件；
- `PTR>0` 但 `SRV/TXT=0`：网关详情响应被网络设备过滤，可指定实际 LAN 网卡重试；
- 已有完整网关但 `mqtt_enabled=false` 或 `role!=1`：该广播不是支持本地 MQTT 的主中枢。

系统原生命令可以独立验证广播：

```bash
# macOS
/usr/bin/dns-sd -B _miot-central._tcp local.

# Linux（安装 avahi-utils 后）
avahi-browse -rt _miot-central._tcp
```

官方本地模式要求独立中枢固件不低于 `3.3.0_0023`，内置中枢软件不低于 `0.8.9`。普通多模网关、旧 Zigbee 网关或仅标注“蓝牙 Mesh 网关”的设备不一定属于这里的中枢网关。

## 已知限制

- OAuth 和证书请求已按官方当前接口结构实现，但没有你的 Client ID、账号和实体中枢，无法完成真实云端授权和硬件握手测试；
- OAuth redirect URL 是否被接受取决于对应 Client ID 的服务端配置；
- 当前连接断开后会结束，不做透明重连；
- 设备 `siid/piid/aiid` 仍需从 MIoT-Spec 或设备模型中获取；
- 云端接口、授权规则和中枢固件行为可能随时变化；
- 中枢本地能力可能因地区、型号和固件而不同。

## 参考

- 官方集成：`https://github.com/XiaoMi/ha_xiaomi_home`
- OAuth/云客户端：`custom_components/xiaomi_home/miot/miot_cloud.py`
- 中枢证书实现：`custom_components/xiaomi_home/miot/miot_cert.py`
- 中枢 MIPS 客户端：`custom_components/xiaomi_home/miot/miot_mips.py`
