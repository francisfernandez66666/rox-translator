# 方案：OCR 功能移除 · 图片型 PDF 引导 · TM 自闭环统一改造

> 状态：✅ 已全部落地（Phase 1/2 见 94d3844 等提交，Phase 3 见 3b7047d；本文转为决策与设计存档）
> 关联决策：图片内容不翻译（产品策略）；TM 唯一入库通道=超管人工审核通过

## 一、背景与结论

- 大纲类「Word 转 PDF」源文件中，部分表格在 Word 内即以截图/图表存在，导出 PDF 后为位图区域：
  文字层无内容、仅像素含中文。文本管线对其无能为力，且按产品策略**图片遗留不动**。
- 历史 OCR 能力（嵌入图 OCR / 整页 OCR / 图内行键收集）按决策**整体移除**；
  对图片型文档改为**上传时引导**（建议传 Word 源文件）。
- 翻译记忆（tm_segments）此前存在多条自动写入路径，产生过脏数据回灌事故。
  统一收敛为：**系统只产生待审候选；仅超管人工点击通过才写入正式 TM**。

## 二、Phase 1 · OCR 功能彻底移除

### Python backend-go/internal/fileproc/docx_translate.py
| 删除项 | 说明 |
|---|---|
| `OCR_LANGS` 常量 | 多语言 OCR 组合 |
| `_ocr_lines` / `ocr_and_translate_image` / `buf_ret` | 行级 OCR 与图内覆盖绘制 |
| `translate_docx_images` | DOCX 内嵌图 OCR（此前已停用调用） |
| extract 内「图片 OCR 行键收集」块 | cmd_extract 中 try 块（pytesseract 收集图内行键） |
| `page_is_graphical` / `pageocr_collect` / `cmd_pageocr_apply` | 整页 OCR 三件套 |
| main 路由中 `pageocr` 分支 | 子命令入口 |

### Go
| 删除项 | 文件 |
|---|---|
| `pdfPageOcrMode` 变量与写回分支 | internal/engine/file.go |
| `ApplyTranslatedPdfPageOcr` | internal/fileproc/pdfwrite.go |
| `ExtractTextsPdfDocx` 的 Mode 字段/pageocr 分支 | internal/fileproc/pdfwrite.go |

### 配套
- 服务器 `/opt/translator/.venv` 卸载 pytesseract（Pillow 保留，其他功能使用）
- 部署指南删除 tesseract 相关安装行
- grep 验收：全仓无 `pytesseract|pageocr|_ocr_lines|translate_docx_images`

## 三、Phase 2 · 图片型 PDF 上传引导（方案 A）

- 判定（Go 实现，与历史阈值一致）：PDF 平均每页 pdftotext 字符 < 200 → `image_heavy=true`
- 挂载点：内部文件工单创建响应 `POST /api/tickets/create-file` 与 OpenAPI 文件任务 202 响应体新增字段
- 前端：创建成功且 `image_heavy` 时展示横幅提示
  - zh：⚠️ 检测到图片型表格：图片中的文字将不做翻译。如需完整翻译请上传 Word 源文件。
  - en：⚠️ Image-based tables detected: text inside images will NOT be translated. Upload the Word source for full coverage.
- i18n key：`tk.imageHeavyHint`

## 四、Phase 3 · TM 自闭环统一改造

### 4.1 规则（定稿口径）
1. 全系统**禁止任何自动写入** tm_segments；唯一入库通道 = 超管在审核台点击「通过」
2. 触发来源两类（均只产生 pending 候选）：
   - A 用户反馈翻译错误（超管在反馈详情填写修正译文并采纳）
   - B 相同 **原文+译文对** 重复出现次数 ≥ 阈值（system_config `tm_review_threshold`，默认 100）
3. 审批流（approved 模块）的人工确认译文视为已审核，**保留直入**
4. bitext / tmx 人工导入**不豁免审核**：一律先进待审池
5. 自闭环全部接口 requireSuperAdmin

