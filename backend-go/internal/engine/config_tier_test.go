// ============ 本文件职责中文说明 ============
// 2026-10-24 云端 UAT 修复批「档位表」的等值单测（engine 侧那一半）。
// 方案原话：「所有键都要有**等值单测**（键名、默认值、非法值回退），避免「配了不生效」」。
//
// 这里逐键钉三件事，缺一件就是现网那种「运营改了没反应」：
//
//	① **键名**：断言里写的是运行期读点那一个字符串常量口径（拼错一个字母＝读不到那行＝恒走默认，
//	   而管理台保存成功、日志零红——本仓 §一·6 第⑥条「探针写库前先问产品读的是哪一把键」同族）；
//	② **默认值**：没配那一行时必须等于方案定的档（2000/30/0.05/3/40）；
//	③ **非法值回退**：脏值不许冒充一个档位（`0`／负数／非数字都按"未配"回落默认，
//	   因为这几枚都是"上限/轮数/调用数"语义——0 轮补译＝阈值判了却一次都不补，
//	   比读默认更坏；真要临时放开走 999999，方案回滚表写的也是这一档）。
//
// env 档优先于库档（AGENTS §一·3）另配一条对照腿，防止热修那行环境变量被库挤掉。
// =============================================
package engine

import (
	"context"
	"testing"
)

// ctEngine 造一个只带 Store 的引擎（这几枚档位都不碰模型与文件系统）。
func ctEngine(t *testing.T) *Engine {
	t.Helper()
	return &Engine{St: newTestStore(t)}
}

// TestCultureRulesMaxCharsTier culture_rules_max_chars：默认 2000／配档生效／脏值回落。
func TestCultureRulesMaxCharsTier(t *testing.T) {
	e := ctEngine(t)
	cases := []struct {
		val  string
		want int
	}{
		{"", 2000}, {"abc", 2000}, {"0", 2000}, {"-5", 2000}, {"1500", 1500}, {" 999999 ", 999999},
	}
	for _, c := range cases {
		if err := e.St.SetConfig("culture_rules_max_chars", c.val); err != nil {
			t.Fatalf("SetConfig: %v", err)
		}
		if got := e.cultureLimit().MaxChars; got != c.want {
			t.Errorf("culture_rules_max_chars=%q 应=%d，实得 %d", c.val, c.want, got)
		}
	}
}

// TestCultureRulesMaxItemsTier culture_rules_max_items：默认 30／配档生效／脏值回落。
func TestCultureRulesMaxItemsTier(t *testing.T) {
	e := ctEngine(t)
	cases := []struct {
		val  string
		want int
	}{
		{"", 30}, {"x", 30}, {"0", 30}, {"-1", 30}, {"5", 5}, {"999999", 999999},
	}
	for _, c := range cases {
		if err := e.St.SetConfig("culture_rules_max_items", c.val); err != nil {
			t.Fatalf("SetConfig: %v", err)
		}
		if got := e.cultureLimit().MaxItems; got != c.want {
			t.Errorf("culture_rules_max_items=%q 应=%d，实得 %d", c.val, c.want, got)
		}
	}
}

// TestCultureRulesRelevanceTier culture_rules_relevance：默认开；只有字面 "0" 关。
// 脏值不表态＝保持默认开（把打错的一个字母读成"关"，等于整块规则按无关序进 prompt，
// 运营看到的却是"我只改了一个字符"）。
func TestCultureRulesRelevanceTier(t *testing.T) {
	e := ctEngine(t)
	cases := []struct {
		val  string
		want bool
	}{
		{"", true}, {"1", true}, {"true", true}, {"maybe", true}, {" 0", false}, {"0", false},
	}
	for _, c := range cases {
		if err := e.St.SetConfig("culture_rules_relevance", c.val); err != nil {
			t.Fatalf("SetConfig: %v", err)
		}
		if got := e.cultureLimit().Relevance; got != c.want {
			t.Errorf("culture_rules_relevance=%q 应=%v，实得 %v", c.val, c.want, got)
		}
	}
}

