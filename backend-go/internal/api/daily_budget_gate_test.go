// ============ 本文件职责中文说明 ============
// ★ 2026-11-03 复核批立（`daily_budget.go` 文件头点名的锁就是本文件）。
// 日限额**量纲混用**那一族的判据：计数器累加的是成本（rate_card 单价×倍率），
// 旧形态却在没配 `max_daily_tokens` 时直接拿 `max_daily_chars` 当同一侧比 —— 拿"成本"比"字符"。
//
// 这条缺陷的坏法值得单独记一笔，因为它**不会报错、不会红、两头都可能**：
//
//	· 有的租户被提前拦死（客户看到"已达到今日用量上限"，像产品坏了）；
//	· 有的被晚拦（成本失控，而看板上一切正常）。
//
// 五条腿分别守五件事：
//
//	D1 三档读数的取值与口径名（纯函数，不写日志、不碰全局态）。
//	D2 **量纲负向**：legacy 档返回的 budget 必须**不是** `MaxDailyChars`（把旧写法放回来即红）。
//	D3 出声腿：每进程**至多一条** WARN，且只有 legacy 档才出声（真正可观测，不是数原子变量）。
//	D4 「0＝不限」语义：0 必须**放行**（把不确定做成拦截是本仓反复出过的形态）。
//	D5 单一事实源：`billing_api.go` 的闸门与配额面都走同一个函数，
//	   旧的内联 `maxDaily = perms.MaxDailyChars` 不许回来。
//
// ⚠️ D3 为什么要抓真日志而不是数 `dailyCharsLegacyWarned`：
// 数原子变量只能证明"CAS 生效"，证明不了"那行 WARN 真写出来了"——
// 把 `observability.Warn` 整句删掉、只留 CAS，数变量的判据照样绿（AGENTS §一·12 那条
// "读数为失败背书"的姊妹形态）。所以这里换一个 slog handler 收字节。
// ============================================
package api

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"translator/internal/billing"
	"translator/internal/store"
	"translator/internal/tenant"

	_ "modernc.org/sqlite"
)

// dbCaptureWarn 把默认 slog 换成写内存的 JSON handler，返回收字节的缓冲与恢复函数。
// 判据要问的是"那行 WARN 真写出来了吗、写了几次"，所以必须抓真日志。
func dbCaptureWarn(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	return &buf, func() { slog.SetDefault(old) }
}

// dbWarnLines 数缓冲区里含指定关键字的日志行数。
func dbWarnLines(buf *bytes.Buffer, needle string) int {
	n := 0
	for _, ln := range strings.Split(buf.String(), "\n") {
		if strings.Contains(ln, needle) {
			n++
		}
	}
	return n
}

// newDailyBudgetServer 只装记账侧的 Server（内存 SQLite）。
//
// ⚠️ DSN 必须用具名 shared-cache 形态，不能裸 `:memory:`：
// 裸写法是「每个连接一个库」，store.New 的迁移落在连接 A、日用量 SELECT 落到连接 B ⇒
// `no such table: usage_ledger` 假红（AGENTS §25-b 记过这一条，本轮不再重踩）。
func newDailyBudgetServer(t *testing.T) *Server {
	t.Helper()
	dsn := "file:dailybudget_" + t.Name() + "?mode=memory&cache=shared"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	return &Server{Store: st, Bill: billing.NewService(st)}
}

// TestDailyBudgetFromPermsThreeModes D1：三档读数的取值与口径名逐条钉住。
func TestDailyBudgetFromPermsThreeModes(t *testing.T) {
	cases := []struct {
		name      string
		perms     tenant.Perms
		wantBuget int64
		wantUnit  string
	}{
		{
			// 成本口径在位：行为**一字不变**（这是等值锁的对象——本批不许动它的判定）
			name:      "只配 tokens ⇒ 按成本口径拦",
			perms:     tenant.Perms{MaxDailyTokens: 50000},
			wantBuget: 50000,
			wantUnit:  dailyWallUnitCost,
		},
		{
			// tokens 优先：两档都在位时旧的字符档必须让位（否则又回到量纲混用）
			name:      "两档都在 ⇒ tokens 优先",
			perms:     tenant.Perms{MaxDailyTokens: 8000, MaxDailyChars: 100000},
			wantBuget: 8000,
			wantUnit:  dailyWallUnitCost,
		},
		{
			name:      "只配旧字符档 ⇒ 本档不拦（口径名点名成因）",
			perms:     tenant.Perms{MaxDailyChars: 100000},
			wantBuget: 0,
			wantUnit:  dailyWallUnitLegacyCharsIgnored,
		},
		{
			name:      "两档都没配 ⇒ 不限",
			perms:     tenant.Perms{},
			wantBuget: 0,
			wantUnit:  dailyWallUnitUnlimited,
		},
		{
			// 脏值（负数）按"没配"处理：perms 是运营面可写的 JSON，负数没有语义。
			name:      "负值按未配置处理",
			perms:     tenant.Perms{MaxDailyTokens: -1, MaxDailyChars: -1},
			wantBuget: 0,
			wantUnit:  dailyWallUnitUnlimited,
		},
	}
	for _, c := range cases {
		gotBudget, gotUnit := dailyBudgetFromPerms(&c.perms)
		if gotBudget != c.wantBuget {
			t.Errorf("%s：budget 应为 %d，实得 %d", c.name, c.wantBuget, gotBudget)
		}
		if gotUnit != c.wantUnit {
			t.Errorf("%s：口径名应为 %q，实得 %q（口径名是对外契约，管理台按词分派文案）", c.name, c.wantUnit, gotUnit)
		}
	}
}

