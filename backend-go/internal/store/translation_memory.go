// ============ 本文件职责中文说明 ============
// 跨单持久译文记忆（translation_memory）——★ 2026-10-28 计价与成本优化批的「缓存 B」。
//
// 为什么要有这一层（用户令：A+B 都做、B 保留 180 天 / 20 万行每租户、默认开、可按租户关）：
//
//	文件链是「整篇一次 prompt × 每语种一腿」的形态，同一份手册反复上传、同一批产品名在
//	多个工单里重复出现是最常见的真实负载。进程内 LRU（缓存 A）只活到重启，且不跨实例；
//	把「源文→译文」按租户落到库里，下一单直接命中，省下的是**真实上游 token**，
//	这才是不改 K 也能把工单真实消耗压下来的那一半成本。
//
// 四条硬口径（改这块前必读）：
//  1. **租户隔离**：唯一键含 tenant_id，读写都带 tenant_id。跨租户共享译文＝把别人的
//     术语与措辞泄露给另一家客户，是数据事故不是性能优化。
//     平台单（tid==0）落 tenant_id=0 且**只与 0 互查**，不与任何客户共享；tid<0 一律不读不写。
//  2. **键必须带「会改变译文的全部输入」**：源语种、目标语种、提示词口径（mode）、
//     模型指纹（model_fp）、表头上下文（context_hash）。少一维就是「换了模型/改了提示词，
//     库里的旧译文继续投」——本仓最典型的静默坏（同 cannedPromptRev 那条）。
//  3. **只存可用译文**：调用方（engine）只在译文通过 `fileTranslationUsable` 后才调用 Put；
//     回填原文/失败占位一律不写（否则以后每次命中都直出"原文"，等于把漏译固化）。
//  4. **一条都读不到不影响翻译**：所有读写失败静默返回零值（memory 是加速器，不是前置）。
//     Fail-open 方向必须朝「照常调模型」，绝不朝「因为缓存读不到所以整单失败」。
//
// 方言：建表/唯一键用双方言通用语法；UPSERT 用 `ON CONFLICT(...) DO UPDATE ... WHERE`
// （SQLite ≥3.24 与 PostgreSQL 均支持，本仓已有多处同形态）；清理用子查询 LIMIT
// （PG 不认 UPDATE/DELETE 尾随 LIMIT，AGENTS §一·4）。
// ==========================================
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"translator/internal/db"
	"translator/internal/observability"
)

// TranslationMemoryRetentionDays 缺省保留天数（180 天未更新即视为过期）。
// 选 180 天而不是永久：术语与产品名会换代，半年前的译文命中反而制造"旧词覆盖新词"的静默坏。
const TranslationMemoryRetentionDays = 180

// TranslationMemoryMaxRowsPerTenant 单租户行数上限（超出按更新时间裁掉最旧的）。
// 20 万行 ≈ 每行 200B 源文 + 200B 译文 ≈ 80MB 量级，对单机 SQLite/PG 都是可控体量。
const TranslationMemoryMaxRowsPerTenant = 200000

// 来源档（对外契约，见 AGENTS §一·19 的 sources 名单同族）：
// human/approved 是人工确认过的译文，优先级高于模型产物；反向覆盖会把人改过的稿子洗回机翻。
const (
	TMSourceModel    = "model"
	TMSourceHuman    = "human"
	TMSourceApproved = "approved"
)

// tmBatchInMax 批量读一次 IN 的占位符上限。500 是双方言都稳的档（PG 参数上限 65535、
// SQLite 默认 32766，取更小的档只为让 SQL 文本长度可控）。
const tmBatchInMax = 500

// TranslationMemoryRecord 一条译文记忆（写入用）。
type TranslationMemoryRecord struct {
	TenantID    int64
	SrcLang     string
	TgtLang     string
	Mode        string // 提示词口径指纹（见 engine.tmPromptMode）
	ModelFP     string // 模型指纹（provider|model）
	ContextHash string // 表头上下文哈希；未启用为 ""
	Src         string // 源文原文（用于命中后等值校验，防哈希碰撞串句）
	Out         string // 译文
	Source      string // model | human | approved
}

