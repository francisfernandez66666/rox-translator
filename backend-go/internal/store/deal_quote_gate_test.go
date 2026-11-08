// ============ 本文件职责中文说明 ============
// 二期 S1 第二批门禁：Q2 部分唯一索引在真 SQLite 驱动下双插必撞，且原始错误
// 经 db.IsUniqueViolation 判 true；accepted 后可再开一张（证明 WHERE 只对在途生效，
// 不是全表唯一）；跨租户各一张且不串；金额量纲只许 int64「分」并配反证；
// 状态七值与部分索引 WHERE 一致。
// ★ PG 结构化分支由 db/conflict_dialect_gate_test.go 的 D1 覆盖，此处不重复造第二套；
//
//	PG 真连线由 PG 主矩阵补，本机无实例时不声称已跑。
//
// ========================================
package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"translator/internal/config"
	"translator/internal/db"
)

// newDealTestStore 建一个自钉 SQLite 方言的测试 Store（AGENTS §一·4 防 PG 泄漏）。
func newDealTestStore(t *testing.T) *Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	return newTestStore(t)
}

// Q2：真驱动双插必撞，撞键返回可辨错误而不是数据库原文；原始错误判据必须为 true。
func TestDealQuoteOpenPerDealIsDialectSafe(t *testing.T) {
	s := newDealTestStore(t)
	q1, err := s.CreateQuote(1, "deal-A", 12345, "CNY")
	if err != nil {
		t.Fatalf("首次报价建档失败: %v", err)
	}
	if q1.TotalCents != 12345 || q1.Status != QuoteStatusDraft {
		t.Fatalf("首次报价读数不对: %+v", q1)
	}
	_, err = s.CreateQuote(1, "deal-A", 999, "CNY")
	if !errors.Is(err, ErrQuoteOneOpenPerDeal) {
		t.Fatalf("Q2：撞在途唯一索引必须返回 ErrQuoteOneOpenPerDeal，实得 %v", err)
	}

	// 绕过 CreateQuote 的翻译层，直接看真驱动吐出的原始错误。
	_, rawErr := db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO deal_quotes (id, tenant_id, deal_id, total_cents, currency, status, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?)`,
		"qt_raw", 1, "deal-A", 1, "CNY", QuoteStatusDraft, "2026-11-07T00:00:00Z", "2026-11-07T00:00:00Z")
	if rawErr == nil {
		t.Fatal("Q2 前提失效：部分唯一索引没拦住第二次插入，本条断言已失明")
	}
	if !db.IsUniqueViolation(rawErr) {
		t.Fatalf("Q2：SQLite 真驱动的原始冲突必须判 true（err=%v）", rawErr)
	}

	// 正向对照：accepted 之后不再是「在途」，必须能再开一张；否则是「全表唯一」冒充部分唯一。
	if err := s.AcceptQuote(1, q1.ID); err != nil {
		t.Fatalf("报价流转失败: %v", err)
	}
	got, err := s.GetQuote(1, q1.ID)
	if err != nil || got.Status != QuoteStatusAccepted {
		t.Fatalf("流转后状态读回不对: %+v err=%v", got, err)
	}
	if _, err := s.CreateQuote(1, "deal-A", 777, "CNY"); err != nil {
		t.Fatalf("Q2 正向对照：accepted 后必须能再开一张（WHERE 只对 draft/sent 生效）: %v", err)
	}
	if err := s.AcceptQuote(1, q1.ID); !errors.Is(err, ErrQuoteNotFound) {
		t.Fatalf("已 accepted 的报价不许二次流转，实得 %v", err)
	}
}

// 跨租户：同一 deal_id 各租户各有一张在途，且按 (tid, quoteID) 不串。
func TestDealQuoteTenantScopeIsolation(t *testing.T) {
	s := newDealTestStore(t)
	a, err := s.CreateQuote(1, "deal-shared", 100, "CNY")
	if err != nil {
		t.Fatalf("A 租户建档失败: %v", err)
	}
	if _, err := s.CreateQuote(2, "deal-shared", 200, "CNY"); err != nil {
		t.Fatalf("跨租户同 deal_id 必须各有一张在途，实得 %v", err)
	}
	if _, err := s.GetQuote(2, a.ID); !errors.Is(err, ErrQuoteNotFound) {
		t.Fatalf("跨租户读报价必须 not-found，实得 %v", err)
	}
}

// 金额量纲：只许 int64「分」；负金额拒；空币种落 CNY。
func TestDealQuoteAmountIsIntegerFenOnly(t *testing.T) {
	s := newDealTestStore(t)
	if _, err := s.CreateQuote(1, "deal-neg", -1, "CNY"); err == nil {
		t.Fatal("负金额必须在写侧拒掉，不许靠数据库兜")
	}
	q, err := s.CreateQuote(1, "deal-zero", 0, "")
	if err != nil {
		t.Fatalf("零金额建档失败: %v", err)
	}
	if q.Currency != "CNY" {
		t.Fatalf("空币种应落 CNY，实得 %s", q.Currency)
	}
	got, err := s.GetQuote(1, q.ID)
	if err != nil || got.TotalCents != 0 {
		t.Fatalf("金额读回不对: %+v err=%v", got, err)
	}
	hits, err := scanAmountUnitLiterals("deal_quote.go")
	if err != nil {
		t.Fatalf("金额量纲扫描失败: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("金额量纲出现第二把尺子（元/浮点与分混用会差 100 倍）: %v", hits)
	}
}

// 反证：扫描器必须真看得见一处违规，防「扫描集为空＝恒绿」。
func TestDealQuoteAmountUnitScanHasTeeth(t *testing.T) {
	dir := t.TempDir()
	bad := "package fake\n\nfunc amount() float64 { return 0 }\n"
	if err := os.WriteFile(filepath.Join(dir, "bad.go"), []byte(bad), 0o644); err != nil {
		t.Fatalf("反证夹具写入失败: %v", err)
	}
	hits, err := scanAmountUnitLiterals(dir)
	if err != nil {
		t.Fatalf("反证扫描失败: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("金额量纲反证必须命中 1 处，实得 %d ⇒ 扫描器失明", len(hits))
	}
}

// 状态七值与部分索引 WHERE 必须一致（只有 draft/sent 在途）。
func TestDealQuoteStatusAndPartialIndexAgree(t *testing.T) {
	all := []string{
		QuoteStatusDraft, QuoteStatusSent, QuoteStatusAccepted, QuoteStatusDeclined,
		QuoteStatusSuperseded, QuoteStatusVoid, QuoteStatusExpired,
	}
	seen := map[string]bool{}
	for _, st := range all {
		if seen[st] {
			t.Fatalf("状态值重复: %s", st)
		}
		seen[st] = true
	}
	if len(seen) != 7 {
		t.Fatalf("状态必须是七值，实得 %d", len(seen))
	}
	src, err := os.ReadFile("deal_quote.go")
	if err != nil {
		t.Fatalf("读 deal_quote.go 失败: %v", err)
	}
	if !strings.Contains(string(src), "WHERE status IN ('draft','sent')") {
		t.Fatal("部分唯一索引的 WHERE 必须只圈在途两态，且双方言同一条 DDL")
	}
}

// stripGoCommentsForAmountScan 剥行注释：说明性注释里提到 orders.amount_money 不算第二把尺子。
// 参数：line=单行源码；返回剥注释后的代码部分。
func stripGoCommentsForAmountScan(line string) string {
	if i := strings.Index(line, "//"); i >= 0 {
		return line[:i]
	}
	return line
}

// scanAmountUnitLiterals 扫单个 .go 文件或目录下所有 .go，找「元／浮点」金额量纲字面。
// 参数：path=文件或目录；返回命中清单（路径:行号）。
func scanAmountUnitLiterals(path string) ([]string, error) {
	banned := []string{"float64", "amount_money", "REAL"}
	var files []string
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
				files = append(files, filepath.Join(path, e.Name()))
			}
		}
	} else {
		files = append(files, path)
	}
	var hits []string
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		for i, line := range strings.Split(string(raw), "\n") {
			code := stripGoCommentsForAmountScan(line)
			for _, lit := range banned {
				if strings.Contains(code, lit) {
					hits = append(hits, f+":"+itoaLocal(i+1))
					break
				}
			}
		}
	}
	return hits, nil
}

// itoaLocal 把正整数转十进制串（本测试文件不引 strconv，避免与 db.itoa 命名冲突）。
// 参数：n=正整数；返回十进制字符串。
func itoaLocal(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