// TestDailyBudgetLegacyCharsNeverActsAsBudget D2：量纲负向锁。
//
// 这是本批的**退步防线**：把 `return perms.MaxDailyChars, dailyWallUnitCost` 那种
// "悄悄回退"写回来，D1 的 budget 断言会红——但只判 D1 时，下一个人可能顺手
// 把 D1 的期望值改成 `100000`（"看起来更合理"），于是锁跟着实现一起漂。
// 所以这里**独立**再判一次：legacy 档的 budget 必须严格为 0，且**绝不等于**字符上限。
//
// 判据写成"不等于"而不是"等于 0"，是为了让报错消息直接点出病因（拿成本比字符）。
func TestDailyBudgetLegacyCharsNeverActsAsBudget(t *testing.T) {
	p := tenant.Perms{MaxDailyChars: 100000}
	budget, unit := dailyBudgetFromPerms(&p)
	if unit != dailyWallUnitLegacyCharsIgnored {
		t.Fatalf("夹具失效：本例该判成 %q，实得 %q ⇒ 下面那条断言会在真空里跑", dailyWallUnitLegacyCharsIgnored, unit)
	}
	if budget == p.MaxDailyChars {
		t.Fatalf("日限额把「每日字符上限」当成判定值了（budget=%d == MaxDailyChars=%d）：\n"+
			"→ 计数器记的是**成本**（单价×倍率），拿它比字符等于两个量纲之间隔着一个计价系数。\n"+
			"   后果是双向误判：有的租户被提前拦死、有的被晚拦，而看板上一切正常。", budget, p.MaxDailyChars)
	}
	if budget != 0 {
		t.Errorf("legacy 档必须回 0（＝不限，CheckDailyQuota 的既有放行语义），实得 %d", budget)
	}
}

// TestDailyBudgetWarnSpeaksOncePerProcess D3：出声腿。
//
// 三件事一起判，缺一条就有对应的假绿：
//
//	① legacy 档**必须**出声——不出声就变成"配置写错了但没人知道，闸门只是不拦了"；
//	② 连调 5 次只出 1 行——配置形状问题不是每请求事件，逐请求刷屏会埋掉真日志；
//	③ 非 legacy 档（cost / unlimited）**一次都不出声**——否则每打开一次配额面板就刷一条，
//	   而那条 WARN 说的其实是"有人正在被放行"，与"有人配错了"是两件事。
func TestDailyBudgetWarnSpeaksOncePerProcess(t *testing.T) {
	buf, restore := dbCaptureWarn(t)
	defer restore()
	atomic.StoreInt32(&dailyCharsLegacyWarned, 0)
	t.Cleanup(func() { atomic.StoreInt32(&dailyCharsLegacyWarned, 0) })

	s := &Server{}
	for i := 0; i < 5; i++ {
		s.noteDailyBudgetUnit(dailyWallUnitLegacyCharsIgnored)
	}
	if n := dbWarnLines(buf, "日限额配置是旧字符口径"); n != 1 {
		t.Errorf("legacy 档连调 5 次应出**恰 1 条** WARN，实得 %d 条 ⇒ "+
			"要么完全没出声（配置写错没人知道），要么逐请求刷屏（把真日志埋掉）", n)
	}
	// 日志里必须点出**动作**（"补 max_daily_tokens"），只报现象等于把排查工作留给下一位。
	if !strings.Contains(buf.String(), "max_daily_tokens") {
		t.Errorf("这行 WARN 必须点名该补哪个键（max_daily_tokens），实得: %s", buf.String())
	}

	buf.Reset()
	atomic.StoreInt32(&dailyCharsLegacyWarned, 0) // 复位，让下面两档有机会出声
	for i := 0; i < 3; i++ {
		s.noteDailyBudgetUnit(dailyWallUnitCost)
		s.noteDailyBudgetUnit(dailyWallUnitUnlimited)
	}
	if n := dbWarnLines(buf, "日限额配置是旧字符口径"); n != 0 {
		t.Errorf("cost / unlimited 两档**一次都不该**出声，实得 %d 条 ⇒ "+
			"运营每打开一次配额面板就会刷一条说「有人正在被放行」的告警", n)
	}
}

