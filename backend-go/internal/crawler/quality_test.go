// ============ quality_test.go · 职责说明（2026-11-23 tier3 质量治理）============
// 采集侧两道质量闸的回归锁：
//   - 闸2 结构化校验：4 类脏候选（源译同形/书写系统不符/拉丁转写/全角标点）必须被拦，
//     正常 he 术语必须放行（正对照，防闸门恒红）；
//   - 闸3 语义门：Judge 低分/报错/未注入 fail-closed，关闭/抽样未命中放行，修复产物再过结构化闸；
//   - 提示词加固：industry 提示词含 4 条质量规则（文案回退即红）；
//   - 抽样确定性：sampleByHash 同输入同判定。
//
// 变异反证思想：每条断言都配一个反向形态，确保闸门不是「恒真/恒假」的假绿。
// ============================================================
package crawler

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"translator/internal/config"
	"translator/internal/store"
)

// ---- 闸2：结构化校验 ----

func TestStructuralRejectDirtyEntries(t *testing.T) {
	cases := []struct {
		name       string
		src, tgt   string
		tgtLang    string
		wantReject bool
	}{
		{"源译同形", "组件", "组件", "he", true},
		{"书写系统不符-中文进he", "组件", "中文译文", "he", true},
		{"拉丁转写", "组件", "komponentnaya", "he", true},
		{"全角标点", "售后", "שירות；אחרי", "he", true},
		{"正常he术语", "组件", "רכיב", "he", false},
		{"正常he带空格", "画像", "פרופיל לקוח", "he", false},
		{"品牌拉丁随文放行", "极石汽车", "ROX רכב", "he", false}, // 品牌拉丁在白名单且含希伯来字母⇒放行
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := structuralReject("zh", tc.src, tc.tgtLang, tc.tgt)
			if (got != "") != tc.wantReject {
				t.Fatalf("src=%q tgt=%q lang=%s: reject=%q wantReject=%v", tc.src, tc.tgt, tc.tgtLang, got, tc.wantReject)
			}
		})
	}
}

// TestStructuralRejectRealAuditBadTerms 用现网已坐实错译形态做射程自证：
// 这些是**语义**错译（希伯来语拼写合法），闸2 不应误拦——它们由闸3 语义门负责。
// 这条反证防止把闸2 误当语义闸，导致「错译没拦住、正确词却被拦」的反向事故。
func TestStructuralRejectRealAuditBadTerms(t *testing.T) {
	for _, tgt := range []string{"מערכת ברקים", "אקדחי הצלה", "חנות", "מיליאנג'"} {
		if got := structuralReject("zh", "任意源词", "he", tgt); got != "" {
			t.Fatalf("语义错译形态 %q 不应被结构化闸拦（应交给语义门）: %s", tgt, got)
		}
	}
}

// ---- 闸3：语义门 ----

type fakeJudge struct {
	pass      bool
	reason    string
	err       error
	called    int
	fixed     string // 非空 = RepairTerm 返回可回写的新译文
	repairErr error
	repairHit int
}

func (f *fakeJudge) JudgeTerm(ctx context.Context, source, translation, targetLang string) (bool, string, error) {
	f.called++
	return f.pass, f.reason, f.err
}

func (f *fakeJudge) RepairTerm(ctx context.Context, source, targetLang string) (string, bool, error) {
	f.repairHit++
	if f.repairErr != nil {
		return "", false, f.repairErr
	}
	if f.fixed == "" {
		return "", false, errors.New("no fix")
	}
	return f.fixed, true, nil
}

func TestQualitySampleZeroIsReal(t *testing.T) {
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "quality.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	st, err := store.New(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetConfig("scrape_tier3_quality_sample", "0"); err != nil {
		t.Fatal(err)
	}
	if got := loadQualityConfig(st).Sample; got != 0 {
		t.Fatalf("抽样 0 必须被读成 0，实得 %v", got)
	}
}

