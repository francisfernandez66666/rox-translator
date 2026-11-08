// ============ mining.go · 职责说明 ============
// 二期 S2 六包平移第一包（talkmining）的**纯函数层**：把人工回复批按「谁说 × 在哪个节点说」
// 聚成回复簇，再按终局转化率排序出候选。本文件零 DB、零网络、零框架依赖，口径全部可单测钉死。
//
// 与外仓原件的三处「迁入即换」：
//
//	① 不再 import 外仓 model 的旅程阶段常量——阶段码到终局档位的映射由调用方按租户配置注入，
//	   引擎层不认任何行业词；这里只消费 Record.Outcome（三个行业无关的漏斗档）。
//	② 不再写死「中国大陆手机号」正则（§5 判据：跨国际客户物理上抽不到）——默认脱敏改成长数字串
//	   通用掩码，且允许 Options.MaskPII 按租户替换，隐私红线「代表原文出产物前必须脱敏」不变。
//	③ 类别标签与提示词不带行业字面（到店／车型／品牌），一律由注入函数出。
//
// ★ 本层只产**候选簇**，绝不直接写库、绝不自动生效；出稿与人审闸在 draft.go 与 store 层。
// ================================================
package talkmining

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Unanchored 锚位哨兵：没有可靠锚位上下文时按阶段归并（-1 不与任何合法锚类型撞号）。
const Unanchored = -1

// 客户终局漏斗档（行业无关）：转化＝档位序 ≥ 目标档位序。
const (
	// OutcomeNone 未达任何终局（含战败／流失，绝不进转化分子）。
	OutcomeNone = "none"
	// OutcomeEngaged 达到第一档终局（默认目标档，语义由租户配置定义）。
	OutcomeEngaged = "engaged"
	// OutcomeClosed 达到最终档终局。
	OutcomeClosed = "closed"
)

// outcomeRank 终局档位序：none < engaged < closed；未知档位查表得 0（fail-safe，不虚增转化）。
var outcomeRank = map[string]int{
	OutcomeNone:    0,
	OutcomeEngaged: 1,
	OutcomeClosed:  2,
}

// 挖掘默认参数（Options 零值字段逐项回落到这里）。
const (
	// DefaultMinSamples 出结论组的最小「去重客户数」。按客户而非消息计数：同一客户连发十条相似话术，
	// 证据量只有一单；5 单起步只够出**候选**（下游仍有人审），不是择优结论。
	DefaultMinSamples = 5
	// DefaultMinTextLen 归一化后代表话的最小长度（rune），滤掉「好的／收到」这类跨行业高频噪声。
	DefaultMinTextLen = 4
)

// DefaultTargetOutcome 默认转化目标档；更严口径由调用方显式传 OutcomeClosed。
const DefaultTargetOutcome = OutcomeEngaged

// longDigitRun 通用长数字串（手机号／订单号／证件号同形），不写死任何国家号段。
var longDigitRun = regexp.MustCompile(`\d{7,}`)

// MaskDigitRuns 默认脱敏：把连续 7 位以上数字整体换成占位串。
// 参数：s=顾问原文；返回：脱敏后的文本（代表话术入产物前必经这一层或租户自定义层）。
func MaskDigitRuns(s string) string { return longDigitRun.ReplaceAllString(s, "[号码已隐去]") }

// RankOutcome 把终局档位换算成序；未知档位一律 0（不许因拼错词虚增转化）。
// 参数：outcome=终局档位码；返回：档位序。
func RankOutcome(outcome string) int { return outcomeRank[outcome] }

// Record 挖掘输入的一条人工回复（纯数据，由调用方装载）。
// ★ 隐私红线：只允许携带「说话方」文本；客户原文进候选池等于把 PII 直接送进模板库。
type Record struct {
	ActorID    uint   // 说话方 ID（会话归属人／消息发送人）
	AnchorType int    // 锚位上下文；无可靠锚位传 Unanchored，按阶段归并
	Stage      string // 当时旅程阶段码（作分组上下文，不解释含义）
	CustomerID uint   // 客户 ID（按客户去重计样本）
	Content    string // 说话方原文（未脱敏；产物输出前统一过掩码）
	Outcome    string // 该客户终局档位（由调用方按租户配置映射）
}

