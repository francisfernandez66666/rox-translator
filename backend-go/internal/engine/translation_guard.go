// ============ 本文件职责中文说明 ============
// 文件翻译主链「译文可用性」的**唯一判定口径**（P0 整改 RC-2：回显/同文/指令残留四处口径不一致）。
//
// 背景：HandleFile 有 5 个译文写入点——① KB 直配命中、② KB 语言的模型批量补漏、
// ③ 其他语言批量直翻、④ 硬闸小批量重译、⑤ 硬闸逐段兜底。历史上这 5 处各自手写
// `!= "" && != "[翻译失败]" && != 源文`，唯独 ① 只判了非空。后果分两层：
//   - 脏行直接进成品：TM/TMX 导入与历史沉淀里存在「源文 == 译文」的行（真实双语文件
//     大量条目本就同文、术语自映射、缓存污染），中文会被当成「命中的译文」写进交付物；
//   - 更致命的是**永久锁死硬闸**：硬闸与 untranslated 统计都按「langTranslations 里
//     有没有这个键」判断是否译出（键存在即跳过）。坏命中把键写进去了 ⇒ 该段既不重试、
//     也不计入 untranslated、更不告警，工单显示「已完成」而用户拿到残缺成品。
//
// 因此本文件把判定收口成 IsTranslationUsable 一处定义，并配套 KB 结果汇总、缺失段计算
// 两个纯函数，让「写入门槛」和「未译统计」天然同一口径（纯函数也便于不打真模型做断言）。
// ========================================
package engine

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// translationFailureMark BatchTranslate 在失败槽位写入的占位字面量（见 engine.go 批量链）。
// 集中成常量是为了让「失败占位」与「真译文」的区分只有一处定义，不再各调用点抄字符串。
const translationFailureMark = "[翻译失败]"

// IsTranslationUsable 判定一份候选译文能否作为交付（文件主链 5 个写入点的唯一口径）。
// 参数 src: 送翻的源文段；out: 候选译文（KB 命中值或模型返回，未经判定前的原始候选）。
// 判不可用的五类，任一命中即 false：
//  1. 空串 / 纯空白 —— 没有内容可言交付；
//  2. 批量失败占位 [翻译失败] —— 上游明确报错的槽位；
//  3. 与源文同文（去空白后逐字相等）—— 模型或 KB 原样回显 = 根本没翻（含 "#"/"1"/"•"
//     这类无上下文短串的回显，与既有模型路径口径一致，宁可走补漏也不静默交付）；
//  4. 指令回显残留 —— 见 hasInstructionEcho 的结构指纹；
//  5. 短格长度爆炸 —— 见 hasLengthExplosion（★ F-38，2026-09-25 批 D：6 字表格单元格被
//     翻成 2100 字符的整封英文邮件，前四判据全放行、直接进交付物）。
//
// ★ 不能只判非空：这是本文件存在的理由，任何新增写入点都必须先过本函数。
func IsTranslationUsable(src, out string) bool {
	o := strings.TrimSpace(out)
	if o == "" || o == translationFailureMark {
		return false
	}
	if normalizeForCompare(src) == normalizeForCompare(out) {
		return false
	}
	if hasInstructionEcho(src, out) {
		return false
	}
	if hasLengthExplosion(src, out) {
		return false
	}
	return true
}

