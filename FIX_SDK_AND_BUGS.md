# SDK 鉴权/路径统一 + 后端 Bug 修复文档

> 生成时间：2026-08-30  
> 状态：待执行

---

## 一、问题清单

### P0-1: go.mod 版本号无效

**文件**：`backend-go/go.mod:3`

```
当前值：go 1.26.5
问题：Go 不存在 1.26.5 版本（当前最新稳定版为 1.22.x）
影响：部分 Go 工具链可能拒绝编译
修复：改为 go 1.22
```

### P0-2: config.go 环境变量赋值 Bug

**文件**：`backend-go/internal/config/config.go:280-281`

```go
// 当前代码（错误）
if v := os.Getenv("ONLINE_API_BASE"); v != "" {
    c.EmbedAPIBase = v  // ← 赋错字段！应该是 OnlineAPIBase
}

// 修复后
if v := os.Getenv("ONLINE_API_BASE"); v != "" {
    c.OnlineAPIBase = v
}
```

**影响**：配置 `ONLINE_API_BASE` 环境变量后，翻译 API 基地址不会生效（仍用默认值），而 Embed API 基地址被意外覆盖。

### P0-3: SDK 鉴权头不一致

| SDK | 当前鉴权头 | 应统一为 |
|-----|-----------|---------|
| Python | `Authorization: Bearer {key}` | `Authorization: Bearer {key}` ✅ |
| JavaScript | `Authorization: Bearer {key}` | `Authorization: Bearer {key}` ✅ |
| TypeScript | `X-API-Key: {key}` ❌ | `Authorization: Bearer {key}` |
| Java | `X-API-Key: {key}` ❌ | `Authorization: Bearer {key}` |

**后端实际实现**：`Authorization: Bearer`（与 Python/JS 一致）

### P0-4: SDK API 路径前缀不一致

| SDK | 当前路径 | 应统一为 |
|-----|---------|---------|
| Python | `/openapi/v1/*` ✅ | `/openapi/v1/*` |
| JavaScript | `/openapi/v1/*` ✅ | `/openapi/v1/*` |
| TypeScript | `/*` ❌ | `/openapi/v1/*` |
| Java | `/*` ❌ | `/openapi/v1/*` |

---

## 二、修复方案

### 2.1 go.mod 版本修复

```
文件：backend-go/go.mod
修改：第 3 行 go 1.26.5 → go 1.22
```

### 2.2 config.go 修复

```
文件：backend-go/internal/config/config.go
修改：第 281 行 c.EmbedAPIBase = v → c.OnlineAPIBase = v
```

### 2.3 TypeScript SDK 统一

**文件**：`sdk/typescript/src/index.ts`

| 行号 | 当前值 | 修复值 |
|------|--------|--------|
| 4 | `// 鉴权：X-API-Key 头。` | `// 鉴权：Bearer Token。` |
| 52 | `"X-API-Key": this.apiKey` | `"Authorization": \`Bearer ${this.apiKey}\`` |
| 84 | `"/tasks"` | `"/openapi/v1/tasks"` |
| 110 | `"/tasks"` | `"/openapi/v1/tasks"` |
| 117 | `` `/tasks/status?id=${taskId}` `` | `` `/openapi/v1/tasks/status?id=${taskId}` `` |
| 124 | 默认轮询 30s | 改为按类型判断（文本 15s / 文件 60s） |
| 143 | `` `/tasks/download?id=...` `` | `` `/openapi/v1/tasks/download?id=...` `` |
| 144 | `"X-API-Key": this.apiKey` | `"Authorization": \`Bearer ${this.apiKey}\`` |
| 152 | `"/balance"` | `"/openapi/v1/balance"` |
| 157 | `"/kb/stats"` | `"/openapi/v1/kb/stats"` |
| 162 | `"/usage"` | `"/openapi/v1/usage"` |
| 167 | `"/keys/rotate"` | `"/openapi/v1/apikey/rotate"` |

### 2.4 Java SDK 统一

**文件**：`sdk/java/src/main/java/com/langcross/sdk/TranslatorClient.java`

| 行号 | 当前值 | 修复值 |
|------|--------|--------|
| 18 | `// 鉴权：X-API-Key 头。` | `// 鉴权：Bearer Token。` |
| 36 | `.header("X-API-Key", apiKey)` | `.header("Authorization", "Bearer " + apiKey)` |
| 65 | `"/tasks"` | `"/openapi/v1/tasks"` |
| 76 | `"/tasks/status?id="` | `"/openapi/v1/tasks/status?id="` |
| 102 | `"/balance"` | `"/openapi/v1/balance"` |
| 107 | `"/usage"` | `"/openapi/v1/usage"` |
| 112 | `"/kb/stats"` | `"/openapi/v1/kb/stats"` |
| 117 | `"/keys/rotate"` | `"/openapi/v1/apikey/rotate"` |

---

## 三、验证清单

| 验证项 | 方法 | 预期结果 |
|--------|------|----------|
| Go 编译 | `cd backend-go && go build ./...` | 编译通过 |
| config 环境变量 | 设置 `ONLINE_API_BASE` 启动后检查日志 | 翻译 API 使用自定义基地址 |
| Python SDK | `python3 sdk/python/translator_sdk.py <url> <key> "test"` | 翻译成功 |
| JS SDK | 使用 JS 调用 `translateAndWait` | 翻译成功 |
| TS SDK | 使用 TS 调用 `translateAndWait` | 翻译成功（修复前会 404） |
| Java SDK | 使用 Java 调用 `translateAndWait` | 翻译成功（修复前会 401） |

---

## 四、提交信息

```
fix: 统一 SDK 鉴权头/路径前缀 + 修复 config.go 环境变量赋值 bug

- go.mod: go 1.26.5 → go 1.22（无效版本号修复）
- config.go: ONLINE_API_BASE 环境变量错误赋值给 EmbedAPIBase，修正为 OnlineAPIBase
- TypeScript SDK: X-API-Key → Bearer，裸路径 → /openapi/v1/ 前缀，轮询间隔对齐
- Java SDK: X-API-Key → Bearer，裸路径 → /openapi/v1/ 前缀
```
