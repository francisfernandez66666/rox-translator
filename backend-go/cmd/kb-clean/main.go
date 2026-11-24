// ============ main.go · 职责说明 ============
// cmd/kb-clean —— 云端历史知识库清洗 job（2026-11-23）。
//
// 清洗对象（严格限定，绝不动用户上传）：
//
//	kb_entries 里 module='scrape:<源ID>' 且该源 tier=3(llm_gen) 的条目。
//	module=brand / migrated / imported / 中文业务名（用户上传/迁移）、tier1/tier2 官方来源 → 一律不碰。
//
// 判定与回写（与实时 tier3 语义闸共用 internal/qualityjudge 那一份判据）：
//
//	① Judge.Evaluate：HY 回译 + evals 打分 → 达标则原样保留（零动作）。
//	② 不达标：Judge.Repair = 中文源词经 HY 正向重译 → 新译文再过回译+打分验证。
//	   验证通过 ⇒ 同一事务回写 kb_entries+tm_segments（"把正确的代替"）；
//	   验证失败/报错 ⇒ 同一事务清 tm_segments 并按 id 删除 kb_entries 作为兜底。
//	★ 判据报错（网络/限流/配置）一律跳过该条、不删不写，绝不把机器抖动当成错译。
//
// 安全：默认 dry-run（只判不写）；显式 -apply 才落库。每条变更先写备份 JSONL，
// 进度写 checkpoint 文件可断点续传（按 id 升序，重跑跳过已处理）。全程审计日志。
//
// 用法：
//
//	# 试点（he 语种，前 100 条，只判不写）
//	kb-clean -dsn "postgres://..." -lang he -limit 100 -out /tmp/kbclean_he
//	# 实际回写（确认 dry-run 结果后）
//	kb-clean -dsn "..." -lang he -limit 100 -apply -out /tmp/kbclean_he
//	# 全量（去 limit）
//	kb-clean -dsn "..." -apply -out /tmp/kbclean_all
//
// ============================================================
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/lib/pq"

	"translator/internal/config"
	"translator/internal/db"
	"translator/internal/llm"
	"translator/internal/qualityjudge"
	"translator/internal/store"
)

// cleanEntry 待清洗条目（从库里读出）。
type cleanEntry struct {
	ID        int64
	TenantID  int64
	PackageID int64
	Layer     int
	SrcLang   string
	SrcText   string
	TgtLang   string
	TgtText   string
	Module    string
	// TMHostTenant 由 store.TMHostTenantForPackage 现算：行业/语言文化包为 0，其余为本租户。
	TMHostTenant int64
}

// changeRec 每条变更的备份/审计记录。
type changeRec struct {
	Time     string       `json:"time"`
	ID       int64        `json:"id"`
	Src      string       `json:"src"`
	Lang     string       `json:"lang"`
	Before   string       `json:"before"`
	After    string       `json:"after"`
	Action   string       `json:"action"` // repaired / deleted / skipped_err
	Reason   string       `json:"reason"`
	Snapshot *rowSnapshot `json:"snapshot,omitempty"`
}

