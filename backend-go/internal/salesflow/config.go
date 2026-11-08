// ============ config.go · 职责说明 ============
// 二期「引擎／配置／资产」三层里的**配置层**：场景差异（行业词表、接管判据、
// 终局定义、每日免费额度）一律通过本对象注入，引擎源码不许按行业分支。
// ★ OutcomeStage 一期只许 inquiry：SCRM 的「到店／成交」在跨境 B2B 不成立，
//
//	用未定义的终局去排序素材会把「没结果」写成结论（二期 §7 断言 A2）。
//
// ================================================
package salesflow

import (
	"errors"
	"strings"
)

// OutcomeStageInquiry 一期唯一允许的终局档：询盘。
const OutcomeStageInquiry = "inquiry"

// Config 一份场景配置实例（三面归一后各场景各持一份，不共享可变状态）。
type Config struct {
	// IndustryCode 场景编码；只作标识与读档用，禁止在引擎侧拿它做 if/switch 分支。
	IndustryCode string
	// AnchorLexicon 锚位词表：跨语言语义命中的唯一来源（§5 中文硬词表外提后的落点）。
	AnchorLexicon map[string][]string
	// TakeoverOnHardFacts 遇报价／MOQ／交期／认证等硬事实不确定时是否交回人工。
	TakeoverOnHardFacts bool
	// OutcomeStage 终局档，一期只许 inquiry。
	OutcomeStage string
	// DailyFreeLimit AI 层每日免费体验上限；耗尽即止，知识层不受它约束。
	DailyFreeLimit int
}

// Validate 校验一份配置是否可安全注入引擎。
// 参数：无（值接收者）；返回：配置不合法的具体错误。
func (c Config) Validate() error {
	if strings.TrimSpace(c.IndustryCode) == "" {
		return errors.New("场景配置缺少行业编码")
	}
	if c.OutcomeStage != OutcomeStageInquiry {
		return errors.New("终局档一期只许 inquiry，不许用未定义终局排序素材")
	}
	if c.DailyFreeLimit < 0 {
		return errors.New("每日免费额度不能为负")
	}
	return nil
}
