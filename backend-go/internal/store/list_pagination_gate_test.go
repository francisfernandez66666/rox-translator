// ============ list_pagination_gate_test.go · 职责说明 ============
// store 包内部测试文件。
//
// ============ 本文件职责中文说明 ============
// ★ 2026-11-04 体验批 A11 立的本批**退化门禁**（方案 §2.11 后半段：回答"下一批会不会又退化"）。
//
// 判据：`internal/store/` 里非测试、**非迁移**的 .go 文件中，出现"列表形状"的写死 LIMIT
// （数字 ≥10）的，每一处都必须在下面这张台账里点名并写理由；台账**只减不增**（棘轮）。
//
//	· 数字 ≥10 作为"列表"的门槛：`LIMIT 1` 是存在性查询（全仓两百多处，都是正当形态），
//	  把它一起收进台账只会让台账长到没人读——而没人读的台账等于没有台账。
//	· 迁移文件（*_migrate.go）**整类豁免**：本闸守的是**列表读路径**，
//	  迁移里的 LIMIT 是 DDL/回填游标，与"客户翻不到第 201 条"无关。
//	  ⚠️ 豁免按**文件名后缀**判，不按"行里有没有 SELECT"判——
//	  indexperf_migrate.go 的 `why:` 说明串里就写着 `LIMIT 200`，按 SELECT 判会把文案算成 SQL。
//	· 扫不够（命中数低于下界）判成**闸门失明**而不是"清单变了"（AGENTS 记忆里那条同判）。
//
// 台账键按 (文件名, 该行去空白后前 60 字节) 生成，不按行号：行号随无关改动漂移，
// 而"这条腿还在不在"才是本闸要答的问题。
// ⚠️ 同形两行（feedback.go 的两处 LIMIT 200）会撞成同一个键——这不是 bug 而是键的形状决定的，
//
//	所以另有一张 collisionLedger 显式登记"这个键覆盖几处"；出现第三处同形而台账没改，
//	本闸当场红（否则棘轮读到的比真实少，比少登记一处更隐蔽）。
//
// =============================================
package store

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// limitLedger 已登记的写死列表档位 ⇒ 理由。还清一处就删一条（留着＝台账替已不存在的形态背书，判红）。
var limitLedger = map[string]string{
	"billing.go|`SELECT order_no, COUNT(*) FROM orders WHERE order_no<>'' GR":    "存量重复号**诊断扫描**（不是客户可见列表）：只数有没有重复号，20 个样本足够定位，全表 GROUP BY 反而贵",
	"feedback.go|q += \" ORDER BY id DESC LIMIT 200\"":                           "反馈列表（租户内管理页）——**待补档位**：本批报障范围只覆盖用户/用量/工单/通知/审计五张表，反馈不在其中；登记在册，不许再加新的写死档",
	"funnel.go|GROUP BY 1 ORDER BY 2 DESC LIMIT 15`, sinceStr)":                  "增长漏斗 Top-15——**展示形状本身有界**（漏斗图只画 15 档），分页在这里没有消费方",
	"reconcile.go|\"SELECT id, order_id, tenant_id, amount_tokens, COALESCE(amo": "对账批扫（后台任务，非列表接口）：20000 是**单轮预算**，不是给客户翻的页",
	"referral.go|WHERE r.inviter_uid=? ORDER BY r.id DESC LIMIT 100`, inviter":   "邀请奖励流水（客户面最近 100 条）——**待补档位**，与 feedback 那一族同因",
	"tmreview.go|q += \" ORDER BY id DESC LIMIT 200\"":                           "TM 人审池列表（审核队列按页翻）——**待补档位**；本批报障范围外，登记在册",
	"usdt.go|`SELECT order_id FROM usdt_orders WHERE matched_tx_hash='' A":       "USDT 过期单扫描（闹钟任务）：取的是**这一轮要处理的一批**，不是列表",
}

// collisionLedger 一个键覆盖**几处**（默认 1）。只登记确实同形撞键的那条。
var collisionLedger = map[string]int{
	"feedback.go|q += \" ORDER BY id DESC LIMIT 200\"": 2,
}

const (
	// ledgerFloor 扫描命中数下界（现读 8 处）：低于它判"闸门失明"，不判"清单变了"。
	ledgerFloor = 8
	// scanFileFloor 扫到的源文件数下界：防"目录切错了也把零命中读成全绿"。
	scanFileFloor = 20
)

var hardcodedLimitRe = regexp.MustCompile(`LIMIT\s+(\d+)`)

// listLimitGateHits 扫包内非测试非迁移 .go，返回写死列表档位的键序列（含重复，重复即同形多处）。
func listLimitGateHits(t *testing.T) ([]string, int) {
	t.Helper()
	// 判据本体是纯函数（listLimitHitsFromSource），真源码与合成件喂**同一个函数**——
	// 否则"扫描器自己坏掉"与"代码里真的没有了"两种形态在屏幕上同形。
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读包目录失败: %v", err)
	}
	var hits []string
	scanned := 0
	for _, e := range files {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || strings.HasSuffix(name, "_migrate.go") {
			continue
		}
		scanned++
		b, rerr := os.ReadFile(name)
		if rerr != nil {
			t.Fatalf("读 %s 失败: %v", name, rerr)
		}
		hits = append(hits, listLimitHitsFromSource(name, string(b))...)
	}
	return hits, scanned
}

