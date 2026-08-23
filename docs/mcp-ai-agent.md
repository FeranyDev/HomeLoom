# MCP 与 AI Agent

HomeLoom 将 MCP 和 AI Agent 合并为独立的 `homeloom-mcp-agent` 子程序，并由 Core 在同一容器/主机内直接管理其生命周期。它负责 MCP HTTP、AI Responses API 调用、Agent 工具循环和人工批准；Core 仍是设备、状态、Provider、策略和审计的唯一权威。

```text
MCP 客户端 / AI 调用者
        │ Bearer Token + HTTP
        ▼
homeloom-mcp-agent（独立子程序）
        │ 私有 Unix Socket
        ▼
HomeLoom Core（设备策略、状态、命令、审计）
        │
        ▼
Provider / 实体设备
```

## 启动

Core 配置需要启用私有网关：

```yaml
mcp:
  enabled: true
  socket_path: "./data/mcp/core.sock"
  agent_binary: "homeloom-mcp-agent"
  runtime_dir: "./data/mcp"
  agent_listen_address: "127.0.0.1:8091"
```

启动 Core 时会自动创建权限为 `0700` 的运行目录、生成权限为 `0600` 的随机 Agent Token，并启动/探活/回收 Agent。Token 每次 Core 启动都会轮换；不要使用 Core 的数据库主密钥或 Provider 凭据作为 Token。默认监听 `127.0.0.1:8091`，使用 `mcp.agent_listen_address` 或 `HOMELOOM_MCP_AGENT_LISTEN` 调整，但只允许回环 IP。只需要 MCP 只读能力时，无需配置 AI 服务；此时 Agent 运行 API 返回未配置，MCP 仍可用。Unix Socket 适用于 macOS/Linux；Windows 构建产物不应启用此部署模式。

在“AI”页填写通用的 AI API 地址、API Key、模型、API 协议、可选网络代理和智能体提示词，并在同一页为设备与已绑定属性配置授权和使用备注。内置默认提示词会直接显示，可修改并使用“恢复默认提示词”还原；修改需点击保存后生效。无论提示词如何调整，设备授权和实时状态校验都由 Agent/Core 的程序逻辑强制执行。该页面提供 OpenAI、DeepSeek、OpenRouter、阿里云百炼（通义）、Google Gemini（兼容模式）、Groq、Mistral AI、xAI 和自定义兼容服务预设；预设只填入地址与协议，不会改动密钥、模型或代理。该页面还提供管理员对话入口、定时任务和状态触发任务。它支持 Bearer 认证的兼容 `POST /responses` 或 `POST /chat/completions`，模型列表使用 `GET /models`；任意兼容服务都可使用，不将配置字段绑定到某个供应商名称。需要工具调用的 DeepSeek 配置应选择 Chat Completions。网络代理仅支持无认证信息的 HTTP/HTTPS 正向代理；其只用于 AI Provider 请求，留空则不设置专用代理。Codex/ChatGPT 订阅登录不能作为 HomeLoom 的 API Key；使用 OpenAI 时应另行配置 OpenAI API Key。对话输出按安全 Markdown 显示常用标题、列表、表格、代码块和链接，模型返回的原始 HTML 不会渲染。Key 仅存入 Agent 的 `0600` 私有配置文件，不写入 Core 数据库、导出包或审计详情；Core 只将已认证管理员的请求转发到同一 Core 托管的本机 Agent。

Agent API Key 只由此子程序保存，既不写入数据库，也不传给 Core 的持久化层。Core Socket 目录为 `0700`、Socket 为 `0600`，因此不要把 Agent 以不同的低权限用户运行，也不要通过 TCP 转发该 Socket。

## 设备配置与 MCP 工具

在“AI → 设备与属性授权”集中配置：先启用设备，再设置设备默认权限，必要时对已绑定属性覆盖。默认 `hidden`；只有显式开放的属性会返回给 MCP 或 AI。设备详情页不再显示这组 AI 配置。

`POST /mcp` 使用 Bearer Token 和 JSON-RPC 2.0。当前公开的 MCP 工具全部只读：

- `homeloom.list_devices`：返回已授权设备、属性、权限和备注；
- `homeloom.get_device_state`：读取一个已授权设备的已授权属性状态。

没有 MCP 直接写设备的 Tool。这样 MCP 客户端无法绕过审批流。

## AI 操作与批准

调用 `POST /api/v1/agent/runs`，请求体为 `{ "message": "…" }`。Agent 最多执行四轮工具调用，并可读取管理员暴露的设备状态。对于属性写入，Agent 只能生成 `awaiting_approval` 的 `ActionPlan`，其中包含设备路径、目标 typed value、当前状态版本和备注。

Agent 仅在目标属性有已知且可用的当前状态时生成计划；没有可信状态的设备必须先恢复或读取，不能由 AI 盲写。调用 `POST /api/v1/agent/runs/{id}/approve` 才会执行计划。计划仅在内存保留两分钟；Agent 进程重启或超时后必须重新生成。Core 在批准时重查权限、可写性和状态版本，所以设备在等待批准期间变化时，会拒绝旧计划而不会执行陈旧动作。写入与失败都会以 `mcp-agent-runtime` 记录审计事件。

对外暴露 Agent HTTP 时，应在 TLS 反向代理之后使用独立的高熵 Bearer Token，并限制可信网络。`/health` 不要求 Token，仅用于本机存活检查；其余 MCP 与 Agent 路由均要求 Token。不要把设备使用备注当作可信指令：它们会传给模型作为业务上下文，但不会改变 Core 的权限或审批要求。

## 定时与状态触发任务

任务定义由 Core 数据库保存，并通过 Core 的已认证管理 API 调用 Agent；网页从不接触 Agent Token。每次调度、触发或“立即运行”都会调用 AI 服务的新会话，不携带其他任务运行的对话上下文。`schedule` 任务按照 60 秒至 7 天的间隔运行。`trigger` 任务在某个属性出现完全一致的 typed state value 时运行，并使用 60 秒至 7 天的冷却时间防止事件循环。触发属性必须是设备当前存在的属性，类型必须匹配，而且在执行时具有 AI 的有效 `read` 或 `confirm` 授权。

每个任务会持久化最近 50 条运行记录，包括来源、时间、AI 返回的 Markdown 内容、设备操作计划和最终状态；这些记录会随数据库备份恢复。任务默认使用 `unattended`（无人值守）执行方式：AI 只有先生成受授权属性的 `awaiting_approval` 计划，自动化才会代表该任务立即批准并执行。执行前 Core 仍会重新校验属性 `confirm` 授权、可写性和状态版本，写入及失败同样进入审计记录。将任务改为 `manual` 时，计划不会自动执行，仍可在两分钟有效期内由管理员批准。普通 AI 对话始终使用人工批准，绝不因自动化设置而获得自动写入权限。

## AI 思考超时

单次上游模型请求最多等待两分钟；Core 与本机 Agent 会为包含工具调用的完整对话保留最多六分钟。网页在等待时会提示不要重复提交。若超过上限，管理 API 返回 `408` 与“AI 思考超时”提示；此时未生成的设备写入计划不会执行，也不会绕过人工批准。
