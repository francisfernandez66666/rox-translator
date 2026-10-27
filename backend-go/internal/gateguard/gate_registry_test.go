package gateguard

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// repoRoot 从本包目录回到仓根（backend-go/internal/gateguard → ../../..）。
func repoRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("取仓根失败：%v", err)
	}
	return abs
}

// lockFiles 现算家族级锁全集（**不写死数量**；数量只做射程自证的下界）。
func lockFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	// Go：backend-go 下所有 *_gate_test.go
	err := filepath.WalkDir(filepath.Join(root, "backend-go"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), "_gate_test.go") {
			rel, _ := filepath.Rel(root, p)
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("遍历 backend-go 失败：%v", err)
	}
	// TS：frontend-react/src 下的 lock/gate/wiring 测试
	err = filepath.WalkDir(filepath.Join(root, "frontend-react", "src"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			return nil
		}
		for _, suf := range []string{".lock.test.ts", ".lock.test.tsx", ".gate.test.ts", ".gate.test.tsx", ".wiring.test.ts"} {
			if strings.HasSuffix(name, suf) {
				rel, _ := filepath.Rel(root, p)
				out = append(out, rel)
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("遍历 frontend-react/src 失败：%v", err)
	}
	sort.Strings(out)
	return out
}

// readBaseline 读 M1 落地时的锁基线（只减不增的台账）。
func readBaseline(t *testing.T, root string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "scripts", "locks_baseline.txt"))
	if err != nil {
		t.Fatalf("读不到 scripts/locks_baseline.txt：%v ⇒ meta 闸门的台账必须随仓库提交", err)
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = true
	}
	return out
}

// registryRows 读变异登记表：返回 id→lock 与 lock 集合。
func registryRows(t *testing.T, root string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "scripts", "gate_mutants.tsv"))
	if err != nil {
		t.Fatalf("读不到 scripts/gate_mutants.tsv：%v", err)
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "id\t") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 3 {
			t.Fatalf("登记表有一行列数不足（tab 分隔）：%q", line)
		}
		if _, dup := out[f[0]]; dup {
			t.Fatalf("登记表 id 重复：%s", f[0])
		}
		out[f[0]] = f[1]
	}
	return out
}

// TestRegistryCoversNewLocks C1：**不在基线里的锁**必须登记变异体；
// 基线条目必须仍存在（删锁要同步删台账行）。数量下界只作射程自证（防 glob 写漂）。
func TestRegistryCoversNewLocks(t *testing.T) {
	root := repoRoot(t)
	locks := lockFiles(t, root)
	if len(locks) < 80 {
		t.Fatalf("现算到的家族级锁只有 %d 个（<80）⇒ glob 写漂了，本锁已失明（不是「锁变少了」）", len(locks))
	}
	baseline := readBaseline(t, root)
	reg := registryRows(t, root)
	registered := map[string]bool{}
	for _, l := range reg {
		registered[l] = true
	}
	// C1：新增锁必须登记。
	for _, l := range locks {
		if baseline[l] {
			continue
		}
		if !registered[l] {
			t.Errorf("新增锁未登记变异体：%s ⇒ 在 scripts/gate_mutants.tsv 里加一行（这是 meta 闸门的设计）", l)
		}
	}
	// C1b：基线条目必须仍存在（删锁必须同步删台账行，否则台账变成"指向空气"）。
	exists := map[string]bool{}
	for _, l := range locks {
		exists[l] = true
	}
	for l := range baseline {
		if !exists[l] {
			t.Errorf("基线里的锁已不存在：%s ⇒ 删锁时请同步删 scripts/locks_baseline.txt 的这一行", l)
		}
	}
}

// TestRegistryEntriesExist C2：登记表里的锁与 target 都必须存在。
func TestRegistryEntriesExist(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "scripts", "gate_mutants.tsv"))
	if err != nil {
		t.Fatalf("读不到登记表：%v", err)
	}
	n := 0
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "id\t") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 5 {
			t.Fatalf("登记表行字段不足（需要 id/lock/kind/run/target）：%q", line)
		}
		n++
		if _, err := os.Stat(filepath.Join(root, f[1])); err != nil {
			t.Errorf("%s 的 lock 不存在：%s", f[0], f[1])
		}
		if _, err := os.Stat(filepath.Join(root, f[4])); err != nil {
			t.Errorf("%s 的 target 不存在：%s", f[0], f[4])
		}
	}
	if n == 0 {
		t.Fatal("登记表为空 ⇒ meta 闸门一条反证都没有（至少要有 1 条）")
	}
}

// TestExemptListFrozen C3：EXEMPT 名单只减不增（当前为空；加一条必须同步改这个哈希）。
func TestExemptListFrozen(t *testing.T) {
	// 名单内容与哈希**成对**：名单为空时哈希＝sha256("")。
	exempt := []string{}
	sum := sha256.Sum256([]byte(strings.Join(exempt, "\n")))
	const want = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" // sha256("")
	if got := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("EXEMPT 名单被改动（当前哈希 %s）⇒ 新增豁免必须写明理由并同步本锁的期望值", got)
	}
}

// TestScopeTargetsExist C4：射程登记的注入目标必须存在（或其父目录存在，供新建文件）。
func TestScopeTargetsExist(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "scripts", "gate_scope.tsv"))
	if err != nil {
		t.Fatalf("读不到 scripts/gate_scope.tsv：%v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "id\t") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 4 {
			t.Fatalf("射程登记行字段不足：%q", line)
		}
		target := filepath.Join(root, f[3])
		if _, err := os.Stat(target); err == nil {
			continue
		}
		if _, err := os.Stat(filepath.Dir(target)); err != nil {
			t.Errorf("%s 的 inject_target 与父目录都不存在：%s", f[0], f[3])
		}
	}
}