// normalizeForCompare 折叠全部空白用于「同文」比较。
// 为什么去空白：模型常把表格单元格里的换行/多个空格压成单空格后原样回显（源文
// "产品  方案\n书" vs 译文 "产品 方案 书"），逐字比较会漏判成「已翻译」，
// 折叠空白后仍是同文 ⇒ 正确判为未译。
func normalizeForCompare(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// hasInstructionEcho 「内部指令泄漏进交付物」的结构指纹：源文本身不含 '<'，
// 候选译文却凭空出现 '<'（实测形态：`<Only output the final translated text,
// enclosed entirely within  and nothing else>`）。
//
// 成因：翻译 prompt 用 <t>…</t> 作输出契约标签，模型违约时把「剥掉标签的指令正文」
// 一起吐回来。postprocess.stripPseudoTags 会拆掉伪标签，但拆完剩下的正是这种
// 「尖括号里包着一整句英文指令」的文本，且这类串不会被后续任何中文清洗链拦住。
//
// ★ 为什么用结构规则而**不是**指令短语黑名单（已实测踩过，别再改回去）：
//   - 本工单文档正文第 259 行本来就是一行讲「只输出译文」的表格说明，
//     按 "only output"/"只输出" 之类短语匹配会把**正常正文**误杀成未译出；
//   - 短语黑名单随目标语言数量线性膨胀（12 语种 × 多模型措辞），永远补不全；
//   - 结构不变量与措辞无关：合法译文不会凭空引入源文里不存在的尖括号。
//     代价是「源文无 < 而译文写成了 a < b」这类极少见译法会被判不可用而走补漏重试，
//     这是可接受的偏保守方向（宁可重试也不交付可疑串）。
func hasInstructionEcho(src, out string) bool {
	return !strings.Contains(src, "<") && strings.Contains(out, "<")
}

// hasLengthExplosion ★ F-38 第 5 判据「短格长度爆炸」的结构指纹（2026-09-25 批 D）。
//
// 实测形态：xlsx 表格 6 字单元格「产品方案书」被上游吐回 2100 字符的整封英文邮件
// （疑似供应商侧缓存/解码串扰，账本已留字节级复现样本）。前四判据对此全放行：
// 非空、非失败占位、与源文不同文、不含凭空尖括号——脏串直接写进交付物。
//
// 判据：只对**短源文格**生效（src ≤ shortGuardMaxSrcRunes，默认 12 rune），
// 译文 rune 数超过 max(ratio×src, floor) 且封顶 absCap 即判爆炸（默认 8× / 下限 80 / 上限 300）。
// 为什么限定短格：长段落翻译（尤其中→德/俄等长语系）长度比天然偏高，全文生效会误杀；
// 而「缩写展开」这类合法短译（AWS→亚马逊云科技）在 80 rune 下限内几乎不可能被拦。
// 阈值全部 env 可配（保守默认，宁可多走一次补漏重试，不静默交付可疑串）。
func hasLengthExplosion(src, out string) bool {
	sr := []rune(strings.TrimSpace(src))
	if len(sr) == 0 || len(sr) > shortGuardMaxSrcRunes() {
		return false // 长段落/空源文不适用本判据（交既有链处理）
	}
	return len([]rune(strings.TrimSpace(out))) > shortGuardLimit(len(sr))
}

// shortGuardLimit 短格译文长度上限：min(max(ratio×srcRunes, floor), absCap)。
// 三段式的用意：ratio 管「随源文缩放」、floor 给极短格留足展开空间（1 字段落译成一句长句合法）、
// absCap 兜底——即使阈值被调大，短格也绝不允许出现「数三百倍」的邮件级长串。
func shortGuardLimit(srcRunes int) int {
	n := shortGuardRatio() * srcRunes
	if f := shortGuardFloor(); n < f {
		n = f
	}
	if c := shortGuardAbsCap(); n > c {
		n = c
	}
	return n
}

// guardEnvInt 读取守卫阈值的 env 整数项：空/非法/非正一律回退默认，防止误配置把闸门关死。
func guardEnvInt(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// 以下四个阈值函数单独抽出是为了让单测可用 t.Setenv 精确验证「env 可配」这条约定。
func shortGuardMaxSrcRunes() int { return guardEnvInt("LC_GUARD_SHORT_MAX_SRC_RUNES", 12) }
func shortGuardRatio() int       { return guardEnvInt("LC_GUARD_SHORT_RATIO", 8) }
func shortGuardFloor() int       { return guardEnvInt("LC_GUARD_SHORT_FLOOR_RUNES", 80) }
func shortGuardAbsCap() int      { return guardEnvInt("LC_GUARD_SHORT_ABS_CAP_RUNES", 300) }

// LengthExplosionInfo 供日志/告警用的长度比描述（provider/model 由调用侧补齐）。
// 命中 hasLengthExplosion 的槽位调用它，把「源 r 字 → 译 r 字（倍数 x，上限 y）」写成一行可聚合的哨兵字段。
func LengthExplosionInfo(src, out string) string {
	sr := len([]rune(strings.TrimSpace(src)))
	or := len([]rune(strings.TrimSpace(out)))
	ratio := 0.0
	if sr > 0 {
		ratio = float64(or) / float64(sr)
	}
	return fmt.Sprintf("src_runes=%d out_runes=%d ratio=%.1f limit=%d", sr, or, ratio, shortGuardLimit(sr))
}

// collectKBPass 汇总 KB 第一遍逐段直配的并行结果，决定「算命中并写入」还是「进模型补漏队列」。
// 参数：texts=有序源文段；kbHitIdx[i]=该段 KB 调用是否返回了内容；kbVal[i]=候选译文；
// blockedSeg=S8 敏感拦截段（已预填占位交付，既不算命中也不进补漏）。
// 返回 accepted（可用命中的段号）与 needModelIdx（需送模型的段号，保持提取顺序）。
//
// ★ 判定必须走 IsTranslationUsable，**不能只判非空**：KB 脏行（源文=译文）会把源文
// 当译文命中，一旦写进 langTranslations，硬闸「按键存在判定是否译出」就永久跳过该段
// ——不重试、不计 untranslated、不告警（详见本文件头）。此处即使上游 goroutine 已判过
// 也再判一次：写入点与命中点分离（并行）时，以**写入前**的判定为准。
func collectKBPass(texts []string, kbHitIdx []bool, kbVal []string, blockedSeg map[string]bool) (accepted []int, needModelIdx []int) {
	for i := range texts {
		if i < len(kbHitIdx) && kbHitIdx[i] && i < len(kbVal) && IsTranslationUsable(texts[i], kbVal[i]) {
			accepted = append(accepted, i)
			continue
		}
		if blockedSeg[texts[i]] {
			continue // ★ S8：拦截段已预填占位，不入模型补漏（送审即上游零暴露回退）
		}
		needModelIdx = append(needModelIdx, i)
	}
	return accepted, needModelIdx
}

// missingSegments 按提取顺序返回该语言**尚未有可交付译文**的源文段。
// 与 IsTranslationUsable 配对使用：不可用译文一律不写入 ⇒ 「键存在」严格等价于「已译出」，
// 于是硬闸重试、untranslated 计数与未译段清单三者共用同一份真相（P0 整改的落点）。
func missingSegments(texts []string, tr map[string]string) []string {
	missing := []string{}
	for _, t := range texts {
		if _, ok := tr[t]; !ok {
			missing = append(missing, t)
		}
	}
	return missing
}
