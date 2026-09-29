// ============ health_dispatch_readiness_test.go · 职责说明 ============
// /api/health 上「远程派发」那一组的**出栈形态**锁（2026-09-29 派发改造第二节）。
//
// 分工：判据本身（自检不通过／缺字段／到期都必须不就绪、四档内存帽怎么翻状态词）
// 锁在 internal/fileproc 的 fileproc_remote_test.go 里——那边有协议桩，能真拨真算哈希。
// 本文件只管这个**无鉴权公开面**的两件事：
//  1. 四个键必须在，缺一个运维脚本的 `jq -r .dispatch_mem_cap` 就会拿到 null 而误判"没配"；
//  2. 出栈里绝不允许出现远端主机名／IP／绝对路径／脚本名
//     （这条不是洁癖：/api/health 无鉴权，把它挂出去等于公开"有一台 2G 的体验机、密钥账号叫 fpd"）。
//
// ★ 负向锁必须配正向对照（AGENTS §一·5）：判"不含主机"的同一份响应里，
//
//	必须真带着 dispatch 状态词，否则"字段整个不存在"也能让负向恒真。
//
// ================================================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"translator/internal/config"
)

// TestHealthDispatchReadinessKeys 派发关闭态：四个键都在、状态词是 off、其余留空。
//
// 为什么关着也要出空串而不是干脆省掉键：健康面是给人和脚本对字段的，
// "关着"与"取不到"在字段缺失时长得一模一样（这正是本批要根除的那类静默）。
func TestHealthDispatchReadinessKeys(t *testing.T) {
	// ★ 自钉方言（AGENTS §一·4）：config.Default() 读 DB_DRIVER env 且副作用写全局 config.C，
	//   不钉的话 PG 模式跑批会把方言漏给同包后续内存 SQLite 用例。
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	t.Setenv("FILEPROC_DISPATCH", "")
	t.Setenv("FILEPROC_DISPATCH_HOST", "fpd@203.0.113.9")
	t.Setenv("FILEPROC_DISPATCH_ROOT", "/opt/fpdispatch")

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	(&Server{}).handleHealth(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health 应 200，实际 %d", rec.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("health 出参非法 JSON: %v", err)
	}
	for _, k := range []string{"dispatch", "dispatch_expire", "dispatch_mem_cap", "dispatch_selftest"} {
		v, ok := body[k]
		if !ok {
			t.Fatalf("健康面缺键 %q（关着也要出空串，不能省键）: %s", k, rec.Body.String())
		}
		if _, isStr := v.(string); !isStr {
			t.Fatalf("健康面 %q 应是状态词字符串，实际 %#v", k, v)
		}
	}
	if body["dispatch"] != "off" {
		t.Fatalf("未开闸时 dispatch 期望 off 实际 %v", body["dispatch"])
	}
	// 关着时其余三档留空：空串＝"没配"，不是"配了但坏了"（后者由 unknown 承担）
	for _, k := range []string{"dispatch_expire", "dispatch_mem_cap", "dispatch_selftest"} {
		if body[k] != "" {
			t.Fatalf("派发关闭时 %q 应为空串，实际 %q", k, body[k])
		}
	}
	// 负向锁：配置里明明有主机与绝对路径，出栈一个都不许带
	for _, forbidden := range []string{"203.0.113.9", "fpd@", "/opt/fpdispatch", "fpdexec", ".venv"} {
		if strings.Contains(rec.Body.String(), forbidden) {
			t.Fatalf("健康面泄露远端坐标 %q: %s", forbidden, rec.Body.String())
		}
	}
	// 反向对照：把总闸打开但没有任何远端可读 ⇒ 状态词必须翻成 degraded 且其余档出 unknown，
	// 而不是停在 off／也不是填一个"看着正常"的默认值。
	// 反向对照：开闸但远端探不通 ⇒ degraded ＋ unknown。
	// 用一个"必失败"的假 ssh 顶在 PATH 最前，而不是真去拨那个地址：
	// 单测不许往公网拨（本包也没有桩的协议形状可讲），且 15s 墙钟会把这条锁拖成慢用例。
	deadBin := t.TempDir()
	dead := filepath.Join(deadBin, "ssh")
	if err := os.WriteFile(dead, []byte("#!/bin/sh\nexit 255\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", deadBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FILEPROC_DISPATCH", "1")
	t.Setenv("FILEPROC_DISPATCH_PROBE_TTL_SEC", "0") // 不留失败缓存给同包后续用例
	rec2 := httptest.NewRecorder()
	(&Server{}).handleHealth(rec2, req)
	var body2 map[string]interface{}
	if err := json.Unmarshal(rec2.Body.Bytes(), &body2); err != nil {
		t.Fatal(err)
	}
	if body2["dispatch"] != "degraded" {
		t.Fatalf("开闸却探不到远端时期望 degraded，实际 %v（body=%s）", body2["dispatch"], rec2.Body.String())
	}
	if body2["dispatch_mem_cap"] != "unknown" || body2["dispatch_selftest"] != "unknown" {
		t.Fatalf("取不到读数必须写 unknown，不得兜一个正常态：%s", rec2.Body.String())
	}
	// 同一份响应里不得出现远端坐标（开了闸也是一样的红线）
	for _, forbidden := range []string{"203.0.113.9", "/opt/fpdispatch", "fpdexec"} {
		if strings.Contains(rec2.Body.String(), forbidden) {
			t.Fatalf("开闸态健康面泄露远端坐标 %q: %s", forbidden, rec2.Body.String())
		}
	}
}