// GroupKey 分组键：说话方 × 锚位 × 阶段。同一句话术在不同节点的转化方向可能相反，
// 不拆节点维度就会把相反结论平均成无害噪声。
type GroupKey struct {
	ActorID    uint
	AnchorType int
	Stage      string
}

// Cluster 一个回复簇（归并结果＋转化统计）。
type Cluster struct {
	Key            GroupKey // 分组键
	Samples        int      // 去重客户数
	Conversions    int      // 达到目标档位的客户数
	ConvRate       float64  // 转化率（分母为 0 时恒 0，不产 NaN）
	Insufficient   bool     // 样本不足：只报统计、不得出稿
	MessageCount   int      // 归并掉的消息总条数（含近似重复）
	VariantCount   int      // 归一化去重后的变体数
	Representative string   // 代表性原文（已脱敏）
	Signature      string   // 簇稳定签名：fnv32a(归一化代表文本)，跨进程可复现
	TargetOutcome  string   // 本簇转化率对应的目标档位
}

// Options 挖掘参数；零值逐项回落默认。
type Options struct {
	MinSamples    int                 // 结论组最小去重客户数
	MinTextLen    int                 // 归一化后最小话术长度
	TargetOutcome string              // 转化目标档位
	TopK          int                 // 最多返回的结论位簇数（不足组仍留尾部）
	MaskPII       func(string) string // 脱敏函数；nil 用 MaskDigitRuns
}