### 4.2 数据模型
```sql
CREATE TABLE IF NOT EXISTS tm_review (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  tenant_id INTEGER NOT NULL DEFAULT 0,
  zh TEXT NOT NULL,            -- 原文
  lang TEXT NOT NULL,          -- 目标语言
  trans TEXT NOT NULL,         -- 候选译文
  source TEXT DEFAULT '',      -- bitext | tmx | hit_threshold
  ref_type TEXT DEFAULT '',    -- feedback | ticket | import
  ref_id INTEGER DEFAULT 0,    -- 反馈ID / 工单ID
  hit_count INTEGER DEFAULT 0, -- 达标时的累计次数
  status TEXT DEFAULT 'pending', -- pending | approved | rejected
  reviewer TEXT DEFAULT '', reviewed_at TEXT DEFAULT '', created_at TEXT
);
CREATE TABLE IF NOT EXISTS tm_hit_count (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  tenant_id INTEGER NOT NULL,
  zh_hash TEXT NOT NULL, lang TEXT NOT NULL, trans_hash TEXT NOT NULL,
  zh TEXT, trans TEXT, n INTEGER DEFAULT 0,
  UNIQUE(tenant_id, zh_hash, lang, trans_hash)
);
```

### 4.3 写入点改造
| 现状位置 | 处置 |
|---|---|
| service/ticket.go `persistTicketTM` | 函数与两处调用删除 |
| orchestrator/workflow.go module="approved" | 保留直入（Q3），补审计日志 |
| api/bitext.go SaveBack | 改 `CreateTmReview(source='bitext', ref_type='import')` |
| api/tmx.go 同 | 同上 source='tmx' |

### 4.4 计数触发器
- 位置①文件管线：engine/file.go 复核重试之后，遍历 langTranslations 调 `Store.BumpTmHit(tid, src, lang, tgt)`（仅统计模型最终产出；KB 命中不计）
- 位置②文本管线：orchestrator 流程完成组装 final_result 处同上
- `BumpTmHit`：UPSERT n=n+1；当 n ≥ 阈值且 tm_review 无同对 pending/approved → 插入候选(source='hit_threshold', hit_count=n)

### 4.5 API（requireSuperAdmin）
```
GET  /api/admin/tm-review/list?status=pending|approved|rejected
POST /api/admin/tm-review/approve {id}     -- SaveBack(module='manual') + status=approved
POST /api/admin/tm-review/reject  {id}
POST /api/admin/tm-review/adopt    {feedback_id, zh, lang, trans} -- 原子：建候选即通过
```

### 4.6 审核台 UI（❓ 问题反馈面板内新子视图「📚 记忆审核」，仅超管可见）
- Tab 切换：用户反馈 | 记忆审核
- 列表列：原文 / 候选译文 / 语言 / 来源 / 关联反馈(可跳详情) / 次数 / 操作[✔通过 ✘驳回]
- 反馈详情页（with_context 时）：各语言原译文行尾出现「✏️ 修正并入库」按钮 → 弹窗填修正文 → adopt

## 五、验收清单
1. `grep -r pytesseract\|pageocr\|translate_docx_images backend-go frontend/src` 零命中
2. 上传图片型 PDF：响应含 `image_heavy:true`，前端出横幅；产物图片区域原样
3. bitext 导入一条 → tm_review 出现 pending，tm_segments 无新增
4. 构造同段达标 → 自动候选；通过后 tm_segments 出现 `module='manual'`
5. 反馈修正 → adopt 后同样生效；非超管调 4 个新 API 全 403
6. 回归：正常 PDF 工单翻译质量不回退（docx 管线未受影响）

## 六、实施顺序
Commit A：Phase 1（OCR 移除）→ Commit B：Phase 2（引导）→ Commit C：Phase 3 后端（表/API/触发器/写入点改造）→ Commit D：Phase 3 前端（子视图+反馈修正入口）。每步独立可回滚。
