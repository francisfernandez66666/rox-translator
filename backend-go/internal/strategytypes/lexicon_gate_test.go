// ============ 本文件职责中文说明 ============
// 二期 §5 L1 纯函数层守卫：所有文本判定必须由配置词表驱动，
// 引擎生产源码里中文词表字面命中数必须归 0（按目录全集扫＋射程下限自证＋反证）。
// 同时钉住按词数的长短判据与 ctx 注入的零行为变化。
// ========================================
package strategytypes

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// bannedLexiconLiterals 外仓曾写死在引擎里的中文词表样本（按全集判，不许清空判绿）。
// 参数：无；返回：禁词全集。
func bannedLexiconLiterals() []string {
	return []string{"到店", "试驾", "预算不够", "太贵", "超出预算", "好哒", "不满意", "极" + "石"}
}

// scanEngineSources 扫指定根下各包的生产 .go（跳过测试文件），返回命中与扫描文件数。
// 参数：root=仓根；dirs=包目录名；banned=禁词；返回：命中列表、扫描数、错误。
func scanEngineSources(root string, dirs, banned []string) ([]string, int, error) {
	var hits []string
	scanned := 0
	for _, dir := range dirs {
		base := filepath.Join(root, "internal", dir)
		entries, err := os.ReadDir(base)
		if err != nil {
			return nil, scanned, err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(base, e.Name()))
			if err != nil {
				return nil, scanned, err
			}
			scanned++
			for i, line := range strings.Split(string(raw), "\n") {
				code := line
				if at := strings.Index(code, "//"); at >= 0 {
					code = code[:at]
				}
				for _, lit := range banned {
					if lit != "" && strings.Contains(code, lit) {
						hits = append(hits, dir+"/"+e.Name()+":"+strconv.Itoa(i+1))
						break
					}
				}
			}
		}
	}
	return hits, scanned, nil
}

// L1：引擎侧零中文词表字面；扫描集下限自证防「目录写漂＝恒绿」。
func TestStrategytypesHasNoLexiconLiterals(t *testing.T) {
	banned := bannedLexiconLiterals()
	if len(banned) < 6 {
		t.Fatalf("L1 禁词表被清空：只有 %d 项 ⇒ 本闸失明", len(banned))
	}
	hits, scanned, err := scanEngineSources(filepath.Join("..", ".."), []string{"strategytypes"}, banned)
	if err != nil {
		t.Fatalf("L1 扫描失败: %v", err)
	}
	if scanned < 2 {
		t.Fatalf("L1 射程自证失败：只扫到 %d 个生产文件", scanned)
	}
	if len(hits) != 0 {
		t.Fatalf("L1：纯函数层出现配置词表字面（必须外提到 sales_lexicon）: %v", hits)
	}
}

// L1 反证：扫描器必须真看得见一处字面。
func TestLexiconScannerHasTeeth(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "internal", "strategytypes")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("反证夹具建目录失败: %v", err)
	}
	bad := "package strategytypes\n\nvar words = []string{\"到店\"}\n"
	if err := os.WriteFile(filepath.Join(base, "bad.go"), []byte(bad), 0o644); err != nil {
		t.Fatalf("反证夹具写入失败: %v", err)
	}
	hits, scanned, err := scanEngineSources(dir, []string{"strategytypes"}, bannedLexiconLiterals())
	if err != nil {
		t.Fatalf("反证扫描失败: %v", err)
	}
	if scanned != 1 || len(hits) != 1 {
		t.Fatalf("L1 反证必须恰好 1 文件 1 命中，实得 files=%d hits=%v", scanned, hits)
	}
}

// 纯函数行为锁：词表注入、语气词精确比、按词数门槛、情绪比较、锚编码、ctx 零行为变化。
func TestPureFunctionsUseInjectedLexicon(t *testing.T) {
	greetings := []string{"hello", "hi"}
	if !HasAnyHit("Hi there", greetings) {
		t.Fatal("大小写归一化后的词表命中必须成立")
	}
	if HasAnyHit("nothing here", greetings) {
		t.Fatal("短 ASCII 词必须按词边界命中，不许把 nothing 里的 hi 判成寒暄")
	}
	if !HasAnyHit("please confirm lead time", []string{"lead time"}) {
		t.Fatal("带空格的短语必须按子串命中")
	}
	if CountHits("hi, hi, nothing", []string{"hi"}) != 2 {
		t.Fatal("词边界计数必须恰好 2 次（nothing 里的 hi 不算）")
	}
	noHook := []string{"ok", "hmm"}
	if CheckHooked("OK", noHook, 3) {
		t.Fatal("纯语气词精确相等必须判未接钩")
	}
	if !CheckHooked("please send the quotation", noHook, 3) {
		t.Fatal("有实际内容且过词数门槛必须判接钩")
	}
	if CheckHooked("太贵了", noHook, 5) {
		t.Fatal("无空白 CJK 文本按字符计量，门槛 5 时必须判未接钩")
	}
	if !CheckHooked("太贵了", noHook, 3) {
		t.Fatal("同一句按语种档位给 3 时必须判接钩（门槛由配置给，不写死在代码里）")
	}
	if DetectEmotion("too expensive", []string{"expensive"}, []string{"great"}) != "negative" {
		t.Fatal("负面词多时必须判 negative")
	}
	if DetectEmotion("great, thanks", []string{"expensive"}, []string{"great"}) != "positive" {
		t.Fatal("正面词多时必须判 positive")
	}
	if DetectEmotion("plain text", []string{"expensive"}, []string{"great"}) != "neutral" {
		t.Fatal("零命中必须判 neutral")
	}
	if GetAnchorName(AnchorScarcity) != "scarcity" || GetAnchorName(99) != "unknown" {
		t.Fatal("锚编码必须稳定且越界回落 unknown")
	}
	ctx := context.Background()
	if SalesPathFromContext(ctx) != nil {
		t.Fatal("未注入时 ctx 不许凭空长出决策")
	}
	d := &SalesPathDecision{CurrentStage: "inquiry", TargetStage: "quote"}
	if SalesPathFromContext(WithSalesPath(ctx, d)) != d {
		t.Fatal("注入后必须原样读回同一对象")
	}
	if WithSalesPath(ctx, nil) != ctx || WithSalesPath(nil, d) != nil {
		t.Fatal("nil 决策或 nil ctx 必须原样返回，保证零行为变化")
	}
}
