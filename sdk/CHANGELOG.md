# SDK 版本记录（CHANGELOG）

> 三端（npm `@langcross/translator-sdk` / PyPI `langcross-translator` / Maven `com.langcross:translator-sdk`）
> 版本号由 `scripts/release-sdk.sh` 统一 bump 与校验，保持一致。
> 格式参考 Keep a Changelog；日期为发布日期。

## [1.0.0] - 2026-09-15（首个可发布基线）

### 新增
- **发布管线**：`scripts/release-sdk.sh`（版本同步校验 → tsc 构建 → sdist/wheel 构建 →
  npm/PyPI 发布，缺省 dry-run，`--publish` 显式开闸）；
  TS `prepublishOnly` 钩子（先 typecheck+build 再打包，防裸 src 上 npm）；
  Python `pyproject.toml`（零依赖单模块 `translator_sdk`，`__version__` 对齐）。
- **TypeScript SDK**（`@langcross/translator-sdk`）：
  - 异步任务模型：`createTask`（JSON 文本 / multipart 文件批量）→ `waitTask` 轮询 →
    `getTask`/`downloadFile`（`fileId` 缺省打包 zip 全部产物）；
  - `balance`/`usage`/`kbStats` 出参含双桶余额口径；
  - （同步短文翻译走 `POST /openapi/v1/translate`，TS SDK 暂未封装，直连即可）；
  - `rotateApiKey` 密钥轮换；错误码 `invalid_api_key` / `key_quota_exceeded` /
    `insufficient` / `not_ready` 等以 `OpenAPIError(code)` 抛出。
- **Python SDK**（`langcross-translator`）：与 TS 同契约（`TranslatorClient.translate_text`
  / `translate_files` / `download_file` / `balance`），urllib 标准库实现，内网/受限环境可直拷。
- **Java SDK**：OkHttp + Jackson，同契约（`TranslatorClient`）。
- **JS 浏览器版**（`translator-sdk.mjs`）：fetch 实现，供无构建工具站点直挂。

### 契约基线（对客文档承诺）
- 认证：`Authorization: Bearer <API Key>`（管理后台「API Key」面板签发，可设日调用限额）。
- 端点：`POST /openapi/v1/tasks`、`GET /openapi/v1/tasks/status`、
  `GET /openapi/v1/tasks/download`（多产物缺省 zip）、`GET /openapi/v1/balance`、
  `POST /openapi/v1/translate`（同步短文，≤2000 字符）、`POST /openapi/v1/apikey/rotate`。
- 文件任务：每目标语言独立产物（文件名含语言码，如 `doc_en.docx`）；
  还原失败按产品决策降级 `.md` 纯文案并回 `warning`，不整单判死。

### 已知限制（将在后续版本跟进）
- SDK 未内置指数退避抖动重试（轮询为固定间隔）；
- Node 侧大文件 multipart 未做流式封装（≤30MB 上限内一次读入）。

[1.0.0]: 内部首发基线（此前 SDK 仅随源码分发，未上 registry）
