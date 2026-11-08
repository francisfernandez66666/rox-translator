// ============ scenes.go · 职责说明 ============
// 二期 S3「三面归一」的配置层：跨境对外沟通／中文私域养客／到店现场服务
// 各持一份 Config，差异全在配置与资产层，引擎侧零行业分支。
// ★ 引导流 schema 升为公共财产（门店端 P1–P5 抽象）：五步只声明「采集什么字段」，
//
//	不写任何行业文案；具体标题由词典键注入，避免每场景各写一份引导流。
//
// ================================================
package salesflow

// 三面场景编码（配置层的标识，不是引擎分支判据）。
const (
	SceneCrossBorder = "cross_border"
	ScenePrivate     = "private_domain"
	SceneInStore     = "in_store"
)

// SceneConfigs 返回三份场景配置实例（按全集判：新增第四面必须同步扩断言）。
// 返回：场景编码 → 配置的只读快照映射。
func SceneConfigs() map[string]Config {
	return map[string]Config{
		SceneCrossBorder: {
			IndustryCode: SceneCrossBorder,
			AnchorLexicon: map[string][]string{
				"price": {"price", "precio", "価格", "价格"},
				"moq":   {"moq", "minimum order", "最小起订量"},
			},
			TakeoverOnHardFacts: true,
			OutcomeStage:        OutcomeStageInquiry,
			DailyFreeLimit:      20,
		},
		ScenePrivate: {
			IndustryCode: ScenePrivate,
			AnchorLexicon: map[string][]string{
				"price": {"报价", "多少钱"},
				"trial": {"试驾", "到店"},
			},
			TakeoverOnHardFacts: true,
			OutcomeStage:        OutcomeStageInquiry,
			DailyFreeLimit:      20,
		},
		SceneInStore: {
			IndustryCode: SceneInStore,
			AnchorLexicon: map[string][]string{
				"menu":    {"menu", "菜单", "メニュー"},
				"service": {"服务铃", "service"},
			},
			TakeoverOnHardFacts: false,
			OutcomeStage:        OutcomeStageInquiry,
			DailyFreeLimit:      20,
		},
	}
}

// GuidedFlowStep 通用引导流一步：只声明采集字段与标题词典键。
type GuidedFlowStep struct {
	// Key 步骤稳定标识（不随语种/场景变化）。
	Key string
	// TitleKey 标题的词典键（文案在 i18n 层，不进引擎）。
	TitleKey string
	// Fields 本步要采集的字段名集合。
	Fields []string
}

// GuidedFlowSchema 五步通用引导流骨架（门店端 P1–P5 的抽象形态）。
// 返回：按序的五步定义。
func GuidedFlowSchema() []GuidedFlowStep {
	return []GuidedFlowStep{
		{Key: "loss", TitleKey: "flow.loss", Fields: []string{"name", "industry"}},
		{Key: "asset", TitleKey: "flow.asset", Fields: []string{"asset"}},
		{Key: "pick", TitleKey: "flow.pick", Fields: []string{"items"}},
		{Key: "langs", TitleKey: "flow.langs", Fields: []string{"target_langs"}},
		{Key: "register", TitleKey: "flow.register", Fields: []string{"email", "password"}},
	}
}
