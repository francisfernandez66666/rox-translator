# 主站 AI 助手（assist）— 轻量 AI 销售/客服接待

源码位置：`backend-go/internal/assist/*` + 入口 `backend-go/cmd/assist-server`。
（★ 改造 1A，2026-09-17：原独立仓库/独立 module `ai-assist/` 已并入主仓单 module，
依赖与主服务共用一份 `go.mod`；独立 SQLite 数据隔离保持不变。）

为「能言」官网与产品前端提供 **常驻 AI 接待**：自动迎接访客、回答销售与使用问题、
推荐功能入口（深链一键直达）。

```
访客提问 → 知识库/话术检索 → LLM(多模型降级) 或 规则兜底 → 回复 + 功能入口按钮
                ↑
超管后台：知识库 / 话术 / 流程 / 功能入口 / 配置 / 会话记录（主后台内嵌管理台）
```

## 组成

| 位置 | 说明 |
|---|---|
| `cmd/assist-server/` | 单二进制入口（Go、零 CGO；产物建议命名 `translator-assist`） |
| `internal/assist/llm/` | OpenAI 兼容多模型降级链（主模型→备用→规则兜底，冷却恢复） |
| `internal/assist/engine/` | 检索打分、Prompt 组装、流程状态机、动作提取 |
| `internal/assist/store/` | SQLite 存储（会话/消息/知识/话术/流程/入口/配置）+ 主库只读桥接 |
| `internal/assist/api/` | C 端接待接口 + 管理端接口（Token 鉴权）+ 管理台托管 |
| `internal/assist/web/admin.html` | 管理台（单文件零构建）——**经 go:embed 随二进制编译** |
| `internal/assist/seed/seed.json` | 内置知识库/话术/流程/功能入口——**经 go:embed 随二进制编译** |

## 快速开始

```bash
cd backend-go
go build -o dist/translator-assist ./cmd/assist-server

# 1) 纯规则模式（零配置，话术/知识库直出，用于演示）
ASSIST_MOCK=1 ./dist/translator-assist

# 2) 接真实模型（硅基流动/OpenAI 兼容均可）
ASSIST_LLM_BASE_URL=https://api.siliconflow.cn/v1 \
ASSIST_LLM_API_KEY=sk-xxx \
ASSIST_LLM_MODEL=deepseek-ai/DeepSeek-V4-Flash \
ASSIST_LLM_MODEL_BACKUP=THUDM/glm-4-9b-chat \
./dist/translator-assist
```

- 服务地址：`http://127.0.0.1:8790`（`ASSIST_ADDR` 可改）
- 健康检查：`/health`
- 管理台：`/assist/admin`

### 单元测试 / UAT

```bash
cd backend-go && go test ./internal/assist/...      # 27 个单测函数（已随 CI backend job 跑）
bash scripts/uat/assist_uat.sh                       # 32 断言端到端（CI assist job）
```

## 管理台 Token（★ 改造 1A：免手填）

Token 解析链（`translator-assist` 服务启动时按序取第一个可用值）：

| 序 | 来源 | 说明 |
|---|---|---|
| 1 | env `ASSIST_ADMIN_TOKEN` | 部署侧显式配置，**优先级最高**（保底）；主服务为 PostgreSQL 时必须走这条 |
| 2 | 主库 `system_config.assist_admin_token` | `enc:v1:` 密文；assist 以**只读**方式连主库 SQLite 读取（`MAIN_DB` 指路径） |
| 3 | 内置默认值 | 仅本地/演示；生产必须至少配 1 或 2 |

主后台「AI 助手」面板挂载时调用 `/api/admin/assist/token`（仅超管）取生效 Token，
写入同源 `localStorage['assist_tok']`；iframe 内的管理台加载时读取该键并静默校验，
**用户不再需要手工粘贴**。同一面板也提供「保存 Token」用于轮换库内配置（密文落库）。

