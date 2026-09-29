// ============ system_values.go · 职责说明 ============
// 「系统现值」注入：拼 LLM 系统提示词前，从主服务的**匿名公开接口**取回
// 平台此刻真实生效的计费系数与可选语种数，作为一段事实素材交给模型。
//
// ★ 081x（2026-09-29，用户指令「涉及到价格、套餐、能力之类的东西，要严格按 RAG、
//
//	  使用系统配置口径」）为什么要有这一段：
//
//		挂件的知识库是一份**手抄的静态文案**（kb_entries / seed.json）。运营在主后台
//		把积分系数、语种清单调了档，挂件是不知道的——它照旧念知识条目里那个抄来的旧数，
//		于是「对外报价」和「扣费公式」（AGENTS §一·5 那条单一事实源）在客户屏幕上分叉。
//		更糟的是知识里写「40+ 语种」而实时接口现值 35 种，这是**对外多承诺**，
//		比语气冷冰冰严重得多。所以价格、语种数这类会变的数字，一律不落在知识条目里，
//		改成每次建 prompt 时取现值。
//
// ★ 四条硬口径（改这个文件前先读完）：
//
//  1. **只取匿名公开口，不越权**：/api/pricing/meta 与 /api/translation/langs
//     都是官网自己也在用的公开口，且已在「公开接口零 token 裸值」的约束下折算成积分口径。
//     本文件禁止改成调超管接口或直接读主库：assist 与主服务是两套部署单元，
//     跨库读（store.ReadMainDBConfig）只在 SQLite 形态可用，生产 PG 下直接失效，
//     而且那等于让挂件持有主库凭据。
//
//  2. **取不到就整段不出现，绝不兜旧值**（与官网定价页同一条判据：
//     「取不到系数就渲染空态，不许兜底旧价」）。这里的兜底代价比前端更高——
//     模型拿到一份写死的旧数字就会把它当事实念给客户。所以失败分支返回空串，
//     话术由 promise.go 第二条接住（「现值没出现就说不支持报数，发你一份清单」）。
//
//  3. **失败也要占缓存位**：失败后同样把 sysValAt 推到当下，TTL 内不再重试。
//     否则主服务一挂，每条对话都要先等两次 HTTP 超时才建 prompt（挂件首响应被拖到秒级），
//     比"暂时取不到现值"更难受。
//
//  4. **算术不交给模型**（★ 082x 第七条，2026-09-29 现网两条英文轮各错一次）：
//     总额由 renderQuoteExamples 在服务端按现值算好写成「现算示例」，配一条报价纪律禁自乘除、
//     禁把字数换算成字符数。只给系数的话模型真的会当场做乘法，还会做错（对外错报价）。
//     这块的判据在 system_values_test.go，改动请连带跑它。
//
// 缓存口径：成功/失败都进 60s TTL；管理台改 main_base_url 后最迟 60s 生效，不必重启
// （与 LLM 配置热加载同一节奏）。想看当前取到了什么，管理台 GET /api/assist/admin/system_values。
//
// =============================================
package engine

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// systemValuesTTL 现值缓存时长。做成变量而不是常量：单测要能把它压到 0 复测刷新，
// 生产口径 60s 足够——运营调档是小时级动作，而每条对话都打一次 HTTP 不划算。
var systemValuesTTL = 60 * time.Second

// systemValuesTimeout 单次现值拉取的墙钟预算。取的是**本机回环**上的主服务，
// 正常毫秒级；给 1.5s 是留出「主服务在重启/满载」时的兜底上限，
// 两条口合计最坏 3s，且命中失败后 TTL 内不再重试（见文件头第 3 条口径）。
const systemValuesTimeout = 1500 * time.Millisecond

// defaultMainBaseURL 主服务默认地址（configs.main_base_url 为空时用它）。
// assist 与主服务同机部署，走回环；跨机部署由管理台填该键覆盖。
const defaultMainBaseURL = "http://127.0.0.1:8787"

// systemValuesClient 复用的 HTTP 客户端（无凭据、无 Cookie：打的两个口都是匿名公开口）
var systemValuesClient = &http.Client{Timeout: systemValuesTimeout}

