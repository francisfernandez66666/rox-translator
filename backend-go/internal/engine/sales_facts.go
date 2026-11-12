// ============ sales_facts.go · 职责说明 ============
// 销售事实集的**跨进程载体**（三期 W2，§0.4 三段链路的中立契约层）。
//
// 为什么住在 internal/engine 而不是 api 或 assist：
//
//	主服务负责「按准入读口组装 + 签名」，assist 负责「验签 + 注入 ctx + 出站守卫消费」。
//	两侧对字段名、签名原文（base64(JSON)+"."+ts）、时间窗、限长四个口径必须逐字一致——
//	各写一份的那一天，症状不是报错，而是「事实集静默不生效」（验签恒失败 ⇒ fail-closed 回避）。
//	所以结构表 / 编解码 / 验签判据只此一处。本包**无 store 依赖**（不碰数据库），主服务与 assist 共用。
//
// 三条硬口径（与 §0.4 事实准入同源）：
//  1. **受控键物理上不带 value**：ControlledKey 结构只有 Kind/Key 两个字段，gated/unverified/locked
//     的事实值在类型层就进不来——不靠「记得过滤」（A13b 的结构性保证）。
//  2. **限长**：事实集 ≤200 条且 ≤32 KB；超限按 priority 截断并置 Truncated，未进集的事实按「无判据」处理。
//  3. **签名复用画像那把 HMAC**（profile.Sign/Canonical/MaxClockSkew），不再造第二套签名实现；
//     头名独立（X-LangCross-Sales-Facts*），与画像头互不覆盖。
//
// HMAC 密钥与画像共用 ASSIST_PROFILE_KEY（两侧运维配成同值）；未配密钥 ⇒ 主服务不发事实头，
// assist 侧无头 ⇒ 空事实集 ⇒ 由 W4 守卫 fail-closed 回避（不是放行）。
// ================================================
package engine

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"translator/internal/assist/profile"
)

// 事实集内部头（主服务出站自赋，assist 只认回环对端 + 正确签名；入站同名头由转发层先删）。
const (
	SalesFactsHeader        = "X-LangCross-Sales-Facts"
	SalesFactsTsHeader      = "X-LangCross-Sales-Facts-Ts"
	SalesFactsSigHeader     = "X-LangCross-Sales-Facts-Sig"
	SalesFactsVersionHeader = "X-LangCross-Facts-Version"
)

// SalesFactsVersion 协议版本位（§7.2 灰度）：assist 不认的版本按无头处理，绝不放行。
const SalesFactsVersion = "1"

// 事实集限长（§0.4 安全边界）：超限截断并置 Truncated。
const (
	MaxSalesFacts      = 200
	MaxSalesFactsBytes = 32 << 10
)

// SalesFact 一条**准入**事实（status='confirmed' AND visibility='public' AND locked=0）的译文值。
// 字段名即跨进程契约，改名前先核两侧消费方。
type SalesFact struct {
	Kind  string `json:"kind"`
	Key   string `json:"key"`
	Value string `json:"value"`
	Lang  string `json:"lang"`
}

// ControlledKey 一条**受控**事实（unverified/gated/locked）的主题键。
// ★ 刻意只有 Kind/Key、没有 Value 字段：受控值在类型层就进不了 prompt（A13b 结构性保证）。
// 模型输出触及这些键 ⇒ W4 守卫直接走回避。
type ControlledKey struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
}

// SalesFactsSet 一轮接待下发给 assist 的事实集。
type SalesFactsSet struct {
	TenantID   int64           `json:"tenant_id"`
	ProductID  int64           `json:"product_id,omitempty"`
	Lang       string          `json:"lang"`
	Facts      []SalesFact     `json:"facts"`
	Controlled []ControlledKey `json:"controlled,omitempty"`
	Truncated  bool            `json:"truncated,omitempty"`
}

// salesFactsCtxKey 事实集上下文的私有键（跨包只经下面两个导出函数存取）。
type salesFactsCtxKey struct{}

// WithSalesFacts 向 ctx 注入销售事实集，供 assist 出站咽喉处的守卫消费。
// ★ 未注入 ⇒ 守卫按「无事实头」fail-closed 回避（W4），既有翻译/客服链零改动。
// 参数 ctx=上下文；set=事实集；返回派生 ctx。
func WithSalesFacts(ctx context.Context, set SalesFactsSet) context.Context {
	return context.WithValue(ctx, salesFactsCtxKey{}, set)
}

