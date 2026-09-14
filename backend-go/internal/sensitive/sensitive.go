// ============ 本文件职责中文说明 ============
// S8 内容安全·敏感词兑底闸（2026-09-14 拍板：开闸前必须上线）：
//   - 词包：平台级配置文件（一行一词，# 注释；超管维护，mtime 热加载免重启）
//   - 检测：输入/输出双向命中检测（归一化 Contains 口径：小写 + NFKC + 零宽剥离 +
//     全半角折叠 + 字符间空白剥离，见 normalize / normalizeLoose）
//   - 处置策略在调用侧（engine：拒译/段落拦截/占位替换 + 审计 + 告警）
// 设计口径：命中段不进模型（上游供应商侧零暴露），交付物只留占位符；
// 误杀走人工复核通道（告警 kind=sensitive_block 留证词）。
// ★ P0-5 修复（2026-09-14）：旧实现仅 ToLower+Contains，全角字母（ＦＩＲＥＡＲＭＳ）、
//   零宽字符插入（枪\u200b支）、字符间空格（F i r e a r m s）均可绕过合规闸。
package sensitive

import (
	"os"
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

// Checker 敏感词包扫描器（并发安全；文件变更 mtime 触发热加载）。
type Checker struct {
	path string

	mu     sync.RWMutex
	words  []string // 小写化的词表（保持原词供命中回显）
	origin []string // 原词（审计/告警展示）
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
			c.words, c.origin = nil, nil
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
	for _, line := range strings.Split(string(b), "\n") {
		w := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), "\r"))
		if w == "" || strings.HasPrefix(w, "#") {
			continue
		}
		origin = append(origin, w)
		words = append(words, normalize(w)) // ★ P0-5：词表与文本同口径归一化
	}
	c.mu.Lock()
	c.words, c.origin = words, origin
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
	words, origin := c.words, c.origin
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
		if strings.Contains(lower, w) || strings.Contains(loose, w) {
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
