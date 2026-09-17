# 能言 SaaS 代码级 Greppable 技术债清单

> 可直接在 IDE/终端运行的搜索命令，定位所有「硬编码/魔数/缺失抽象/待重构」代码点  
> 分类：🔴 P0 必改 / 🟠 P1 应改 / 🟡 P2 可改 / 📝 文档化

---

## 1. 魔数与硬编码阈值（迁移至 system_config 热更新）

### 1.1 后端 Go 魔数
```bash
# 文件大小/页数限制
grep -rn "15.*MB\|15<<20\|120.*页\|120" backend-go/ --include="*.go" | grep -v "_test.go"

# 内存限制
grep -rn "650Mi\|650<<20\|950M\|950<<20\|GOMEMLIMIT\|MemoryMax" backend-go/ deploy/ --include="*.go" --include="*.conf"

# 并发/超时参数
grep -rn "maxConcurrent\|MaxConcurrent\|maxBatch\|flushInterval\|30.*Second\|1800\|DefaultLeaseSec\|DefaultMaxAttempts" backend-go/ --include="*.go" | grep -v "_test.go"

# 计费/余额阈值
grep -rn "100000\|50000\|5000\|200\|500\|estimate_tokens_per_sentence\|MarkupMultiplier" backend-go/ --include="*.go" | grep -v "_test.go"

# 缓存封顶
grep -rn "cjkCacheScopeMax\|cultureCacheMax\|128\|4096\|cultureCacheTTL" backend-go/ --include="*.go"

# 重试/熔断参数
grep -rn "BreakerThreshold\|BreakerCoolDownSec\|HunyuanFirstTimeoutSec\|maxTokens\|8192\|4096\|2048" backend-go/ --include="*.go" | grep -v "_test.go"

# 低额告警/留存天数
grep -rn "low_balance_alert_tokens\|ticket_retention_days\|tm_review_threshold\|14.*天\|7.*天\|3.*天\|1.*天" backend-go/ --include="*.go" deploy/ --include="*.sh"
```

### 1.2 前端魔数
```bash
# 并发/分页/限制
grep -rn "maxConcurrent\|pageSize\|page_size\|limit.*[0-9]\|[0-9].*limit" frontend-react/src/ --include="*.ts" --include="*.tsx"

# 防抖/节流/轮询间隔
grep -rn "setTimeout\|setInterval\|debounce\|throttle\|5000\|3000\|1000\|500\|200" frontend-react/src/ --include="*.ts" --include="*.tsx" | grep -v "node_modules"

# 本地存储键
grep -rn "localStorage\|sessionStorage" frontend-react/src/ --include="*.ts" --include="*.tsx" | grep -v "node_modules"
```

---

## 2. 错误码体系缺失（统一 ErrorCode 枚举 + APIError 结构）

> ⚠️ 状态更新（2026-09-04）：OpenAPI 层错误码已收敛——`internal/errors/codes.go` 新增 snake_case 常量，`api_openapi_tasks.go`/`admin_openapi.go` 全部字面量替换，`openapi.v1.json` 补统一 Error schema，前端 `core.ts` 新增 `ApiError`/`bizErrorCode` 透传（提交 309a126）。本清单剩余部分（内部非 OpenAPI 路径的 fmt.Errorf/HTTP 硬编码）仍有效，供后续批量迁移检索。

### 2.1 后端错误构造点
```bash
# fmt.Errorf 字符串错误
grep -rn 'return fmt.Errorf\|errors.New(' backend-go/internal/ --include="*.go" | grep -v "_test.go" | head -50

# quotaErr 等自定义错误
grep -rn "quotaErr\|insufficient_balance\|ErrInsufficient" backend-go/ --include="*.go"

# HTTP 状态码硬编码
grep -rn "WriteHeader(\(4[0-9][0-9]\|5[0-9][0-9]\))" backend-go/ --include="*.go"

# 业务错误码字符串
grep -rn '"error".*:"' backend-go/internal/api/ --include="*.go" | head -30
```

