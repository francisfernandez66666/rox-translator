// ============ lexicon_seed.go · 职责说明 ============
// ★ 配置种子文件（二期 §5 L1 白名单位）：全仓**唯一**允许出现这批中文词表字面的地方。
// 引擎代码禁止抄任何词，只能经 LoadLexicon 读配置；运营在管理台改过的租户档优先于平台档。
// 种子只写 tenant_id=0 平台默认档，且幂等（已存在不覆盖，SeedLexiconIfAbsent 条件 INSERT）。
// 跨境 B2B 通用口径：只取中文私域通用场景词，汽车品牌／试驾／续航等行业专属词不进种子，
// 行业差异由运营按租户配（§5.3 零代码分支）。
// ================================================
package salesflow

import (
	"context"
	"fmt"

	"translator/internal/observability"
	"translator/internal/store"
)

// 词表类别编码（引擎与后台配置面共用同一份真值，禁止各写一套字符串）。
const (
	// KindAnchorGreeting 寒暄词。
	KindAnchorGreeting = "anchor_greeting"
	// KindAnchorIntent 意向词。
	KindAnchorIntent = "anchor_intent"
	// KindResistancePrice 价格抗性词。
	KindResistancePrice = "resistance_price"
	// KindResistanceSpec 规格抗性词。
	KindResistanceSpec = "resistance_spec"
	// KindResistanceService 服务抗性词。
	KindResistanceService = "resistance_service"
	// KindResistanceBrand 品牌抗性词。
	KindResistanceBrand = "resistance_brand"
	// KindShortNoHook 纯语气词（精确相等才算「没接钩」）。
	KindShortNoHook = "short_no_hook"
	// KindEmotionNegative 负面情绪词。
	KindEmotionNegative = "emotion_negative"
	// KindEmotionPositive 正面情绪词。
	KindEmotionPositive = "emotion_positive"
	// KindTakeoverVisit 到场／线下引导词。
	KindTakeoverVisit = "takeover_visit"
)

// LexiconKinds 返回全部词表类别（管理台下拉与射程自证按全集判）。
// 参数：无；返回：类别编码切片。
func LexiconKinds() []string {
	return []string{
		KindAnchorGreeting, KindAnchorIntent,
		KindResistancePrice, KindResistanceSpec, KindResistanceService, KindResistanceBrand,
		KindShortNoHook, KindEmotionNegative, KindEmotionPositive, KindTakeoverVisit,
	}
}

// LexiconSeedEntry 一条平台默认档种子：场景＋语种＋类别＋词表。
type LexiconSeedEntry struct {
	Scene string
	Lang  string
	Kind  string
	Words []string
}