// NormalizeForDedup 近似重复归并：只保留 Unicode 字母／数字并统一小写，
// 一次 pass 收拢「加标点／带 emoji／全角半角」这类最高频伪重复。
// 参数：s=原文；返回：归一化文本。
func NormalizeForDedup(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// variantAcc 簇内一个归一化变体的累加器。
type variantAcc struct {
	display string // 首见原文（输出前脱敏）
	count   int    // 消息条数
	order   int    // 首见序号（并列取更早者，保证排序确定性）
}

// groupAcc 单个分组键的累加器。
type groupAcc struct {
	variants     map[string]*variantAcc
	custOutcome  map[uint]int
	messageCount int
	nextOrder    int
}

// Mine 核心纯函数：聚类并按转化率排序输出。
// 排序＝非不足组按（转化率 desc、样本数 desc、分组键 asc），不足组一律垫底；
// TopK 只截结论位，不足组保留在尾部供观测，但下游 DraftTemplates 会拒收。
// 参数：records=人工回复批；opts=挖掘参数；返回：候选簇（永不为 nil）。
func Mine(records []Record, opts Options) []Cluster {
	minSamples := opts.MinSamples
	if minSamples <= 0 {
		minSamples = DefaultMinSamples
	}
	minTextLen := opts.MinTextLen
	if minTextLen <= 0 {
		minTextLen = DefaultMinTextLen
	}
	target := opts.TargetOutcome
	// 「未达终局」不许当目标档：那会让每个客户都算转化，转化率恒 100%。
	if _, ok := outcomeRank[target]; !ok || target == OutcomeNone {
		target = DefaultTargetOutcome
	}
	targetRank := outcomeRank[target]
	mask := opts.MaskPII
	if mask == nil {
		mask = MaskDigitRuns
	}

	groups := map[GroupKey]*groupAcc{}
	for _, rec := range records {
		// 无归属记录直接丢弃：说话方=0 无法回答「这是谁的打法」，客户=0 是孤儿消息、会虚增分母。
		if rec.ActorID == 0 || rec.CustomerID == 0 {
			continue
		}
		norm := NormalizeForDedup(rec.Content)
		if len([]rune(norm)) < minTextLen {
			continue
		}
		key := GroupKey{ActorID: rec.ActorID, AnchorType: rec.AnchorType, Stage: rec.Stage}
		g := groups[key]
		if g == nil {
			g = &groupAcc{variants: map[string]*variantAcc{}, custOutcome: map[uint]int{}}
			groups[key] = g
		}
		v := g.variants[norm]
		if v == nil {
			v = &variantAcc{display: rec.Content, order: g.nextOrder}
			g.variants[norm] = v
			g.nextOrder++
		}
		v.count++
		g.messageCount++
		rank := outcomeRank[rec.Outcome] // 未知档位得 0
		// 未转化客户也必须入图：分母＝全部去重客户；只记转化者会让转化率恒 100%。
		prev, seen := g.custOutcome[rec.CustomerID]
		if !seen || rank > prev {
			g.custOutcome[rec.CustomerID] = rank
		}
	}

	clusters := make([]Cluster, 0, len(groups))
	for key, g := range groups {
		c := Cluster{Key: key, TargetOutcome: target}
		c.Samples = len(g.custOutcome)
		for _, rank := range g.custOutcome {
			if rank >= targetRank {
				c.Conversions++
			}
		}
		if c.Samples > 0 {
			c.ConvRate = float64(c.Conversions) / float64(c.Samples)
		}
		c.Insufficient = c.Samples < minSamples
		c.MessageCount = g.messageCount
		c.VariantCount = len(g.variants)
		rep, repNorm := pickRepresentative(g)
		c.Representative = mask(rep)
		c.Signature = fnv32Hex(repNorm)
		clusters = append(clusters, c)
	}

	sortClusters(clusters)
	if opts.TopK > 0 && len(clusters) > opts.TopK {
		keep := make([]Cluster, 0, len(clusters))
		quotas := opts.TopK
		var tail []Cluster
		for _, cl := range clusters {
			if !cl.Insufficient && quotas > 0 {
				keep = append(keep, cl)
				quotas--
			} else {
				tail = append(tail, cl)
			}
		}
		clusters = append(keep, tail...)
	}
	return clusters
}

// pickRepresentative 选簇内代表变体：消息数最多者胜，并列取更早出现（确定性）。
// 参数：g=分组累加器；返回：代表原文与它的归一化文本。
func pickRepresentative(g *groupAcc) (string, string) {
	bestNorm := ""
	var best *variantAcc
	for norm, v := range g.variants {
		if best == nil || v.count > best.count || (v.count == best.count && v.order < best.order) {
			best, bestNorm = v, norm
		}
	}
	return best.display, bestNorm
}

// sortClusters 排序：结论位在前，样本不足组垫底；同档按转化率→样本数→键字典序，保证确定性。
// 参数：clusters=待排序切片（原地排序）。
func sortClusters(clusters []Cluster) {
	sort.SliceStable(clusters, func(i, j int) bool {
		a, b := clusters[i], clusters[j]
		if a.Insufficient != b.Insufficient {
			return !a.Insufficient
		}
		if a.ConvRate != b.ConvRate {
			return a.ConvRate > b.ConvRate
		}
		if a.Samples != b.Samples {
			return a.Samples > b.Samples
		}
		if a.Key.ActorID != b.Key.ActorID {
			return a.Key.ActorID < b.Key.ActorID
		}
		if a.Key.AnchorType != b.Key.AnchorType {
			return a.Key.AnchorType < b.Key.AnchorType
		}
		return a.Key.Stage < b.Key.Stage
	})
}

// fnv32Hex fnv32a 稳定哈希（8 位十六进制）：幂等判重不能用带 seed 的哈希。
// 参数：s=归一化文本；返回：十六进制签名。
func fnv32Hex(s string) string {
	h := fnv.New32a()
	_, _ = fmt.Fprintf(h, "%s", s)
	return fmt.Sprintf("%08x", h.Sum32())
}