// main 运行历史知识库清洗；默认 dry-run，显式 -apply 才写库。
func main() {
	dsn := flag.String("dsn", "", "目标 PostgreSQL DSN（缺省取 DB_DSN 环境变量）")
	apply := flag.Bool("apply", false, "实际落库；不设则 dry-run（只判不写）")
	lang := flag.String("lang", "", "只清洗该目标语种（如 he）；空=全部")
	limit := flag.Int("limit", 0, "本次最多处理条数（0=不限）；试点用")
	outDir := flag.String("out", "/tmp/kb-clean", "备份/进度目录")
	minScore := flag.Int("min-score", 0, "覆盖判据阈值（0=用配置/默认70）")
	retries := flag.Int("retries", 3, "回译清洗判定重试次数（默认3；3次都失败才删除）")
	flag.Parse()
	if *retries < 1 {
		*retries = 1
	}

	if *dsn == "" {
		*dsn = os.Getenv("DB_DSN")
	}
	if *dsn == "" {
		fmt.Fprintln(os.Stderr, "缺少 DSN：-dsn 或 DB_DSN")
		os.Exit(2)
	}
	if err := os.MkdirAll(*outDir, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "建目录失败: %v\n", err)
		os.Exit(2)
	}
	mode := "DRY-RUN"
	if *apply {
		mode = "APPLY"
	}
	fmt.Printf("[kb-clean] mode=%s lang=%q limit=%d out=%s\n", mode, *lang, *limit, *outDir)

	cfg := config.Default()
	cfg.DatabaseDriver = "postgres"
	cfg.DatabaseDSN = *dsn
	raw, err := db.Open(db.Config{Driver: db.DriverPostgres, DSN: *dsn})
	if err != nil {
		fmt.Fprintf(os.Stderr, "打开 DB 失败: %v\n", err)
		os.Exit(2)
	}
	defer raw.Close()
	st, err := store.New(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "建 Store 失败: %v\n", err)
		os.Exit(2)
	}
	client := llm.NewClient(cfg)
	judge := qualityjudge.New(cfg, client, st)
	if judge == nil {
		fmt.Fprintln(os.Stderr, "判据初始化失败（cfg/client/store 缺失）")
		os.Exit(2)
	}
	if *minScore > 0 {
		judge.SetMinScore(float64(*minScore))
		fmt.Printf("[kb-clean] 阈值覆盖为 %d\n", *minScore)
	}
	ctx := context.Background()

	// 断点：已处理 id 集合
	doneIDs, err := loadCheckpoint(*outDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "断点读取失败: %v\n", err)
		os.Exit(2)
	}
	fmt.Printf("[kb-clean] 断点已加载 %d 条\n", len(doneIDs))

	// ★ dry-run 不写备份/进度：预演不能污染 -apply 的断点（否则真跑会跳过预演过的条目）。
	var bf, cp *os.File
	backupPath := filepath.Join(*outDir, "backup.jsonl")
	if *apply {
		bf, err = os.OpenFile(backupPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintf(os.Stderr, "备份文件打开失败: %v\n", err)
			os.Exit(2)
		}
		defer bf.Close()
	}

	// 拉待清洗条目（tier3 scrape，排除用户上传/brand/tier1/2）
	entries, err := loadCleanEntries(raw, *lang, *limit, doneIDs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取待清洗条目失败: %v\n", err)
		os.Exit(2)
	}
	fmt.Printf("[kb-clean] 待清洗 %d 条\n", len(entries))

	stat := struct{ kept, repaired, deleted, errSkip, writeFail int }{}
	if *apply {
		cp, err = os.OpenFile(filepath.Join(*outDir, "checkpoint.txt"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintf(os.Stderr, "进度文件打开失败: %v\n", err)
			os.Exit(2)
		}
		defer cp.Close()
	}

	persistRec := func(rec changeRec) bool {
		if err := writeRec(bf, rec); err != nil {
			fmt.Printf("[kb-clean] 回执写入失败 id=%d: %v\n", rec.ID, err)
			return false
		}
		if bf != nil {
			if err := bf.Sync(); err != nil {
				fmt.Printf("[kb-clean] 备份刷盘失败 id=%d: %v\n", rec.ID, err)
				return false
			}
		}
		return true
	}
	markDone := func(id int64) {
		if cp == nil {
			return
		}
		if _, err := fmt.Fprintf(cp, "%d\n", id); err != nil {
			fmt.Fprintf(os.Stderr, "断点写入失败 id=%d: %v\n", id, err)
			os.Exit(3)
		}
		if err := cp.Sync(); err != nil {
			fmt.Fprintf(os.Stderr, "断点刷盘失败 id=%d: %v\n", id, err)
			os.Exit(3)
		}
	}
	resolveTMHost := func(e cleanEntry) (cleanEntry, error) {
		host, err := store.TMHostTenantForPackage(raw, e.PackageID, e.TenantID)
		if err != nil {
			return e, err
		}
		e.TMHostTenant = host
		return e, nil
	}

	for i, e := range entries {
		// ★ 整条判定链（回译+打分→判错则重译+验证）最多重试 N 次；
		//   任一尝试达标/修复成功即采纳；N 轮全是有效判错且修不好才删除。
		verdict, fixed, lastReason := cleanWithRetry(ctx, judge, e, *retries)
		rec := changeRec{Time: nowStr(), ID: e.ID, Src: e.SrcText, Lang: e.TgtLang, Before: e.TgtText, Reason: lastReason}
		backedUp := false
		switch verdict {
		case verdictErr:
			stat.errSkip++
			rec.Action = "skipped_err"
			if !persistRec(rec) {
				continue
			}
			continue // 无法判定不落 checkpoint，上游恢复后必须重试
		case verdictKeep:
			stat.kept++
			rec.Action = "kept"
		case verdictDelete:
			rec.Action = "deleted"
			if *apply {
				var herr error
				e, herr = resolveTMHost(e)
				if herr != nil {
					stat.errSkip++
					fmt.Printf("[kb-clean] TM 宿主解析失败 id=%d: %v\n", e.ID, herr)
					continue
				}
				snap, serr := loadCleanEntrySnapshot(raw, e)
				if serr != nil {
					stat.errSkip++
					fmt.Printf("[kb-clean] 删除前快照失败 id=%d: %v\n", e.ID, serr)
					continue
				}
				rec.Snapshot = &snap
				// 完整原始行先落可回滚备份并刷盘，再动业务数据；备份失败绝不删除。
				if !persistRec(rec) {
					stat.errSkip++
					continue
				}
				backedUp = true
				if werr := applyCleanChangeTx(ctx, raw, e, "", true); werr != nil {
					stat.errSkip++
					stat.writeFail++
					fmt.Printf("[kb-clean] 原子删除失败 id=%d: %v\n", e.ID, werr)
					continue
				}
				store.InvalidateKBTermCache()
			}
			stat.deleted++
		case verdictRepaired:
			rec.Action = "repaired"
			rec.After = fixed
			if *apply {
				var herr error
				e, herr = resolveTMHost(e)
				if herr != nil {
					stat.errSkip++
					fmt.Printf("[kb-clean] TM 宿主解析失败 id=%d: %v\n", e.ID, herr)
					continue
				}
				snap, serr := loadCleanEntrySnapshot(raw, e)
				if serr != nil {
					stat.errSkip++
					fmt.Printf("[kb-clean] 回写前快照失败 id=%d: %v\n", e.ID, serr)
					continue
				}
				rec.Snapshot = &snap
				// 完整原始行先备份；TM 与 KB 随后在同一事务内提交，失败整体回滚。
				if !persistRec(rec) {
					stat.errSkip++
					continue
				}
				backedUp = true
				if werr := applyCleanChangeTx(ctx, raw, e, fixed, false); werr != nil {
					stat.errSkip++
					stat.writeFail++
					fmt.Printf("[kb-clean] 原子回写失败 id=%d: %v\n", e.ID, werr)
					continue
				}
				store.InvalidateKBTermCache()
			}
			stat.repaired++
		}
		if !backedUp && !persistRec(rec) {
			stat.errSkip++
			continue
		}
		markDone(e.ID)

		if (i+1)%50 == 0 {
			fmt.Printf("[kb-clean] 进度 %d/%d kept=%d repaired=%d deleted=%d errSkip=%d writeFail=%d\n",
				i+1, len(entries), stat.kept, stat.repaired, stat.deleted, stat.errSkip, stat.writeFail)
		}
	}
	fmt.Printf("[kb-clean] 完成 kept=%d repaired=%d deleted=%d errSkip=%d writeFail=%d 备份=%s\n",
		stat.kept, stat.repaired, stat.deleted, stat.errSkip, stat.writeFail, backupPath)
	if *apply && stat.writeFail > 0 {
		os.Exit(3)
	}
}

