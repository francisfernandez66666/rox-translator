package api

// ============ demo_unit_execstart_gate_test.go · 职责说明 ============
// 锁「演示站 systemd 单元 ExecStart 不得再带回 -kb/-kbdb」——2026-10-18 主机减负 P1-7 的机械防线。
//
// 背景：演示站曾用 `-kb /opt/translator-demo/data/tm_embeddings.npz -kbdb .../tm.sqlite3`
// 冗余加载约 62MB 预计算向量索引（≈86MB Go 堆），而演示库 langcross_demo 已装 pgvector 且
// tm_segments 全量带 embedding，语义检索走 PG（启动日志即打
// `kb: pgvector HNSW 索引就绪——生产语义检索唯一路径；npz 索引仅保留给 dev/单测（S4）`）。
// 主站 ExecStart 本就无 `-kb`。去掉后演示站与主站对齐、省 ~86MB 常驻内存。
//
// ⚠️ 代价（已在《部署指南》§十九.3 记档）：应用内「重建向量索引」与「导入后自动回填」以
//     NPZPath≠"" 为前置，去 -kb 后该界面路径不可用（主站亦然），回填改用独立 CLI
//     cmd/rebuild-kb-index。若哪天要让演示站恢复界面重建，此处判据要同步改，不许只改脚本。
// ================================================================

import (
	"strings"
	"testing"
)

// demoExecStartKbViolation 判据纯函数：src=脚本全文；返回 "" 表示合规，否则返回违规说明。
// 抽成函数的唯一目的是配反证——拿变异体喂**同一条判据**必须报违规，否则这条锁是空转的。
func demoExecStartKbViolation(src string) string {
	found := false
	for _, line := range strings.Split(src, "\n") {
		s := strings.TrimSpace(line)
		if !strings.HasPrefix(s, "ExecStart=") || !strings.Contains(s, "translator-server") {
			continue
		}
		found = true
		if strings.Contains(s, " -kb ") || strings.HasSuffix(s, " -kb") ||
			strings.Contains(s, " -kbdb ") || strings.HasSuffix(s, " -kbdb") {
			return "演示站 ExecStart 又带上了 -kb/-kbdb（冗余加载 npz 向量索引，白吃 ~86MB 内存）：" + s
		}
	}
	if !found {
		return "没读到演示站 ExecStart 行 ⇒ 锚点漂了、这条锁已失明（这不是「合规」，是判据失效）"
	}
	return ""
}

// TestDemoUnitExecStartHasNoNpzFlags 正向锁 ＋ 变异反证两腿。
func TestDemoUnitExecStartHasNoNpzFlags(t *testing.T) {
	src := readGateFile(t, "../../../scripts/bootstrap-demo.sh")
	if v := demoExecStartKbViolation(src); v != "" {
		t.Fatalf("%s", v)
	}

	// ---- 反证：把 -kb/-kbdb 写回 ExecStart，同一条判据必须报违规 ----
	const anchor = "-frontend ${DEMO_DIR}/web"
	mutant := strings.Replace(src, anchor,
		anchor+" -kb ${DEMO_USER_DATA}/tm_embeddings.npz -kbdb ${DEMO_USER_DATA}/tm.sqlite3", 1)
	if mutant == src {
		t.Fatalf("反证失效：没能在脚本里定位 %q ⇒ 判据锚点已漂，变异体与原文件相同", anchor)
	}
	if demoExecStartKbViolation(mutant) == "" {
		t.Fatalf("反证失败：写回 -kb/-kbdb 后判据仍判合规 ⇒ 这条锁没有射程")
	}
}
