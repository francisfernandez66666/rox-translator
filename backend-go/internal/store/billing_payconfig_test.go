// ============ billing_payconfig_test.go · 职责说明 ============
// store 层「支付渠道凭据」（2026-09-22 管理台可配改造）的自动化断言，钉死三条最易被
// 后续改动悄悄破坏的口径：
//
//	① 敏感项加密落库 + 掩码不回写：保存链路收到 "********" 时保留库内旧密文，
//	   真值只在 EncryptSecret 后入库（库里永远看不到明文私钥）；
//	② 留空=清除：管理台清空一栏即删掉该 system_config 键，读侧回到「未配置」，
//	   不会留下一个让渠道误判「已配置」的空值；
//	③ 字段白名单：未登记键名一律拒写——否则这个口就退化成任意 system_config 写入器
//	   （能改 pay_mode、billing_enforced 等计费开关）。
//
// 方言：固定 SQLite 内存库并显式钉死 config.C（AGENTS.md §4，防 run_uat 的 PG 模式泄漏）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/store/ -run TestPayConfig
// =============================================
package store

import (
	"strings"
	"testing"

	"translator/internal/config"
	"translator/internal/secret"
)

// payCfgEnv 钉方言并给出内存库 Store。
func payCfgEnv(t *testing.T) *Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	return newTestStore(t)
}

// rawCfg 直读 system_config 原值（绕过解密，验证「落库即密文」）。
func rawCfg(t *testing.T, st *Store, key string) string {
	t.Helper()
	v, err := st.GetConfig(key)
	if err != nil {
		t.Fatalf("读配置 %s 失败: %v", key, err)
	}
	return v
}

// TestPayConfigSecretEncryptedAndMaskNotWrittenBack 敏感项加密落库，且掩码输入不覆盖真值。
func TestPayConfigSecretEncryptedAndMaskNotWrittenBack(t *testing.T) {
	st := payCfgEnv(t)
	const realKey = "MIIB-private-key-material-0123456789"
	if err := st.SetPayConfigField(PayConfigWechatPrivateKey, realKey); err != nil {
		t.Fatalf("保存私钥失败: %v", err)
	}
	stored := rawCfg(t, st, PayConfigWechatPrivateKey)
	if !strings.HasPrefix(stored, secret.SecretEncPrefix) {
		t.Fatalf("私钥未加密落库，原值=%q", stored)
	}
	if strings.Contains(stored, realKey) {
		t.Fatalf("密文里出现明文私钥: %q", stored)
	}
	if got := st.GetPayChannelConfig().Wechat.PrivateKey; got != realKey {
		t.Fatalf("解密回读不符，got=%q want=%q", got, realKey)
	}
	// 管理台把回显的掩码原样提交回来（用户只改了别的字段）——必须保留旧密文
	if err := st.SetPayConfigField(PayConfigWechatPrivateKey, PayConfigMask); err != nil {
		t.Fatalf("提交掩码失败: %v", err)
	}
	if rawCfg(t, st, PayConfigWechatPrivateKey) != stored {
		t.Fatalf("掩码被写回库，真私钥已丢失: %q", rawCfg(t, st, PayConfigWechatPrivateKey))
	}
	if got := st.GetPayChannelConfig().Wechat.PrivateKey; got != realKey {
		t.Fatalf("掩码提交后真值应不变，got=%q", got)
	}
	// 回显侧同样不得出现真值
	if v := st.PayConfigForEcho()[PayConfigWechatPrivateKey]; v != PayConfigMask {
		t.Fatalf("敏感项回显应为掩码，got=%q", v)
	}
}

// TestPayConfigEmptyClearsField 留空即清除配置项，读侧回到「未配置」。
func TestPayConfigEmptyClearsField(t *testing.T) {
	st := payCfgEnv(t)
	if err := st.SetPayConfigField(PayConfigAlipayAppID, "2021000123456789"); err != nil {
		t.Fatalf("保存 AppID 失败: %v", err)
	}
	if err := st.SetPayConfigField(PayConfigAlipayAppID, "  "); err != nil {
		t.Fatalf("清除 AppID 失败: %v", err)
	}
	if v := rawCfg(t, st, PayConfigAlipayAppID); v != "" {
		t.Fatalf("清空后仍残留配置值: %q", v)
	}
	if got := st.GetPayChannelConfig().Alipay.AppID; got != "" {
		t.Fatalf("清空后读侧应回到未配置，got=%q", got)
	}
}

// TestPayConfigUnknownKeyRejected 白名单外键名拒写（防退化为任意配置写入器）。
func TestPayConfigUnknownKeyRejected(t *testing.T) {
	st := payCfgEnv(t)
	for _, k := range []string{"pay_mode", "billing_enforced", "paych_not_a_field", ""} {
		if err := st.SetPayConfigField(k, "1"); err == nil {
			t.Fatalf("未登记键 %q 被写入了，白名单已失效", k)
		}
	}
	if v := rawCfg(t, st, "pay_mode"); v != "" {
		t.Fatalf("pay_mode 被支付配置接口改写: %q", v)
	}
}

// TestPayConfigSwitchTriState 开关三态：未设置=不表态，"0"=停用，"1"=启用。
// 为什么单独测：把「未设置」误判成「停用」会让存量 env 部署升级即断流。
func TestPayConfigSwitchTriState(t *testing.T) {
	st := payCfgEnv(t)
	if got := st.GetPayChannelConfig().Wechat.Switch; got != PaySwitchUnset {
		t.Fatalf("无配置时应为未设置，got=%v", got)
	}
	if err := st.SetPayConfigField(PayConfigWechatEnabled, "0"); err != nil {
		t.Fatalf("停用失败: %v", err)
	}
	if got := st.GetPayChannelConfig().Wechat.Switch; got != PaySwitchOff {
		t.Fatalf("显式 0 应为停用，got=%v", got)
	}
	if err := st.SetPayConfigField(PayConfigWechatEnabled, "1"); err != nil {
		t.Fatalf("启用失败: %v", err)
	}
	if got := st.GetPayChannelConfig().Wechat.Switch; got != PaySwitchOn {
		t.Fatalf("显式 1 应为启用，got=%v", got)
	}
}
