// ============ health.go · 职责说明 ============
// 四期 W3（KN9）：健康面三态 ok / degraded / unavailable 的单一真值。
//   - unavailable＝连不上知识服务；
//   - degraded＝连得上但 contract_version 不匹配（不可信的半通）；
//   - ok＝连得上且契约版本一致。
// 三者必须各不同形（都写 ok 会让 KN9 反证 M0291 判红）。
// ================================================
package contract

// ClassifyHealth 把「可达性 + 远端契约版本」映成健康三态。
// 参数：reachable=健康面是否可达；remoteContractVersion=远端 /internal/health 回报的版本。
func ClassifyHealth(reachable bool, remoteContractVersion string) string {
	if !reachable {
		return HealthUnavailable
	}
	if remoteContractVersion != ContractVersion {
		return HealthDegraded
	}
	return HealthOK
}