func TestFlushEntriesWritesRepairedTextBeforeApprove(t *testing.T) {
	b, err := os.ReadFile("crawler.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	gate := strings.Index(src, "c.passSemanticGate(")
	write := strings.Index(src, "e.TgtText = fixed")
	approve := strings.Index(src, "c.St.AutoApproveEntry(")
	if gate < 0 || write < gate || approve < write {
		t.Fatalf("修复译文回写接线缺失或顺序错误：gate=%d write=%d approve=%d", gate, write, approve)
	}
	if !strings.Contains(src, "c.St.AutoApproveEntry(store.SharedHostTenant, e)") {
		t.Fatal("采集词条必须落平台共享宿主0；硬编码租户1会让共享包查不到采集内容")
	}
	if !strings.Contains(src, "c.St.AutoApprovePhrase(store.SharedHostTenant, p)") {
		t.Fatal("采集安全句必须与词条同宿主，不能落进租户1")
	}
}

func TestFlushEntriesLandsInSharedPackageHost(t *testing.T) {
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "shared-host.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	st, err := store.New(raw)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := st.CreateKBPackage(store.SharedHostTenant, 0, "auto", "汽车行业", store.PackIndustry, store.PackRoleSource)
	if err != nil {
		t.Fatal(err)
	}
	c := &Crawler{St: st, Quality: &fakeJudge{pass: true}}
	e := &store.KBStagedEntry{
		SourceID: 314, TargetPackID: pkg.ID, PackType: store.PackIndustry, Tier: 3, Layer: 1,
		SrcLang: "zh", SrcText: "刹车系统", TgtLang: "he", TgtText: "מערכת בלמים",
	}
	n, err := c.flushEntries([]*store.KBStagedEntry{e}, true)
	if err != nil || n != 1 {
		t.Fatalf("采集条目应落库：n=%d err=%v", n, err)
	}
	for _, tc := range []struct {
		tid  int64
		want int
	}{{store.SharedHostTenant, 1}, {1, 0}} {
		var got int
		if err := raw.QueryRow("SELECT COUNT(*) FROM kb_entries WHERE package_id=? AND tenant_id=?", pkg.ID, tc.tid).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Fatalf("共享包条目落错宿主：tenant=%d got=%d want=%d", tc.tid, got, tc.want)
		}
	}
	pn, err := c.flushPhrases([]*store.KBStagedPhrase{{
		PackageID: pkg.ID, Lang: "he", Phrase: "ביטוי", Kind: "style", Replacement: "安全表达", Tier: 2,
	}}, true)
	if err != nil || pn != 1 {
		t.Fatalf("采集安全句应落库：n=%d err=%v", pn, err)
	}
	for _, tc := range []struct {
		tid  int64
		want int
	}{{store.SharedHostTenant, 1}, {1, 0}} {
		var got int
		if err := raw.QueryRow("SELECT COUNT(*) FROM kb_safety_phrases WHERE package_id=? AND tenant_id=?", pkg.ID, tc.tid).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Fatalf("共享包安全句落错宿主：tenant=%d got=%d want=%d", tc.tid, got, tc.want)
		}
	}
}

