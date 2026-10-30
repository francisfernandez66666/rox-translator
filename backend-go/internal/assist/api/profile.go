// ============ profile.go · 职责说明 ============
// 挂件访客画像的**assist 侧验签**（★ 2026-10-30 三事合一批 组7 §8.2，路 A′）。
//
// 只做三件事：① 只信任回环对端；② ±60s 时间窗；③ HMAC 等值（hmac.Equal）。
// 三条任一不过 ⇒ 返回 nil（匿名），**绝不返 4xx**（P-5：身份腿坏了不许把挂件整条打死）。
//
// 为什么还要判回环对端：
//
//	现网 assist 监听在 127.0.0.1，画像头只可能来自同机的 Caddy/主服务；一旦有人把端口
//	直接暴露出去（或本地起了个透传代理），签名就退化成「任何拿到密钥的人都能冒充」。
//	回环判定是纵深防御，不是身份来源——真正判身份的是签名本身。
//
// 失败语义（P-5）：缺头＝正常匿名（不出声）；**带头但验不过**＝出一次去重 WARN（不返错）。
// 去重按「档位」而不是按访客：否则一个错配的密钥会让每条对话都刷一行日志。
// =============================================
package api

import (
	"context"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"translator/internal/assist/profile"
	"translator/internal/observability"
	"translator/internal/secret"
)

// assistProfileKeyEnv / assistProfileKeyConfig 必须与主服务侧逐字一致
// （internal/api/assist_profile.go 的同名常量）。两处都写成了常量而不是引用：
// 主服务与 assist 是两个部署单元、不共享包；跨侧等值由 profile 包的契约锁与单测覆盖。
const (
	assistProfileKeyEnv    = "ASSIST_PROFILE_KEY"
	assistProfileKeyConfig = "assist_profile_key"
)

// profilePeerIsLoopback 直连对端是否回环。
// 用 net.SplitHostPort 而不是 httpx.ClientIP：后者会采纳 XFF，而这里问的是
// 「这一枪的 TCP 对端是谁」——代理头对身份判定没有价值（签名才是身份）。
func profilePeerIsLoopback(r *http.Request) bool {
	if r == nil {
		return false
	}
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// profileKey 读共享签名密钥（env > 自身 configs，库里是密文）。
// 注意：这是 assist 自己的 configs 表（与主服务的 system_config 不共享），
// 两侧必须由运维配成同一个值——配不上时的症状是「画像静默不生效」，见文件头。
func (s *Server) profileKey() []byte {
	if v := strings.TrimSpace(os.Getenv(assistProfileKeyEnv)); v != "" {
		return []byte(v)
	}
	if s == nil || s.db == nil {
		return nil
	}
	v := strings.TrimSpace(secret.DecryptSecret(strings.TrimSpace(s.db.GetConfig(assistProfileKeyConfig, ""))))
	if v == "" || secret.IsSecretMasked(v) {
		return nil
	}
	return []byte(v)
}

// warnProfileOnce 每个失败档位只出一条 WARN（进程生命周期内）。
// 只带档位，不带任何画像字段/头值（P-3：画像不进日志）。
func (s *Server) warnProfileOnce(ctx context.Context, reason string) {
	if s == nil {
		return
	}
	if _, loaded := s.profileWarn.LoadOrStore(reason, struct{}{}); loaded {
		return
	}
	observability.Warn(ctx, "assist.api 访客画像未通过校验，本轮按匿名处理",
		"reason", reason, "hint", "检查两侧 ASSIST_PROFILE_KEY 是否一致、时钟是否同步")
}

// visitorProfileFromRequest 验签并解出画像；任何失败都返回 nil（匿名）。
func (s *Server) visitorProfileFromRequest(r *http.Request) *profile.Visitor {
	if r == nil {
		return nil
	}
	payload := strings.TrimSpace(r.Header.Get(profile.HeaderProfile))
	ts := strings.TrimSpace(r.Header.Get(profile.HeaderTs))
	sig := strings.TrimSpace(r.Header.Get(profile.HeaderSig))
	if payload == "" && ts == "" && sig == "" {
		return nil // 正常匿名：不出声
	}
	if !profilePeerIsLoopback(r) {
		s.warnProfileOnce(r.Context(), "peer")
		return nil
	}
	key := s.profileKey()
	if len(key) == 0 {
		s.warnProfileOnce(r.Context(), "key")
		return nil
	}
	v, err := profile.Verify(key, payload, ts, sig, time.Now())
	if err != nil {
		reason := "mismatch"
		switch err {
		case profile.ErrBadTimestamp:
			reason = "expired"
		case profile.ErrBadPayload:
			reason = "payload"
		case profile.ErrNoKey:
			reason = "key"
		}
		s.warnProfileOnce(r.Context(), reason)
		return nil
	}
	return v
}
