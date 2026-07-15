# 双段设备映射架构

HomeLoom 只把统一智能设备模型作为 Provider 与 Consumer 之间的稳定契约：

```text
Provider 原始属性
  Endpoint / Capability / Property
          │ Provider Profile（可选）
          ▼
统一智能设备模型
  Endpoint / Capability / Property
  required / optional / custom
          │ Consumer Profile（可选）
          ▼
Consumer 属性
  例如 HomeKit Service.Characteristic
```

两段关系分别存储在 SQLite 的 `mapping_bindings` 中，而且都以具体设备为作用域。Provider 路由按 Provider、设备和原始三级路径匹配；Consumer 路由按 Provider、设备、Target 桥、桥内虚拟设备、Consumer 和目标属性匹配。同型号的两台来源设备，以及同一来源设备发布到不同桥或不同虚拟设备时，都可以配置完全不同的路径与转换。Profile 为空表示不转换，配置 Profile 时事件走正向转换，读取和控制走反向转换。

## Provider 边界

Provider Manager 只校验快照的结构、稳定 ID、值类型和约束，不要求原始快照提前满足统一模型。Device Service 随后应用 Provider 路由、移动属性路径、转换属性定义和值，最后才校验统一模型的必需参数。写操作从统一模型路径反向解析为 Provider 原始路径。

映射目录读取独立的 Provider 原始目录契约，而不是把统一模型注册表反向当成来源。每台设备带有 `catalog.complete/source/specType/fetchedAt/error` 元数据；只有 Provider 能枚举原生 schema 时才允许标记完整。页面展示全部 Endpoint、Capability、Property、Action、Event、权限、单位和类型。Provider 目录不可用时回退数据必须标记为不完整。

Xiaomi Provider 在 MQTT 连接建立并取得 `getDevList` 后，根据 `specType` 或 `model` 解析公开 MIoT Spec V2 实例。实例原文缓存于 SQLite `miot_spec_cache`，未进入旧式配置的原生属性使用 `miot-{SIID}/service-{SIID}/property-{PIID}` 稳定路径，并支持读取、写入和属性通知。无法解析 Spec 时保留兼容配置，但明确返回不完整状态。

原始目录同时返回逐属性的临时值状态。`known=true, available=true` 表示本次运行中已经从 Provider 读取或收到通知，页面显示“当前值”；设备离线但仍有历史观察值时显示“上次值”；尚未成功读取时显示“当前值未知”，不会把类型零值占位误报为设备状态。值和观察时间只驻留内存，重启后重新获取。

## 统一模型边界

内置模型目录定义 required 和 optional 参数。数据库表 `custom_model_properties` 保存 custom 参数，内容包含设备类型、三级路径、显示名称、值类型、单位、最小值、最大值、步长、枚举及 R/W/N 权限。自定义路径不能覆盖标准路径，正在被路由使用时不能删除。

设备注册表和设备中心详情只接收命中该模型目录的三级属性：内置 required/optional 属性，以及已登记的 custom 属性。未命中模型目录的 Provider 原始 Property、Action 和 Event 不会再被自动标记为 custom 混入设备详情，仍完整保留在对应设备的“配置映射”来源目录中。

## Consumer 边界

Consumer 目录公开平台能提供的全部目标属性。Target 先创建独立桥，再在桥内创建拥有稳定 ID、名称和启停状态的虚拟设备；每台虚拟设备绑定一个统一注册表来源设备。属性映射入口位于该桥的对应虚拟设备，编辑器锁定 `providerId + deviceId + targetId + consumerDeviceId`，不会展示或修改其他桥内设备的路由。设备类型只用于筛选当前设备可用的统一模型和 Consumer 属性。HomeKit 路由在该桥内虚拟设备独立的 Consumer 投影视图中生效，不会改写 Core 注册表、影响同型号的其他设备或影响 Web 等其他 Consumer。通知值走正向 Profile；HomeKit 控制先按桥和虚拟设备作用域做反向 Profile，再写入对应的统一模型属性，随后继续沿来源设备的 Provider 路由写回设备。

Provider 路由变化只刷新原始设备快照，不重启 Provider。Consumer 路由变化会保留 HomeKit 身份目录和配对资料，重建附件图以应用新的 Service/Characteristic 关系。

## 前端配置边界

设备中心的每张设备卡片提供 Provider → 统一模型的“配置映射”入口。桥接中心的每张桥卡片提供“配置虚拟设备”入口；虚拟设备保存后，才能进入该实例的统一模型 → Consumer 三栏关系图。顶层“统一模型”页面以设备模型及端点 / 能力 / 属性三级字段为主工作区：完整展示必需、可选、自定义字段及发布端/消费端规则，并提供数据库自定义字段入口；跨设备复用的转换 Profile 和转换预览收纳在次级标签中。
