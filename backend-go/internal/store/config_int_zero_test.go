// ============ 本文件职责中文说明 ============
// Store.ConfigIntAllowZero 的等值单测（2026-10-24 云端 UAT 修复批 · 档位表收口）。
//
// 为什么单独锁这一枚：《修改文档_UAT云端E2E核实与修法_20261024》档位表要求
// 「所有键都要有等值单测（键名、默认值、非法值回退），避免『配了不生效』」，
// 而 `ticket_repair_max=0＝不重发`、`text_ticket_per_line=0＝回退整篇` 这两条**应急开关**
// 原先走的是 Store.ConfigInt——它把 ≤0 读成"没配"并回落默认，于是库里填 0、进程读回 3／1，
// 开关结构性按不下去（现网表现＝「已经配成 0 了怎么还在重发」，日志零线索）。
// 本文件把「0 是一档真实值」这条语义钉死，并配负向对照（同一份库喂 ConfigInt 必须仍回落默认），
// 否则下一位把调用点改回 ConfigInt 时这里不会红。
// ========================================
package store

import (
	"testing"

	"translator/internal/config"
)

// czStore 建一个自钉 SQLite 方言的内存 Store（AGENTS §一·4：新增后端单测必须自钉方言，
// 否则 run_uat 的 PG 模式会把方言泄漏给同包后续测试）。
func czStore(t *testing.T) *Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	return newTestStore(t)
}

// TestConfigIntAllowZeroHonorsZero 逐档等值：缺键／空串／0／正数／负数／脏值。
func TestConfigIntAllowZeroHonorsZero(t *testing.T) {
	s := czStore(t)
	cases := []struct {
		name string
		set  string // 写进库的字面值（"" 表示不写＝缺键）
		want int
	}{
		{"缺键回落默认", "", 7},
		{"空串回落默认", " ", 7},
		{"零是一档真实值", "0", 0},
		{"正数如实返回", "3", 3},
		{"带空格如实返回", " 2 ", 2},
		{"负数是脏值回落默认", "-1", 7},
		{"非数字是脏值回落默认", "abc", 7},
		{"小数是脏值回落默认", "1.5", 7},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := s.SetConfig("cz_probe_key", c.set); err != nil {
				t.Fatalf("写配置失败: %v", err)
			}
			if got := s.ConfigIntAllowZero("cz_probe_key", 7); got != c.want {
				t.Errorf("库里值 %q：ConfigIntAllowZero 应回 %d，实得 %d", c.set, c.want, got)
			}
		})
	}
}

// TestConfigIntSwallowsZeroIsStillTheOldRuler 负向对照：既有 ConfigInt 的「≤0＝未配置」语义不变。
// 采集节奏那一族护栏（chunk_size/poll_sec 等）依赖它——0 或负数会让循环打歪，所以那里回落默认是对的；
// 这条锁存在的目的是**防止有人把两枚尺子合并**：合并＝把护栏的"脏值兜底"换成"脏值放行"。
func TestConfigIntSwallowsZeroIsStillTheOldRuler(t *testing.T) {
	s := czStore(t)
	if err := s.SetConfig("cz_probe_key", "0"); err != nil {
		t.Fatalf("写配置失败: %v", err)
	}
	if got := s.ConfigInt("cz_probe_key", 5); got != 5 {
		t.Errorf("ConfigInt 的「≤0 视为未配置」语义不许被合并掉：应回默认 5，实得 %d", got)
	}
}