// verdict 清洗判定三态 + 错误态。
type verdict int

// 清洗结果四档：保留、修正、删除与判据报错跳过。
const (
	verdictKeep     verdict = iota // 原译文达标，保留
	verdictRepaired                // 判错但重译修正成功，回写
	verdictDelete                  // N 次都判错且修不好，删除
	verdictErr                     // N 次判据都报错，跳过不删（保守）
)

// oneRound 单轮判定结果（可注入，便于重试逻辑单测）。
type cleanJudge interface {
	Evaluate(context.Context, string, string, string) (qualityjudge.Result, error)
	Repair(context.Context, string, string) (string, bool, error)
}

// oneRound 记录单轮清洗判定，错误轮必须打断有效判错连续链。
type oneRound struct {
	pass   bool   // 达标保留
	fixed  string // 判错后修正成功的新译文（非空即回写）
	reason string // 说明
	err    error  // 判据报错
}

// retryDecision 纯函数：多轮判定 → 三态。注入 rounds（每轮结果），不碰网络。
func retryDecision(rounds []oneRound) (verdict, string, string) {
	wrongStreak := 0
	lastReason := ""
	for i, r := range rounds {
		if r.err != nil {
			wrongStreak = 0
			lastReason = fmt.Sprintf("第%d轮判据报错: %v", i+1, r.err)
			continue
		}
		if r.pass {
			return verdictKeep, "", r.reason
		}
		wrongStreak++
		lastReason = r.reason
		if strings.TrimSpace(r.fixed) != "" {
			return verdictRepaired, r.fixed, r.reason + "（第" + fmt.Sprint(i+1) + "轮修正成功）"
		}
		lastReason = r.reason + " | 第" + fmt.Sprint(i+1) + "轮修正失败"
	}
	if len(rounds) > 0 && wrongStreak == len(rounds) {
		return verdictDelete, "", lastReason + "（连续" + fmt.Sprint(len(rounds)) + "轮有效判错且修正失败）"
	}
	return verdictErr, "", lastReason + "（判错序列被报错打断或全为报错，跳过不删）"
}

