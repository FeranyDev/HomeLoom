# HomeLoom 文档

更新时间：2026-08-01

## 先看这里

- [实施清单](implementation-checklist.md)：当前代码、自动化测试和实机验收的唯一进度清单。
- [Matter 桥实施方案](matter-target-plan.md)：Matter Target 的实现边界与剩余验收项。
- [多协议摄像头接入实现计划](HomeLoom_多协议摄像头接入实现计划_v3.md)：Camera Provider、媒体 Runtime、HomeKit Camera 和 Matter Camera 的边界。
- [小米 Provider](xiaomi-provider.md)：小米三类接入方式和实际配置流程。
- [Sonoff/eWeLink Provider](sonoff-provider.md)：Sonoff LAN/云端双通道、常用 UIID 能力和安全配置边界。

## 稳定契约

- [Device Model v1](device-model.md)：设备、Endpoint、Capability、Property 和状态语义。
- [统一模型参数](unified-model-parameters.md)：模型参数分级以及 Provider/Consumer 的职责。
- [HTTP API](http-api.md) / [OpenAPI](openapi.yaml)：管理 API 的行为和机器可读契约。
- [MQTT 协议](mqtt-protocol.md) / [JSON Schema](schemas/mqtt-protocol.schema.json)：`homeloom-v1` 消息协议。
- [HomeKit 基础设备契约](homekit-device-contracts.md) / [支持矩阵](homekit-model-support.md)：HomeKit Consumer 映射。

## 运行与运维

- [双段映射架构](mapping-architecture.md)
- [日志规范](logging.md)
- [Matter Runtime 运维边界](matter-runtime.md)
- [打包边界](packaging.md)
- [小米云 MQTT/MIPS 方案](xiaomi-cloud-mips-plan.md)

## 专项验收

- [Matter Camera MC-6 验证清单](Matter_Camera_MC6_验证清单.md)：外部 Controller 缺失时保持 `NOT RUN`，不得以自动化测试替代实机结果。
- [Demo 01 记录](demo-01.md)：Virtual Provider、Web 和 HomeKit 的基线验证记录。

## 文档维护规则

- `[x]` 只表示代码和自动化测试已验证；涉及真实设备、局域网、Controller 或长期运行时使用 `[~]`。
- 当前进度只在[实施清单](implementation-checklist.md)和对应专项方案中维护；早期开发计划不再重复记录完成状态。
- 稳定契约只描述当前行为，历史排障日志、已完成的修改文件列表和一次性备注不保留在正式文档中。