### 2.2 前端错误处理
```bash
# MessagePlugin.error 直接用字符串
grep -rn "MessagePlugin.error\|MessagePlugin.warning\|toast.error" frontend-react/src/ --include="*.tsx" | head -30

# catch 后直接用 e.message
grep -rn "catch.*e.*message\|\.catch.*message" frontend-react/src/ --include="*.tsx"
```

---

## 3. 缓存无统一框架（各自实现 map+mutex+封顶）

```bash
# CJK 缓存
grep -rn "cjkCache\|cjkCacheByTenant\|cjkMu" backend-go/internal/engine/ --include="*.go"

# 文化闸门缓存
grep -rn "cultureCache\|cultureEntries\|cultureMu" backend-go/internal/engine/ --include="*.go"

# Embedding 缓存
grep -rn "embedCache\|getCachedEmbed\|putCachedEmbed\|EmbedLookupFrom" backend-go/internal/engine/ --include="*.go"

# 影子余额
grep -rn "shadow.*map\|shadowOk\|UsageSink" backend-go/internal/billing/ --include="*.go"

# 限流内存计数
grep -rn "qpsWindow\|concurrent.*int\|quotaByTenant" backend-go/internal/billing/ --include="*.go"

# 登录/注册限流
grep -rn "loginLimit\|regGuard\|rate_limits" backend-go/internal/api/ --include="*.go"
```

---

## 4. 国际化键硬编码（无提取工具、无类型安全）

### 4.1 前端 i18n 键
```bash
# t('key') / gt / useT 调用
grep -rn "t(\|gt(\|useT(" frontend-react/src/ --include="*.tsx" --include="*.ts" | grep -v "node_modules" | head -50

# 面板级 dict 文件键
grep -rn "export.*{" frontend-react/src/i18n/panels/ --include="*.ts" | head -30

# 硬编码中文/英文字符串（应提取为 i18n）
grep -rn "[\u4e00-\u9fa5].{5,}" frontend-react/src/components/ --include="*.tsx" | grep -v "t(" | grep -v "//" | head -30
```

### 4.2 后端错误消息中文硬编码
```bash
# 返回给前端的中文错误信息
grep -rn "message.*:.*[\u4e00-\u9fa5]" backend-go/internal/api/ --include="*.go" | grep -v "_test.go" | head -30

# 审计日志 action/resource 中文
grep -rn "LogAudit.*[\u4e00-\u9fa5]" backend-go/ --include="*.go"
```

---

## 5. 测试覆盖极低（无集成/E2E/Contract 测试）

```bash
# 统计测试文件
find backend-go -name "*_test.go" | wc -l
find frontend-react -name "*.test.ts" -o -name "*.spec.ts" -o -name "*.e2e.ts" | wc -l

# 单元测试覆盖的包
grep -rn "func Test" backend-go/ --include="*_test.go" | cut -d: -f1 | sort -u

# 无测试的核心包
ls backend-go/internal/ | while read d; do [ -f "backend-go/internal/$d/*_test.go" ] || echo "NO TEST: $d"; done
```

---

## 6. OpenAPI 手写维护（无 Spec 自动生成、无 SDK 流水线）

```bash
# 手写 openapi-docs 相关
grep -rn "openapi.docs\|OpenAPIDocs" backend-go/internal/api/ --include="*.go"

# 硬编码的 API 路径/参数
grep -rn "/openapi/v1/" backend-go/internal/api/ --include="*.go"

# SDK 手写维护点
cat sdk/python/translator_sdk.py | grep -n "hardcoded\|TODO\|FIXME\|手动"
```

---

## 7. Python 子进程耦合（版本锁定、环境脆弱、难容器化）

```bash
# 子进程调用点
grep -rn "subprocess\|Command\|exec\|pdf2docx\|libreoffice\|python-docx\|fonttools\|pillow" backend-go/internal/fileproc/ --include="*.go"

# Python 依赖版本
cat deploy/requirements.txt 2>/dev/null || echo "无 requirements.txt"
ls -la /opt/translator/.venv/lib/python*/site-packages/ 2>/dev/null | grep -E "pdf2docx|docx|pillow|fonttools" || echo "需服务器查看"

# 临时文件/目录硬编码
grep -rn "_uploads\|_output\|tmp\|temp" backend-go/internal/fileproc/ --include="*.go"
```

