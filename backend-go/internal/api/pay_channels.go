// ============ pay_channels.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 支付渠道凭据（微信 Native v3 / 支付宝当面付）的「有效配置」解析与管理台读写口。
//
// 背景（★ 2026-09-22）：此前商户参数只能靠环境变量注入，拿到微信/支付宝商户资质后
// 必须改部署配置并重启才能开始收款。本文件把它升级为「管理台可配 + 加密落库」，
// 同时保留环境变量通道并让它优先。
//
// 取值优先级：**环境变量 > 数据库（管理台）配置**，理由：
//   - env 由发布/运维掌控，是应急与灰度的最后闸门——线上密钥疑似泄露或管理台被误配时，
//     运维只要注入 env 重启即可压过库里的值，不必先进管理台救火；
//     若反过来让库优先，一次误保存就能把应急通道废掉。
//   - env 是「随发布声明」的，改动留痕在部署流水线；库配置是「运行期可改」的，
//     两者冲突时以更强的那一层（发布）为准，行为更可预期。
//   - 因此新部署只填管理台即可收款；已用 env 的存量部署升级后行为不变（env 继续压住库值）。
//
// fail-closed 语义不变：合并后的配置仍任一项缺失即由 payment 适配器拒绝出单，
// 绝不静默降级成 mock（「配置了却给出废码」比「不出码」更贵，见 #41 整改）。
//
// 接口（均需 super_admin，requireAdminUser 即等级 4）：
//   - GET  /api/admin/pay/channels       回显（敏感项为掩码，环境变量接管的字段单独标注）
//   - POST /api/admin/pay/channels/save  保存（掩码输入不落库；空串=清除该项）
// ========================================

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sort"
	"strings"

	apierrors "translator/internal/errors"
	"translator/internal/payment"
	"translator/internal/store"
)

// payConfigEnvKeys 配置键 → 对应环境变量名。
// 这张表是「env 优先」的唯一实现点：新增字段只在此加一行，
// 读取（payGatewayConfig）与回显标注（payConfigEnvOverridden）自动对齐，
// 不会出现「读了 env 却没告诉管理员 env 正在接管」的口径分叉。
var payConfigEnvKeys = map[string]string{
	store.PayConfigNotifyBase:         "PAY_NOTIFY_BASE",
	store.PayConfigWechatAppID:        "PAY_WECHAT_APP_ID",
	store.PayConfigWechatMchID:        "PAY_WECHAT_MCH_ID",
	store.PayConfigWechatSerialNo:     "PAY_WECHAT_SERIAL_NO",
	store.PayConfigWechatAPIv3Key:     "PAY_WECHAT_APIv3_KEY",
	store.PayConfigWechatPrivateKey:   "PAY_WECHAT_PRIVATE_KEY",
	store.PayConfigWechatPlatformCert: "PAY_WECHAT_PLATFORM_CERT",
	store.PayConfigWechatNotifyURL:    "PAY_WECHAT_NOTIFY_URL",
	store.PayConfigAlipayAppID:        "PAY_ALIPAY_APP_ID",
	store.PayConfigAlipayPrivateKey:   "PAY_ALIPAY_PRIVATE_KEY",
	store.PayConfigAlipayPublicKey:    "PAY_ALIPAY_PUBLIC_KEY",
	store.PayConfigAlipaySellerID:     "PAY_ALIPAY_SELLER_ID",
	store.PayConfigAlipayGateway:      "PAY_ALIPAY_GATEWAY",
	store.PayConfigAlipayNotifyURL:    "PAY_ALIPAY_NOTIFY_URL",
}

// payConfigValue 取某字段的生效值：环境变量非空则用之，否则用库（管理台）值。
func payConfigValue(key, dbVal string) string {
	if env, ok := payConfigEnvKeys[key]; ok {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return v
		}
	}
	return dbVal
}

// payGatewayConfig 按「当前有效配置」合成支付网关参数（env > 管理台库配置）。
// 供 payProviderFor（下单与回调验签两条链路）共用——两处必须同一份配置，
// 否则会出现「下单用 A 私钥签名、回调用 B 密钥解密」的验签死局。
func (s *Server) payGatewayConfig() *payment.Config {
	c := s.Store.GetPayChannelConfig()
	cfg := &payment.Config{
		NotifyBase: payConfigValue(store.PayConfigNotifyBase, c.NotifyBase),
		Wechat: payment.WechatConfig{
			AppID:      payConfigValue(store.PayConfigWechatAppID, c.Wechat.AppID),
			MchID:      payConfigValue(store.PayConfigWechatMchID, c.Wechat.MchID),
			SerialNo:   payConfigValue(store.PayConfigWechatSerialNo, c.Wechat.SerialNo),
			APIv3Key:   payConfigValue(store.PayConfigWechatAPIv3Key, c.Wechat.APIv3Key),
			PrivateKey: payConfigValue(store.PayConfigWechatPrivateKey, c.Wechat.PrivateKey),
			NotifyURL:  payConfigValue(store.PayConfigWechatNotifyURL, c.Wechat.NotifyURL),
			// 平台证书当前只随配置归档（回调解密走 APIv3 密钥），
			// 待切「平台证书验签」口径时在此接线，不必再动读配置这条路。
			PlatformCert: payConfigValue(store.PayConfigWechatPlatformCert, c.Wechat.PlatformCert),
			// 开关没有 env 对应项：它表达的是「运营是否允许该渠道出单」，
			// 历史上也从未有过这个环境变量，再造一个只会多出第二个真源。
			Disabled: c.Wechat.Switch == store.PaySwitchOff,
		},
		Alipay: payment.AlipayConfig{
			AppID:      payConfigValue(store.PayConfigAlipayAppID, c.Alipay.AppID),
			PrivateKey: payConfigValue(store.PayConfigAlipayPrivateKey, c.Alipay.PrivateKey),
			PublicKey:  payConfigValue(store.PayConfigAlipayPublicKey, c.Alipay.PublicKey),
			SellerID:   payConfigValue(store.PayConfigAlipaySellerID, c.Alipay.SellerID),
			Gateway:    payConfigValue(store.PayConfigAlipayGateway, c.Alipay.Gateway),
			NotifyURL:  payConfigValue(store.PayConfigAlipayNotifyURL, c.Alipay.NotifyURL),
			Disabled:   c.Alipay.Switch == store.PaySwitchOff,
		},
	}
	return cfg
}

