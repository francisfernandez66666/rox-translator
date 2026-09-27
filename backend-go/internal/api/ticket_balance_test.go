// 改进2回归测试：文件/文本工单建单前余额预检
//   - estimateTicketTokens：★ F-41（2026-09-25 批 D）改为按模式系数 K 估算（纯函数），
//     ★ F-72（2026-09-27 〇-W）在其上补固定开销项，成「固定项＋线性项」两段式
//   - 边界：空字符、无语言、非法 K 回退、超大文本、固定项 0/负数
//   - 基准锁：工单 88/89 生产实测数据（est 必须覆盖实烧/外推需求，宁高勿低）
package api

import "testing"

// estDefaultParams 生产缺省档的两段式参数（K：pro 160 / fast 60；F：pro 3,000 / fast 1,200）。
// 锁里一律用它，公式改动只会让下面这批等值一起红，不会把「调档」和「改公式」两件事混在一条红里。
func estDefaultParams() estParams {
	return estParams{kPro: 160, kFast: 60, fixedPro: 3000, fixedFast: 1200}
}

// TestEstimateTicketTokens F-72 后的口径：est = F(mode) + chars × langs × K(mode)。
// 两档参数由结构体注入（配置读取另有 estTokensPerChar / estTokensFixed 专项测试），本测试锁公式本身。
func TestEstimateTicketTokens(t *testing.T) {
	cases := []struct {
		name      string
		chars     int64
		langCount int
		mode      string
		p         estParams
		want      int64 // 等值锁（公式无方言/浮点歧义，直接钉死）
	}{
		{"pro3语5k字", 5000, 3, "pro", estDefaultParams(), 2403000},
		{"fast单语1k字", 1000, 1, "fast", estDefaultParams(), 61200},
		{"mode空按pro默认", 100, 1, "", estDefaultParams(), 19000},
		{"无语言不估算", 5000, 0, "pro", estDefaultParams(), 0},
		{"无字符不估算", 0, 2, "pro", estDefaultParams(), 0},
		{"K非法回退保守默认160", 100, 1, "pro", estParams{kPro: 0, kFast: -5, fixedPro: 3000, fixedFast: 1200}, 19000},
		// fast 的脏 K 同样回退到**更高的 pro 档**（回退方向只能更保守），固定项仍按 fast 档取。
		{"fast脏K回退pro档不回落fast档", 100, 1, "fast", estParams{kPro: 160, kFast: 0, fixedPro: 3000, fixedFast: 1200}, 17200},
		// ★ F-72 主证据：19 字 ×1 语的 pro 单（生产工单 47 的规模），旧纯线性给 3,040、实烧 18,304。
		{"短单19字pro含固定项", 19, 1, "pro", estDefaultParams(), 6040},
		// 固定项配 0 = 运维显式退回纯线性（不是脏配置），必须原样生效，否则「关掉固定项」这条运维手段无效。
		{"固定项0退回纯线性", 19, 1, "pro", estParams{kPro: 160, kFast: 60}, 3040},
		// 固定项为负属脏配置：回退 pro 档 3,000（负数会把预估往回扣，比清零更危险）。
		{"固定项负数回退pro档", 19, 1, "pro", estParams{kPro: 160, kFast: 60, fixedPro: -1, fixedFast: -1}, 6040},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := estimateTicketTokens(c.chars, c.langCount, c.mode, c.p)
			if got != c.want {
				t.Fatalf("estimateTicketTokens(%d, %d, %q, %+v) = %d，期望 %d",
					c.chars, c.langCount, c.mode, c.p, got, c.want)
			}
		})
	}
}

// TestEstimateTicketTokensMonotonic 更大文本/更多语言估算更大（单调性 sanity），且 fast < pro。
func TestEstimateTicketTokensMonotonic(t *testing.T) {
	p := estDefaultParams()
	small := estimateTicketTokens(1000, 1, "pro", p)
	larger := estimateTicketTokens(10000, 1, "pro", p)
	if larger <= small {
		t.Fatalf("更大文本估算应更大，small=%d larger=%d", small, larger)
	}
	if many := estimateTicketTokens(1000, 3, "pro", p); many <= small {
		t.Fatalf("更多语言估算应更大，single=%d multi=%d", small, many)
	}
	if fast := estimateTicketTokens(1000, 1, "fast", p); fast >= small {
		t.Fatalf("fast 估算应低于 pro，fast=%d pro=%d", fast, small)
	}
}

