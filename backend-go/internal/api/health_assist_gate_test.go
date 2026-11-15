// ============ health_assist_gate_test.go · 职责说明 ============
// 四期 W5（施工图 §4.5.5 验收第 3 条）：/api/health 必须出 assist 段。
//
// 旧形态：health 只有 sales_facts / knowledge 两段（审计 §3.3），
// 即使本地引擎装上了，部署机也没有任何现网读数能证明「并进程发生了」。
//
// 三条腿：
//
//	① 源码接线腿：server.go 的 health map 里必须有 `"assist": s.assistHealth(),`
//	   （函数写得再好，没人调它等于这一面不存在）；
//	② 行为腿：未装配 ⇒ mode=proxy / ready=false；显式 ASSIST_LINK=off ⇒ mode=off；
//	③ 反证（进程内）：把 ready 写死 true ⇒ 未装配态必红；只出状态词、不出坐标。
//
// ================================================
package api

import (
	"os"
	"strings"
	"testing"
)

// withNoAssistLocal 把包级装配清空并复原，保证本用例不受同包其他测试的装配污染。
func withNoAssistLocal(t *testing.T) {
	t.Helper()
	old := assistLocalPtr.Load()
	assistLocalPtr.Store(nil)
	t.Cleanup(func() { assistLocalPtr.Store(old) })
}

func TestHealthAssistSegmentWired(t *testing.T) {
	b, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("读 server.go 失败: %v", err)
	}
	if !strings.Contains(string(b), `"assist": s.assistHealth(),`) {
		t.Fatal("验收#3 未达成：/api/health 的 map 里没有 assist 段 ⇒ 收敛与否在现网不可见")
	}
}

func TestHealthAssistReflectsReality(t *testing.T) {
	withNoAssistLocal(t)
	s := &Server{}

	// ③ 反证：测试进程此刻**没有**装配本地引擎，ready 必须是 false。
	h := s.assistHealth()
	if h["ready"] != false {
		t.Fatalf("反证失效：未装配却报 ready=true（把状态词写死会让本锁绿着说谎）")
	}
	if h["mode"] != "proxy" {
		t.Fatalf("未装配应回落 proxy，实得 %v", h["mode"])
	}
	// ② 两个显式档位；t.Setenv 会在用例结束自动复原。
	t.Setenv(assistLinkLocalEnv, "off")
	if got := s.assistHealth()["mode"]; got != "off" {
		t.Fatalf("ASSIST_LINK=off 应报 off，实得 %v", got)
	}
	t.Setenv(assistLinkLocalEnv, "remote")
	if got := s.assistHealth()["mode"]; got != "proxy" {
		t.Fatalf("ASSIST_LINK=remote 应报 proxy，实得 %v", got)
	}
	// 只出状态词与布尔：绝不把库路径 / token / 端点清单带进匿名健康面。
	if len(h) != 2 {
		t.Fatalf("assist 段字段数 %d（应恰为 mode/ready 两项，不许外泄坐标）", len(h))
	}
}