// pricingMode 单个计费模式的现值系数。
// 做成**具名类型**而不是留在 pricingMetaDoc 里的匿名结构：报价守卫（quote_guard.go）要把
// "句中能认出的那个模式"交给替换句，匿名结构在函数签名上写不出同型（带 json tag 与不带的两种
// 匿名结构在 Go 里就不是同一类型），硬套只能多复制一层——那一层迟早跟源结构分叉。
type pricingMode struct {
	Code             string  `json:"code"`
	PointsPer1kChars float64 `json:"points_per_1k_chars"`
	PointsFixed      float64 `json:"points_fixed"`
}

// pricingMetaDoc GET /api/pricing/meta 响应结构
// （字段名与主服务 internal/api/pricing_meta.go 交叉锁：改那边必须同步这里，
//
//	否则解出来的 Modes 是空切片，本段静默不出现——所以 system_values_status.go 里给了管理台读数）
type pricingMetaDoc struct {
	Success bool          `json:"success"`
	Modes   []pricingMode `json:"modes"`
	// ↑ 2026-09-29 092x：Modes 的元素从匿名 struct 提为具名 pricingMode，
	//   json tag 在 pricingMode 上一字未改，解析行为与改前等价（别误读成接口口径变了）。
	PointsPriceMoney float64 `json:"points_price_money"`
	Unit             string  `json:"unit"`
}

// langsDoc GET /api/translation/langs 响应结构（只数条数，明细让客户看界面）
type langsDoc struct {
	KbLangs []map[string]string `json:"kb_langs"`
}

// systemValuesBlock 返回【系统现值】整段文本；无可用现值时返回空串（调用方据此整段不拼）。
// 参数 ctx 随对话请求取消。缓存见文件头第 3 条口径。
func (e *Engine) systemValuesBlock(ctx context.Context) string {
	block, _ := e.systemValuesWithDoc(ctx)
	return block
}

// systemValuesWithDoc 同一条缓存里既给渲染好的那段文本，也给**结构化现值**。
//
// ★ 092x 红腿二：出栈的报价核验（quote_guard.go）要按系数复算模型报出的总额，
// 拿渲染文本去反解数字等于把「present in prose」当成判据——那是最容易被一句文案改动产物失效的锁。
// 缓存必须一次拿到两份，才能保证「模型看到的数字」与「我们据以核验的数字」同源。
func (e *Engine) systemValuesWithDoc(ctx context.Context) (string, *pricingMetaDoc) {
	e.sysValMu.Lock()
	defer e.sysValMu.Unlock()
	if time.Since(e.sysValAt) < systemValuesTTL {
		return e.sysValBlock, e.sysValDoc
	}
	base := e.MainBaseURL()
	doc := e.fetchPricingMeta(ctx, base)
	block := renderSystemValues(base, doc, e.fetchLangCount(ctx, base))
	e.sysValBlock, e.sysValDoc, e.sysValAt = block, doc, time.Now()
	return block, doc
}

// SystemValuesCached 返回对话链路当前会拼进 prompt 的那段现值与其落盘时间。
// 管理台「现值」读数用它看**客户正在拿到的**是什么；现取另走 SystemValuesSnapshot。
func (e *Engine) SystemValuesCached() (string, time.Time) {
	e.sysValMu.Lock()
	defer e.sysValMu.Unlock()
	return e.sysValBlock, e.sysValAt
}

// MainBaseURL 当前生效的主服务地址（configs.main_base_url 现值，空则代码默认）。
// 暴露给管理台，是为了让「现值取不到」这类问题能一眼看出拨的是哪个地址——
// 这个键此前只在取数时读一次，运维界面上看不见，故障时只能靠猜。
func (e *Engine) MainBaseURL() string {
	return strings.TrimRight(e.db.GetConfig("main_base_url", defaultMainBaseURL), "/")
}

