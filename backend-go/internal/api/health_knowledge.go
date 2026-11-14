// ============ health_knowledge.go · 职责说明 ============
// 四期 W3 KN9：主服务 /api/health 的「knowledge」段。
//
//   - 探针目标：知识服务独立进程（:8791，internal/knowledge/api）。主服务只持有客户端，
//     不持有知识库连接（KN11 重构后主库零知识表）。
//   - 三态（与 internal/knowledge/contract.health.go 单一真值一致）：
//     ok＝可达且契约版本一致；degraded＝可达但契约版本不匹配（不可信的半通）；
//     unavailable＝连不上；not_configured＝本进程未装配知识客户端（nil）。
//   - 口径同 #42：健康面匿名可达，只出状态词，绝不暴露 token / 主机 / 路径 / 版本原文以外坐标。
//   - 探测自带超时且永不 panic：知识服务挂了不该把 /api/health 自己打成 5xx。
// =============================================
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"translator/internal/knowledge"
	"translator/internal/knowledge/contract"
)

// knowledgeHealthProbeTimeout 探针超时：健康面要在编排器超时前回状态词，故短于业务调用预算。
const knowledgeHealthProbeTimeout = 600 * time.Millisecond

// knowledgeHealth /api/health 的 knowledge 段：状态词 + 可达性 + 远端契约版本（口径见文件头）。
// 参数 s: 服务实例（可能未装配 KnowledgeClient）。返回：含 status/reachable/contract_version 的映射。
func (s *Server) knowledgeHealth() map[string]interface{} {
	out := map[string]interface{}{
		"status":    "not_configured",
		"reachable": false,
	}
	kc := s.KnowledgeClient
	if kc == nil {
		return out
	}
	out["status"] = s.knowledgeStatus(kc)
	out["reachable"] = out["status"] == contract.HealthOK
	return out
}

// knowledgeStatus 真探知识服务 /internal/health，按 contract.ClassifyHealth 落三态。
// 参数 kc: 知识服务客户端。返回：ok / degraded / unavailable（绝不在本函数内写成 ok 兜底）。
func (s *Server) knowledgeStatus(kc *knowledge.Client) string {
	ctx, cancel := context.WithTimeout(context.Background(), knowledgeHealthProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, kc.BaseURL+contract.PathHealth, nil)
	if err != nil {
		return contract.HealthUnavailable
	}
	if kc.Token != "" {
		req.Header.Set("X-Knowledge-Token", kc.Token)
	}
	resp, err := kc.HTTPClient.Do(req)
	if err != nil {
		// 连不上 ⇒ unavailable（绝不允许回落成 ok，否则 KN9 失去探照意义）。
		return contract.HealthUnavailable
	}
	defer resp.Body.Close()
	var hr contract.HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&hr); err != nil {
		return contract.HealthUnavailable
	}
	// ★ KN9 单一真值：可达性 + 远端契约版本 ⇒ 三态。M0291 反证＝把这里硬编码成 HealthOK 即判红。
	return contract.ClassifyHealth(resp.StatusCode == http.StatusOK, hr.ContractVersion)
}
