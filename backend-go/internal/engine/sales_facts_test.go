// ============ sales_facts_test.go · 职责说明 ============
// 三期 W2：销售事实集中立契约层（编解码/验签/限长）的单元测试。
// 行为面由 api/assist_proxy_attribution_gate_test.go 与 assist/api/sales_facts_verify_test.go 锁；
// 本文件只锁契约层自己的四个错误档位与往返等值——两侧共用件漂了，症状是「静默不生效」。
// ================================================
package engine

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSalesFactsRoundtrip(t *testing.T) {
	set := SalesFactsSet{TenantID: 7, Lang: "en",
		Facts:      []SalesFact{{Kind: "spec", Key: "warranty", Value: "30 days", Lang: "en"}},
		Controlled: []ControlledKey{{Kind: "price", Key: "quote"}}}
	payload, err := EncodeSalesFacts(set)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	key := []byte("k")
	ts := time.Now().Unix()
	got, err := VerifySalesFacts(key, payload, itoa(ts), SignSalesFacts(key, payload, ts), time.Now())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.TenantID != 7 || len(got.Facts) != 1 || got.Facts[0].Value != "30 days" || len(got.Controlled) != 1 {
		t.Fatalf("往返不等值：%+v", got)
	}
}

func TestSalesFactsVerifyErrorTaxonomy(t *testing.T) {
	key := []byte("k")
	set := SalesFactsSet{TenantID: 1, Lang: "zh"}
	payload, _ := EncodeSalesFacts(set)
	ts := time.Now().Unix()
	sig := SignSalesFacts(key, payload, ts)
	cases := []struct {
		name    string
		key     []byte
		payload string
		ts      string
		sig     string
		want    error
	}{
		{"未配密钥", nil, payload, itoa(ts), sig, ErrSalesFactsNoKey},
		{"时间戳非数", key, payload, "abc", sig, ErrSalesFactsTimestamp},
		{"过期", key, payload, itoa(ts - 3600), SignSalesFacts(key, payload, ts-3600), ErrSalesFactsTimestamp},
		{"错签", key, payload, itoa(ts), "deadbeef", ErrSalesFactsSignature},
		{"负载非 base64", key, "!!!", itoa(ts), SignSalesFacts(key, "!!!", ts), ErrSalesFactsPayload},
	}
	for _, c := range cases {
		_, err := VerifySalesFacts(c.key, c.payload, c.ts, c.sig, time.Now())
		if err != c.want {
			t.Fatalf("%s：档位漂移 want=%v got=%v", c.name, c.want, err)
		}
	}
}

func TestSalesFactsLimitKeepsOrderAndCopies(t *testing.T) {
	set := SalesFactsSet{TenantID: 1, Lang: "zh"}
	for i := 0; i < 5; i++ {
		set.Facts = append(set.Facts, SalesFact{Kind: "spec", Key: string(rune('a' + i)), Value: "v", Lang: "zh"})
	}
	out := LimitSalesFacts(set)
	if out.Truncated || len(out.Facts) != 5 {
		t.Fatalf("限内集合不许动：truncated=%v n=%d", out.Truncated, len(out.Facts))
	}
	out.Facts[0].Value = "mutated"
	if set.Facts[0].Value != "v" {
		t.Fatal("LimitSalesFacts 必须拷贝，不许改原集（调用方下一轮还要用）")
	}
	big := SalesFactsSet{TenantID: 1, Lang: "zh", Facts: []SalesFact{
		{Kind: "spec", Key: "a", Value: strings.Repeat("x", MaxSalesFactsBytes/2), Lang: "zh"},
		{Kind: "spec", Key: "b", Value: strings.Repeat("y", MaxSalesFactsBytes/2), Lang: "zh"},
		{Kind: "spec", Key: "c", Value: "z", Lang: "zh"},
	}}
	out2 := LimitSalesFacts(big)
	if !out2.Truncated {
		t.Fatal("超字节必须置 truncated")
	}
	if len(out2.Facts) > 0 && out2.Facts[0].Key != "a" {
		t.Fatal("截断必须保前丢后（priority 序由读口负责，本层不重排）")
	}
}

// itoa 测试内小助手：unix 秒 → 十进制串（与生产头口径一致）。
func itoa(v int64) string { return strconv.FormatInt(v, 10) }
