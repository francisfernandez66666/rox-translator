# 能言 LangCross VS Code 插件（★ H12）

在 VS Code 侧边栏调用能言平台开放接口完成划译与文本翻译，配合平台 TMX 导入/导出端点与 Trados Studio / memoQ 形成翻译记忆双向同步闭环。

## 安装与调试

- 开发调试：VS Code 打开本目录 → F5（Extension Development Host）。
- 打包分发：`npx @vscode/vsce package` 生成 `.vsix` → 扩展页「...」→ Install from VSIX。
- 依赖 VS Code ≥ 1.75（使用 `fetch` 全局对象与 Webview 视图）。

## 配置

| 配置项 | 说明 |
| --- | --- |
| `langcross.serverUrl` | 平台地址，如 `https://translate.example.com`（默认 `http://127.0.0.1:8787`） |
| `langcross.apiKey` | 开放接口 API Key（后台「API Keys」页生成，需 translate 权限）；建议用命令录入存入 SecretStorage |
| `langcross.targetLangs` | 默认目标语言代码列表（最多 5 个），如 `["en","de"]` |
| `langcross.mode` | `fast` 快速 / `pro` 专业校对 |

命令面板：
- `能言：设置 API Key`（存入本机 SecretStorage，优先于明文配置）
- `能言：设置服务地址`
- `能言：检索术语表`（输入术语→列表选择→回车复制译文；侧边栏亦有「查术语」小面板）
- `能言：翻译编辑器选中文本`（默认快捷键 Cmd/Ctrl+Alt+T，翻译后就地替换，多语言结果弹窗选择）

侧边栏「能言翻译」工作台：输入文本 → 翻译 → 按语言卡片展示译文。

## 接口约定

- `POST {serverUrl}/openapi/v1/translate`，`Authorization: Bearer <apiKey>`
- 请求 `{"text":"…","target_langs":["en"],"mode":"fast"}`
- 响应 `{"success":true,"translations":{"en":"…"}}`；单次上限 2000 字符，长文走 `POST /openapi/v1/tasks` 异步任务。
- `GET {serverUrl}/openapi/v1/terms?q=服务器&lang=en&limit=20`（术语检索，`translate`/`kb` 权限 Key 均可用）→ `{"success":true,"terms":[{"source":"服务器","target":"server","source_lang":"zh","target_lang":"en","package":"企业术语包","exact":true}]}`。
- 计费/配额沿用平台开放接口口径（429=Key 日限额、余额不足/日配额错误透传 message）。

## 与 Trados / memoQ 的翻译记忆同步（★ H12 配套）

两类工具均以 TMX 1.x 为标准交换格式，平台提供双向通道：

1. **平台 → Trados/memoQ（导出）**：`GET /api/translation/export-tmx[?lang=de&module=approved]`（部门管理员及以上，浏览器直接下载 `.tmx`）
   - `lang` 仅导出该目标语言非空的句对（增量迁移常用）；`module` 按记忆来源过滤（如 `approved`=人工确认句对）。
   - Trados：翻译记忆 → 导入 → TMX；memoQ：主存储库 → 导入 → TMX 文件，源语言 `zh-CN`。
2. **Trados/memoQ → 平台（导入）**：在工具中导出 TMX 后走平台既有 `POST /api/translation/import-tmx`（后台知识库页上传），按 zh_hash 幂等去重覆盖。
3. **句对级实时回流**：平台译后反馈（H4）低分句对自动进入 TM 审核队列，人工确认后写入记忆库，下次导出即包含——客户工具无需实时连接，按里程碑导出即可。

> 说明：本插件不直接改写编辑器以外数据；Trados/memoQ 同步以文件交换为边界（无网络直连插件），与两工具主流企业实践一致。