// listLimitHitsFromSource 纯函数判据：从一份源码文本里挑出"列表形状的写死 LIMIT"键。
func listLimitHitsFromSource(name, src string) []string {
	var hits []string
	for _, line := range strings.Split(src, "\n") {
		st := strings.TrimSpace(line)
		if strings.HasPrefix(st, "//") || strings.HasPrefix(st, "*") {
			continue // 注释里的 LIMIT 是说明文案，不是 SQL
		}
		m := hardcodedLimitRe.FindStringSubmatch(st)
		if m == nil {
			continue
		}
		if atoiGate(m[1]) < 10 {
			continue // LIMIT 1~9＝存在性/取一行，正当形态不收进台账
		}
		key := name + "|"
		if len(st) > 60 {
			key += st[:60]
		} else {
			key += st
		}
		hits = append(hits, key)
	}
	sort.Strings(hits)
	return hits
}

// TestListPaginationRatchet 写死列表档位必须逐条在册，且台账不许替已消失的形态背书。
func TestListPaginationRatchet(t *testing.T) {
	hits, scanned := listLimitGateHits(t)
	if scanned < scanFileFloor {
		t.Fatalf("只扫了 %d 个源文件（下界 %d）——射程不够，本闸等于没装", scanned, scanFileFloor)
	}
	if len(hits) < ledgerFloor {
		t.Fatalf("扫到 %d 处写死列表档位，低于下界 %d ⇒ 判据靶子搬家或扫描器坏了（**闸门失明**，不是清单变了）", len(hits), ledgerFloor)
	}
	seen := map[string]int{}
	for _, h := range hits {
		seen[h]++
		if _, ok := limitLedger[h]; !ok {
			t.Fatalf("新增/未登记的写死列表档位：%s\n⇒ 要么收成常量对＋参数化 LIMIT（正解），要么在本闸台账里写明理由（并说明为什么它不是客户要翻的列表）", h)
		}
		want := collisionLedger[h]
		if want == 0 {
			want = 1
		}
		if seen[h] > want {
			t.Fatalf("%s 已出现 %d 处，台账只承认 %d 处——同形档位又长了一处而没人登记", h, seen[h], want)
		}
	}
	// 台账侧反向：每条台账必须仍在源码里找得到（还清一处就删条目）
	set := map[string]bool{}
	for _, h := range hits {
		set[h] = true
	}
	for k := range limitLedger {
		if !set[k] {
			t.Fatalf("台账里的 %s 在源码里已经找不到了——那条腿已被分页化，**把台账条目删掉**（留着＝台账说谎）", k)
		}
	}
	// collisionLedger 也不许替已不存在的碰撞背书
	for k, n := range collisionLedger {
		if seen[k] != n {
			t.Fatalf("碰撞台账 %s 登记 %d 处，实得 %d 处", k, n, seen[k])
		}
	}
	// 理由不许空写：空理由与"只加名字不写原因"判红（照 §19 allowedBypass 那张表的现成写法）
	for k, why := range limitLedger {
		if len([]rune(strings.TrimSpace(why))) < 12 {
			t.Fatalf("台账 %s 的理由短到不构成理由（%q）", k, why)
		}
	}
}

// TestListPaginationScannerItselfHasTeeth 扫描器的自证（正向对照）：
// 喂一段**合成源码**，里面既有注释里的 LIMIT 200（不许算）、也有 SQL 里的 LIMIT 300（必须算）、
// 还有 LIMIT 1（存在性，不许算）。缺任何一腿，"零命中"就分不清是代码干净还是扫描器失明。
func TestListPaginationScannerItselfHasTeeth(t *testing.T) {
	src := strings.Join([]string{
		"package store",
		"// 说明注释里写 LIMIT 200 是文案，不是 SQL",
		"func A() { q := \"SELECT id FROM t ORDER BY id DESC LIMIT 300\" }",
		"func B() { q := \"SELECT 1 FROM t WHERE x=? LIMIT 1\" }",
		"func C() { q := \"SELECT id FROM t ORDER BY id DESC LIMIT 9 OFFSET 3\" }",
	}, "\n")
	hits := listLimitHitsFromSource("synth.go", src)
	if len(hits) != 1 {
		t.Fatalf("合成件应命中恰 1 处（LIMIT 300），实得 %d 处: %v", len(hits), hits)
	}
	if !strings.HasPrefix(hits[0], "synth.go|func A()") {
		t.Fatalf("命中的不是那条 SQL：%q（注释或存在性查询被算进来了）", hits[0])
	}
}

// atoiGate 前缀数字解析（判据里唯一的数字读法，单独一个函数免得散在多处）。
func atoiGate(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
