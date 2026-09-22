// ============ billing_payconfig.go · 职责说明 ============
// 支付渠道凭据（微信 Native v3 / 支付宝当面付）的库内配置数据层（★ 2026-09-22 需求：
// 「拿到商户资质后不必重新发布就能填参数」）。
//
// 为什么落在 system_config 而不是新建表：
//   - 这是一份「平台级单例配置」（全平台一份，非按租户/按记录增长），
//     与既有 pay_mode / static_qr_image / usdt_* 同类；建表反而要额外处理「只允许一行」。
//   - 沿用 KV 口径即天然幂等，老库无需迁移（新增列才需要 db.EnsureColumns）。
//   - 若将来要按租户配商户号，届时再升级为带 tenant_id 的表，读接口已收口在本文件。
//
// 安全口径：
//   - 敏感项（APIv3 密钥、商户/应用私钥）落库前一律 secret.EncryptSecret（enc:v1: 前缀），
//     回显给管理台时只给掩码，管理台保存时按掩码识别「用户没改」并回填旧值——
//     于是「表单原样提交」永远不可能把掩码写进库、把真密钥冲掉。
//   - 字段键名走白名单表（payConfigFields），杜绝借本接口往 system_config 写任意键。
//
// 注意：本文件只管「库里配了什么」，不判断「取哪个值」。环境变量 > 数据库的优先序
// 在 api 层收口（见 internal/api/pay_channels.go），数据层保持无环境耦合以便单测。
// =============================================
package store

import (
	"context"
	"strings"

	"translator/internal/db"
	"translator/internal/observability"
	"translator/internal/secret"
)

// 支付渠道配置的 system_config 键名（前缀 paych_ = pay channel）。
const (
	PayConfigNotifyBase = "paych_notify_base" // 回调地址前缀（拼 /api/pay/notify/:channel）

	PayConfigWechatEnabled      = "paych_wechat_enabled"
	PayConfigWechatAppID        = "paych_wechat_app_id"
	PayConfigWechatMchID        = "paych_wechat_mch_id"
	PayConfigWechatSerialNo     = "paych_wechat_serial_no"
	PayConfigWechatAPIv3Key     = "paych_wechat_apiv3_key" // 敏感
	PayConfigWechatPrivateKey   = "paych_wechat_private_key"
	PayConfigWechatPlatformCert = "paych_wechat_platform_cert"
	PayConfigWechatNotifyURL    = "paych_wechat_notify_url"
)

// 支付宝当面付（#74 渠道凭据）配置键：私钥为敏感项（enc:v1: 密文落库、掩码回显），
// 其余为公开参数；键名统一 paych_ 前缀，与微信侧对称，见文件头说明。
const (
	PayConfigAlipayEnabled    = "paych_alipay_enabled"
	PayConfigAlipayAppID      = "paych_alipay_app_id"
	PayConfigAlipayPrivateKey = "paych_alipay_private_key" // 敏感
	PayConfigAlipayPublicKey  = "paych_alipay_public_key"
	PayConfigAlipaySellerID   = "paych_alipay_seller_id"
	PayConfigAlipayGateway    = "paych_alipay_gateway"
	PayConfigAlipayNotifyURL  = "paych_alipay_notify_url"
)

// PayConfigMask 敏感项在管理台的回显占位串。
// 必须含 "****"：保存链路用 secret.IsSecretMasked 判定「这是回显值而非用户新填值」，
// 从而回填库内旧密文（见 SetPayConfigField）。
const PayConfigMask = "********"

// PaySwitch 渠道开关三态。
// 为什么需要「未设置」这一态：存量部署只配了环境变量（甚至什么都没配），
// 若把「库里没有值」当成「关闭」，升级后所有走 env 的真实收款会集体 fail-closed；
// 反之把「未设置」当「开启」又会让只填了 AppID 的半成品配置被当作可用渠道。
// 因此未设置=不表态（由 pay_mode 与凭据齐全度决定），显式 0 才拦截下单。
type PaySwitch int

// 三态取值：逐态语义见上方 PaySwitch 注释（未设置不表态、显式 0 才拦截）。
const (
	PaySwitchUnset PaySwitch = iota // 库里无值：跟随支付模式，不额外拦截
	PaySwitchOn                     // 管理台显式启用
	PaySwitchOff                    // 管理台显式停用（下单 fail-closed）
)

// PayWechatCreds 微信支付（Native v3）库内凭据（值均为解密后的明文，仅在后内存中短暂存在）。
type PayWechatCreds struct {
	Switch       PaySwitch
	AppID        string
	MchID        string
	SerialNo     string
	APIv3Key     string // APIv3 密钥（32 字节，回调解密必需）
	PrivateKey   string // 商户 API 私钥（apiclient_key.pem 全文）
	PlatformCert string // 微信支付平台证书/公钥（当前回调解密走 APIv3 密钥，本项先存档备用）
	NotifyURL    string
}