// PlatformLexiconSeed 平台默认档全集（zh 与 en 各一套真实对应词，不靠翻译兜底）。
// ★ 这是配置数据，不是判定逻辑；任何引擎文件出现这些字面即属违规。
var PlatformLexiconSeed = []LexiconSeedEntry{
	// ---- 中文：跨境／私域通用，不含任何行业专属品牌词 ----
	{Scene: SceneCrossBorder, Lang: "zh", Kind: KindAnchorGreeting, Words: []string{"你好", "在吗", "哈喽", "早上好", "您好"}},
	{Scene: SceneCrossBorder, Lang: "zh", Kind: KindAnchorIntent, Words: []string{"价格", "报价", "起订量", "交期", "样品", "目录", "怎么买"}},
	{Scene: SceneCrossBorder, Lang: "zh", Kind: KindResistancePrice, Words: []string{"太贵", "超出预算", "预算不够", "再少", "再降", "划算吗"}},
	{Scene: SceneCrossBorder, Lang: "zh", Kind: KindResistanceSpec, Words: []string{"规格不够", "参数不符", "配置低", "尺寸不合适"}},
	{Scene: SceneCrossBorder, Lang: "zh", Kind: KindResistanceService, Words: []string{"售后", "质保", "保修", "维修", "退换"}},
	{Scene: SceneCrossBorder, Lang: "zh", Kind: KindResistanceBrand, Words: []string{"没听过", "不知名", "换一家"}},
	{Scene: SceneCrossBorder, Lang: "zh", Kind: KindShortNoHook, Words: []string{"哦", "嗯", "啊", "好", "知道了", "呵呵", "哈哈", "好哒"}},
	{Scene: SceneCrossBorder, Lang: "zh", Kind: KindEmotionNegative, Words: []string{"不满意", "不值", "不好", "不行", "投诉", "垃圾", "算了", "不考虑", "失望"}},
	{Scene: SceneCrossBorder, Lang: "zh", Kind: KindEmotionPositive, Words: []string{"喜欢", "满意", "不错", "棒", "赞", "谢谢", "感谢", "专业", "有兴趣", "想了解"}},
	{Scene: SceneCrossBorder, Lang: "zh", Kind: KindTakeoverVisit, Words: []string{"到店", "去店里", "来看看", "线下", "现场", "上门"}},
	// ---- 英文：真正的英文对应词，不是中文词表的机翻占位 ----
	{Scene: SceneCrossBorder, Lang: "en", Kind: KindAnchorGreeting, Words: []string{"hi", "hello", "hey", "good morning"}},
	{Scene: SceneCrossBorder, Lang: "en", Kind: KindAnchorIntent, Words: []string{"price", "quote", "moq", "lead time", "sample", "catalog", "how to order"}},
	{Scene: SceneCrossBorder, Lang: "en", Kind: KindResistancePrice, Words: []string{"too expensive", "over budget", "budget", "discount", "cheaper", "worth it"}},
	{Scene: SceneCrossBorder, Lang: "en", Kind: KindResistanceSpec, Words: []string{"spec mismatch", "wrong size", "low spec", "not the model"}},
	{Scene: SceneCrossBorder, Lang: "en", Kind: KindResistanceService, Words: []string{"after sales", "warranty", "repair", "return", "refund"}},
	{Scene: SceneCrossBorder, Lang: "en", Kind: KindResistanceBrand, Words: []string{"never heard of", "unknown brand", "other supplier"}},
	{Scene: SceneCrossBorder, Lang: "en", Kind: KindShortNoHook, Words: []string{"ok", "okay", "hmm", "uh", "yeah", "got it"}},
	{Scene: SceneCrossBorder, Lang: "en", Kind: KindEmotionNegative, Words: []string{"not happy", "not worth", "bad", "complaint", "no thanks", "disappointed"}},
	{Scene: SceneCrossBorder, Lang: "en", Kind: KindEmotionPositive, Words: []string{"like it", "satisfied", "great", "thanks", "professional", "interested"}},
	{Scene: SceneCrossBorder, Lang: "en", Kind: KindTakeoverVisit, Words: []string{"visit", "on site", "in person", "come over", "showroom"}},
}

// SeedPlatformLexicon 幂等种入平台默认档：运营已改过的档一律不覆盖。
// 参数：st=存储层；返回：种入失败错误（含档位定位）。
func SeedPlatformLexicon(st *store.Store) error {
	if st == nil {
		return fmt.Errorf("词表种子缺少存储层")
	}
	inserted := 0
	for _, e := range PlatformLexiconSeed {
		ok, err := st.SeedLexiconIfAbsent(0, e.Scene, e.Lang, e.Kind, e.Words)
		if err != nil {
			return fmt.Errorf("词表种子 %s/%s/%s 失败: %w", e.Scene, e.Lang, e.Kind, err)
		}
		if ok {
			inserted++
		}
	}
	observability.Info(context.Background(), "平台词表种子完成", "total", len(PlatformLexiconSeed), "inserted", inserted)
	return nil
}

// LoadLexicon 引擎唯一的词表读口：租户档 > 平台档 > 空（回落规则在 store 层单一实现）。
// 参数：ctx=上下文；st=存储层；tid=显式租户；scene/lang/kind=档位；返回：词表与错误。
func LoadLexicon(ctx context.Context, st *store.Store, tid int64, scene, lang, kind string) ([]string, error) {
	if tid <= 0 {
		return nil, fmt.Errorf("词表读取必须带有效租户（tid<=0 拒收）")
	}
	if st == nil {
		return nil, fmt.Errorf("词表读取缺少存储层")
	}
	words, err := st.LoadLexiconWords(tid, scene, lang, kind)
	if err != nil {
		observability.Warn(ctx, "词表读取失败，本轮按空词表处理", "tenant_id", tid, "scene", scene, "lang", lang, "kind", kind, "error", err.Error())
		return nil, err
	}
	return words, nil
}