> 注意：`/assist-api` 为**同源**路径反代（Caddy `uri strip_prefix`），父页面与 iframe 共用
> 同一 localStorage 才能自动注入。若部署为跨域 iframe，需回退到管理台右上角手工粘贴。

## 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `ASSIST_ADDR` | `127.0.0.1:8790` | 监听地址 |
| `ASSIST_DB` | `data/assist.db` | 本服务 SQLite 路径（与业务库隔离） |
| `ASSIST_ADMIN_TOKEN` | 空 | 管理台/管理 API Token（优先级最高，建议生产配置） |
| `MAIN_DB` | `$USER_DATA_DIR/tm.sqlite3` | 主服务 SQLite 路径（只读桥接取管理 Token；`DB_DRIVER=postgres` 时忽略） |
| `ASSIST_CORS` | `*` | 允许跨域来源（逗号分隔） |
| `ASSIST_SEED` | 空 | 外置 seed 路径；留空用**内嵌** seed |
| `ASSIST_WEB` | 空 | 外置管理页目录；留空用**内嵌**页面 |
| `ASSIST_MOCK` | 关 | 1=不调 LLM，纯规则兜底 |
| `ASSIST_LLM_BASE_URL` | 空 | OpenAI 兼容 base URL（★ 亦可经管理台在线配置，保存即生效；env 显式配置优先） |
| `ASSIST_LLM_API_KEY` | 空 | 主模型 Key（★ 同上；管理台以掩码回显） |
| `ASSIST_LLM_MODEL` | 空 | 主模型名（★ 同上） |
| `ASSIST_LLM_MODEL_BACKUP` | 空 | 备用模型名（失败自动降级；★ 同上） |
| `ASSIST_LLM_API_KEY_BACKUP` | 空 | 备用 Key（空则复用主 Key） |
| `ASSIST_LLM_TIMEOUT` | `45` | 单模型超时秒数 |

> 日志（★ 改造 1A）：已接入主仓 `internal/observability` 的 slog JSON 日志器，
> 与主服务同格式并带 `trace_id`（HTTP 请求上下文贯穿 LLM 调用链）。

## 与主站（能言）集成

前端已内置常驻挂件（`frontend-react/src/components/AiAssist.tsx`）：
除登录/注册页外，官网落地页、前台工作台、管理后台右下角常驻。

接口约定见 `frontend-react/src/api/assist.ts`，基址默认 `/assist-api`（同源反代）。

### 开发环境

```bash
# 终端 1：assist 服务
cd backend-go && go build -o dist/translator-assist ./cmd/assist-server && ASSIST_MOCK=1 ./dist/translator-assist
# 终端 2：主站前端（vite 已配置 /assist-api 代理到 8790）
cd frontend-react && npm run dev
```

### 生产环境（Caddy）

在主站站点配置中追加（caddy 配置已含 CSP 互斥分流，管理台可被主站同源 iframe 嵌入）：

```caddy
# AI 助手反代（挂件与管理台同走此路径）
handle /assist-api/* {
    uri strip_prefix /assist-api
    reverse_proxy 127.0.0.1:8790
}
```

systemd 见 `deploy/systemd/ai-assist.service`（`ExecStart=/opt/ai-assist/bin/translator-assist`）。

### 管理后台入口

主站「管理后台 → 🤖 AI 助手」（L3+ 可见），内嵌管理台：
知识库、话术、流程、功能入口、全局配置、会话记录全部可视化增删改。

## API

