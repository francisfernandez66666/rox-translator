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
//	   验证通过 ⇒ SaveEntry 回写 kb_entries+tm_segments（"把正确的代替"）；
//	   验证失败/报错 ⇒ 删除该错译条目（DeleteEntry + 清 tm_segments 对应语种列）作为兜底。
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
}

// changeRec 每条变更的备份/审计记录。
type changeRec struct {
	Time   string `json:"time"`
	ID     int64  `json:"id"`
	Src    string `json:"src"`
	Lang   string `json:"lang"`
	Before string `json:"before"`
	After  string `json:"after"`
	Action string `json:"action"` // repaired / deleted / skipped_err
	Reason string `json:"reason"`
}

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
	doneIDs := loadCheckpoint(*outDir)
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

	stat := struct{ kept, repaired, deleted, errSkip int }{}
	if *apply {
		cp, err = os.OpenFile(filepath.Join(*outDir, "checkpoint.txt"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintf(os.Stderr, "进度文件打开失败: %v\n", err)
			os.Exit(2)
		}
		defer cp.Close()
	}

	for i, e := range entries {
		// 先记进度（仅 apply；崩溃重跑不重复判）
		if cp != nil {
			_, _ = fmt.Fprintf(cp, "%d\n", e.ID)
		}

		// ★ 整条判定链（回译+打分→判错则重译+验证）最多重试 N 次；
		//   任一尝试达标/修复成功即采纳；N 次都判错且修不好才删除；N 次都报错则跳过不删。
		verdict, fixed, lastReason := cleanWithRetry(ctx, judge, e, *retries)
		switch verdict {
		case verdictErr:
			// 判据反复报错（网络/限流）⇒ 跳过，绝不把机器抖动当错译删掉
			stat.errSkip++
			writeRec(bf, changeRec{Time: nowStr(), ID: e.ID, Src: e.SrcText, Lang: e.TgtLang,
				Before: e.TgtText, Action: "skipped_err", Reason: lastReason})
			continue
		case verdictKeep:
			stat.kept++
			continue
		case verdictDelete:
			if *apply {
				if derr := st.DeleteEntry(e.ID, e.TenantID); derr != nil {
					fmt.Printf("[kb-clean] 删除失败 id=%d: %v\n", e.ID, derr)
					stat.errSkip++
					continue
				}
				clearTMSegment(raw, e)
			}
			stat.deleted++
			writeRec(bf, changeRec{Time: nowStr(), ID: e.ID, Src: e.SrcText, Lang: e.TgtLang,
				Before: e.TgtText, Action: "deleted", Reason: lastReason})
			continue
		case verdictRepaired:
			// 修正成功：回写（SaveEntry 同 upsert 语义，含 tm_segments 与缓存失效）
			if *apply {
				if _, serr := st.SaveEntry(e.TenantID, e.PackageID, e.Layer, e.SrcLang, e.SrcText, e.TgtLang, fixed, e.Module); serr != nil {
					fmt.Printf("[kb-clean] 回写失败 id=%d: %v\n", e.ID, serr)
					stat.errSkip++
					continue
				}
				// ★ SaveEntry 命中已存在条目时不同步 tm_segments（kbpackages.go:525-529），
				//   必须显式同步，否则 TM 检索仍召回旧错译＝没洗干净。
				syncTMSegment(raw, e, fixed)
			}
			stat.repaired++
			writeRec(bf, changeRec{Time: nowStr(), ID: e.ID, Src: e.SrcText, Lang: e.TgtLang,
				Before: e.TgtText, After: fixed, Action: "repaired", Reason: lastReason})
		}

		if (i+1)%50 == 0 {
			fmt.Printf("[kb-clean] 进度 %d/%d kept=%d repaired=%d deleted=%d errSkip=%d\n",
				i+1, len(entries), stat.kept, stat.repaired, stat.deleted, stat.errSkip)
		}
	}
	fmt.Printf("[kb-clean] 完成 kept=%d repaired=%d deleted=%d errSkip=%d 备份=%s\n",
		stat.kept, stat.repaired, stat.deleted, stat.errSkip, backupPath)
}