// cleanWithRetry 整条判定链带重试：
//
//	每轮：Evaluate（回译+打分）→ 达标即保留；判错则 Repair（正向重译+再验证）→ 成功即回写。
//	判据报错或判错修不好都计入一轮重试；连续 retries 轮都没能给出可用结论：
//	  - 若连续 N 轮都是有效判错（只是修不好）⇒ 删除；
//	  - 若判错序列被报错打断，或全是判据报错 ⇒ 跳过不删。
//
// ★ 重试是防误删护栏：单次 429/空输出不该让一条词条被判死。
func cleanWithRetry(ctx context.Context, judge cleanJudge, e cleanEntry, retries int) (verdict, string, string) {
	rounds := make([]oneRound, 0, retries)
	for attempt := 1; attempt <= retries; attempt++ {
		r, jerr := judge.Evaluate(ctx, e.SrcText, e.TgtText, e.TgtLang)
		if jerr != nil || !r.Conclusive {
			if jerr == nil {
				jerr = fmt.Errorf("判据未给出有效裁决: %s", r.Reason)
			}
			rounds = append(rounds, oneRound{err: jerr})
			continue // 无法判定与报错同类，重试后仍不删
		}
		if r.Pass {
			rounds = append(rounds, oneRound{pass: true, reason: r.Reason})
			break
		}
		fixed, ok, rerr := judge.Repair(ctx, e.SrcText, e.TgtLang)
		if rerr != nil {
			rounds = append(rounds, oneRound{err: rerr})
			continue
		}
		if ok && strings.TrimSpace(fixed) != "" {
			rounds = append(rounds, oneRound{fixed: fixed, reason: r.Reason})
			break
		}
		rounds = append(rounds, oneRound{reason: r.Reason + " | 修正失败"})
	}
	return retryDecision(rounds)
}

// nowStr 返回备份与日志共用的 RFC3339 时间。
func nowStr() string { return time.Now().Format(time.RFC3339) }

// errStr 把错误转成可写入回执的文本，nil 统一返回空串。
func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// writeRec 在 apply 时写备份文件；dry-run 时把同一份逐条回执打到标准输出。
func writeRec(bf *os.File, r changeRec) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if bf == nil {
		fmt.Println("DRY|" + string(b))
		return nil
	}
	_, err = bf.Write(append(b, '\n'))
	return err
}

// loadCheckpoint 读取已处理 id，支持 apply 断点续传；文件不存在时返回空集合。
func loadCheckpoint(dir string) (map[int64]bool, error) {
	out := map[int64]bool{}
	b, err := os.ReadFile(filepath.Join(dir, "checkpoint.txt"))
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var id int64
		if _, err := fmt.Sscan(line, &id); err != nil || id <= 0 {
			return nil, fmt.Errorf("断点存在坏行 %q", line)
		}
		out[id] = true
	}
	return out, nil
}