C 端（免登录，会话自证）：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/assist/greeting?session=&page=` | 开场：欢迎词 + 快捷提问 chips + 会话 ID |
| POST | `/api/assist/chat` | `{session,message,page}` → `{reply,actions[],model,source}` |
| GET | `/api/assist/history?session=&limit=` | 历史消息（刷新回显） |
| GET | `/api/assist/features` | 启用中的功能入口 |

管理端（Header `X-Assist-Admin: <token>`）：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET/PUT | `/api/assist/admin/config` | 读/写单项配置（welcome/persona/temperature/…） |
| GET/POST/PUT/DELETE | `/api/assist/admin/kb` | 知识库 CRUD |
| 同上 | `/api/assist/admin/scripts` | 话术 CRUD（greeting/keyword/fallback） |
| 同上 | `/api/assist/admin/flows` | 流程 CRUD（steps JSON） |
| 同上 | `/api/assist/admin/features` | 功能入口 CRUD |
| GET | `/api/assist/admin/sessions` | 会话列表 + 统计 + 未答问题清单 + llm_mode |
| POST | `/api/assist/admin/llm/test` | LLM 测试连通（回显模型/耗时/错误） |

主后台侧（★ 改造 1A，仅超管）：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/admin/assist/token` | 下发生效管理 Token + 来源（env/db/none） |
| POST | `/api/admin/assist/token` | 轮换（`{"token":"…"}` 密文落库；空串=清除回落 env） |

## 检索与回复来源（R0 增补）

- **分级召回（★ 任务 #58，2026-09-22）**：知识库检索三级打分，编排语义不变（话术直配 → 知识命中 → LLM 融合 → 零命中兜底）。
  第 1 级＝精确/同义词关键词命中（恒压过后续级别，话术与流程触发**只用第 1 级**，保证直配零误伤）；
  第 2 级＝无外部依赖的中文相似度（bigram 包含/Dice + 字符集包含，配全半角/繁简/标点/语气词归一，阈值保守）；
  第 3 级＝向量召回（**默认关闭**）：`configs.embed_recall=on` 且 `configs.embed_model=<嵌入模型名>` 时启用，
  复用 `llm_base_url/llm_api_key` 走 OpenAI 兼容 `/embeddings`；未配置、调用失败或冷却期内静默回落第 2 级，绝不报错给前端。
  黄金集回归闸门见 `golden_test.go`（命中率棘轮，当前 40/40，下限钉死在常量注释）。
- **同义词归一**：`configs.synonyms` 每行「标准词=同义词1|同义词2」，输入命中同义词按标准词计分（管理台在线编辑，保存即生效）。seed 预置 充值/翻译/价格 三组；存量部署需在管理台粘贴一次。
- **未答问题登记**：知识/话术/流程三层零命中时，输入去重登记进 `configs.unanswered_questions`（上限 200），管理台「会话记录 → 待补料问题」可见，运营据此补知识库。

## 回复来源（source 字段）

| source | 含义 |
|---|---|
| `rule` | 关键词话术直配（毫秒级，未走 LLM） |
| `flow` | 引导流程步骤输出 |
| `llm` | 大模型生成（知识库注入） |
| `fallback` | LLM 不可用时的知识库/兜底话术 |

## 设计取舍

- **独立进程、数据隔离**：assist 仍是独立服务与独立 SQLite——访客免登录服务不应碰业务库；
  融合的是「仓库/构建/观测/鉴权/CI」五件事（见改造 1A），不是数据库。
- **挂件降级**：assist 挂掉只影响挂件（显示离线提示），不阻塞产品功能。
- **无鉴权 C 端**：会话 ID 自证（localStorage 持久化），轻量化处理。
- **种子只灌一次**：seed 仅在对应表为空时灌入，后台编辑不会被覆盖；想重置删库重启即可。
- **降级链移植自 ai-scrm**：每次从主模型开始尝试、失败降级、3 连败冷却 5 分钟、链级总预算防挂死。

## 遗留观察项

- 管理页 `web/admin.html` 为单文件内联 JS（600+ 行），融合 A 阶段未动它；
  B 阶段可考虑 React 化并入主前端（目前 iframe 方案可用性尚可）。
- 主服务切换 PostgreSQL 后 `MAIN_DB` 桥接不适用，需显式配置 `ASSIST_ADMIN_TOKEN`。