// TranslationMemoryMigrate 建表（幂等）。随 store.New 一起执行；
// 旧库没有这张表时启动即补，不需要手工迁移。
//
// ⚠️ 本表尚未发版，但本机跑过 UAT 的库里可能有 v1 形状（`id INTEGER` + `target` 列 + 四维 UNIQUE）。
// 因此：建表用 `IF NOT EXISTS`、缺列逐个幂等补列（`db.EnsureColumns`），**绝不 DROP TABLE**
// （AGENTS §一·4 迁移唯一入口）。v1 行读出来 `out` 为空 ⇒ 按未命中处理，不会投出坏译文。
func (s *Store) TranslationMemoryMigrate() {
	d := db.CurrentDialect()
	db.Exec(s.db, d, `CREATE TABLE IF NOT EXISTS translation_memory (
		id TEXT PRIMARY KEY,
		tenant_id BIGINT NOT NULL,
		src_lang TEXT NOT NULL,
		tgt_lang TEXT NOT NULL,
		src_hash TEXT NOT NULL,
		src TEXT NOT NULL,
		mode TEXT NOT NULL DEFAULT '',
		model_fp TEXT NOT NULL DEFAULT '',
		context_hash TEXT NOT NULL DEFAULT '',
		out TEXT NOT NULL,
		source TEXT NOT NULL DEFAULT 'model',
		hits INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL)`)
	// 幂等补列：老形状缺哪列补哪列（顺序无关，EnsureColumns 自己判存在性）。
	_ = db.EnsureColumns(s.db, d, "translation_memory", map[string]string{
		"src":          "TEXT NOT NULL DEFAULT ''",
		"mode":         "TEXT NOT NULL DEFAULT ''",
		"model_fp":     "TEXT NOT NULL DEFAULT ''",
		"context_hash": "TEXT NOT NULL DEFAULT ''",
		"out":          "TEXT NOT NULL DEFAULT ''",
		"source":       "TEXT NOT NULL DEFAULT 'model'",
		"hits":         "INTEGER NOT NULL DEFAULT 0",
	})
	// 唯一键七维：租户 + 源文指纹 + 源语种 + 目标语种 + 提示词口径 + 模型指纹 + 表头上下文。
	// 少了 src_lang/tgt_lang 会让"中→英"与"英→中"互串（同一句两个方向译文相反）。
	db.Exec(s.db, d, `CREATE UNIQUE INDEX IF NOT EXISTS ux_tm ON translation_memory(
		tenant_id, src_hash, src_lang, tgt_lang, mode, model_fp, context_hash)`)
	// 清理腿要按 updated_at 排序取最旧，这条索引专供它。
	db.Exec(s.db, d, `CREATE INDEX IF NOT EXISTS idx_tm_tenant_updated ON translation_memory(tenant_id, updated_at)`)
}

// translationMemoryHash 源文指纹：唯一键用哈希而不是原文。
// 理由：PG 的 btree 单键长度有上限（约 2704B），长段落直接入库会报「index row size exceeds」；
// 同时 TEXT 等值在大段中文上也不经济。哈希取 sha256 前 32 hex（碰撞概率对 20 万行可忽略，
// 真撞了也只是"命中一条别人的同哈希译文"——所以读侧必须再做一次 src 等值校验）。
func translationMemoryHash(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:16])
}