---

## 8. 前端状态管理分散（Context 嵌套、Props Drilling）

```bash
# Provider 嵌套层级
grep -rn "<.*Provider" frontend-react/src/App.tsx

# 自定义 Hook 定义
grep -rn "export function use[A-Z]\|export const use[A-Z]" frontend-react/src/hooks/ --include="*.tsx"

# Context 定义与使用
grep -rn "createContext\|useContext" frontend-react/src/ --include="*.tsx" | grep -v "node_modules"

# 组件间通过 props 传递状态（超过 3 层）
grep -rn "selectedLangs\|mode\|qualityMode\|user\|tenant" frontend-react/src/components/ --include="*.tsx" | grep "props\|{" | head -20
```

---

## 9. 结构化日志缺失（无 TraceID、无统一字段、无采样）

```bash
# 直接用 log.Printf/log.Printf
grep -rn "log\.(Printf|Println|Fatal)" backend-go/ --include="*.go" | grep -v "_test.go" | wc -l

# 无 TraceID 注入
grep -rn "trace_id\|traceID\|request_id\|RequestID" backend-go/ --include="*.go" | head -20

# 关键路径无结构化字段
grep -rn "billing\|payment|deduct|charge" backend-go/internal/billing/ --include="*.go" -A2 -B2 | grep "log\."
```

---

## 10. 配置三源不一致（system_config + 环境变量 + config.json）

```bash
# system_config 读取点
grep -rn "GetConfig\|system_config" backend-go/ --include="*.go" | grep -v "_test.go" | grep -v "migrate" | wc -l

# 环境变量读取点
grep -rn "os.Getenv" backend-go/ --include="*.go" | grep -v "_test.go" | wc -l

# config.json 读取
grep -rn "LoadConfigFromJSON\|config.json" backend-go/ --include="*.go"

# 同一配置项多源读取（如 OnlineAPIKey）
grep -rn "OnlineAPIKey\|OnlineAPIBase\|OnlineModel\|EmbedAPIKey\|EmbedAPIBase" backend-go/internal/config/ --include="*.go" -A3 -B3
```

---

## 11. 安全相关硬编码/弱配置

```bash
# JWT 密钥/算法硬编码
grep -rn "JWT_SECRET\|HS256\|SigningMethod" backend-go/ --include="*.go"

# CORS 来源硬编码
grep -rn "CORSOrigins\|Access-Control-Allow-Origin" backend-go/ --include="*.go"

# 密钥生成随机数强度
grep -rn "rand\.(Read|Int)\|crypto/rand" backend-go/ --include="*.go" | grep -v "_test.go"

# SQL 注入风险（字符串拼接 SQL）
grep -rn "fmt.Sprintf.*SELECT\|fmt.Sprintf.*INSERT\|fmt.Sprintf.*UPDATE\|fmt.Sprintf.*DELETE" backend-go/ --include="*.go" | grep -v "_test.go" | head -20
```

---

## 12. 数据库迁移/Schema 管理

```bash
# 迁移脚本分散
ls backend-go/internal/store/*.go | xargs grep -l "migrate\|Migrate" | head -10

# 无版本号的 ALTER TABLE
grep -rn "ALTER TABLE.*ADD COLUMN" backend-go/internal/store/ --include="*.go"

# 索引创建无并发/锁控制
grep -rn "CREATE INDEX" backend-go/internal/store/ --include="*.go"
```

---

## 13. 待办/技术债注释（FIXME/TODO/HACK/XXX）

```bash
# 全仓 TODO/FIXME
grep -rn "TODO\|FIXME\|HACK\|XXX\|BUG\|TECHDEBT" backend-go/ frontend-react/src/ --include="*.go" --include="*.ts" --include="*.tsx" | grep -v "node_modules" | grep -v "_test.go" | wc -l

# 分类统计
grep -rn "TODO\|FIXME\|HACK\|XXX" backend-go/ frontend-react/src/ --include="*.go" --include="*.ts" --include="*.tsx" | grep -v "node_modules" | cut -d: -f3 | cut -d' ' -f1 | sort | uniq -c | sort -rn
```

