// ============ 本文件职责中文说明 ============
// S3 防薅·一次性/丢弃邮箱域名黑名单（2026-09-14）：
//
//	阻止用 throwaway 邮箱批量注册白嫖体验积分（注册礼包=唯一对外钩子，防薅优先级高）。
//
// 判定口径：取 @ 后域名，命中内置清单或 system_config `disposable_email_domains`
//
//	（逗号/换行分隔，超管可增补）即拒绝；内置清单为公开常见 temp-mail 服务商。
//
// 挂接点：发码 / 注册 / 换绑 / 忘记密码 四处（见 disposableEmailRejected 调用）。
package api

import (
	"strings"
)

// builtinDisposableDomains 常见一次性邮箱服务商域名（小写，精确域名匹配）。
var builtinDisposableDomains = []string{
	"mailinator.com", "mailinator.net", "guerrillamail.com", "guerrillamail.info",
	"sharklasers.com", "grr.la", "guerrillamailblock.com", "trashmail.com",
	"trashmail.de", "temp-mail.org", "temp-mail.io", "tempmail.com", "temp-mail.ru",
	"10minutemail.com", "20minutemail.com", "minutemail.com", "moakt.com", "moakt.co",
	"yopmail.com", "yopmail.fr", "yopmail.co", "throwawaymail.com", "fakemail.net",
	"getairmail.com", "dispostable.com", "maildrop.cc", "mailcatch.com", "inboxes.com",
	"spamgourmet.com", "mytemp.email", "tempr.email", "ronguay.org", "mailexpire.com",
	"fakeinbox.com", "guerrillamail.org", "mailmetrash.com", "trash2009.com", "spam4.me",
	"byom.de", "die-mail.com", "gustr.com", "harakirimail.com", "inboxbear.com",
	"maol.de", "mcv.nanosoft-int.com", "mt2009.com", "spamfree24.org", "tanantrum.mv",
	"teleworm.us", "tempinbox.com", "trashymail.com", "wegwerfmail.de", "wegwerfmail.net",
	"yopmail.it", "yopmail.nl", "10minutemail.net", "email-temp.com", "luxurymail.com",
	"getnada.com", "nada.email", "tempail.com", "discard.email", "discardmail.com",
	"spamdebris.com", "trashmail.me", "0-mail.com", "10minute-mail.org", "byke.ca",
}

// isDisposableEmail 判断邮箱域名是否落一次性黑名单（内置 + 运营可配）。
// 参数：email=待检邮箱；extraCfg=system_config disposable_email_domains 原文。
// 返回：true=一次性邮箱应拒。域名匹配大小写不敏感、去首尾空格。
func isDisposableEmail(email, extraCfg string) bool {
	i := strings.LastIndex(email, "@")
	if i < 0 || i == len(email)-1 {
		return false
	}
	dom := strings.ToLower(strings.TrimSpace(email[i+1:]))
	if dom == "" {
		return false
	}
	blocked := make(map[string]bool, len(builtinDisposableDomains))
	for _, d := range builtinDisposableDomains {
		blocked[d] = true
	}
	for _, d := range strings.FieldsFunc(extraCfg, func(r rune) bool { return r == ',' || r == '\n' || r == ';' || r == ' ' }) {
		if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
			blocked[d] = true
		}
	}
	if blocked[dom] {
		return true
	}
	// 子域命中：foo@mailinator.net 已在表内；*.temp-mail.io 之类按后缀通配内置域
	for d := range blocked {
		if strings.HasSuffix(dom, "."+d) {
			return true
		}
	}
	return false
}

// disposableEmailRejected 统一入口：读运营配置并判断，命中返回拒绝话术（否则空串）。
func (s *Server) disposableEmailRejected(email string) string {
	if !strings.Contains(email, "@") {
		return ""
	}
	extra, _ := s.Store.GetConfig("disposable_email_domains")
	if isDisposableEmail(strings.ToLower(strings.TrimSpace(email)), extra) {
		return "为保障活动额度公平，平台暂不支持一次性/临时邮箱注册，请使用常用邮箱"
	}
	return ""
}
