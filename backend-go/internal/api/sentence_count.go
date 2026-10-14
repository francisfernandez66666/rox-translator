package api

// 本文件是「源句数」切分的唯一实现在（★ 2026-10-13 重做，修 "e.g."／"600912.SH" 各切一刀的缺陷本体）。
//
// 为什么单独成文件：`countSentences` 原先住在 billing_api.go 里，而它的判据从"一律切"换成
// "按上下文判 ASCII 句点"是一次**语义改动**（不是提速、不是整理），混在大文件里读不出改了哪一维。
//
// 三条设计取向（改动前必读）：
// ① **只重判 ASCII 句点「.」**。换行、拉丁 `! ? ;` 与全角 `。！？；` 的边界语义与改造前逐字一致——
//    缺陷只长在句点这一维（缩写、小数、代码、文件名都用它），把 `;` 也一起"聪明化"会牵动
//    代码片段／配置串那一类文本的读数，属于超出本批必要性的改动。
// ② **判不出倾向时按边界处理**（宁可多算一句，不许少算）：句数这个读数是给客户看的"这段话大约几句"，
//    少算＝客户以为更便宜，代价方向与「预估偏低」同族（AGENTS §一·3④ 那条实收高于预估的教训）。
// ③ **这里绝不进成本公式**（AGENTS §一·3④ 已立为硬约定）：本函数只出 `sentences` 这一个展示读数，
//    预估的 token 由 `engine.EstimateChatTokens` 按字符书写体系算，与句数无关。
//    ⚠️ 旧注释里"它还服务余额≈句数换算与免费额度发放"那句**不成立**（2026-10-13 实读代码核正）：
//    余额句数走 `balancePayload` 的 `total / TokenSentenceRate`，试用额度按请求计次、
//    套餐额度取 `packages.sentences` 固定值——三条都不读这里。所以改切分不动钱，
//    但"不动钱"这件事由那几个读点自己成立，**不是**由本函数的判据成立。

import (
	"strings"
)

// abbrevBeforeDot 是「点前那个词是常见缩写」的名单（小写比对，不含点）。
// 名单里的每一条都满足：它出现在句末位置时，后面接的通常**不是**一个新句子。
//
// ⚠️ 刻意分成两组（`abbrevBeforeDot` 与 `abbrevNumberLed`），因为「No.」这一类有歧义：
// 「No. 5」是编号引用（不切），「No. I disagree.」是整句（要切）——判据是**后面紧跟的是不是数字**。
var abbrevBeforeDot = map[string]bool{
	// 拉丁缩写（后面必然还有正文，切开会把一句拆成两句）
	// ⚠️ 这里**只收"点前那一个词"**——"e.g."／"i.e."／"a.k.a." 内部的点由
	//    「点后紧跟拉丁字母」（③）与「单字母首字母缩写」（④）两条挡住，不收带点的键
	//    （带点的键在这份判据下永远命中不到，留着只会误导下一个人）。
	"etc": true, "vs": true, "cf": true, "viz": true, "ibid": true, "et": true, "al": true,
	"approx": true,
	// 称谓与机构后缀
	"dr": true, "mr": true, "mrs": true, "ms": true, "prof": true, "phd": true, "md": true,
	"jr": true, "sr": true, "inc": true, "ltd": true, "co": true, "corp": true, "llc": true,
	"ltda": true,
	// 出版／文档引用
	"ed": true, "eds": true, "sec": true, "dept": true, "est": true, "trans": true,
	"rd": true, "ave": true, "blvd": true, "mt": true,
	// 歧义那一族（"No."／"Fig." 既可能是编号引用也可能是整句结尾）：**必须同时出现在
	// abbrevBeforeDot 与 abbrevNumberLed 里**——外层名单决定"要不要走编号那条二次判定"，
	// 只写进后者是死码（这一族曾经只在外层删、内层留，判据不报错，行为静默退化）。
	"no": true, "nos": true, "st": true, "vol": true, "vols": true, "fig": true, "figs": true,
	"p": true, "pp": true, "ca": true, "min": true, "max": true, "ref": true,
	"ch": true, "art": true, "para": true, "cl": true,
}

// abbrevNumberLed 是「只有当点后紧跟数字时才当缩写」的那一组（编号式引用）。
// 这一组词本身也是合法的整句结尾（"No."／"Yes" 式短句、"Min." 单独成行），
// 不加这一层条件就会把"客户打出一个 No. 就结束了这一句"误判成一句没结束。
var abbrevNumberLed = map[string]bool{
	"no": true, "nos": true, "st": true, "rd": true, "vol": true, "vols": true,
	"fig": true, "figs": true, "sec": true, "ch": true, "art": true, "para": true,
	"pp": true, "p": true, "ca": true, "min": true, "max": true, "approx": true, "ref": true,
}

