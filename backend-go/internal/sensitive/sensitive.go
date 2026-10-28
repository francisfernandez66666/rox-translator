// ============ 本文件职责中文说明 ============
// S8 内容安全·敏感词兑底闸（2026-09-14 拍板：开闸前必须上线）：
//   - 词包：平台级配置文件（一行一词，# 注释；超管维护，mtime 热加载免重启）
//   - 检测：输入/输出双向命中检测（归一化 Contains 口径：小写 + NFKC + 零宽剥离 +
//     全半角折叠 + 字符间空白剥离，见 normalize / normalizeLoose）
//   - 处置策略在调用侧（engine：拒译/段落拦截/占位替换 + 审计 + 告警）
//
// 设计口径：命中段不进模型（上游供应商侧零暴露），交付物只留占位符；
// 误杀走人工复核通道（告警 kind=sensitive_block 留证词）。
// ★ P0-5 修复（2026-09-14）：旧实现仅 ToLower+Contains，全角字母（ＦＩＲＥＡＲＭＳ）、
//
//	零宽字符插入（枪\u200b支）、字符间空格（F i r e a r m s）均可绕过合规闸。
package sensitive

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// zeroWidths 零宽/不可见混淆字符集（零宽空格、零宽连接符、双向控制、软连字符、BOM）。
var zeroWidths = func() map[rune]bool {
	m := map[rune]bool{}
	for _, r := range "\u200b\u200c\u200d\u200e\u200f\u2060\u00ad\ufeff\u202a\u202b\u202c\u202d\u202e" {
		m[r] = true
	}
	return m
}()

// normalize 强归一化：小写 + NFKC（全角→半角）+ 剥离零宽字符。用于词表与文本同口径。
func normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if zeroWidths[r] {
			continue
		}
		b.WriteRune(r)
	}
	return norm.NFKC.String(b.String())
}

// normalizeLoose 宽松归一化：强归一化后再剥离全部空白——用于文本侧第二遍检测，
// 击穿「F i r e a r m s」式字符间空格混淆（词表侧不做此剥离，避免短语类词误拼）。
func normalizeLoose(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range normalize(s) {
		if unicode.IsSpace(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// latinWord 词条是否「纯拉丁/数字/空白」。
// ★ 这类词**不许**走松散口径的子串匹配（2026-10-29 现网实证）：
// `normalizeLoose` 把空格全剥掉，正常英文译文里「…calls down…」会拼成「…callsdown…」，
// 里面恰好有 `lsd` ⇒ 词表里的 `LSD` 把一行**正常业务译文**整段判成敏感词，
// 交付物上那一行变成占位符（按交付口径回落原文＝图上留中文），客户看到「该翻的没翻」。
// 所以拉丁词一律走**词边界**匹配（见 latinPattern），CJK 词才用子串+松散口径。
func latinWord(w string) bool {
	if w == "" {
		return false
	}
	for _, r := range w {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == ' ' || r == '-' || r == '.' || r == '_' {
			continue
		}
		return false
	}
	return true
}

// latinPattern 为一个拉丁词条构造「字符间可夹空白 + 两端词边界」的匹配式：
// `lsd` ⇒ `\bl\s*s\s*d\b`——既能击穿「L S D」，又不会跨词边界误命中。
// 两端 `\b` 在 Go 里按 ASCII 词字符判定，CJK 紧邻时（如「是lsd」）仍成立。
func latinPattern(w string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString(`\b`)
	for i, r := range w {
		if i > 0 {
			b.WriteString(`\s*`)
		}
		b.WriteString(regexp.QuoteMeta(string(r)))
	}
	b.WriteString(`\b`)
	return regexp.MustCompile(b.String())
}

// Checker 敏感词包扫描器（并发安全；文件变更 mtime 触发热加载）。
type Checker struct {
	path string

	mu     sync.RWMutex
	words  []string         // 小写化的词表（保持原词供命中回显）
	origin []string         // 原词（审计/告警展示）
	latin  []bool           // 与 words 同序：该词条是否纯拉丁/数字（走词边界匹配，见 latinWord）
	res    []*regexp.Regexp // 与 words 同序：拉丁词条的边界匹配式（CJK 词为 nil）
	mtime  time.Time
	loaded time.Time
}

// New 创建扫描器并立即尝试加载词包（文件缺失=空表，不报错——闸口自然关闭）。
func New(path string) *Checker {
	c := &Checker{path: path}
	c.reload(true)
	return c
}

// Path 词包文件路径（诊断/告警文案用）。
func (c *Checker) Path() string { return c.path }

// reload 若文件 mtime 变化则重载；force=启动期强制首读。
// 读取失败保留旧表（宁可用旧词包继续拦，不可瞬间失防）。
func (c *Checker) reload(force bool) {
	st, err := os.Stat(c.path)
	if err != nil {
		c.mu.Lock()
		if force {
			c.words, c.origin, c.latin, c.res = nil, nil, nil, nil
		}
		c.mu.Unlock()
		return
	}
	c.mu.RLock()
	fresh := st.ModTime().Equal(c.mtime) && !force
	c.mu.RUnlock()
	if fresh {
		return
	}
	b, err := os.ReadFile(c.path)
	if err != nil {
		return
	}
	var words, origin []string
	var latin []bool
	var res []*regexp.Regexp
	for _, line := range strings.Split(string(b), "\n") {
		w := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), "\r"))
		if w == "" || strings.HasPrefix(w, "#") {
			continue
		}
		nw := normalize(w) // ★ P0-5：词表与文本同口径归一化
		origin = append(origin, w)
		words = append(words, nw)
		isLatin := latinWord(nw)
		latin = append(latin, isLatin)
		if isLatin {
			res = append(res, latinPattern(nw))
		} else {
			res = append(res, nil)
		}
	}
	c.mu.Lock()
	c.words, c.origin, c.latin, c.res = words, origin, latin, res
	c.mtime = st.ModTime()
	c.loaded = time.Now()
	c.mu.Unlock()
}