// SystemValuesSnapshot 无视缓存、现取一次现值并渲染（供 system_values 状态口与单测直接调用；
// 对话链路请走 systemValuesBlock，别在这儿绕缓存）。
func (e *Engine) SystemValuesSnapshot(ctx context.Context) string {
	return renderSystemValues(e.MainBaseURL(), e.fetchPricingMeta(ctx, e.MainBaseURL()), e.fetchLangCount(ctx, e.MainBaseURL()))
}

// fetchPricingMeta 拉计费系数；任何异常（连不上/非 200/不是 JSON/字段缺失/数字为 0）返回 nil，
// 由上层把这一段整体省掉——**半截的系数比没有系数更危险**（模型会拿 0 去算钱）。
func (e *Engine) fetchPricingMeta(ctx context.Context, base string) *pricingMetaDoc {
	var doc pricingMetaDoc
	if !getJSON(ctx, base+"/api/pricing/meta", &doc) {
		return nil
	}
	// 系数与单价必须成对齐全：points_price_money 为 0 意味着主服务侧汇率/尺子脏了
	// （moneyPerPoint 对脏配置刻意回 0），这时候报出去的人民币单价是假的。
	if !doc.Success || len(doc.Modes) == 0 || doc.PointsPriceMoney <= 0 {
		return nil
	}
	for _, m := range doc.Modes {
		if m.Code == "" || m.PointsPer1kChars <= 0 {
			return nil
		}
	}
	return &doc
}

// fetchLangCount 拉实时可选语种数；取不到返回 0（上层据此不写这一行）。
func (e *Engine) fetchLangCount(ctx context.Context, base string) int {
	var doc langsDoc
	if !getJSON(ctx, base+"/api/translation/langs", &doc) {
		return 0
	}
	return len(doc.KbLangs)
}

// getJSON 发一次匿名 GET 并把响应体解进 out；失败一律 false（不区分原因，现值注入是软路径）。
// 判据按「拿不到就用不上」写死：非 200、读取中断、体积超 1MB（防御异常大响应）、JSON 解析失败都算失败。
func getJSON(ctx context.Context, url string, out any) bool {
	reqCtx, cancel := context.WithTimeout(ctx, systemValuesTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := systemValuesClient.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return false
	}
	return json.Unmarshal(body, out) == nil
}

// systemValuesHead 现值段的段首前缀（断言与判空都用它，别只用「【系统现值】」四个字：
// 承诺边界正文里也要引用这一段的名字（「只用【系统现值】给的数字」），
// 只按那四个字判在不在，会把"承诺段说了要看向值"误判成"现值段真的在"——081x 首跑即此假红）。
const systemValuesHead = "【系统现值】（下面是平台此刻真实生效的配置值"

