# Grok Inspection

CPA（CLIProxyAPI）原生插件：在服务端后台巡检 xAI/Grok 账号健康度（权限、额度、登录态），支持完整/增量巡检、建议操作、按筛选批量禁用/删除、结果落盘与导出。

版本：`0.1.2` · 菜单名：**Grok 账号巡检**

---

## 目录

- [整体架构](#整体架构)
- [工作流程](#工作流程)
- [页面按钮与后端流转](#页面按钮与后端流转)
- [对外 / 对内接口一览](#对外--对内接口一览)
- [单账号探测逻辑](#单账号探测逻辑)
- [结果分类与建议动作](#结果分类与建议动作)
- [数据落盘](#数据落盘)
- [安装与构建](#安装与构建)
- [安全说明](#安全说明)

---

## 整体架构

```text
┌─────────────────┐     Management Key      ┌──────────────────┐
│  浏览器 UI 页面  │ ───────────────────────► │  CPA Management  │
│  (插件 Resource) │ ◄── JSON / HTML ─────── │  HTTP API        │
└─────────────────┘                         └────────┬─────────┘
                                                     │ management.handle
                                                     ▼
                                            ┌──────────────────┐
                                            │ grok-inspection  │
                                            │ 插件 (so/dll)    │
                                            │ engine + UI HTML │
                                            └────────┬─────────┘
                         ┌───────────────────────────┼───────────────────────────┐
                         │ host 回调                  │ host 回调                  │ 进程内 HTTP
                         ▼                           ▼                           ▼
                  host.auth.list/get/save      host.http.do              127.0.0.1:8317
                  （读写 CPA Auth 凭证）        （带 token 访问上游）     Management 删凭证
                                                     │
                                                     ▼
                                        cli-chat-proxy.grok.com
                                        /v1/models
                                        /v1/responses  (input: "ping")
                                        /v1/chat/completions  (fallback)
```

要点：

1. 插件以 **C shared library** 形式加载进 CPA 进程（`cgo` + `cliproxy_plugin_init`）。
2. 巡检/批量操作在插件内 **goroutine 异步** 执行，关闭页面不中断。
3. 页面只通过 CPA Management 路由与插件通信；探测 Grok 走 **host.http.do**，不经过浏览器。

---

## 工作流程

### 1. 插件加载

1. CPA 加载 `grok-inspection.dll/.so/.dylib`。
2. 调用 `cliproxy_plugin_init`，注册 `call` / `shutdown`。
3. Host 调用 `plugin.register`、`management.register`，挂上管理路由与菜单「Grok 账号巡检」。
4. 插件 `init` 时尝试从本地 `results.json` **恢复上次结果**。

### 2. 打开页面

1. 浏览器打开 Resource：`GET /v0/resource/plugins/grok-inspection/status` → 返回内嵌 HTML/JS。
2. 用户填写 **CPA Management Key**（仅存浏览器 localStorage）。
3. 页面 **一次性** `GET .../status` 拉当前内存快照（列表/进度）。
4. **空闲时不轮询**；仅当 `running` 或 `applying` 为 true 时每 1.5s 拉 status，结束后自动停止。

### 3. 完整巡检

1. 用户点「开始巡检」→ `POST .../start`（`incremental: false`）。
2. 插件清空内存结果，起后台任务：
   - `host.auth.list` 列出 Auth；
   - 过滤 xAI/Grok（及可选已禁用策略）；
   - 并发（默认 6，范围 1–16）对每个账号 `inspectAccount`；
   - 结果 append + 定期/结束时写入 `results.json`。
3. 前端在 `running=true` 期间轮询 status 刷新进度与表格。
4. 全部完成后 `running=false`，轮询停止。

### 4. 增量巡检

1. 前提：内存/落盘中已有上次结果；否则报错「需要已有结果」。
2. `POST .../start`（`incremental: true`）。
3. **不清空**旧结果；`host.auth.list` 后与已有行比对  
   （`auth_index` / 文件名 / email / name），**只探测新增**。
4. 新结果 append 合并进列表并落盘。

### 5. 操作账号（禁用 / 删除）

- **禁用/启用**：后台 `host.auth.get` → 改 JSON `disabled` → `host.auth.save`（不走 Management HTTP，避免死锁）。
- **删除**：后台 `DELETE http://127.0.0.1:8317/v0/management/auth-files?name=...`（需进程环境密码）→ 校验 list 已消失 → 从结果列表移除并落盘。
- 单条与批量均为 **异步**：接口先返回 202，后台串行执行；status 可读 `apply_*` 进度。

### 6. 关闭 / 卸载

- 关页面：后台任务继续。
- 插件 shutdown：`engine.shutdown()` 停止任务并等待结束。

---

## 页面按钮与后端流转

| UI 控件 | 用户操作 | 前端请求 | 插件行为 | 是否异步 |
|--------|----------|----------|----------|----------|
| Management Key | 输入/变更 | 有 Key 后 `GET /status` 一次 | 返回快照；无 Key 不请求 | — |
| **并发** | 填 1–16 | 随 start 提交 `workers` | 校验，非法 400 | — |
| **包含已禁用** / **仅巡检已禁用** | 勾选 | start body | 过滤目标账号 | — |
| **开始巡检** | 点击 | `POST /start` `{incremental:false,...}` | 清空结果 → 全量探测 | 是（任务后台） |
| **增量巡检** | 点击 | `POST /start` `{incremental:true,...}` | 保留结果 → 只测新增 | 是 |
| **停止** | 点击 | `POST /stop` | `stopped=true`，不再投递新探测 | 即时 |
| **执行建议操作** | 确认后 | `POST /apply` `{}` | 对 `action`∈{disable,enable,delete} 的建议串行执行 | 是 |
| **批量禁用** | 按当前筛选确认 | `POST /apply` `{force_action:"disable", auth_indexes:[...]}` | 强制禁用筛选内账号 | 是 |
| **批量删除** | 按当前筛选确认 | `POST /apply` `{force_action:"delete", auth_indexes:[...]}` | 强制删除筛选内账号 + Auth 文件 | 是 |
| 表格行 **禁用/启用/删除** | 点击 | `POST /action` | 单账号异步执行 | 是 |
| **导出 JSON / TXT** | 点击 | **无服务端请求** | 浏览器按当前筛选下载 | 纯前端 |
| 筛选卡片 / 筛选按钮 | 点击 | 无 | 只改前端 `filter`（导出/批量共用） | — |

### 典型时序：完整巡检

```text
用户点「开始巡检」
  → POST /start  → 立刻返回 snapshot(running=true)
  → 后台: auth.list → 并发 inspect → 写 results.json
  → 前端 startPolling: 每 1.5s GET /status
  → running=false 后 stopPolling，列表保留
```

### 典型时序：批量删除（当前筛选「需重登」）

```text
用户筛「需重登」→ 点「批量删除」
  → POST /apply { force_action:"delete", auth_indexes:[...] }
  → 立刻 202 + applying=true
  → 后台逐条: DELETE auth-files → 校验 → 本地 results 删行 → 落盘
  → 前端轮询 status 看 apply_done/apply_total
  → applying=false 后停轮询
```

### Status 轮询策略

| 状态 | 是否请求 `/status` |
|------|-------------------|
| 打开页面 / 改 Key | 1 次 |
| `running` 或 `applying` | 每 1.5s |
| 空闲 | **不请求** |

`/status` **只读内存快照**，不会触发 Grok 探测，也不会改 Auth。

---

## 对外 / 对内接口一览

### A. 浏览器 ↔ CPA（需 Management Key）

Base：`/v0/management/plugins/grok-inspection`

| 方法 | 路径 | 作用 | 请求体要点 | 响应要点 |
|------|------|------|------------|----------|
| GET | `/status` | 进度 + 结果列表 | — | `running/applying/done/total/results/summary/...` |
| POST | `/start` | 开始巡检 | `workers`, `include_disabled`, `only_disabled`, `incremental` | 立即 snapshot |
| POST | `/stop` | 停止巡检 | `{}` | snapshot |
| POST | `/apply` | 批量建议或强制操作 | 见下 | **202** + snapshot（后台跑） |
| POST | `/action` | 单条操作 | `name`/`auth_index`, `disabled`, `delete` | **202** + snapshot |

Resource（菜单页，一般不需 Key）：

| 方法 | 路径 | 作用 |
|------|------|------|
| GET | `/v0/resource/plugins/grok-inspection/status` | 返回巡检 HTML 页面 |

#### `/start` body

```json
{
  "workers": 6,
  "include_disabled": false,
  "only_disabled": false,
  "incremental": false
}
```

#### `/apply` body

```json
// 执行建议：只处理 results 里 action 为 disable/enable/delete 的行
{}

// 按筛选强制禁用/删除（与导出同源：前端传入当前筛选的 id）
{
  "force_action": "disable",   // 或 "delete" / "enable"
  "auth_indexes": ["auth_index_or_file_or_name", "..."]
}
```

#### `/action` body

```json
{
  "auth_index": "...",
  "name": "显示名或文件名",
  "disabled": true,
  "delete": false
}
```

---

### B. 插件 ↔ CPA Host（插件 ABI 回调，无 HTTP）

| Host 方法 | 使用场景 | 做什么 |
|-----------|----------|--------|
| `host.auth.list` | 巡检列账号；删除/禁用前查找；删除后校验 | 列出 Auth 凭证元数据 |
| `host.auth.get` | 取 token 探测；禁用时读 JSON | 按 `auth_index` 取凭证 JSON |
| `host.auth.save` | 禁用/启用 | 写回 `disabled` 字段到 Auth 文件 |
| `host.http.do` | 探测 Grok | 用解析出的 Bearer token 请求上游 HTTP |

插件 **未使用** 的 host 能力示例：`host.auth.get_runtime`、stream、model.execute 等。

---

### C. 插件 ↔ 上游 Grok（经 `host.http.do`）

统一请求头（对齐 Grok CLI / CPA xAI executor）：

- `Authorization: Bearer <token>`
- `X-XAI-Token-Auth: xai-grok-cli`
- `x-grok-client-version: 0.2.93`
- `User-Agent: xai-grok-workspace/0.2.93`

| 顺序 | 方法 | URL | Body | 作用 |
|------|------|-----|------|------|
| 1 | GET | `https://cli-chat-proxy.grok.com/v1/models` | — | 选模型（优先 grok-4.5-build-free / grok-4.5 / …） |
| 2 | POST | `https://cli-chat-proxy.grok.com/v1/responses` | `{"model":"...","input":"ping","stream":false}` | **主探测**（内容是 `ping`，不是 hello） |
| 3 | POST | `https://cli-chat-proxy.grok.com/v1/chat/completions` | `messages:[{role,user,content:"ping"}]` | 主探测返回 401/403/429/402 时 **fallback** |

Token 从 auth JSON 字段依次取：`access_token` → `token` → `api_key` → `id_token`。

---

### D. 插件 ↔ 本机 CPA Management HTTP（仅删除）

| 方法 | URL | 作用 | 鉴权 |
|------|-----|------|------|
| DELETE | `http://127.0.0.1:8317/v0/management/auth-files?name=<文件名>` | 删除 Auth 物理凭证 | 优先复用页面请求的 `Authorization` / `X-Management-Key`；环境变量 `MANAGEMENT_PASSWORD` / `CPA_MANAGEMENT_KEY` 作 fallback |

说明：

- **必须在 management.handle 返回之后的后台 goroutine 里调用**，避免 Management 重入死锁。
- HTTP Client 超时 **8s**。
- 管理 API 固定打本机 CPA（`127.0.0.1:8317`，或 `CPA_BASE_URL` / `PORT` / `CPA_PORT`），**不会**用浏览器反代 Host 端口。
- Disable/enable/delete 会复用页面 Management Key（请求头）；无 Key 时才回退到进程环境变量。

禁用/启用 **不再** 调 `PATCH .../auth-files/status`，改走 host.auth.save。

---

### E. 插件 ABI 入口（CPA → 插件）

| method | 作用 |
|--------|------|
| `plugin.register` / `plugin.reconfigure` | 元数据与能力注册 |
| `management.register` | 注册管理路由与 Resource 菜单 |
| `management.handle` | 处理上述 status/start/stop/apply/action 与 Resource HTML |
| shutdown | 停止巡检/批量任务 |

---

## 单账号探测逻辑

对应 `inspectAccount`：

```text
host.auth.get → 取 token
    → GET /v1/models → pickModel（失败则默认 grok-4.5）
    → POST /v1/responses  input="ping"
    → 若 401/403/429/402 → POST /v1/chat/completions content="ping"
    → classifyProbe(HTTP 状态 + 错误码/文案 + 是否已禁用)
    → 得到 classification / action / reason
```

**不解析模型自然语言回复**；以 HTTP 状态与错误 JSON 分类即可。

---

## 结果分类与建议动作

| classification | 含义 | 默认 action |
|----------------|------|-------------|
| `healthy` | 对话探测 2xx | `keep`；若账号已禁用则 `enable` |
| `permission_denied` | 403/402 等权限问题 | `disable`（已禁用则 `keep`） |
| `quota_exhausted` | 429 / 额度用尽文案 | `disable`（已禁用则 `keep`） |
| `reauth` | 401 / token 失效 | `delete`（建议删凭证后重登） |
| `model_unavailable` | 模型不可用 | `keep` |
| `probe_error` | 网络/解码/缺 auth_index 等 | `keep` |

「执行建议操作」只处理 action 为 `disable` / `enable` / `delete` 的行。  
「批量禁用/删除」用 `force_action`，**不依赖**建议，只跟当前筛选列表走。

---

## 数据落盘

路径（相对 CPA 工作目录）：

```text
data/grok-inspection/results.json
```

或环境变量：`GROK_INSPECTION_DATA_DIR/results.json`。

内容为**轻量展示结果**（账号标识 + 分类 + 建议 + HTTP/模型等），**不含** Auth 目录里的 token / 完整凭证 JSON。

写入时机：

- 完整巡检开始（清空后写）
- 探测过程中每完成约 10 条
- 巡检结束
- 禁用/删除成功后

---

## 安装与构建

### 要求

- CPA 支持原生插件
- 本地构建：Go 1.21+ 与 C 编译器（`-buildmode=c-shared`）
- 删除账号：CPA 进程配置 `MANAGEMENT_PASSWORD` 或 `CPA_MANAGEMENT_KEY`

### 本地构建

```bash
# Linux/macOS
./build.sh

# Windows
./build.ps1
```

产物：`dist/grok-inspection.{so|dll|dylib}`

### GitHub Actions（无需本机 Go）

| 工作流 | 触发 | 产物 |
|--------|------|------|
| `.github/workflows/ci.yml` | push / PR / 手动 | 三端 artifact（Actions 页下载） |
| `.github/workflows/release.yml` | tag `v*.*.*` | GitHub Release zip + checksums |

```bash
git push origin main          # 触发 CI
git tag v0.1.2 && git push origin v0.1.2   # 触发 Release
```

### 安装到 CPA

```text
plugins/windows/amd64/grok-inspection.dll
plugins/linux/amd64/grok-inspection.so
plugins/darwin/arm64/grok-inspection.dylib
```

`config.yaml`：

```yaml
plugins:
  enabled: true
  configs:
    grok-inspection:
      enabled: true
      priority: 1
```

重启 CPA，打开：

```text
/v0/resource/plugins/grok-inspection/status
```

---

Disable/enable/delete actions reuse the same Management Key from the page
request Authorization header. Environment variables `MANAGEMENT_PASSWORD` or
`CPA_MANAGEMENT_KEY` remain optional fallbacks for headless container setups.

## 安全说明

- 巡检后**不会**自动禁用/删除，必须用户确认操作。
- 批量删除会删 CPA Auth 凭证文件，需重新登录才能恢复。
- `permission_denied` / 额度用尽只是建议，不代表必须删文件。
- 勿把 Management Key、Auth JSON、results 中的账号隐私提交到公开仓库。
- 页面 Management Key 会传给插件后台删号；未配置时仍可用进程环境密码作 fallback。

---

## 源码结构（逻辑对应）

| 文件 | 职责 |
|------|------|
| `cgo_bridge.go` | 插件 ABI、`callHost` |
| `main.go` | 注册路由、`dispatchManagement` |
| `engine.go` | 巡检/增量/批量/探测/禁用删除 |
| `classify.go` | 账号过滤与结果分类 |
| `store.go` | `results.json` 读写 |
| `ui.go` | 管理页 HTML/JS |
| `host.go` | envelope 编解码辅助 |

---

## License

MIT