---

## 14. 依赖版本锁定/供应链

```bash
# Go 依赖
cat backend-go/go.mod | grep -v "// indirect" | wc -l
cat backend-go/go.sum | wc -l

# 前端依赖
cat frontend-react/package.json | jq '.dependencies + .devDependencies | keys | length'

# 过期/有漏洞依赖（需运行）
# cd backend-go && govulncheck ./...
# cd frontend-react && npm audit
```

---

## 15. 性能反模式

```bash
# 循环中查询 DB
grep -rn "for.*range.*{.*Query\|for.*{.*\.Query\|for.*{.*\.Exec" backend-go/ --include="*.go" | grep -v "_test.go" | head -20

# 无限制切片扩容
grep -rn "append.*\[\]" backend-go/ --include="*.go" | grep -v "_test.go" | head -10

# 同步等待通道（可能阻塞）
grep -rn "<-.*chan\|select.*case.*chan" backend-go/ --include="*.go" | grep -v "_test.go" | head -10

# 大对象拷贝传参
grep -rn "func.*\([A-Z][a-zA-Z]* [A-Z][a-zA-Z]*\)" backend-go/ --include="*.go" | grep -v "_test.go" | head -10
```

---

## 16. 可运行的完整扫描脚本

保存为 `scan_tech_debt.sh` 并 `chmod +x`：

```bash
#!/bin/bash
set -euo pipefail

ROOT="/Users/zhangzifei/Desktop/翻译助手"
OUT_DIR="$ROOT/tech_debt_scan_$(date +%Y%m%d_%H%M%S)"
mkdir -p "$OUT_DIR"

echo "=== 1. 后端魔数 ===" | tee "$OUT_DIR/summary.txt"
grep -rn "15.*MB\|120.*页\|650Mi\|950M\|100000\|50000\|128\|4096\|8192\|4096\|2048\|1800\|30.*Second" "$ROOT/backend-go" --include="*.go" | grep -v "_test.go" > "$OUT_DIR/go_magic_numbers.txt"
wc -l "$OUT_DIR/go_magic_numbers.txt" | tee -a "$OUT_DIR/summary.txt"

echo "=== 2. 前端魔数 ===" | tee -a "$OUT_DIR/summary.txt"
grep -rn "5000\|3000\|1000\|500\|200\|maxConcurrent\|pageSize" "$ROOT/frontend-react/src" --include="*.ts" --include="*.tsx" > "$OUT_DIR/fe_magic_numbers.txt"
wc -l "$OUT_DIR/fe_magic_numbers.txt" | tee -a "$OUT_DIR/summary.txt"

echo "=== 3. 错误码字符串 ===" | tee -a "$OUT_DIR/summary.txt"
grep -rn 'return fmt.Errorf\|errors.New(' "$ROOT/backend-go/internal" --include="*.go" | grep -v "_test.go" > "$OUT_DIR/go_errors.txt"
wc -l "$OUT_DIR/go_errors.txt" | tee -a "$OUT_DIR/summary.txt"

echo "=== 4. 缓存自研实现 ===" | tee -a "$OUT_DIR/summary.txt"
grep -rn "cjkCache\|cultureCache\|embedCache\|shadow.*map\|quotaByTenant\|loginLimit\|regGuard" "$ROOT/backend-go" --include="*.go" > "$OUT_DIR/caches.txt"
wc -l "$OUT_DIR/caches.txt" | tee -a "$OUT_DIR/summary.txt"

echo "=== 5. i18n 键硬编码 ===" | tee -a "$OUT_DIR/summary.txt"
grep -rn "t(\|gt(\|useT(" "$ROOT/frontend-react/src" --include="*.tsx" --include="*.ts" | grep -v "node_modules" > "$OUT_DIR/i18n_keys.txt"
wc -l "$OUT_DIR/i18n_keys.txt" | tee -a "$OUT_DIR/summary.txt"

echo "=== 6. Python 子进程耦合 ===" | tee -a "$OUT_DIR/summary.txt"
grep -rn "subprocess\|pdf2docx\|libreoffice\|python-docx" "$ROOT/backend-go/internal/fileproc" --include="*.go" > "$OUT_DIR/python_coupling.txt"
wc -l "$OUT_DIR/python_coupling.txt" | tee -a "$OUT_DIR/summary.txt"

echo "=== 7. 前端 Provider 嵌套 ===" | tee -a "$OUT_DIR/summary.txt"
grep -rn "<.*Provider" "$ROOT/frontend-react/src/App.tsx" > "$OUT_DIR/providers.txt"
cat "$OUT_DIR/providers.txt" | tee -a "$OUT_DIR/summary.txt"

echo "=== 8. 结构化日志缺失 ===" | tee -a "$OUT_DIR/summary.txt"
grep -rn "log\.(Printf|Println|Fatal)" "$ROOT/backend-go" --include="*.go" | grep -v "_test.go" > "$OUT_DIR/logging.txt"
wc -l "$OUT_DIR/logging.txt" | tee -a "$OUT_DIR/summary.txt"

echo "=== 9. 配置三源 ===" | tee -a "$OUT_DIR/summary.txt"
grep -rn "GetConfig\|os.Getenv\|LoadConfigFromJSON" "$ROOT/backend-go" --include="*.go" | grep -v "_test.go" > "$OUT_DIR/config_sources.txt"
wc -l "$OUT_DIR/config_sources.txt" | tee -a "$OUT_DIR/summary.txt"

echo "=== 10. TODO/FIXME ===" | tee -a "$OUT_DIR/summary.txt"
grep -rn "TODO\|FIXME\|HACK\|XXX" "$ROOT/backend-go" "$ROOT/frontend-react/src" --include="*.go" --include="*.ts" --include="*.tsx" | grep -v "node_modules" > "$OUT_DIR/todos.txt"
wc -l "$OUT_DIR/todos.txt" | tee -a "$OUT_DIR/summary.txt"

echo "=== 扫描完成，结果在 $OUT_DIR ===" | tee -a "$OUT_DIR/summary.txt"
cat "$OUT_DIR/summary.txt"
```