// verdict 清洗判定三态 + 错误态。
type verdict int

const (
	verdictKeep     verdict = iota // 原译文达标，保留
	verdictRepaired                // 判错但重译修正成功，回写
	verdictDelete                  // N 次都判错且修不好，删除
	verdictErr                     // N 次判据都报错，跳过不删（保守）
)

// oneRound 单轮判定结果（可注入，便于重试逻辑单测）。
type oneRound struct {
	pass   bool   // 达标保留
	fixed  string // 判错后修正成功的新译文（非空即回写）
	reason string // 说明
	err    error  // 判据报错
}

// retryDecision 纯函数：多轮判定 → 三态。注入 rounds（每轮结果），不碰网络。
func retryDecision(rounds []oneRound) (verdict, string, string) {
	sawWrong := false
	lastReason := ""
	for i, r := range rounds {
		if r.err != nil {
			lastReason = fmt.Sprintf("第%d轮判据报错: %v", i+1, r.err)
			continue
		}
		if r.pass {
			return verdictKeep, "", r.reason
		}
		sawWrong = true
		lastReason = r.reason
		if strings.TrimSpace(r.fixed) != "" {
			return verdictRepaired, r.fixed, r.reason + "（第" + fmt.Sprint(i+1) + "轮修正成功）"
		}
		lastReason = r.reason + " | 第" + fmt.Sprint(i+1) + "轮修正失败"
	}
	if sawWrong {
		return verdictDelete, "", lastReason + "（连续" + fmt.Sprint(len(rounds)) + "轮判错且修正失败）"
	}
	return verdictErr, "", lastReason + "（连续" + fmt.Sprint(len(rounds)) + "轮判据报错，跳过不删）"
}

// cleanWithRetry 整条判定链带重试：
//
//	每轮：Evaluate（回译+打分）→ 达标即保留；判错则 Repair（正向重译+再验证）→ 成功即回写。
//	判据报错或判错修不好都计入一轮重试；连续 retries 轮都没能给出可用结论：
//	  - 若期间至少有过"明确判错"（只是修不好）⇒ 删除；
//	  - 若全是判据报错（网络/限流）⇒ 跳过不删。
//
// ★ 重试是防误删护栏：单次 429/空输出不该让一条词条被判死。
func cleanWithRetry(ctx context.Context, judge *qualityjudge.Judge, e cleanEntry, retries int) (verdict, string, string) {
	rounds := make([]oneRound, 0, retries)
	for attempt := 1; attempt <= retries; attempt++ {
		r, jerr := judge.Evaluate(ctx, e.SrcText, e.TgtText, e.TgtLang)
		if jerr != nil {
			rounds = append(rounds, oneRound{err: jerr})
			continue // 报错也重试
		}
		if r.Pass {
			rounds = append(rounds, oneRound{pass: true, reason: r.Reason})
			break
		}
		fixed, ok, rerr := judge.Repair(ctx, e.SrcText, e.TgtLang)
		if ok && rerr == nil && strings.TrimSpace(fixed) != "" {
			rounds = append(rounds, oneRound{fixed: fixed, reason: r.Reason})
			break
		}
		rounds = append(rounds, oneRound{reason: r.Reason + " | 修正失败: " + errStr(rerr)})
	}
	return retryDecision(rounds)
}

func nowStr() string { return time.Now().Format(time.RFC3339) }
func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func writeRec(bf *os.File, r changeRec) {
	if bf == nil {
		return // dry-run 不落备份
	}
	b, _ := json.Marshal(r)
	_, _ = bf.Write(append(b, '\n'))
}

func loadCheckpoint(dir string) map[int64]bool {
	out := map[int64]bool{}
	b, err := os.ReadFile(filepath.Join(dir, "checkpoint.txt"))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		var id int64
		if _, err := fmt.Sscan(strings.TrimSpace(line), &id); err == nil {
			out[id] = true
		}
	}
	return out
}
