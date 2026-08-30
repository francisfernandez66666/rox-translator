# 翻译助手 SDK

开放 API（`/openapi/v1/*`）的官方薄客户端，零第三方依赖。
**翻译采用异步任务模型**：提交任务 → 返回 `task_id` → 轮询（文本建议 15s / 文件 60s）→ 终态取结果。

| 语言 | 文件 | 要求 |
|------|------|------|
| Python | `python/translator_sdk.py` | Python 3.8+（仅标准库） |
| JavaScript | `js/translator-sdk.mjs` | Node 18+ 或现代浏览器（fetch/ESM） |

## 快速开始

### 文本翻译（一站式阻塞等待）

```python
# Python
from translator_sdk import TranslatorClient
cli = TranslatorClient("https://translator.example.com", "你的APIKey")
r = cli.translate_and_wait("蓝牙钥匙已激活", ["en", "ja"], mode="pro")
print(r["translations"])   # {"en": "...", "ja": "..."}
print(r["tokens_used"])    # 本次消耗 token
```

```js
// JavaScript
import { TranslatorClient } from "./translator-sdk.mjs";
const cli = new TranslatorClient("https://translator.example.com", "你的APIKey");
const r = await cli.translateAndWait("蓝牙钥匙已激活", ["en", "ja"], "pro");
console.log(r.translations);
```

### 文件批量翻译

```python
# Python：提交 → 轮询 → 下载 zip 产物
t = cli.create_file_task(["手册.docx", "清单.xlsx"], ["en"], mode="pro")
r = cli.wait_task(t["task_id"])                       # 建议间隔自动取 60s
if r["status"] == "completed":
    cli.download_file(t["task_id"], "./产物.zip")     # 多文件自动打包 zip
```

```js
// JavaScript
const t = await cli.createFileTask(files, ["en"], "pro");
await cli.waitTask(t.task_id);
const blob = await cli.downloadFile(t.task_id);
```

### 翻译模式

| mode | 说明 | 典型延迟 |
|------|------|----------|
| `pro`（默认） | 🎓 专业校对：知识库匹配 + 初翻 + 校对 + 双评估 + 文化闸门全流水线 | 分钟级 |
| `fast` | ⚡ 快速：不走知识库，AI 初翻 + 校对 | 十秒级 |

## 接口一览

- `create_task(text, target_langs, mode)` / `create_file_task(files, ...)` — 提交任务
- `get_task(id)` — 轮询；未完成 `status=queued/processing`，终态 `completed/failed`
- `wait_task(id)` / `translate_and_wait(text, ...)` — 阻塞等待封装
- `download_file(id, path, file_id?)` — 文件产物下载（缺省 zip）
- `balance()` — 查询 token 余额与 ≈句数换算
- `kb_stats()` / `usage()` / `rotate_api_key()` — 辅助接口

## 错误处理（独立错误码）

失败响应携带 `error_code` 出参，SDK 抛出 `TranslatorError.error_code`：

| error_code | 处理建议 |
|------------|----------|
| `insufficient_balance` | **余额不足——请充值或升级套餐**后重试 |
| `rate_limited` | 稍后重试 |
| `daily_quota_exceeded` | 次日再试或联系管理员调额 |
| `task_failed` / `not_ready` / `no_result` | 见 message 详情 |

## 计费说明

按任务全链路真实 LLM token 消耗 × 均摊系数（默认 1.5）从余额扣减；
每次轮询响应均携带 `balance_tokens` 与 `balance_sentences_approx`。

## 前置条件

1. 管理后台「开放 API」面板签发 API Key
2. 完整接口文档见服务端 `/openapi/docs`

## API Key 权限范围

签发 Key 时权限（`perms`）仅接受以下取值（服务端按 Key 校验，SDK 不携带该字段）：

| 权限 | 可用接口 |
|------|----------|
| `all` | 全部开放接口（translate + kb + billing + 轮换） |
| `translate` | 任务创建/轮询/下载、余额、同步翻译 |
| `kb` | 知识库统计 |
| `billing` | 用量明细 |

> 注意：权限字符串为 `kb` / `billing` / `translate` / `all`，**不是** `kb/all`、`billing/all`、`translate/all` 之类的写法。

## 待办（需外部资源）

- CI 自动发 PyPI/npm 包（需账号）；当前以源码文件分发