// TestEstimateTicketTokensNeverBelowFixed ★ F-72 断言①「同一单预估必须 ≥ 每次调用固定项」：
// 固定项就是「一次调用最少要烧掉的 token」，任何有效单（chars/langs>0）估出来低于它，
// 等于短单低估复发——这条与字数无关，纯线性年代它恒假（1 字单估 160 < 真实一次性开销）。
func TestEstimateTicketTokensNeverBelowFixed(t *testing.T) {
	p := estDefaultParams()
	for _, chars := range []int64{1, 19, 67, 1000, 20000} {
		for _, langs := range []int{1, 5} {
			if got := estimateTicketTokens(chars, langs, "pro", p); got < int64(p.fixedPro) {
				t.Fatalf("pro 预估低于固定项 %v（固定项丢失）：chars=%d langs=%d est=%d", p.fixedPro, chars, langs, got)
			}
			if got := estimateTicketTokens(chars, langs, "fast", p); got < int64(p.fixedFast) {
				t.Fatalf("fast 预估低于固定项 %v：chars=%d langs=%d est=%d", p.fixedFast, chars, langs, got)
			}
		}
	}
}

// TestEstimateTicketTokensFixedNotInflatingLong ★ F-72 断言②「长单预估不得因固定项膨胀超一个数量级」：
// 固定项只能兜短单下限，绝不能变成大文档单的报价主项。判据取**比值**：
// 1 万字 ×5 语（线性项 800 万 token）加上固定项后增幅必须 <1%，即离「一个数量级」还差两个量级。
// 反向对照：若有人把 F 配成 8,000,000 这种「给短单用的量级」，本条立刻红灯（旧短单实烧 p90 仅 3,289）。
func TestEstimateTicketTokensFixedNotInflatingLong(t *testing.T) {
	p := estDefaultParams()
	linearOnly := estParams{kPro: p.kPro, kFast: p.kFast} // 固定项为 0 ＝ F-72 之前的纯线性口径
	longPro := estimateTicketTokens(10000, 5, "pro", p)
	longPure := estimateTicketTokens(10000, 5, "pro", linearOnly)
	if longPure <= 0 {
		t.Fatalf("对照基准失效：纯线性长单 est=%d", longPure)
	}
	// 增幅 >10% 即视为「固定项开始决定长单报价」，一个数量级(10×)的红线远早于此就该拦。
	if longPro*100/longPure > 101 {
		t.Fatalf("长单被固定项撑大：pure=%d withFixed=%d 增幅=%d%%（应 ≤1%%）",
			longPure, longPro, longPro*100/longPure-100)
	}
	if longPro >= longPure*10 {
		t.Fatalf("长单预估因固定项膨胀超一个数量级（F-72 红线）：pure=%d withFixed=%d", longPure, longPro)
	}
	// 短单方向相反：固定项必须**真的加进去**（增量恰为 F），且在 6 字这种极短单里它是报价主项
	// （3,000 vs 线性项 960）——否则等于只加了个零头，F-72 没修。
	shortWith := estimateTicketTokens(19, 1, "pro", p)
	shortPure := estimateTicketTokens(19, 1, "pro", linearOnly)
	if shortWith-shortPure != int64(p.fixedPro) {
		t.Fatalf("短单固定项增量应为 %v：pure=%d withFixed=%d", p.fixedPro, shortPure, shortWith)
	}
	if tiny := estimateTicketTokens(6, 1, "pro", p); int64(p.fixedPro)*2 <= tiny {
		t.Fatalf("6 字极短单里固定项应占主导（>50%%），est=%d fixed=%v", tiny, p.fixedPro)
	}
}

// TestEstimateFileSourceChars 校验按文件类型分档的字节→字符估算（任务1，2026-09-15）。
// 核心回归点：二进制文档（PDF/Office）不得按“整包皆文本”高估（旧口径 size/3 会误拦 700KB PDF）。
func TestEstimateFileSourceChars(t *testing.T) {
	size := int64(700 * 1024)
	txt := estimateFileSourceChars("a.txt", size)
	pdf := estimateFileSourceChars("a.pdf", size)
	docx := estimateFileSourceChars("a.docx", size)
	unk := estimateFileSourceChars("a", size)
	if pdf >= txt {
		t.Fatalf("PDF(二进制)估算字符数应远小于同体积纯文本：pdf=%d txt=%d", pdf, txt)
	}
	if pdf > size {
		t.Fatalf("PDF 估算不应超过字节数：pdf=%d", pdf)
	}
	if docx >= txt || docx < pdf {
		t.Fatalf("docx 应介于纯文本与 pdf 之间：txt=%d docx=%d pdf=%d", txt, docx, pdf)
	}
	if unk <= 0 {
		t.Fatalf("未知扩展名应有折中估算，got=%d", unk)
	}
	if estimateFileSourceChars("x.pdf", 0) != 0 {
		t.Fatal("0 字节应返回 0")
	}
	// 关键：700KB PDF 折成 token 预估后应明显低于旧口径（/3）
	old := size / 3
	if pdf*2 > old { // 新口径至少比旧口径小一半以上
		t.Fatalf("PDF 估算未显著收敛：new=%d old(/3)=%d", pdf, old)
	}
}
