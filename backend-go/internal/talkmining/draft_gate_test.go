// ============ draft_gate_test.go · 职责说明 ============
// 二期 §7 出稿／人审闸的守卫级＋行为级判据：
//
//	D1 样本不足簇绝不出稿（配变异体 M0182，摘掉门槛必须红在「样本不足不得出稿」上）；
//	D2 出稿层不许出现转正／approved 字样的第二把尺子（源码扫描＋反证）；
//	D3 作用域 fail-closed（tid<=0 与零值 Scope 都拒）；
//	D4 幂等／上限／归纳缺失／归纳失败四档账目各自不同形，过程量不许静默吞。
//
// ★ 本包不连库：写口用内存替身；真库行为由 store/sales_draft_mined_gate_test.go 判。
// ================================================
package talkmining

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"translator/internal/salesflow"
)

// fakeWriter 内存写口替身：记录每次落库参数，并可模拟「同 ID 已存在」。
type fakeWriter struct {
	calls   []string
	exists  map[string]bool
	written []string
}

// CreateMinedDraft 记录调用并按 exists 决定幂等命中；返回是否真写入。
func (w *fakeWriter) CreateMinedDraft(_ int64, id, _, _, _, _ string) (bool, error) {
	w.calls = append(w.calls, id)
	if w.exists != nil && w.exists[id] {
		return false, nil
	}
	w.written = append(w.written, id)
	return true, nil
}

// goodCluster 构造一个达门槛的候选簇（5 个去重客户）。
func goodCluster() Cluster {
	return Mine(recordsFor(5, 4), Options{})[0]
}

// withGenerator 临时装配注入的归纳函数，测试结束自动还原全局（避免用例间串味）。
func withGenerator(t *testing.T, fn func(salesflow.Scope, string) (string, error)) {
	t.Helper()
	old := GenerateDraftFunc
	GenerateDraftFunc = fn
	t.Cleanup(func() { GenerateDraftFunc = old })
}

// testScope 造一个合法作用域。
func testScope(t *testing.T) salesflow.Scope {
	t.Helper()
	s, err := salesflow.NewScope(context.Background(), 42)
	if err != nil {
		t.Fatalf("造作用域失败: %v", err)
	}
	return s
}

// scanProductionGo 扫一个目录下的非测试 .go 生产源码，剥行注释后找禁词。
// 参数：dir=目录；banned=禁词全集；返回命中清单与扫描文件数。
func scanProductionGo(dir string, banned []string) ([]string, int) {
	var hits []string
	scanned := 0
	entries, err := os.ReadDir(dir)
	if err != nil {
		return hits, scanned
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		scanned++
		for i, line := range strings.Split(string(raw), "\n") {
			if j := strings.Index(line, "//"); j >= 0 {
				line = line[:j]
			}
			for _, lit := range banned {
				if lit != "" && strings.Contains(line, lit) {
					hits = append(hits, e.Name()+":"+strconv.Itoa(i+1))
					break
				}
			}
		}
	}
	return hits, scanned
}

// D1 主判据：样本不足簇只记统计，绝不出稿。
func TestDraftGateInsufficientNeverWritten(t *testing.T) {
	withGenerator(t, func(salesflow.Scope, string) (string, error) { return "可复用说法", nil })
	scope := testScope(t)
	weak := Mine(recordsFor(2, 2), Options{})[0]
	if !weak.Insufficient {
		t.Fatal("夹具失效：2 个客户本该是样本不足")
	}
	w := &fakeWriter{}
	stat, err := DraftTemplates(scope, []Cluster{weak}, 0, w)
	if err != nil {
		t.Fatalf("出稿失败: %v", err)
	}
	if len(w.calls) != 0 {
		t.Fatalf("样本不足不得出稿：写口被调用了 %d 次（噪声占用了人审人力）", len(w.calls))
	}
	if stat.SkippedInsufficient != 1 || stat.Created != 0 {
		t.Fatalf("样本不足不得出稿：账目错 %+v", stat)
	}
}

// D2 源码级：出稿层不许长出转正／自动生效的第二把尺子（配反证）。
func TestDraftGateNoSecondWriteSide(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	banned := []string{"approved", "Approve"}
	hits, scanned := scanProductionGo(dir, banned)
	if scanned < 2 {
		t.Fatalf("射程自证失败：只扫到 %d 个生产文件 ⇒ 扫描器失明", scanned)
	}
	if len(hits) != 0 {
		t.Fatalf("出稿层出现转正字样（人审是唯一入口）: %v", hits)
	}
	// 反证：同样的扫描器喂一行违规源码必须命中，防「扫描集为空＝恒绿」。
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "bad.go"), []byte("package x\n\nfunc f() { _ = \"approved\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	badHits, badScanned := scanProductionGo(tmp, banned)
	if badScanned != 1 || len(badHits) != 1 {
		t.Fatalf("反证必须命中 1 处，实得 scanned=%d hits=%v", badScanned, badHits)
	}
}