// PayAlipayCreds 支付宝当面付库内凭据。
type PayAlipayCreds struct {
	Switch     PaySwitch
	AppID      string
	PrivateKey string // 应用私钥（RSA2 签名）
	PublicKey  string // 支付宝公钥（下单响应与回调验签；公钥本身非敏感，明文存储便于核对）
	SellerID   string
	Gateway    string
	NotifyURL  string
}

// PayChannelConfig 支付渠道配置快照（管理台回显与渠道初始化共用同一份读口径）。
type PayChannelConfig struct {
	NotifyBase string
	Wechat     PayWechatCreds
	Alipay     PayAlipayCreds
}

// payConfigField 单个可编辑字段的元数据：键名 + 是否敏感（敏感=加密落库+掩码回显）。
type payConfigField struct {
	key    string
	secret bool
}

// payConfigFields 字段白名单：SetPayConfigField/PayConfigFieldKey 只认这里登记的键。
// 收口成一张表的目的：新增字段时只需在此加一行，读写与掩码逻辑自动对齐，
// 不会出现「写路径加密了、读路径忘了解密」这类成对错误。
var payConfigFields = []payConfigField{
	{PayConfigNotifyBase, false},
	{PayConfigWechatEnabled, false},
	{PayConfigWechatAppID, false},
	{PayConfigWechatMchID, false},
	{PayConfigWechatSerialNo, false},
	{PayConfigWechatAPIv3Key, true},
	{PayConfigWechatPrivateKey, true},
	{PayConfigWechatPlatformCert, false},
	{PayConfigWechatNotifyURL, false},
	{PayConfigAlipayEnabled, false},
	{PayConfigAlipayAppID, false},
	{PayConfigAlipayPrivateKey, true},
	{PayConfigAlipayPublicKey, false},
	{PayConfigAlipaySellerID, false},
	{PayConfigAlipayGateway, false},
	{PayConfigAlipayNotifyURL, false},
}

// IsPayConfigKey 键名是否登记在字段白名单里（供 api 层在**写库之前**整体校验请求体）。
//
// WHY 单独导出判定：白名单只有 store 知道，而「未知键 → 400」必须在写入前完成——
// 否则一批字段写到第 N 个才发现 N+1 未登记，前面已落库的就成了半套凭据
// （微信配齐、支付宝缺私钥，渠道 fail-closed 且管理员看不出哪半坏了）。
func IsPayConfigKey(key string) bool {
	_, ok := payConfigFieldOf(key)
	return ok
}

// payConfigFieldOf 按键名查白名单；第二个返回值 false=未登记（拒绝写入，防任意键注入）。
func payConfigFieldOf(key string) (payConfigField, bool) {
	for _, f := range payConfigFields {
		if f.key == key {
			return f, true
		}
	}
	return payConfigField{}, false
}

// SetPayConfigField 写入一个支付渠道配置字段。
// 参数：key=白名单键名；raw=管理台提交的原始值。
//
// 三条写路径规则（都是为了避免「把管理台的回显值当真值落库」）：
//  1. 敏感项收到掩码串（含 ****）→ 直接跳过，保留库内旧密文；
//  2. raw 为空 → 删除该键（管理台「清空」语义，而不是留一个空值迷惑读路径）；
//  3. 其余敏感项 → EncryptSecret 后落库。
//
// 返回 error：键名不在白名单，或写库失败。
func (s *Store) SetPayConfigField(key, raw string) error {
	f, ok := payConfigFieldOf(key)
	if !ok {
		return &errTxt{"未知的支付渠道配置项: " + key}
	}
	v := strings.TrimSpace(raw)
	if f.secret && secret.IsSecretMasked(v) {
		return nil // 掩码=未改动：绝不把 "********" 写回库（否则真密钥被覆盖、渠道当场失效）
	}
	if v == "" {
		return s.deletePayConfigKey(f.key)
	}
	if f.secret {
		v = secret.EncryptSecret(v)
	}
	return s.SetConfig(f.key, v)
}

// deletePayConfigKey 删除配置键。
// 为什么不走通用 DeleteConfig：system_config 是共享 KV 表，本域只允许删自己登记过的键，
// 白名单校验在调用方（SetPayConfigField）已做完，这里只负责执行。
func (s *Store) deletePayConfigKey(key string) error {
	_, err := db.Exec(s.db, db.CurrentDialect(), "DELETE FROM system_config WHERE key=?", key)
	return err
}

