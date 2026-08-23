// ============ 本文件职责中文说明 ============
// Token 计费一次性迁移：把存量租户的句数余额（tenants.permissions.sentence_balance）
// 按「estimate_tokens_per_sentence」换算率折算充入 balance_accounts（token 底层账户）。
// 迁移完成后写入全局标记 billing_token_migrated=1：
//   - 之后所有扣费/闸门只认 token 余额（Service.Enabled() 据此自动生效）
//   - sentence_enforced 开关封存废弃；句数字段仅作历史展示镜像
//
// 幂等保证：以标记位为闸，重复启动不会二次发放。
// =============================================
package billing

import (
	"log"
	"time"

	"translator/internal/store"
	"translator/internal/tenant"
)

// RunTokenMigration 启动时执行句数→token 一次性迁移（幂等）。
// 逻辑：billing_token_migrated 标记存在则直接返回；否则遍历全部租户，
// 对 SentenceBalance>0 的租户按换算率 Charge 等值 token 并清零句数镜像，
// 同时站内通知由调用方（server 启动日志）兜底说明。
// 参数：st=平台存储。无返回值；失败仅打日志不阻断启动。
func RunTokenMigration(st *store.Store) {
	if st == nil {
		return
	}
	// 幂等闸：已迁移过则跳过
	if v, _ := st.GetConfig("billing_token_migrated"); v == "1" {
		return
	}
	rate := st.TokenSentenceRate()
	rows, err := st.DB().Query("SELECT id, COALESCE(permissions,'') FROM tenants")
	if err != nil {
		log.Printf("[billing-token-migrate] 读取租户失败: %v", err)
		return
	}
	defer rows.Close()
	type item struct {
		id       int64
		sentence int64
	}
	var items []item
	for rows.Next() {
		var id int64
		var permsRaw string
		if err := rows.Scan(&id, &permsRaw); err != nil {
			continue
		}
		p := tenant.ParsePerms(permsRaw)
		if p.SentenceBalance > 0 {
			items = append(items, item{id: id, sentence: p.SentenceBalance})
		}
	}
	rows.Close()

	converted := int64(0)
	for _, it := range items {
		tokens := it.sentence * rate
		if err := st.Charge(it.id, tokens); err != nil {
			log.Printf("[billing-token-migrate] 租户 %d 充值失败: %v", it.id, err)
			continue
		}
		// 清零句数镜像（token 已承载额度；PackageCode 订阅身份保留）
		if perms, err := st.GetTenantPerms(it.id); err == nil {
			perms.SentenceBalance = 0
			_ = st.SaveTenantPerms(it.id, perms)
		}
		_ = st.CreateNotification(0, "计费体系升级：句数额度已折算为 token",
			"您的剩余句数额度已按 1 句 = "+itoa(rate)+" token 折算充入余额，后续翻译按实际 token 消耗计费。",
			"tenant", it.id)
		converted++
		log.Printf("[billing-token-migrate] 租户 %d：%d 句 → %d token", it.id, it.sentence, tokens)
	}
	_ = st.SetConfig("billing_token_migrated", "1")
	_ = st.SetConfig("billing_token_migrated_at", time.Now().Format(time.RFC3339))
	log.Printf("[billing-token-migrate] 完成：共迁移 %d 个租户，换算率 %d token/句", converted, rate)
}

// itoa 整数转字符串（避免 migrate 文件引入 strconv 仅一处使用）。
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