---

## 17. 修复优先级映射表

| Greppable 类别 | 对应 P0/P1/P2 项 | 建议修复顺序 |
|----------------|------------------|--------------|
| 魔数/硬编码阈值 | P0-2、P0-3、P1-E1、P1-B1、P1-R1 | 先迁移 system_config，再删代码 |
| 错误码体系 | P1-R1、P1-A2 | 先定枚举，再批量替换 |
| 缓存无统一框架 | P0-2（Redis 接入时同步）、P1-R2 | Redis 上线前统一接口 |
| 国际化键 | P2-F3 | 接入 Lingui 后统一提取 |
| 测试覆盖 | P1-R1-R4（Contract/Integration/E2E） | 先补 Contract 测试保护核心接口 |
| OpenAPI/SDK | P1-A2 | 接入 oapi-codegen 立即生成 Spec |
| Python 子进程 | P0-3（文件转换服务化） | 优先抽离 gRPC 服务 |
| 前端状态 | P2-F1 | 先迁移 auth/chat 核心模块 |
| 结构化日志 | P1-R2 | 接入 Zap+OTel 统一输出 |
| 配置三源 | P0-3（Config Service） | etcd 上线前合并 Schema |

---

## 18. 一键修复建议（部分可自动化）

```bash
# 1. 魔数 → system_config 常量定义（需人工审核每处语义）
# 建议：写 codemod 脚本，按模式替换

# 2. 错误码 → 统一枚举
# 建议：定义 internal/errors/codes.go，IDE 重构替换

# 3. log.Printf → zap.L().Info/Sugar().Infow
# 建议：goast/structural search replace 批量转换

# 4. i18n 键提取
# 建议：npx lingui extract --clean

# 5. TODO/FIXME → GitHub Issues 批量创建
# 建议：脚本解析生成 issue yaml，批量导入
```

---

> **使用方式**：每周一运行 `scan_tech_debt.sh`，对比上周新增/减少项，纳入 Sprint 规划。  
> **治理目标**：P0 类清零、P1 类趋势下降、P2 类可控、TODO/FIXME 关联 Issue 100%。