// translationMemoryID 行主键：由七维键派生（确定性，无需随机源）。
// 唯一性本来就由 ux_tm 保证，这个 id 只服务"按 id 批量删"的清理腿。
func translationMemoryID(tid int64, srcHash, srcLang, tgtLang, mode, modelFP, ctxHash string) string {
	h := sha256.New()
	for _, part := range []string{strconv.FormatInt(tid, 10), srcHash, srcLang, tgtLang, mode, modelFP, ctxHash} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// TranslationMemoryGet 按（租户, 源语种, 目标语种, 提示词口径, 模型指纹, 表头上下文, 源文）
// 取一条已确认译文。命中返回 (译文, true)；未命中 / 库读失败一律 ("", false)。
//
// 副作用：命中时把 hits 自增（运营用它判断缓存收益），但**不改 updated_at**
// （保留期口径是「这条译文多久没被重新确认/写入」，而不是「多久没被读到」——
// 否则一个热门句会永远不过期，术语换代后再也换不掉）。
func (s *Store) TranslationMemoryGet(tid int64, srcLang, tgtLang, mode, modelFP, ctxHash, source string) (string, bool) {
	if s == nil || tid < 0 || source == "" || tgtLang == "" {
		return "", false
	}
	d := db.CurrentDialect()
	hash := translationMemoryHash(source)
	var gotSrc, out string
	err := db.QueryRow(s.db, d, `SELECT src, out FROM translation_memory
		WHERE tenant_id=? AND src_hash=? AND src_lang=? AND tgt_lang=? AND mode=? AND model_fp=? AND context_hash=?`,
		tid, hash, srcLang, tgtLang, mode, modelFP, ctxHash).Scan(&gotSrc, &out)
	if err != nil || out == "" {
		return "", false
	}
	// 哈希等值不等于源文等值（碰撞），必须逐条再核一次原文。
	if gotSrc != source {
		observability.Warn(context.Background(), "译文记忆命中但源文不一致，按未命中处理（疑似哈希碰撞）",
			"tenant", tid, "lang", tgtLang)
		return "", false
	}
	// 自增失败只丢一个统计读数，不影响本次命中结论。
	db.Exec(s.db, d, `UPDATE translation_memory SET hits=hits+1
		WHERE tenant_id=? AND src_hash=? AND src_lang=? AND tgt_lang=? AND mode=? AND model_fp=? AND context_hash=?`,
		tid, hash, srcLang, tgtLang, mode, modelFP, ctxHash)
	return out, true
}

// TranslationMemoryGetBatch 批量读（一次 IN，最多 500 个占位符一批）。
//
// 返回 map[源文]译文（键是**源文原文**而不是哈希，调用方零额外计算）。
// 命中后逐条做 src 等值校验：哈希相同但原文不同 ⇒ 丢弃并 WARN（绝不把别人的句子投给客户）。
// 读失败 ⇒ 返回空 map（fail-open：照常调模型）。
func (s *Store) TranslationMemoryGetBatch(tid int64, srcLang, tgtLang, mode, modelFP, ctxHash string, sources []string) map[string]string {
	out := make(map[string]string, len(sources))
	if s == nil || tid < 0 || len(sources) == 0 || tgtLang == "" {
		return out
	}
	// 去重 + 建「哈希→原文」的期望表（同一批里重复段很常见，去重能显著缩短 IN 列表）。
	expect := make(map[string]string, len(sources))
	hashes := make([]string, 0, len(sources))
	for _, src := range sources {
		if src == "" {
			continue
		}
		h := translationMemoryHash(src)
		if _, seen := expect[h]; seen {
			continue
		}
		expect[h] = src
		hashes = append(hashes, h)
	}
	if len(hashes) == 0 {
		return out
	}
	d := db.CurrentDialect()
	for start := 0; start < len(hashes); start += tmBatchInMax {
		end := start + tmBatchInMax
		if end > len(hashes) {
			end = len(hashes)
		}
		chunk := hashes[start:end]
		args := make([]interface{}, 0, len(chunk)+6)
		args = append(args, tid, srcLang, tgtLang, mode, modelFP, ctxHash)
		ph := make([]string, len(chunk))
		for i, h := range chunk {
			ph[i] = "?"
			args = append(args, h)
		}
		rows, err := db.Query(s.db, d, `SELECT src_hash, src, out FROM translation_memory
			WHERE tenant_id=? AND src_lang=? AND tgt_lang=? AND mode=? AND model_fp=? AND context_hash=?
			  AND src_hash IN (`+strings.Join(ph, ",")+`)`, args...)
		if err != nil {
			// 读失败＝未命中（缓存是加速器，不是前置）。
			return out
		}
		verified := make([]string, 0, len(chunk))
		for rows.Next() {
			var h, gotSrc, target string
			if err := rows.Scan(&h, &gotSrc, &target); err != nil {
				continue
			}
			want, ok := expect[h]
			if !ok || want != gotSrc || target == "" {
				observability.Warn(context.Background(), "译文记忆批量命中但源文不一致，按未命中处理（疑似哈希碰撞）",
					"tenant", tid, "lang", tgtLang)
				continue
			}
			out[want] = target
			verified = append(verified, h)
		}
		rows.Close()
		// 命中行批量自增 hits（一条 UPDATE，不在热路径上逐行打库）。
		if len(verified) > 0 {
			vargs := make([]interface{}, 0, len(verified)+6)
			vargs = append(vargs, tid, srcLang, tgtLang, mode, modelFP, ctxHash)
			vph := make([]string, len(verified))
			for i, h := range verified {
				vph[i] = "?"
				vargs = append(vargs, h)
			}
			db.Exec(s.db, d, `UPDATE translation_memory SET hits=hits+1
				WHERE tenant_id=? AND src_lang=? AND tgt_lang=? AND mode=? AND model_fp=? AND context_hash=?
				  AND src_hash IN (`+strings.Join(vph, ",")+`)`, vargs...)
		}
	}
	return out
}

// TranslationMemoryPut 写入/刷新一条译文记忆（同键 UPSERT）。
//
// 调用方只应传入**通过可用性判据的译文**；写失败静默忽略（加速器不参与成败）。
//
// 来源优先级（v1 §4.1）：human/approved 覆盖 model，反向不许覆盖——
//
//	`DO UPDATE … WHERE translation_memory.source NOT IN ('human','approved')
//	                    OR excluded.source IN ('human','approved')`
//
// 两种方言都支持带 WHERE 的 DO UPDATE；写成两条语句（先查后写）会在并发下有 TOCTOU 窗口。
func (s *Store) TranslationMemoryPut(rec TranslationMemoryRecord) {
	if s == nil || rec.TenantID < 0 || rec.Src == "" || rec.Out == "" || rec.TgtLang == "" {
		return
	}
	source := strings.TrimSpace(rec.Source)
	if source == "" {
		source = TMSourceModel
	}
	srcHash := translationMemoryHash(rec.Src)
	id := translationMemoryID(rec.TenantID, srcHash, rec.SrcLang, rec.TgtLang, rec.Mode, rec.ModelFP, rec.ContextHash)
	now := time.Now().UTC().Format(time.RFC3339)
	db.Exec(s.db, db.CurrentDialect(), `INSERT INTO translation_memory
		(id, tenant_id, src_lang, tgt_lang, src_hash, src, mode, model_fp, context_hash, out, source, hits, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,0,?,?)
		ON CONFLICT(tenant_id, src_hash, src_lang, tgt_lang, mode, model_fp, context_hash)
		DO UPDATE SET out=excluded.out, source=excluded.source, src=excluded.src, updated_at=excluded.updated_at
		WHERE translation_memory.source NOT IN ('human','approved')
		   OR excluded.source IN ('human','approved')`,
		id, rec.TenantID, rec.SrcLang, rec.TgtLang, srcHash, rec.Src, rec.Mode, rec.ModelFP, rec.ContextHash,
		rec.Out, source, now, now)
}

// TranslationMemoryPrune 清理一个租户的过期行与超额行（幂等，可反复调用）。
// 两条腿：
//  1. 过期：updated_at 早于 keepDays 天前的行直接删；
//  2. 超额：超过 maxRows 时删掉最旧的那部分——**LIMIT 必须放进子查询**
//     （DELETE 尾随 LIMIT 在 SQLite 驱动与 PG 上都是语法错误，AGENTS §一·4）。
//
// 返回删除行数（仅供日志读数，失败返回 0）。
func (s *Store) TranslationMemoryPrune(tid int64, keepDays, maxRows int) int {
	if s == nil || tid < 0 {
		return 0
	}
	if keepDays <= 0 {
		keepDays = TranslationMemoryRetentionDays
	}
	if maxRows <= 0 {
		maxRows = TranslationMemoryMaxRowsPerTenant
	}
	d := db.CurrentDialect()
	cutoff := time.Now().UTC().AddDate(0, 0, -keepDays).Format(time.RFC3339)
	res, err := db.Exec(s.db, d, `DELETE FROM translation_memory WHERE tenant_id=? AND updated_at<>'' AND updated_at<?`, tid, cutoff)
	deleted := 0
	if err == nil && res != nil {
		if n, e := res.RowsAffected(); e == nil {
			deleted = int(n)
		}
	}
	// 超额裁旧：只在该租户行数确实超限时才跑，避免每单都做一次全表 count。
	var cnt int
	if err := db.QueryRow(s.db, d, `SELECT COUNT(*) FROM translation_memory WHERE tenant_id=?`, tid).Scan(&cnt); err != nil {
		return deleted
	}
	if cnt <= maxRows {
		return deleted
	}
	overflow := cnt - maxRows
	res2, err := db.Exec(s.db, d, `DELETE FROM translation_memory WHERE tenant_id=? AND id IN (
		SELECT id FROM translation_memory WHERE tenant_id=? ORDER BY updated_at ASC LIMIT ?)`, tid, tid, overflow)
	if err == nil && res2 != nil {
		if n, e := res2.RowsAffected(); e == nil {
			deleted += int(n)
		}
	}
	return deleted
}