// TestFileLeakRatioTier file_leak_repair_ratio：默认 0.05／回滚档 0.5／脏值回落／env 压过库档。
func TestFileLeakRatioTier(t *testing.T) {
	e := ctEngine(t)
	cases := []struct {
		val  string
		want float64
	}{
		{"", 0.05}, {"abc", 0.05}, {"0", 0.05}, {"-1", 0.05}, {"1.5", 0.05}, // >1 不是合法比例
		{"0.5", 0.5}, {"0.10", 0.10},
	}
	for _, c := range cases {
		if err := e.St.SetConfig("file_leak_repair_ratio", c.val); err != nil {
			t.Fatalf("SetConfig: %v", err)
		}
		if got := e.fileLeakRatio(); got != c.want {
			t.Errorf("file_leak_repair_ratio=%q 应=%v，实得 %v", c.val, c.want, got)
		}
	}
	// env 档优先（运维急救那一行压得住管理台）
	if err := e.St.SetConfig("file_leak_repair_ratio", "0.5"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	t.Setenv("FILE_LEAK_REPAIR_RATIO", "0.05")
	if got := e.fileLeakRatio(); got != 0.05 {
		t.Errorf("env 必须压过库档，实得 %v", got)
	}
	// env 脏值不表态 ⇒ 落回库档 0.5（同 §一 那条「脏值一律不表态，落到下一档」）
	t.Setenv("FILE_LEAK_REPAIR_RATIO", "banana")
	if got := e.fileLeakRatio(); got != 0.5 {
		t.Errorf("env 脏值不许冒充表态把库档挤掉，实得 %v", got)
	}
}

// TestFileRepairMaxRoundsTier file_repair_max_rounds：默认 3／配档生效／脏值与 0 回落／env 优先。
// 0 之所以按脏值处理（与 ticket_repair_max 那一枚不同）：这一档的语义是"追加补译几轮"，
// 0 会让 5% 阈值判定成立后一次都不补，产物带缺失段却报完成——方案回滚表给的临时档是 1/999999。
func TestFileRepairMaxRoundsTier(t *testing.T) {
	e := ctEngine(t)
	cases := []struct {
		val  string
		want int
	}{
		{"", 3}, {"abc", 3}, {"0", 3}, {"-2", 3}, {"1", 1}, {"999999", 999999},
	}
	for _, c := range cases {
		if err := e.St.SetConfig("file_repair_max_rounds", c.val); err != nil {
			t.Fatalf("SetConfig: %v", err)
		}
		if got := e.fileRepairMaxRounds(); got != c.want {
			t.Errorf("file_repair_max_rounds=%q 应=%d，实得 %d", c.val, c.want, got)
		}
	}
	if err := e.St.SetConfig("file_repair_max_rounds", "1"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	t.Setenv("FILE_REPAIR_MAX_ROUNDS", "5")
	if got := e.fileRepairMaxRounds(); got != 5 {
		t.Errorf("FILE_REPAIR_MAX_ROUNDS 必须压过库档，实得 %d", got)
	}
}

// TestFileHardgateMaxCallsTier file_hardgate_max_calls：默认 40／配档生效／脏值回落／env 优先。
// 这一枚是成本硬闸（5% 阈值把补译放大时的天花板），脏值必须回落到默认而不是 0——
// 回落 0＝一条都不补（漏译常驻），回落默认＝按既定预算补。
func TestFileHardgateMaxCallsTier(t *testing.T) {
	e := ctEngine(t)
	cases := []struct {
		val  string
		want int
	}{
		{"", 40}, {"x", 40}, {"0", 40}, {"-1", 40}, {"10", 10}, {"999999", 999999},
	}
	for _, c := range cases {
		if err := e.St.SetConfig("file_hardgate_max_calls", c.val); err != nil {
			t.Fatalf("SetConfig: %v", err)
		}
		if got := e.fileRepairMaxCalls(); got != c.want {
			t.Errorf("file_hardgate_max_calls=%q 应=%d，实得 %d", c.val, c.want, got)
		}
	}
	if err := e.St.SetConfig("file_hardgate_max_calls", "10"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	t.Setenv("FILE_HARDGATE_MAX_CALLS", "1")
	if got := e.fileRepairMaxCalls(); got != 1 {
		t.Errorf("FILE_HARDGATE_MAX_CALLS 必须压过库档（成本止血那一档），实得 %d", got)
	}
}

// TestArtifactLangSuffixTier artifact_lang_suffix：默认 `-<lang>`／配 0 回退旧分隔符 `_`／脏值不表态。
// 这一枚是 O-03（2026-10-24 用户令「译文文件名统一 -<lang>」）的应急回退档：
// 脏值必须回到**新档**（`-`）而不是旧档——回退只在显式写 0 时发生，
// 否则"打错一个字符"就把新产物改名，客户侧脚本按 `-en.txt` 找文件会静默找不到。
func TestArtifactLangSuffixTier(t *testing.T) {
	e := ctEngine(t)
	cases := []struct {
		val  string
		want string
	}{
		{"", "-en"}, {"1", "-en"}, {"true", "-en"}, {"maybe", "-en"},
		{"0", "_en"}, {" 0", "_en"}, // 库里带前导空格仍是 0 档
	}
	for _, c := range cases {
		if err := e.St.SetConfig("artifact_lang_suffix", c.val); err != nil {
			t.Fatalf("SetConfig: %v", err)
		}
		if got := e.artifactLangSuffix("en"); got != c.want {
			t.Errorf("artifact_lang_suffix=%q 应=%q，实得 %q", c.val, c.want, got)
		}
	}
}

// TestFileEchoGuardTier file_echo_guard：默认开／库档 0 关（应急）／脏值不表态／env 压过库档。
// 这一枚补的是本批对账抓出的缺口：读点原先只看 env `FILE_ECHO_GUARD`，
// 方案 §10.1.1 写的是「env ＞ system_config ＞ 代码默认」⇒ 运营在管理台填 0 完全不生效，
// 关闸必须重启进程（现网表现＝"改了配置没反应"，而应急档的意义正是**不发版就能关**）。
// 靶子是同包那条回显样本：判据必须真的作用在 fileTranslationUsable 上（守卫级），不是只测开关函数。
func TestFileEchoGuardTier(t *testing.T) {
	cases := []struct {
		val      string
		wantEcho bool // 期望「回显样本被判不可用」
	}{
		{"", true},      // 缺省＝开闸（DEF-01 的修法本体）
		{"1", true},     // 显式开
		{"maybe", true}, // 脏值不许冒充"关"：关掉闸＝把提示词回显写进客户产物
		{"0", false},    // ★ 应急档：库里填 0 必须当场生效
		{" 0", false},   // 带前导空格仍是 0 档（保存链路不保证 trim）
	}
	for _, c := range cases {
		t.Run("库档"+c.val, func(t *testing.T) {
			t.Setenv("FILE_ECHO_GUARD", "") // 钉掉 env 档，否则上一轮 t.Setenv 会跨用例泄漏
			e := ctEngine(t)
			if err := e.St.SetConfig("file_echo_guard", c.val); err != nil {
				t.Fatalf("SetConfig: %v", err)
			}
			if got := e.fileTranslationUsable(context.Background(), echoSampleSrc, echoSampleOut, "en"); got == c.wantEcho {
				t.Fatalf("file_echo_guard=%q：回显样本判可用=%v，期望相反（=%v 才算这档生效）", c.val, got, !c.wantEcho)
			}
			// 合法译文在任一档都必须放行——关闸只关"回显判据"，不关基础可用性
			legit := "First paragraph: This is a Chinese sample text for acceptance, containing the industry term Sales Leads."
			if !e.fileTranslationUsable(context.Background(), echoSampleSrc, legit, "en") {
				t.Fatalf("file_echo_guard=%q 时合法译文被误杀", c.val)
			}
		})
	}
	// env 档优先于库档（运维那一行必须压得住管理台；同 AGENTS §一·3「环境变量 > 数据库配置」）
	e := ctEngine(t)
	if err := e.St.SetConfig("file_echo_guard", "1"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	t.Setenv("FILE_ECHO_GUARD", "0")
	if !e.fileTranslationUsable(context.Background(), echoSampleSrc, echoSampleOut, "en") {
		t.Fatal("env=0 必须压过库档 1（应急档不该要求先改库再重启）")
	}
	// env 脏值不表态 ⇒ 落到库档 1＝开闸（脏值不许冒充"关"）
	t.Setenv("FILE_ECHO_GUARD", "banana")
	if e.fileTranslationUsable(context.Background(), echoSampleSrc, echoSampleOut, "en") {
		t.Fatal("env 脏值不许冒充表态把库档挤掉")
	}
}
