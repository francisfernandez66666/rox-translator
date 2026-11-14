package knowledge

import (
	"context"
	"net/http"
	"testing"
	"time"

	"translator/internal/knowledge/contract"
)

// knHangingTransport 永不返回，直到请求 ctx 被超时预算取消（验证预算真接在 HTTP 层，不是常量摆设）。
type knHangingTransport struct{}

func (knHangingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	<-req.Context().Done()
	return nil, req.Context().Err()
}

// KN7 延迟预算：常量现读、>0、且真被 Call 用上。
// ★ 把预算调成 0（M0289）⇒ 第一条断言当场红（证明判据有射程，不是写死分子）。
func TestKN7BudgetPositiveAndEnforced(t *testing.T) {
	if RequestBudget <= 0 {
		t.Fatalf("KN7 超时预算必须 >0（把预算调成 0 ⇒ M0289 在此红）")
	}
	if RequestBudget > 5*time.Second {
		t.Fatalf("KN7 预算异常过大: %v", RequestBudget)
	}
	c := &Client{
		BaseURL:     "http://127.0.0.1:1",
		ServiceRead: false, // 影子期：不可达应回落直读
		HTTPClient:  &http.Client{Transport: knHangingTransport{}, Timeout: RequestBudget},
		DirectRead: func(context.Context, string, any) (any, error) { return nil, nil },
	}
	start := time.Now()
	o, err := c.Call(context.Background(), contract.PathI18nList,
		contract.I18nListRequest{TenantID: 1}, &contract.I18nListResponse{})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("KN7 Call 不该把超时吞成硬错误: %v", err)
	}
	if !o.FallbackToDirect {
		t.Fatalf("KN7 预算到期应落到影子回落档: %+v", o)
	}
	if elapsed > RequestBudget+500*time.Millisecond {
		t.Fatalf("KN7 超时预算没生效：耗时 %v 远超 %v", elapsed, RequestBudget)
	}
}