// D3 fail-closed：零值 Scope 与 tid<=0 都拒，不回落默认租户。
func TestDraftGateScopeFailClosed(t *testing.T) {
	withGenerator(t, func(salesflow.Scope, string) (string, error) { return "说法", nil })
	if _, err := DraftTemplates(salesflow.Scope{}, []Cluster{goodCluster()}, 0, &fakeWriter{}); !errors.Is(err, ErrInvalidDraftScope) {
		t.Fatalf("零值作用域必须拒，实得 %v", err)
	}
	if _, err := salesflow.NewScope(context.Background(), 0); !errors.Is(err, salesflow.ErrInvalidTenant) {
		t.Fatalf("tid<=0 必须 fail-closed，实得 %v", err)
	}
	if _, err := DraftTemplates(testScope(t), []Cluster{goodCluster()}, 0, nil); err == nil {
		t.Fatal("缺写口必须报错，不能静默丢稿")
	}
}

// D4 账目四档：归纳函数缺失／归纳失败／幂等命中／上限，各自不同形。
func TestDraftGateStatLegsAreDistinct(t *testing.T) {
	scope := testScope(t)
	good := goodCluster()

	// ① 归纳函数未注入：零账目、零写口调用。
	withGenerator(t, nil)
	w0 := &fakeWriter{}
	st0, err := DraftTemplates(scope, []Cluster{good}, 0, w0)
	if err != nil || st0.Created != 0 || len(w0.calls) != 0 {
		t.Fatalf("未注入应整轮跳过：stat=%+v calls=%d err=%v", st0, len(w0.calls), err)
	}

	// ② 归纳失败／空返回：只记 SkippedLLM，不落库。
	withGenerator(t, func(salesflow.Scope, string) (string, error) { return "", errors.New("模型不可用") })
	w1 := &fakeWriter{}
	st1, _ := DraftTemplates(scope, []Cluster{good}, 0, w1)
	if st1.SkippedLLM != 1 || st1.Created != 0 || len(w1.calls) != 0 {
		t.Fatalf("归纳失败账目错：%+v", st1)
	}

	// ③ 正常出稿 + 幂等命中 + 上限：三档各不同形。
	withGenerator(t, func(salesflow.Scope, string) (string, error) { return "  可复用说法  ", nil })
	st2, err := DraftTemplates(scope, []Cluster{good}, 0, &fakeWriter{})
	if err != nil || st2.Created != 1 {
		t.Fatalf("正常出稿应记 Created=1：%+v err=%v", st2, err)
	}
	id := minedDraftID(scope.TenantID(), good)
	st3, _ := DraftTemplates(scope, []Cluster{good}, 0, &fakeWriter{exists: map[string]bool{id: true}})
	if st3.SkippedExisting != 1 || st3.Created != 0 {
		t.Fatalf("幂等命中账目错：%+v", st3)
	}
	st4, _ := DraftTemplates(scope, []Cluster{good, good}, 1, &fakeWriter{})
	if st4.Created != 1 {
		t.Fatalf("maxDrafts=1 应只出 1 条，实得 %d", st4.Created)
	}
}

// D5 确定性 ID：同租户同簇恒等，换租户必变（跨租户不串）。
func TestDraftGateMinedIDIsDeterministicAndTenantScoped(t *testing.T) {
	c := goodCluster()
	a := minedDraftID(42, c)
	b := minedDraftID(42, c)
	if a != b || !strings.HasPrefix(a, "sdr_m_") {
		t.Fatalf("确定性 ID 形状/稳定性不对：%q / %q", a, b)
	}
	if minedDraftID(43, c) == a {
		t.Fatal("换租户后 ID 不变 ⇒ 跨租户会互相幂等掉草稿")
	}
}

// D6 提示词必须带已脱敏代表文本与占位符要求，且不带行业词。
func TestDraftGatePromptCarriesMaskedSample(t *testing.T) {
	recs := recordsFor(5, 3)
	for i := range recs {
		recs[i].Content = "请回拨 13800138000 确认配置"
	}
	p := BuildDraftPrompt(Mine(recs, Options{})[0], nil)
	if strings.Contains(p, "13800138000") || !strings.Contains(p, "{{price}}") {
		t.Fatal("提示词漏了脱敏或占位符约束")
	}
	if strings.Contains(p, "极石") || strings.Contains(p, "到店") {
		t.Fatal("提示词里出现行业字面（通用型红线）")
	}
}
