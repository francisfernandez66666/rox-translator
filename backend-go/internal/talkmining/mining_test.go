// ============ mining_test.go · 职责说明 ============
// 迁入第一包纯函数层的行为锁：分母按去重客户、未知档位不虚增、样本不足只出统计、
// TopK 不把不足组挤进结论位、代表原文出产物前必脱敏、签名跨进程稳定、孤儿记录丢弃。
// 与 draft_gate_test.go 的分工：那边管「出稿／人审闸」，这边管「统计口径」。
// ================================================
package talkmining

import (
	"strings"
	"testing"
)

// recordsFor 造一组同键记录：前 converted 个客户达成目标档，其余未达成。
// 参数：n=客户总数；converted=达成档位客户数；返回：记录批。
func recordsFor(n, converted int) []Record {
	out := make([]Record, 0, n)
	for i := 1; i <= n; i++ {
		oc := OutcomeNone
		if i <= converted {
			oc = OutcomeEngaged
		}
		out = append(out, Record{
			ActorID: 7, AnchorType: 2, Stage: "s1", CustomerID: uint(i),
			Content: "这个配置可以按需选", Outcome: oc,
		})
	}
	return out
}

// TestMineDenominatorUsesDistinctCustomers 分母必须是全部去重客户，只记转化者会让转化率恒 100%。
func TestMineDenominatorUsesDistinctCustomers(t *testing.T) {
	recs := recordsFor(5, 2)
	recs = append(recs, recs[0]) // 同一客户重复发同一句：不能虚增样本
	got := Mine(recs, Options{})
	if len(got) != 1 {
		t.Fatalf("应聚成 1 簇，实得 %d", len(got))
	}
	c := got[0]
	if c.Samples != 5 || c.Conversions != 2 || c.ConvRate != 0.4 {
		t.Fatalf("分母/分子错：samples=%d conversions=%d rate=%.2f", c.Samples, c.Conversions, c.ConvRate)
	}
	if c.MessageCount != 6 {
		t.Fatalf("消息条数应含重复那条，实得 %d", c.MessageCount)
	}
	if c.Insufficient {
		t.Fatal("5 个去重客户已达默认门槛，不该标样本不足")
	}
}

// TestMineUnknownOutcomeNeverInflates 未知终局档位按 none 处理（fail-safe）。
func TestMineUnknownOutcomeNeverInflates(t *testing.T) {
	recs := recordsFor(5, 0)
	recs[0].Outcome = "bogus-stage"
	got := Mine(recs, Options{})
	if got[0].Conversions != 0 || got[0].ConvRate != 0 {
		t.Fatalf("未知档位虚增了转化：%d / %.2f", got[0].Conversions, got[0].ConvRate)
	}
}

// TestMineTargetNoneFallsBackToDefault 把「未达终局」当目标会让转化率恒 100%，必须回落默认档。
func TestMineTargetNoneFallsBackToDefault(t *testing.T) {
	got := Mine(recordsFor(5, 0), Options{TargetOutcome: OutcomeNone})
	if got[0].TargetOutcome != DefaultTargetOutcome {
		t.Fatalf("目标档未回落：%s", got[0].TargetOutcome)
	}
	if got[0].Conversions != 0 {
		t.Fatalf("回落前把未达终局算成了转化：%d", got[0].Conversions)
	}
}

// TestMineInsufficientSortsLastAndTopKKeepsTail 不足组垫底，且 TopK 截断不许把它挤进结论位。
func TestMineInsufficientSortsLastAndTopKKeepsTail(t *testing.T) {
	good := recordsFor(5, 4)
	weak := recordsFor(2, 2)
	for i := range weak {
		weak[i].ActorID = 9 // 另起一个分组键
	}
	all := append(good, weak...)
	got := Mine(all, Options{TopK: 1})
	if len(got) != 2 {
		t.Fatalf("TopK 仍要保留不足组供观测，实得 %d 簇", len(got))
	}
	if got[0].Insufficient || !got[1].Insufficient {
		t.Fatalf("排序错：首簇应充分、尾簇应不足（%v/%v）", got[0].Insufficient, got[1].Insufficient)
	}
	if got[1].Key.ActorID != 9 {
		t.Fatal("不足组被 TopK 挤掉或换了簇")
	}
}

// TestMineRepresentativeIsMasked 代表原文入产物前必须脱敏（默认掩码抹长数字串）。
func TestMineRepresentativeIsMasked(t *testing.T) {
	recs := recordsFor(5, 3)
	for i := range recs {
		recs[i].Content = "请回拨 13800138000 确认配置"
	}
	got := Mine(recs, Options{})
	if strings.Contains(got[0].Representative, "13800138000") {
		t.Fatal("长号码没被抹掉：代表原文直接进产物＝PII 泄漏")
	}
	if !strings.Contains(got[0].Representative, "号码已隐去") {
		t.Fatalf("默认掩码没生效：%q", got[0].Representative)
	}
}

// TestMineCustomMaskPIIIsUsed 租户自定义脱敏必须真的替换默认掩码。
func TestMineCustomMaskPIIIsUsed(t *testing.T) {
	got := Mine(recordsFor(5, 3), Options{MaskPII: func(string) string { return "MASKED" }})
	if got[0].Representative != "MASKED" {
		t.Fatalf("自定义脱敏未生效：%q", got[0].Representative)
	}
	// 签名取归一化原文（脱敏前），换掩码不应改变幂等键。
	plain := Mine(recordsFor(5, 3), Options{})
	if got[0].Signature != plain[0].Signature {
		t.Fatal("换脱敏函数改变了簇签名 ⇒ 幂等键不稳定")
	}
}

// TestMineDropsOrphanRecords 无说话方／无客户的孤儿消息必须丢弃。
func TestMineDropsOrphanRecords(t *testing.T) {
	recs := []Record{
		{ActorID: 0, CustomerID: 1, Content: "配置可按需选", Outcome: OutcomeEngaged},
		{ActorID: 1, CustomerID: 0, Content: "配置可按需选", Outcome: OutcomeEngaged},
	}
	if got := Mine(recs, Options{}); len(got) != 0 {
		t.Fatalf("孤儿记录不该成簇，实得 %d", len(got))
	}
}

// TestMineSignatureIsStable 同一批记录两次挖掘签名一致（幂等判重的物理前提）。
func TestMineSignatureIsStable(t *testing.T) {
	a := Mine(recordsFor(6, 4), Options{})
	b := Mine(recordsFor(6, 4), Options{})
	if a[0].Signature != b[0].Signature || len(a[0].Signature) != 8 {
		t.Fatalf("签名不稳定或形状不对：%q / %q", a[0].Signature, b[0].Signature)
	}
}

// TestNormalizeForDedupStripsPunctuation 标点／emoji／全角差异收拢成同一个变体。
func TestNormalizeForDedupStripsPunctuation(t *testing.T) {
	if NormalizeForDedup("配置，可选！🙂") != NormalizeForDedup("配置可选") {
		t.Fatal("同类话术没归并")
	}
	recs := recordsFor(5, 3)
	recs[0].Content = "这个配置可以按需选！"
	got := Mine(recs, Options{})
	if got[0].VariantCount != 1 {
		t.Fatalf("加标点不该生成第二个变体，实得 %d", got[0].VariantCount)
	}
}
