# SDK 版本记录（CHANGELOG）

> 三端（npm `@langcross/translator-sdk` / PyPI `langcross-translator` / Maven `com.langcross:translator-sdk`）
> 版本号由 `scripts/release-sdk.sh` 统一 bump 与校验，保持一致。
> 格式参考 Keep a Changelog；日期为发布日期。

## [1.0.2] - 2026-09-26（对外契约状态码诚实：SDK 侧跟随改版）

### 变更（三端一致，语义向后兼容）
- **错误码取法**：`code` 为正主（与对外文档 `Error.code` 枚举同名同值），`error_code` 降为
  `<1.0.4` 别名并在响应体里同值下发。四端（Python / TypeScript / Java / 浏览器 JS）统一改成
  **"code 优先、error_code 兜底"** ⇒ 打新服务端读 `code`、打老服务端读 `error_code`，两边都拿得到码。
  旧属性名一个都没删（`error_code` / `errorCode` 仍在），升级 SDK 不需要改调用方代码。
- **限流退避时长**：429 时新增可读字段（Python `retry_after`、TS `retryAfter`、
  Java `retryAfterSeconds`、JS `retryAfter`）：取 JSON `retry_after`，取不到再取 HTTP
  `Retry-After` 头（中间层可能只透传其中之一）；只认纯数字秒，非限流为 `None`/`undefined`/`null`。
  由来：同一仓里曾有两套限流口径（登录锁定回 400 且不给时长，注册/验证码回 429 给 `Retry-After`），
  客户端只能瞎猜退避窗口——猜短了继续撞闸，猜长了用户白等（服务端侧见批 I-7 / F-47）。
- **`downloadFile` 失败不再只剩一行状态码**（TypeScript + 浏览器 JS）：旧写法
  `throw new TranslatorError("下载失败 HTTP " + status, status)` 把**错误体整个丢掉**，
  于是"产物没翻完（该等）""Key 没权限（该找管理员）""任务 id 写错（该改代码）"在调用方眼里
  长得一模一样。现在解析错误体并带出 `message` / `code` / `retryAfter`。
- **2xx 上的 JSON 错误体守卫保留**（TS `downloadFile` 的 R-L2 判据、Python/JS 的 `success:false` 判据）：
  服务端已改为发真实状态码，但"新 SDK 打老后端"的混跑窗口是真实存在的，
  判据删掉就会有人把内容为 JSON 的"假产物"存成 `.zip`。
- **任务 `status:"failed"` 仍走 200 正常返回**（四端注释均已写明口径）：那是**任务的业务状态**，
  不是本次请求的失败；请求层面的失败（400/401/402/403/404/409/429/500）一律在传输层抛出。

### 版本与发布管线
- **三端版本回到一致**：`1.0.2`（python `pyproject.toml` + `__version__` / typescript `package.json` /
  java `pom.xml`）。此前 TS 在 09-16 单独 bump 到 1.0.1、python 与 java 停在 1.0.0，
  而 `scripts/release-sdk.sh` 的缺省动作就是校验"三端版本一致"——
  **这条一致性闸从 09-16 起就一直把 SDK 发布拦停在第一步**，本批随改动一并恢复。
- 托管产物已重打：`frontend-react/public/sdk/*-1.0.2.*` + `-latest` 别名 + `manifest.json` + `.sha256`
  （`scripts/build_sdk.sh` → `--check` 绿）。★ 生效条件：产物在 `public/` 下，**只换 `/opt/translator/web` 即生效**，
  不需要换后端二进制。

### 补断言（不发版就等于没修）
- Python：`test_translator_sdk` 新增 429 取字段/取头、非限流不造时长、老服务端只有 `error_code`
  仍取到码、`downloadFile` 409 不落盘（19 例全绿）。
- TypeScript：`client.test.mjs` 新增 403 双键同值、只有别名的混跑、`retry_after` 字段与
  `Retry-After` 头两条腿、`downloadFile` 409 带出 message/code 且失败不落盘（13 例全绿）；
  fetch mock 补 `headers.get()` 形状——不给它，"头兜底"那条分支会被永久短路成假绿。
- Java：本机无 JDK，已用 `javac 17` 对 jackson API 桩编译通过（**类型检查级别，非真依赖编译**），
  发布前需在带 maven 的环境跑一次 `mvn -q package`。

## [1.0.1] - 2026-09-16（追溯补记：当时只改了 CHANGELOG 以外的版本源）

### 修复
- TypeScript `waitTask` 的显式轮询间隔被吞：`interval ?? st.type === "files" ? 60 : 15`
  实际解析为 `(interval ?? cond) ? 60 : 15`，传进来的 `interval` 恒失效（改为显式加括号）。
- Java SDK 删除虚假能力宣称（文件批量/下载并未实现，改为在类注释里写清边界）。

### 为什么当时没记
- 该批只 bump 了 `sdk/typescript/package.json`（1.0.0 → 1.0.1），python/java 未同批 bump，
  也没写条目 ⇒ 正是上面"三端版本一致性闸被自己拦停"的起点。此处补记以免下一个人再猜 1.0.1 是什么。

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

[1.0.2]: 随批 I-7（F-47＋F-64①）改动，本地已 bump 并重打托管产物；npm/PyPI 正式发布仍需显式 `scripts/release-sdk.sh 1.0.2 --publish`