// Count 当前词包词条数（热加载后统计，启动日志用）。
func (c *Checker) Count() int {
	c.reload(false)
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.words)
}

// Hits 返回文本命中的原词（去重、按词表序、最多回显 5 个——审计留证够用）。
func (c *Checker) Hits(text string) []string {
	if text == "" {
		return nil
	}
	c.reload(false)
	c.mu.RLock()
	words, origin, latin, res := c.words, c.origin, c.latin, c.res
	c.mu.RUnlock()
	if len(words) == 0 {
		return nil
	}
	// ★ P0-5（2026-09-14）：两遍归一化检测——
	//   第一遍：强归一化（小写+NFKC+零宽剥离），词表与文本同口径，可对齐全角/半角、零宽插入；
	//   第二遍：宽松口径（再剥离全部空白），击穿字符间空格混淆。
	lower := normalize(text)
	loose := normalizeLoose(text)
	var out []string
	seen := map[string]bool{}
	for i, w := range words {
		if w == "" || seen[w] {
			continue
		}
		// ★ 拉丁词只走词边界（见 latinWord 的现网实证）；CJK 词才用子串+松散口径。
		//   把拉丁词也丢进 loose 子串，就是「calls down ⇒ callsdown 命中 lsd」那一族误杀。
		hit := false
		if i < len(latin) && latin[i] {
			if i < len(res) && res[i] != nil && res[i].MatchString(lower) {
				hit = true
			}
		} else if strings.Contains(lower, w) || strings.Contains(loose, w) {
			hit = true
		}
		if hit {
			seen[w] = true
			out = append(out, origin[i])
			if len(out) >= 5 {
				break
			}
		}
	}
	sort.Strings(out) // 回显稳定序（避免词表顺序变化引起告警文案抖动）
	return out
}

// Has 是否命中（任一词）。
func (c *Checker) Has(text string) bool { return len(c.Hits(text)) > 0 }