// GetPayChannelConfig 读取支付渠道配置快照（敏感项解密）。
//
// 解密失败（典型原因：JWT_SECRET 轮换后旧密文不可解）按「未配置」处理并告警——
// 宁可 fail-closed 拒绝出单，也绝不能把一段密文当成私钥发给渠道（签名必错，
// 报错却发生在渠道侧，运维排查成本远高于此处一条日志）。
func (s *Store) GetPayChannelConfig() *PayChannelConfig {
	out := &PayChannelConfig{}
	out.NotifyBase = s.payConfigPlain(PayConfigNotifyBase)
	out.Wechat = PayWechatCreds{
		Switch:       paySwitchOf(s.payConfigPlain(PayConfigWechatEnabled)),
		AppID:        s.payConfigPlain(PayConfigWechatAppID),
		MchID:        s.payConfigPlain(PayConfigWechatMchID),
		SerialNo:     s.payConfigPlain(PayConfigWechatSerialNo),
		APIv3Key:     s.payConfigSecret(PayConfigWechatAPIv3Key),
		PrivateKey:   s.payConfigSecret(PayConfigWechatPrivateKey),
		PlatformCert: s.payConfigPlain(PayConfigWechatPlatformCert),
		NotifyURL:    s.payConfigPlain(PayConfigWechatNotifyURL),
	}
	out.Alipay = PayAlipayCreds{
		Switch:     paySwitchOf(s.payConfigPlain(PayConfigAlipayEnabled)),
		AppID:      s.payConfigPlain(PayConfigAlipayAppID),
		PrivateKey: s.payConfigSecret(PayConfigAlipayPrivateKey),
		PublicKey:  s.payConfigPlain(PayConfigAlipayPublicKey),
		SellerID:   s.payConfigPlain(PayConfigAlipaySellerID),
		Gateway:    s.payConfigPlain(PayConfigAlipayGateway),
		NotifyURL:  s.payConfigPlain(PayConfigAlipayNotifyURL),
	}
	return out
}

// payConfigPlain 读非敏感字段原值。
func (s *Store) payConfigPlain(key string) string {
	v, _ := s.GetConfig(key)
	return strings.TrimSpace(v)
}

// payConfigSecret 读敏感字段并解密；密文不可解时告警后按未配置处理。
func (s *Store) payConfigSecret(key string) string {
	stored := s.payConfigPlain(key)
	if stored == "" {
		return ""
	}
	plain := secret.DecryptSecret(stored)
	if plain == "" && strings.HasPrefix(stored, secret.SecretEncPrefix) {
		observability.Warn(context.Background(), "支付渠道密文配置解密失败（按未配置处理）", "key", key)
	}
	return strings.TrimSpace(plain)
}

// paySwitchOf 开关字符串 → 三态（"1"=启用，"0"=停用，其余含空=未设置）。
func paySwitchOf(v string) PaySwitch {
	switch strings.TrimSpace(v) {
	case "1":
		return PaySwitchOn
	case "0":
		return PaySwitchOff
	default:
		return PaySwitchUnset
	}
}

// PayConfigForEcho 管理台回显用的字段映射：非敏感项给原值，敏感项有值即给掩码。
// 与 SetPayConfigField 成对——回显什么形态，保存时就按同一规则识别「未改动」。
func (s *Store) PayConfigForEcho() map[string]string {
	plainOf := func(key string) string {
		f, ok := payConfigFieldOf(key)
		if !ok || f.secret {
			return ""
		}
		return s.payConfigPlain(key)
	}
	out := map[string]string{}
	for _, f := range payConfigFields {
		if !f.secret {
			out[f.key] = plainOf(f.key)
			continue
		}
		// 敏感项一律不回真值：有值即掩码，无值即空串（前端据此显示「未配置」）
		if v := s.payConfigPlain(f.key); v != "" {
			out[f.key] = PayConfigMask
		} else {
			out[f.key] = ""
		}
	}
	// 开关以三态字符串回显：未设置时给空串，前端显示「跟随支付模式」。
	// 这里刻意不复用 GetPayChannelConfig——回显不该触发一次全量解密（既费 CPU，
	// 也会在 JWT_SECRET 轮换时刷一串与本接口无关的告警）。
	out[PayConfigWechatEnabled] = paySwitchStr(paySwitchOf(s.payConfigPlain(PayConfigWechatEnabled)))
	out[PayConfigAlipayEnabled] = paySwitchStr(paySwitchOf(s.payConfigPlain(PayConfigAlipayEnabled)))
	return out
}

// paySwitchStr 三态 → 回显字符串（""=未设置）。
func paySwitchStr(v PaySwitch) string {
	switch v {
	case PaySwitchOn:
		return "1"
	case PaySwitchOff:
		return "0"
	default:
		return ""
	}
}