func TestSemanticGateDecisions(t *testing.T) {
	e := &store.KBStagedEntry{SrcText: "刹车系统", TgtText: "מערכת ברקים", TgtLang: "he", Tier: 3}
	cfg := qualityGateConfig{MinScore: 70, Sample: 1}

	t.Run("低分且无法修正则丢弃", func(t *testing.T) {
		c := &Crawler{Quality: &fakeJudge{pass: false, reason: "semantic=30"}}
		fixed, keep := c.passSemanticGate(context.Background(), cfg, e)
		if keep || fixed != "" {
			t.Fatalf("Judge 判不过且无修正必须丢弃，实得 fixed=%q keep=%v", fixed, keep)
		}
	})
	t.Run("低分但重译成功则回写", func(t *testing.T) {
		c := &Crawler{Quality: &fakeJudge{pass: false, reason: "semantic=30", fixed: "מערכת בלמים"}}
		fixed, keep := c.passSemanticGate(context.Background(), cfg, e)
		if !keep || fixed != "מערכת בלמים" {
			t.Fatalf("重译修正成功应保留并回写，实得 fixed=%q keep=%v", fixed, keep)
		}
	})
	t.Run("重译产物仍需过结构化闸", func(t *testing.T) {
		c := &Crawler{Quality: &fakeJudge{pass: false, reason: "semantic=30", fixed: "badlatin"}}
		fixed, keep := c.passSemanticGate(context.Background(), cfg, e)
		if keep || fixed != "" {
			t.Fatalf("重译产物带拉丁转写必须丢弃，实得 fixed=%q keep=%v", fixed, keep)
		}
	})
	t.Run("Judge报错fail-closed", func(t *testing.T) {
		c := &Crawler{Quality: &fakeJudge{err: errors.New("judge down")}}
		fixed, keep := c.passSemanticGate(context.Background(), cfg, e)
		if keep || fixed != "" {
			t.Fatalf("Judge 无法判定时必须丢弃，不能自动审批，实得 fixed=%q keep=%v", fixed, keep)
		}
	})
	t.Run("未注入fail-closed", func(t *testing.T) {
		c := &Crawler{}
		fixed, keep := c.passSemanticGate(context.Background(), cfg, e)
		if keep || fixed != "" {
			t.Fatalf("语义门启用但 Quality 未注入时必须丢弃，实得 fixed=%q keep=%v", fixed, keep)
		}
	})
	t.Run("关闭阈值放行", func(t *testing.T) {
		j := &fakeJudge{pass: false}
		c := &Crawler{Quality: j}
		fixed, keep := c.passSemanticGate(context.Background(), qualityGateConfig{MinScore: 0, Sample: 1}, e)
		if !keep || fixed != "" {
			t.Fatalf("min=0 是语义门回滚开关，应放行，实得 fixed=%q keep=%v", fixed, keep)
		}
		if j.called != 0 {
			t.Fatal("语义门关闭时不应调用 Judge")
		}
	})
	t.Run("抽样率0放行", func(t *testing.T) {
		j := &fakeJudge{pass: false}
		c := &Crawler{Quality: j}
		fixed, keep := c.passSemanticGate(context.Background(), qualityGateConfig{MinScore: 70, Sample: 0}, e)
		if !keep || fixed != "" {
			t.Fatalf("sample=0 应放行且不调 Judge，实得 fixed=%q keep=%v", fixed, keep)
		}
		if j.called != 0 {
			t.Fatal("抽样率0不应调用 Judge")
		}
	})
}

func TestSampleByHashDeterministic(t *testing.T) {
	// 同输入恒同判定（跨轮一致），且 1.0 恒真 / 0 恒假
	if !sampleByHash("组件", "רכיב", 1.0) {
		t.Fatal("rate=1 必须恒真")
	}
	if sampleByHash("组件", "רכיב", 0) {
		t.Fatal("rate=0 必须恒假")
	}
	a := sampleByHash("刹车系统", "מערכת ברקים", 0.5)
	for i := 0; i < 20; i++ {
		if sampleByHash("刹车系统", "מערכת ברקים", 0.5) != a {
			t.Fatal("同输入抽样判定不稳定")
		}
	}
}

// ---- 提示词加固 ----

func TestTier3PromptQualityRules(t *testing.T) {
	p := &llmProducer{src: &store.KBScrapeSource{Lang: "he", Industry: "auto", PackType: "industry"}}
	prompt := p.buildPrompt(&SourceDeps{TargetPackID: 1}, 0)
	for _, rule := range []string{
		"真实存在的标准译法",
		"禁止生造词",
		"禁止把音译当术语",
		"品牌名与拉丁缩写保留原文",
		"不是逐字直译",
	} {
		if !strings.Contains(prompt, rule) {
			t.Fatalf("industry 提示词缺少质量规则 %q：\n%s", rule, prompt)
		}
	}
	// 反证：persona 分支没被误加这些规则（改动射程只到 industry）
	pp := &llmProducer{src: &store.KBScrapeSource{Lang: "he", Industry: "sales", PackType: "persona"}}
	if strings.Contains(pp.buildPrompt(&SourceDeps{}, 0), "禁止生造词") {
		t.Fatal("本期只加固 industry 提示词，persona 不应被误加")
	}
}
