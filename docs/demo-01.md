# Demo 01：Virtual Provider + Web + HomeKit

## 已完成

- 前后端分离目录；
- 虚拟开关、灯泡、插座、温湿度、接触和活动传感器；
- REST 设备查询与开关写入；
- Provider 状态订阅；
- HomeKit Bridge、Switch 和 Temperature Sensor；
- HAP UUID、密钥及 pairing 文件持久化；
- SQLite migration 和 WAL；实时设备状态仅保存在内存；
- 可同时运行多个 Apple HAP Bridge；
- Target 类型、设备分配、独立端口和独立身份配置；
- 标准 HomeKit Setup URI 与 PNG 配对二维码；
- 前端桥接中心页面；
- Device Registry 作为设备查询的统一事实来源；
- 有界、按设备分片的 Provider 事件队列；
- 同设备事件顺序处理与优雅排空；
- 统一属性 State Store 与确定性冲突归并；
- 状态来源、质量、sequence、version 和时间诊断 API；
- 命令 queued/sent/accepted/confirmed/rejected/timeout 状态机；
- Provider 真实状态回报确认命令；
- Target 独立有界队列和慢消费者隔离；
- Core/Target 队列及命令 Prometheus 指标；
- Provider 边界深拷贝快照，避免异步共享可变 slice；
- Provider SDK 与多 Provider 聚合管理、设备归属路由；
- Device Model v1、稳定 ID 校验和统一 Capability/Property 契约；
- 通用 PropertyReader、PropertyWriter 和带幂等 key 的 Action/Command；
- SQLite Provider 配置、默认种子和类型 Factory；
- 统一 API 错误包装、Request ID、OpenAPI 与 runtime 指标；
- 前后端独立容器、Linux host-network Compose 和数据卷；
- 项目级 Go/npm 缓存；
- Go 测试、ESLint 和前端生产构建。

## 本机验证

- HTTP `8090` 监听成功；
- HAP TCP `51826` 监听成功；
- REST 开关写入成功；
- 服务重启后 HAP UUID 和 keypair 校验值保持一致；
- 服务重启后由 Provider 重新建立设备状态；
- 当前受限运行环境报告 netlink link-update 警告。

## 局域网验收

1. 确保 Mac 和 iPhone 位于同一局域网；
2. 启动后端；
3. 在 Apple Home 中添加附近配件；
4. 使用配对码 `001-02-003`；
5. 检查客厅开关和客厅温度是否出现；
6. 分别从 Web 和 Apple Home 控制开关；
7. 重启后端并确认无需重新配对。

## 下一步

- 完成 Apple Home 实机配对、双向控制和三次重启验收；
- 加入管理员认证、CSRF 防护和敏感操作二次确认；
- 将已完成的 SQLite 一致性备份/离线安全恢复接入管理员认证后的 Web 运维入口；
- 开始 MQTT Provider 与 discovery/state/command 协议实现。
