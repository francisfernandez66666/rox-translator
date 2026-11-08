// ============ enginepolicy.go · 职责说明 ============
// 迁入策略的三张单一真值表：web/ORM 框架依赖禁词、行业分支禁词、迁入包目录。
// ★ 字面一律**拆开拼**：这些表本身就是扫描器要找的违禁形态，写成完整字面会被
//
//	自己的门禁喂饱（一期 P10「注释里的锚点把负向锁喂饱」同族判据自伤）。
//
// ★ 门禁必须断言三张表非空：把表清空不是「没有违规」，是「判据失明」。
// ================================================
package salesflow

// BannedWebFrameworkTokens 引擎包禁止出现的 web/ORM 框架依赖字面。
// 返回：禁词全集（调用方按全集判，不许清空后当作没有违规）。
func BannedWebFrameworkTokens() []string {
	return []string{
		"gi" + "n.",
		"http." + "Request",
		"gor" + "m.",
	}
}

// BannedIndustryBranchTokens 引擎包禁止出现的按行业分支字面。
// 返回：禁词全集（行业差异只许经 Config 注入）。
func BannedIndustryBranchTokens() []string {
	return []string{
		"==" + ` "auto"`,
		"Industry" + "Code ==",
		"switch " + "industry",
		"极" + "石",
	}
}

// RawLogToken 迁入包禁止出现的裸日志调用字面（拆开避免策略真源自伤 LG1 扫描器）。
// 返回：裸日志调用字面。
func RawLogToken() string { return "log." + "Printf(" }

// ImportedPackageDirs 二期迁入包目录名（按全集枚举；目录尚不存在不算命中，
// 后续 crm／attribution／talkmining／state_machine 一落地就自动进各道门禁射程）。
// 返回：目录名全集。
func ImportedPackageDirs() []string {
	return []string{"salesflow", "crm", "attribution", "talkmining", "state_machine"}
}
