// ============ outlet_fullset_gate_test.go · 职责说明 ============
// A04（三期 W2 改判）：回复归因的**出站口全集**锁。
// 扫描面＝主服务代理层（assist_open_proxy.go 的响应解析/回写点 + assist_sales_facts.go 的写口），
// **不再是 assist 的 Respond**——assist 是独立进程 + 独立 SQLite，归因写主库只能发生在代理层。
//
// 判据：
//
//	① 代理层恰好一个 payload 出站口（w.Write(payload)），且其后紧跟归因回写装配；
//	② writeBackAttribution 里 RecordReply 与 SyncPackStats 两个写口都在（修 B5：不许摇摆到 W6）；
//	③ outlets==0 / scanned==0 一律 Fatal（射程空集自证：锚点漂了不许静默放行）。
//
// 反证：M0206 摘 SyncPackStats 写口必红；M0207 摘 RecordReply 写口（只剩日志）必红。
// ================================================
package attribution

import (
	"os"
	"strings"
	"testing"
)

func TestAttributionOutletFullset(t *testing.T) {
	proxyB, err := os.ReadFile("../api/assist_open_proxy.go")
	if err != nil {
		t.Fatalf("读代理层源码失败（扫描面漂移）: %v", err)
	}
	factsB, err := os.ReadFile("../api/assist_sales_facts.go")
	if err != nil {
		t.Fatalf("读归因装配源码失败（扫描面漂移）: %v", err)
	}
	proxy, facts := string(proxyB), string(factsB)

	// ① 出站口枚举：payload 回写口 + 归因装配口，缺任一即射程为空。
	outlets := strings.Count(proxy, "w.Write(payload)")
	if outlets == 0 {
		t.Fatal("outlets==0：代理层响应出站口锚点缺失（源码扫描不许空集判绿）")
	}
	if outlets != 1 {
		t.Fatalf("payload 出站口应恰好 1 个（新增出站口必须同批接归因），实际 %d", outlets)
	}
	if strings.Count(proxy, "s.writeBackAttribution(") != 1 {
		t.Fatal("归因回写装配应恰好挂在唯一出站口之后（缺失=只转发不归因；多处=重复写）")
	}

	// ② 写口全集：RecordReply 与 SyncPackStats 都必须在 writeBackAttribution 里真实调用。
	for _, anchor := range []string{"eng.RecordReply(", "eng.SyncPackStats("} {
		if !strings.Contains(facts, anchor) {
			t.Fatalf("归因写口缺失：%s（只写日志不写归因＝伪装配，M0206/M0207 的靶）", anchor)
		}
	}

	// ③ ★ v1.4 第三档「pending_human 行内落点不许丢」：RecordReply 的 INSERT 列全集
	//    必须含 pending_human，且取值必须来自 row.PendingHuman——不许改常量 0、
	//    不许交给按会话定位的 MarkPendingHuman 代写（它是 W6 的活，按会话回填只补映射）。
	//    assist 回避轮的「转人工」标记靠这一列进 reply_attributions，删列＝标记静默消失
	//    （有降级链的静默失败那一族：写口照回 200，库侧那一行永远 pending_human=0）。
	//    反证＝M0267（删 INSERT 的 pending_human 列必红）。
	storeB, err := os.ReadFile("../store/sales_attribution.go")
	if err != nil {
		t.Fatalf("读 store 归因源码失败（扫描面漂移）: %v", err)
	}
	src := string(storeB)
	insStart := strings.Index(src, "INSERT INTO reply_attributions")
	if insStart < 0 {
		t.Fatal("找不到 RecordReply 的 INSERT INTO reply_attributions（行内落点判据失明）")
	}
	insBody := src[insStart:]
	if end := strings.Index(insBody, "UPDATE reply_attributions"); end > 0 {
		insBody = insBody[:end] // 只判 INSERT 段；幂等刷新分支刻意不含 pending_human（不重复计数）
	}
	if !strings.Contains(insBody, "pending_human") {
		t.Fatal("RecordReply 的 INSERT 列全集缺 pending_human：转人工标记必须有行内落点（v1.4 第三档，M0267 靶）")
	}
	if !strings.Contains(insBody, "row.PendingHuman") {
		t.Fatal("pending_human 的取值必须来自 row.PendingHuman（改常量 0／交 MarkPendingHuman 代写都不许）")
	}
}