// countSentences 统计文本的源句数（至少 1，保证计量读数非零）。
// 参数 text: 源文本。返回: 句/段数。
//
// 判据（★ 2026-10-13）：
//   - 一律切：`\n` `\r` 全角 `。！？；` 拉丁 `! ? ;`（与改造前一致）；
//   - 按上下文切：ASCII 句点 `.`——小数、代码／域名／文件名（点后紧跟拉丁字母或数字）、
//     连点（省略号中间的那些）、点前是缩写词且点后是空白，这四种**不切**，其余照切。
func countSentences(text string) int64 {
	if strings.TrimSpace(text) == "" {
		return 1 // 空文本兜底为 1 句（与改造前一致，保证非零）
	}
	rs := []rune(text)
	n := int64(0)
	dirty := false // 当前段里是否出现过非空白内容
	// ★ 判序是"先判边界、再记内容"，不是反过来（2026-10-13 那条兜底用例当场抓到的缺陷本体）：
	//   写成"先记内容再判边界"时，边界字符自己（。！？）。会把 dirty 抬成 true，
	//   于是纯标点文本 "。。。" 数成 3 句而旧口径数成 1——客户看到的读数字面变了，
	//   而这是本批刻意**不许动**的那一维（非 ASCII 句点维度必须与改造前逐字一致）。
	for i, r := range rs {
		if isSentenceBoundary(rs, i, r) {
			if dirty {
				n++
				dirty = false
			}
			continue
		}
		// 只有**不是边界**的字符才算内容；句点这类"既可能是边界又可能是内容"的字符
		// 由 dotEndsSentence 先给答案（不切的那些点——e.g.／600912.SH——落到这一行记为内容）。
		if !isSpaceRune(r) {
			dirty = true
		}
	}
	if dirty {
		n++ // 末段没有终止标点也算一句（客户常常不打最后一个句号）
	}
	if n < 1 {
		n = 1
	}
	return n
}

// isSentenceBoundary 判 rs[i] 这一个字符是否结束一句。
// 参数 rs: 全文 rune 切片；i: 下标；r: rs[i]。返回: 是否句末边界。
func isSentenceBoundary(rs []rune, i int, r rune) bool {
	switch r {
	case '\n', '\r', '。', '！', '？', '；':
		return true
	case '!', '?', ';':
		return true // 与改造前一致：这三族不分上下文，一律切
	case '.':
		return dotEndsSentence(rs, i)
	}
	return false
}

// dotEndsSentence 是「ASCII 句点到底算不算句末」的判据本体。
// 参数 rs: 全文 rune 切片；i: 句点下标。返回: 该句点是否结束一句。
func dotEndsSentence(rs []rune, i int) bool {
	prev, next := runeAt(rs, i-1), runeAt(rs, i+1)

	// ① 连点（省略号／点阵）：只让**最后一个点**参与判定，前面的点一律不切。
	//    这样 "Hmm... sure" 出两句（与改造前一致），而 "..." 不会自己刷出三个空句。
	if next == '.' {
		return false
	}

	// ② 小数与版本号：两侧都是数字（6.5／1.2.3）。
	//    ⚠️ 只认"两侧都是数字"，不认"点前是数字"——"第 3. 条"这种点后是空白的编号，
	//    改造前就切，本批不改它的行为（多切一句的代价方向符合上面第 ② 条设计取向）。
	if isDigitRune(prev) && isDigitRune(next) {
		return false
	}

	// ③ 代码／域名／文件名／股票代码：点后**紧跟**拉丁字母或数字（没有一个空格隔开的"新句子"）。
	//    这一条同时挡住 600912.SH、example.com、report.pdf、v1.2（已被 ② 挡）、以及
	//    a.k.a. 这类带点缩写串的内部点。
	if isLatinAlnum(next) {
		return false
	}

	// ④ 点前那一个拉丁词是缩写（e.g.／etc.／Mr.／Inc. 这类），且点后是空白或文本结束。
	//    单字母词（J. Smith、e.g. 里那个 g.、U.S.A. 的每一点）一并挡住——首字母缩写是同一形态。
	word := latinWordBefore(rs, i)
	if word != "" {
		lower := strings.ToLower(word)
		if abbrevBeforeDot[lower] {
			// 名单里但属"歧义那一组"的，只有点后紧跟数字才当缩写（No. 5 vs No.）
			if abbrevNumberLed[lower] && !nextStartsWithDigit(rs, i) {
				return true
			}
			return false
		}
		if isASCIILetter([]rune(word)[0]) && len(word) == 1 {
			return false // 首字母缩写
		}
	}
	return true
}

// latinWordBefore 取句点之前紧挨着的那一段拉丁字母／数字串（用作缩写词比对）。
// 参数 rs: 全文 rune 切片；i: 句点下标。返回: 那一段串（可能为空）。
func latinWordBefore(rs []rune, i int) string {
	j := i
	for j > 0 && isLatinAlnum(rs[j-1]) {
		j--
	}
	if j == i {
		return ""
	}
	return string(rs[j:i])
}

// nextStartsWithDigit 判句点之后的第一个非空白字符是不是数字（编号式引用的识别）。
// 参数 rs: 全文 rune 切片；i: 句点下标。返回: 是数字（或文本就此结束则回 false）。
func nextStartsWithDigit(rs []rune, i int) bool {
	for j := i + 1; j < len(rs); j++ {
		if isSpaceRune(rs[j]) {
			continue
		}
		return isDigitRune(rs[j])
	}
	return false
}

// runeAt 安全取下标处的 rune（越界回 0，调用方按"不是任何标点"处理）。
func runeAt(rs []rune, i int) rune {
	if i < 0 || i >= len(rs) {
		return 0
	}
	return rs[i]
}

// isSpaceRune 判空白（含不换行空格，客户从网页复制的文本里高频出现）。
func isSpaceRune(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f' || r == '\u00a0'
}

// isDigitRune 判 ASCII 数字（刻意只认 ASCII，全角数字"１２３"不是编号引用形态）。
func isDigitRune(r rune) bool { return r >= '0' && r <= '9' }

// isASCIILetter 判 ASCII 字母（大小写）。
func isASCIILetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// isLatinAlnum 判「拉丁字母或数字」——它是"点后直接接下一个词"这一族的判据：
// 真句末之后必然有空白（或文本结束），而 600912.SH／example.com／report.pdf 之后紧跟字母。
func isLatinAlnum(r rune) bool { return r != 0 && (isASCIILetter(r) || isDigitRune(r)) }
