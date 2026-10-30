// ============ seed_retired_formats_gate_test.go · 职责说明 ============
// 钉住 2026-10-30「格式收缩＋音频交付简化」在**挂件知识源**里没有留下旧承诺。
//
// 为什么这条必须单独存在（seed_gate_test.go 那把尺子够不到的地方）：
//
//	`promise.go` 的【承诺边界】管的是模型开口那一刻，而撞关键词直出的中文话术**不经模型**。
//	本批撤掉的交付形态恰好就写在那几条 kb 正文里（「srt/vtt/json/yaml 以对照表（xlsx）交付」
//	「交付三件——带原时间轴的译文字幕（srt）…」）。seed 只首启灌库，存量库靠
//	`scripts/assist_kb_sync.py --apply` 跟进——**文案改了但库里没同步**是这一族的已知盲区，
//	而「文案压根没改」更坏：那条锁只在源码层看得见，所以这里补一道源头闸门，
//	让"旧句子写回 seed.json"在提交前就红，而不是等客户截图才发现挂件还在承诺对照表。
//
// 三条判据各管一件事：
//
//	① 负向（退役承诺清零）：四种旧形态的字面一个都不许再出现在任何直出文本里；
//	② 正向对照（新口径真写进去了）：只判"旧的没了"会被"整段删空"糊过去，
//	   所以新写的那两句必须能在同一批文本里读到；
//	③ 反证：把旧句子喂进**同一个**判定函数必须被抓（证明 ① 有射程，不是恒真）。
//
// ⚠️ ① 的名单只收「交付形态的旧写法」这种**完整短语**，不收「对照表」「字幕」这类裸词：
//
//	裸词会命中「原文与译文对照的文本文件」这类仍然正确的说法，也会命中本文件自己的注释语境，
//	那种锁的归宿是被人在下一批整个删掉（AGENTS §一·14「判红一次的代价是闸门被人摘掉」）。
//	名单里每一条都对应 2026-10-30 之前 seed.json 里真实存在过的一句话，不是凭印象编的。
//
// ==========================================================
package seed

import (
	"strings"
	"testing"
)

// retiredDeliveryPromisesSeed 是 2026-10-30 起不再成立的四种旧承诺写法。
// 逐条对应改前的 seed.json 原文（kb 能力一览／格式一览／音频三条）。
var retiredDeliveryPromisesSeed = []string{
	"以对照表（xlsx）交付",
	"交付三件——带原时间轴的译文字幕（srt）",
	"文档/PPT/表格/PDF/字幕等",
}

// newCalibrationPhrases 是新口径的字面（正向对照：删空不算改对）。
var newCalibrationPhrases = []string{
	"不再作为文件入口",
	"交付两件",
}

// scanRetiredPromises 是 ① 与 ③ 共用的判定本体（正例反例同一把尺子）。
func scanRetiredPromises(where, text string) []string {
	var bad []string
	for _, p := range retiredDeliveryPromisesSeed {
		if strings.Contains(text, p) {
			bad = append(bad, where+"：仍在承诺已退役的交付形态「"+p+"」")
		}
	}
	return bad
}

func TestSeedHasNoRetiredDeliveryPromise(t *testing.T) {
	texts := seedTexts(t)
	if len(texts) < 8 {
		t.Fatalf("seed 文本只摊平出 %d 条 ⇒ 判据射程可疑（结构变了会让这条锁恒绿）", len(texts))
	}
	var bad []string
	for where, text := range texts {
		bad = append(bad, scanRetiredPromises(where, text)...)
	}
	for _, b := range bad {
		t.Error(b)
	}

	// ② 正向对照：新口径必须真写在某条文本里（不是被删空）。
	joined := strings.Builder{}
	for _, text := range texts {
		joined.WriteString(text)
		joined.WriteString("\n")
	}
	all := joined.String()
	for _, p := range newCalibrationPhrases {
		if !strings.Contains(all, p) {
			t.Errorf("seed 里读不到新口径「%s」⇒ 要么没改，要么整段被删空（删空同样是不告诉客户路怎么走）", p)
		}
	}

	// ③ 反证：旧句子喂进同一个函数必须被抓。
	dead := "支持的文件格式：docx/pptx/xlsx 直接输出，srt/vtt/json/yaml 以对照表（xlsx）交付"
	if len(scanRetiredPromises("反证", dead)) == 0 {
		t.Error("反证落空：把旧承诺句子写回来却没被抓 ⇒ 上面那条负向锁射程为零")
	}
	// 反证的另一半：新写法不许被误判（否则这条锁第二天就被摘）。
	live := "交付两件——同容器的译后配音与译文 .md；.srt 等五类 2026-10-30 起不再作为文件入口"
	if v := scanRetiredPromises("反证·新写法", live); len(v) != 0 {
		t.Errorf("新口径被判成旧承诺（判据过严）：%v", v)
	}
}