// TestDailyBudgetZeroMeansUnlimited D4：「0＝不限」语义。
//
// 本仓对这一条有过两次教训（F-55：读取失败兜 0 等于拆墙；本条的反面：
// 把读不出量纲当成额度 0 等于把客户挡在门外）。
// 所以两个方向都在这里钉一次：**传入 0 必须放行**。
func TestDailyBudgetZeroMeansUnlimited(t *testing.T) {
	s := newDailyBudgetServer(t)
	if err := s.Bill.CheckDailyQuota(1, 0); err != nil {
		t.Fatalf("maxDaily=0 是「不限」，CheckDailyQuota 必须放行，实得: %v", err)
	}
	// 反向对照：先真记一笔正常用量，再拿一个很小的正数当上限 ⇒ 必须**真拦**。
	// 只判"0 放行"时，把 CheckDailyQuota 整个改成 return nil 也全绿（闸门变摆设）——
	// AGENTS §三 那条「正向判据必须配反向对照」在这里的具体形态。
	if err := s.Store.LogUsage(1, 0, "translate", "test", "m", "en", 1000, "text", "fast"); err != nil {
		t.Fatalf("落一笔正常用量失败（这一腿没跑到，不许当通过）: %v", err)
	}
	used, err := s.Store.DailyUsage(1)
	if err != nil || used <= 0 {
		t.Fatalf("夹具失效：今日用量应为正数，实得 used=%d err=%v ⇒ 下面那条断言会在真空里跑", used, err)
	}
	if err := s.Bill.CheckDailyQuota(1, 1); err == nil {
		t.Fatalf("今日用量已 %d、上限给 1 时必须拦下；实得放行 ⇒ "+
			"这说明上面那条「0 放行」的判据是空转的（闸门根本没在判）", used)
	} else if !strings.Contains(err.Error(), "上限") {
		t.Errorf("拦下时的文案必须让客户看懂是日限额（含「上限」），实得: %v", err)
	}
	_ = context.Background()
}

// TestDailyBudgetSingleSource D5：单一事实源（源码级）。
//
// 两个读点（闸门 `gateUsageDepth` 与配额读写面 `quotaDailyPair`）必须走**同一个**函数：
// 两处各读各的 perms 迟早出现"面板说拦着、闸门其实没拦"——
// 那正是本仓 §一·5「多把尺子」反复出现的形态，而这一面还多一层：
// 客户会在配额面板上看到 `max_daily_chars: 100000` 并以为日墙生效了。
func TestDailyBudgetSingleSource(t *testing.T) {
	b, err := os.ReadFile("billing_api.go")
	if err != nil {
		t.Fatalf("读 billing_api.go 失败（这一腿没跑到，不许当通过）: %v", err)
	}
	src := string(b)
	if n := strings.Count(src, "dailyBudgetFromPerms("); n < 2 {
		t.Errorf("dailyBudgetFromPerms 在 billing_api.go 里应至少被调用 2 次（闸门 + 配额面），实得 %d ⇒ "+
			"其中一处又自己拼了一遍判定", n)
	}
	// 旧的内联形态必须不在（逐行剥注释判：注释里说明"旧形态长什么样"是允许且必要的）。
	for i, ln := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.Contains(trimmed, "maxDaily = perms.MaxDailyChars") ||
			strings.Contains(trimmed, "maxDaily = p.MaxDailyChars") {
			t.Errorf("billing_api.go:%d 又出现了「直接拿字符上限当日限额」的旧形态：%s\n"+
				"→ 那是本缺陷本体（拿成本比字符），请走 dailyBudgetFromPerms", i+1, trimmed)
		}
	}
	// 口径名是**对外契约**：三个常量必须逐字是这三个词（前端与管理台按词分派文案）。
	// ⚠️ 这条不放进 daily_budget.go 自己那里判——放这里是为了让"改常量名"与"改消费点"同一条锁红。
	for name, want := range map[string]string{
		dailyWallUnitCost:               "cost",
		dailyWallUnitLegacyCharsIgnored: "legacy_chars_ignored",
		dailyWallUnitUnlimited:          "unlimited",
	} {
		if name != want {
			t.Errorf("日限额口径名是对外契约（管理台 daily_wall_unit 出栈的就是它），应为 %q，实得 %q", want, name)
		}
	}
}
