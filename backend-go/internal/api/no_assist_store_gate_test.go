// ============ no_assist_store_gate_test.go · 职责说明 ============
// 四期 §4.5.3 **A6**：主服务（internal/api）不再 import assist/store。
// 会话/消息迁主库之后，api 层的**业务读写**不许再直接握 assist 独立库；
// 本地装配只持有 *assist/engine.Engine（引擎内部依赖不算 api 的直接 import），
// 会话迁移只接两个 *sql.DB 句柄，都不在 api 层直接 import assist/store。
//
// 反证（M0303）：在任一 api 非测试文件加回一处 import ⇒ 红。
// ★ 判据只认带引号的完整 import path，注释里出现的
//
//	「internal/assist/store/store.go」/「*assist/store.DB」不算命中——
//	否则说明性注释会让锁恒红，那才是空转锁。
//
// ================================================
package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const a6AssistStoreImport = `"translator/internal/assist/store"`

// a6ImportCount 一段源码里 assist/store 的 import 次数（只认带引号的 import path）。
func a6ImportCount(src string) int {
	return strings.Count(src, a6AssistStoreImport)
}

// TestA6NoAssistStoreImport api 全目录零直接 import + 本地装配形态在位。
func TestA6NoAssistStoreImport(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("A6 列 api 目录失败: %v", err)
	}
	total := 0
	offenders := []string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue // 测试夹具可以用 assist/store 造旧库，那是搬迁自证，不是业务读写
		}
		b, rerr := os.ReadFile(f)
		if rerr != nil {
			t.Fatalf("A6 读 %s 失败: %v", f, rerr)
		}
		if n := a6ImportCount(string(b)); n > 0 {
			total += n
			offenders = append(offenders, f)
		}
	}
	if total != 0 {
		t.Fatalf("A6 主服务 api 层出现 %d 处 assist/store 直接 import（期望 0）：%v ——加回一处必红（M0303）", total, offenders)
	}

	// 补偿判据：本地装配确实持有引擎、迁移确实只接句柄（不是「干脆没装」混过零 import）。
	localB, err := os.ReadFile("assist_local.go")
	if err != nil {
		t.Fatalf("A6 读 assist_local.go 失败: %v", err)
	}
	if !strings.Contains(string(localB), `assistengine "translator/internal/assist/engine"`) {
		t.Fatal("A6 本地装配没有持有 *assist/engine.Engine：零 import 可能是「干脆没收敛」")
	}
	migB, err := os.ReadFile("assist_session_migrate.go")
	if err != nil {
		t.Fatalf("A6 读 assist_session_migrate.go 失败: %v", err)
	}
	if !strings.Contains(string(migB), "mainDB, assistDB *sql.DB") {
		t.Fatal("A6 会话迁移没有按 *sql.DB 句柄接收两库：可能改回了直接读写 assist/store")
	}

	// ★ 反证（射程自证）：加回一处 import 的坏样本必须被计数判据抓到。
	bad := "package api\n\nimport (\n\t\"context\"\n\t_ " + a6AssistStoreImport + "\n)\n"
	if a6ImportCount(bad) != 1 {
		t.Fatal("A6 反证失明：坏样本里的 assist/store import 没被数出来 ⇒ 扫描器空转")
	}
	if a6ImportCount(`// 说明里提到 translator/internal/assist/store 不算 import`) != 0 {
		t.Fatal("A6 判据过敏：注释里的裸路径被当成 import，锁会恒红")
	}
}