// payConfigEnvOverridden 标注哪些字段正由环境变量提供（键 → 环境变量名），
// 管理台据此把该栏置灰并写明变量名，提示「改这里不生效」。
// 为什么要把这件事摆到界面上：env 压住库值是设计上的取舍（见文件头），
// 但对管理员而言，「保存成功却仍在用旧密钥」是极难自查的坑，必须显式告知。
func payConfigEnvOverridden() map[string]string {
	out := map[string]string{}
	for key, env := range payConfigEnvKeys {
		if strings.TrimSpace(os.Getenv(env)) != "" {
			out[key] = env
		}
	}
	return out
}

// handleAdminPayChannels GET /api/admin/pay/channels —— 回显支付渠道配置（敏感项掩码）。
// 返回: fields=配置键→回显值（敏感项为 "********"，未配置为空串）、
// env_overridden=配置键→接管该字段的环境变量名。
func (s *Server) handleAdminPayChannels(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdminUser(r); err != nil {
		// 鉴权分流（★ F-64③ 批 I-10 收尾）：未登录 401、等级不足 403，同一句错误不再两种码
		s.writeAuthzError(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"success":        true,
		"fields":         s.Store.PayConfigForEcho(),
		"env_overridden": payConfigEnvOverridden(),
	})
}

// handleAdminPayChannelsSave POST /api/admin/pay/channels/save —— 保存支付渠道配置（super_admin）。
// 请求 body: {"fields": {"paych_wechat_app_id": "wx123…", "paych_alipay_private_key": "****…", …}}
//
// 语义约定（与前端表单一次性提交全部字段配套）：
//   - 只处理本接口认识的键（store 白名单），未知键直接 400——防止该口变成任意配置写入器；
//   - 敏感项收到掩码串=「用户没动这一栏」，由 store 跳过写库保留旧密文（绝不要把掩码当真值存）；
//   - 值留空=清除该配置项（回到「未配置」，渠道重新走 fail-closed）。
//
// 校验先行、整批落库在后（同 C28 整改口径）：任一项非法即整次保存不落任何写，避免「半套凭据」。
func (s *Server) handleAdminPayChannelsSave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		// 与 GET 同口径分流（★ F-64③ 批 I-10 收尾）：未登录 401、等级不足 403
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		Fields map[string]string `json:"fields"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Fields) == 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求格式错误或 fields 为空"))
		return
	}
	// map 遍历顺序随机，校验前按键名排序，保证错误信息与审计轨迹的键序稳定可复现
	keys := make([]string, 0, len(req.Fields))
	for k := range req.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// ★ 未知键必须在这一步（写库之前）整体拒绝：SetPayConfigField 也会拒未知键，
	//   但那已在第二个循环里，前面的字段就落库了 —— 半套凭据比整单失败难查得多。
	for _, k := range keys {
		if !store.IsPayConfigKey(k) {
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "未知的支付渠道配置项: "+k))
			return
		}
	}
	for _, k := range keys {
		v := strings.TrimSpace(req.Fields[k])
		switch k {
		case store.PayConfigWechatEnabled, store.PayConfigAlipayEnabled:
			if v != "" && v != "0" && v != "1" {
				s.writeError(w, r, apierrors.New(apierrors.ErrValidation,
					k+` 仅支持 "0"/"1"/留空（跟随支付模式）`))
				return
			}
		case store.PayConfigNotifyBase, store.PayConfigWechatNotifyURL, store.PayConfigAlipayNotifyURL, store.PayConfigAlipayGateway:
			// 回调/网关地址必须是完整 URL：相对路径能存进来，但渠道永远调不到，
			// 表现为「配了却收不到款」——这类问题必须在保存时就拦住。
			// 敏感项的掩码回显值不参与 URL 校验（它不是用户新填的值）。
			if v != "" && !hasMask(v) && !strings.HasPrefix(v, "https://") && !strings.HasPrefix(v, "http://") {
				s.writeError(w, r, apierrors.New(apierrors.ErrValidation,
					k+" 需为完整地址（http:// 或 https:// 开头）"))
				return
			}
		}
	}
	var saved []string
	for _, k := range keys {
		if err := s.Store.SetPayConfigField(k, req.Fields[k]); err != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation, publicErrMessage(r.Context(), err)))
			return
		}
		saved = append(saved, k)
	}
	if len(saved) > 0 {
		s.Store.LogAudit(s.effTenant(r, u), u.ID, "pay_channels_save", "system", strings.Join(saved, ","))
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "saved": saved})
}

// payChannelDisabledErr 判断渠道下单错误是否为「管理台停用」，供文案分流复用。
func payChannelDisabledErr(err error) bool {
	return errors.Is(err, payment.ErrPayChannelDisabled)
}
