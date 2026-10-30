// ============ instant_free_scopes.go · 职责说明 ============
// 即时翻译免费档的四个计数桶 scope 名（★ 2026-10-30 三事合一批 组4，D-1／A-5）。
//
// 物理形态沿用 `rate_limits` 表、按 UTC 日对齐（读写走 `ratelimit_day.go`，**不是**首笔起固定窗）。
// scope 名是**对外排障契约**（运营/运维直接按它查库、看板按它聚合），改名等于让历史读数断档：
//
//	free_pt_user   积分：账号当日免费的"暗额度"用量（隐形，不出数字）
//	free_pt_tenant 积分：租户共享池当日用量（共享租户里防止一个免费用户吃光全员额度）
//	free_ip        次：每 IP 当日免费请求次数（防换号刷量）
//	free_wall      次：**撞墙脚印**，只在被拒那一枪 +1，**不参与任何准入判定**
//	               （成功路径也加 ⇒ 助手的 hit_wall 恒真 ⇒ 对着新用户推销付费，正是要防的形态）
//
// =============================================
package store

// 四个日计数器 scope 的常量组（语义见文件头；改名会让历史读数断档）。
const (
	RateScopeFreePointUser   = "free_pt_user"
	RateScopeFreePointTenant = "free_pt_tenant"
	RateScopeFreeIP          = "free_ip"
	RateScopeFreeWall        = "free_wall"
)