// renderSystemValues 把现值拼成 prompt 里的一段。纯函数，便于单测逐臂断言。
// 参数：pricing 为 nil / langCount<=0 表示该项没取到，对应的行整行不出现；两者都缺则返回空串。
func renderSystemValues(base string, pricing *pricingMetaDoc, langCount int) string {
	if pricing == nil && langCount <= 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(systemValuesHead + "，刚从主服务接口 " + base + " 取回。价格、语种数**只能**用这里的数字；这里没写的数字就是没取到，不许凭印象补，更不能引用别处看来的旧数。）\n")
	if langCount > 0 {
		sb.WriteString("- 可选目标语言：" + strconv.Itoa(langCount) + " 种（以产品界面语种清单为准；「40+」「上百种」这类说法与现值不符，不许说）\n")
	}
	if pricing != nil {
		sb.WriteString("- 计费方式：按实际用量扣积分，用多少扣多少。系数（积分口径）——")
		for i, m := range pricing.Modes {
			if i > 0 {
				sb.WriteString("；")
			}
			sb.WriteString(modeLabel(m.Code) + "每 1000 源字符·单语种 " + trimNum(m.PointsPer1kChars) + " 积分，另每次建单固定 " + trimNum(m.PointsFixed) + " 积分")
		}
		sb.WriteString("\n")
		sb.WriteString("- 积分单价：1 积分 ≈ " + trimNum(pricing.PointsPriceMoney) + " 元（积分与人民币的换算口径；客户实际付多少以套餐页面为准，别拿它当套餐价报）\n")
		sb.WriteString(renderQuoteExamples(pricing))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// quoteExampleTiers 现算示例给的源字符档。
// 为什么是这三档：1000 是系数本身（客户问「一个字多少」时唯一能诚实回答的形状），
// 5000／20000 覆盖现网挂件实际被问到的那份量级（一份合同、一份中等文档），
// 再多就是给模型一份可以随意摘抄的数字表——**示例是给它的引用面，不是计算器**。
var quoteExampleTiers = []int{1000, 5000, 20000}

// renderQuoteExamples 把「N 源字符花多少积分、约合多少元」在**服务端**算好交给模型引用。
//
// ★ 082x 第七条（2026-09-29 现网复问第二批取证，两条英文轮各错一次）：
//
//	光给系数等于给模型出题——它真的会当场做乘法，而且做错：
//	一条把 2000 个英文单词按「1 词 ≈ 400 字符」折算（平台按**源字符**计费，字数与字符之间
//	从来没有官方换算），另一条直接报出「800credits + 7.5 = 807.5 credits（约 ¥80）」
//	这类谁都没核过的总额。**这是对外错报，不是措辞问题**：客户拿这个数去理解账单，
//	和官网报价页、和实际扣费三条口径打架（AGENTS §一·5 那条单一事实源最怕这个）。
//
//	修法不是再写一句更凶的「不许自己算」（那还是请求）：**把算术从模型手里拿走**。
//	总额由这里算完、连人民币约等数一起写好，它只能整条引用；
//	数字对不上就是没取到现值，走文件头那条「整段不出现」的 fail-soft 而不是现编。
func renderQuoteExamples(pricing *pricingMetaDoc) string {
	if pricing == nil || len(pricing.Modes) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("- 现算示例（下面每个总额都是系统按上面的系数**算好的**，报数时整条引用，不许自己再算、也不许报这里没有的总额）：\n")
	for _, m := range pricing.Modes {
		segs := make([]string, 0, len(quoteExampleTiers))
		for _, chars := range quoteExampleTiers {
			points := m.PointsPer1kChars*float64(chars)/1000 + m.PointsFixed
			segs = append(segs, strconv.Itoa(chars)+" 源字符 = "+trimNum(points)+" 积分（约 "+trimNum(round2(points*pricing.PointsPriceMoney))+" 元）")
		}
		sb.WriteString("  " + modeLabel(m.Code) + "：" + strings.Join(segs, "；") + "\n")
	}
	sb.WriteString("- 报价纪律：平台按**源字符**计费，字数与字符之间没有官方换算——" +
		"客户给的是字数时，禁止把字数换算成字符数、禁止自己做任何乘除、禁止报上面示例之外的总额；" +
		"只报「每 1000 源字符 = 上面的系数」这条单价，再补一句总额看实际字符量、把文件发过来就能估准。" +
		"人民币金额只许引用上面算好的那个「约 X 元」，不许自己按汇率或单价现算。\n")
	return sb.String()
}

// round2 人民币约等数收到两位小数（分）。为什么不留六位：现值那份 trimNum 的六位是给**单价**用的，
// 总额报出「12.717442 元」既不是客户能核对的数、也不会让他更信，反而像计算器漏出来的尾巴。
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// modeLabel 计费模式代码转客户听得见的说法（未知代码原样带出，不猜语义）
func modeLabel(code string) string {
	switch code {
	case "fast":
		return "快速模式"
	case "pro":
		return "专业模式"
	default:
		return "模式 " + code + "："
	}
}

// trimNum 数字出栈前的收尾：最多 6 位小数、去掉无业务含义的浮点尾巴和尾随 0。
// 为什么是 6 位：主服务侧 points_price_money 本身就是 round6 出来的六位（如 0.099668），
// 这里再往粗收就成了错报价的根源——挂件拿它乘积分算钱，收到 4 位（0.0997）就是千分之三的系统性高估。
// 为什么不去尾：直接 FormatFloat(-1) 会吐出 0.09966800000000001 这类串，模型照着念给客户同样难看。
func trimNum(v float64) string {
	s := strconv.FormatFloat(v, 'f', 6, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" {
		return "0"
	}
	return s
}