// SalesFactsFromContext 取销售事实集；未注入返回 ok=false。
// 参数 ctx=上下文；返回事实集、是否注入。
func SalesFactsFromContext(ctx context.Context) (SalesFactsSet, bool) {
	v, ok := ctx.Value(salesFactsCtxKey{}).(SalesFactsSet)
	return v, ok
}

// EncodeSalesFacts 把事实集序列化成 base64(JSON)（与签名入参同一形态）。
// 参数 set=事实集；返回 base64 负载与错误。
func EncodeSalesFacts(set SalesFactsSet) (string, error) {
	b, err := json.Marshal(set)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// DecodeSalesFacts 把 base64(JSON) 解回事实集（不做验签，验签走 VerifySalesFacts）。
// 参数 payloadB64=base64 负载；返回事实集与错误。
func DecodeSalesFacts(payloadB64 string) (SalesFactsSet, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payloadB64))
	if err != nil {
		return SalesFactsSet{}, ErrSalesFactsPayload
	}
	var set SalesFactsSet
	if err := json.Unmarshal(raw, &set); err != nil {
		return SalesFactsSet{}, ErrSalesFactsPayload
	}
	return set, nil
}

// LimitSalesFacts 把事实集压进限长（≤200 条且 JSON 序列化后 ≤32 KB），超限从尾部截断并置 Truncated。
// 截断方向＝保前丢后：准入读口（W4 ListSalesFactsForInjection）负责按 priority 排序下发，
// 本函数只认 slice 顺序，不自己重排（两侧共用同一把尺，重排口径各写一份必然漂移）。
// 参数 set=原始事实集；返回限长后的事实集（原集不被修改）。
func LimitSalesFacts(set SalesFactsSet) SalesFactsSet {
	out := set
	if len(out.Facts) > MaxSalesFacts {
		out.Facts = append([]SalesFact(nil), out.Facts[:MaxSalesFacts]...)
		out.Truncated = true
	} else if len(out.Facts) > 0 {
		out.Facts = append([]SalesFact(nil), out.Facts...)
	}
	for len(out.Facts) > 0 {
		b, err := json.Marshal(out)
		if err == nil && len(b) <= MaxSalesFactsBytes {
			break
		}
		out.Facts = out.Facts[:len(out.Facts)-1]
		out.Truncated = true
	}
	return out
}

// 验签失败的四个内部档位（调用方据此 fail-closed 回避 + 去重 WARN，**不返 4xx**）。
var (
	ErrSalesFactsNoKey     = errors.New("sales facts key not configured")
	ErrSalesFactsTimestamp = errors.New("sales facts timestamp invalid")
	ErrSalesFactsSignature = errors.New("sales facts signature mismatch")
	ErrSalesFactsPayload   = errors.New("sales facts payload invalid")
)

// SignSalesFacts 生成事实集签名（薄委托画像包同一实现，主服务出站与 assist 验签共用）。
// 参数 key=共享密钥；payloadB64=base64 负载；ts= unix 秒；返回 hex 签名。
func SignSalesFacts(key []byte, payloadB64 string, ts int64) string {
	return profile.Sign(key, payloadB64, ts)
}

// VerifySalesFacts 验签并解出事实集；任一档不过返回 error，调用方据此 fail-closed（绝不放行）。
// 签名原文 / 时间窗复用画像包同一实现（profile.Canonical/Sign/MaxClockSkew），不另造第二套。
// 参数 key=共享密钥；payloadB64/tsStr/sig=三个头；now=当前时刻；返回事实集与错误。
func VerifySalesFacts(key []byte, payloadB64, tsStr, sig string, now time.Time) (SalesFactsSet, error) {
	if len(key) == 0 {
		return SalesFactsSet{}, ErrSalesFactsNoKey
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(tsStr), 10, 64)
	if err != nil {
		return SalesFactsSet{}, ErrSalesFactsTimestamp
	}
	skew := now.Unix() - ts
	if skew < 0 {
		skew = -skew
	}
	if skew > int64(profile.MaxClockSkew/time.Second) {
		return SalesFactsSet{}, ErrSalesFactsTimestamp
	}
	want := profile.Sign(key, payloadB64, ts)
	if !hmac.Equal([]byte(strings.ToLower(strings.TrimSpace(sig))), []byte(want)) {
		return SalesFactsSet{}, ErrSalesFactsSignature
	}
	return DecodeSalesFacts(payloadB64)
}
