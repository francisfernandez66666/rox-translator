package api

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"translator/internal/knowledge"
	"translator/internal/knowledge/contract"
)

// w3FailTransport 直接返回连接失败，不依赖真网络（KN4 腿② HTTP 层夹具）。
type w3FailTransport struct{}

func (w3FailTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("dial failed")
}

// KN4 四档失败语义必须各自不同形（M0286 靶）。
// ① 影子期不可达 ⇒ 回落直读 + WARN；② 切读后不可达 ⇒ 503 + WARN（不许回落/假装空）；
// ③ 可达但未接管 ⇒ 503 + reason=not_owned_until_w5；④ 业务空 ⇒ 200 + 空数组。
// ★ ② 与 ④ 绝不合形：把不可达写成空结果就是 AGENTS §一·12 静默降级那一族。
func TestKN4FourFailureModesDistinct(t *testing.T) {
	o1 := knowledge.DecideReadOutcome(false, false, true, false) // ①
	if !(o1.HTTPStatus == http.StatusOK && o1.FallbackToDirect && o1.Warn) {
		t.Fatalf("KN4 腿① 影子回落档错: %+v", o1)
	}
	o2 := knowledge.DecideReadOutcome(true, false, true, false) // ②
	if !(o2.HTTPStatus == http.StatusServiceUnavailable && o2.Warn && !o2.FallbackToDirect && !o2.EmptyArray) {
		t.Fatalf("KN4 腿② 切读不可达必须 503: %+v", o2)
	}
	o3 := knowledge.DecideReadOutcome(true, true, false, false) // ③
	if !(o3.HTTPStatus == http.StatusServiceUnavailable && o3.Reason == contract.ReasonNotOwnedUntilW5 && !o3.EmptyArray) {
		t.Fatalf("KN4 腿③ 未接管档错: %+v", o3)
	}
	o4 := knowledge.DecideReadOutcome(true, true, true, true) // ④
	if !(o4.HTTPStatus == http.StatusOK && o4.EmptyArray) {
		t.Fatalf("KN4 腿④ 业务空档错: %+v", o4)
	}
	// 四档互不覆盖：①≠②（回落 vs 503）、②≠④（不可达 vs 空结果）。
	if reflect.DeepEqual(o1, o2) {
		t.Fatalf("KN4 腿①② 合形（回落与 503 必须不同形）")
	}
	if reflect.DeepEqual(o2, o4) {
		t.Fatalf("KN4 腿②④ 合形（不可达写成空结果 ⇒ M0286 在此红）")
	}
	// 反证判别力：构造「不可达被写成空结果」的假档，它必须与真业务空档同形（证明合形判据有射程）。
	bogus := knowledge.Outcome{HTTPStatus: http.StatusOK, EmptyArray: true}
	if !reflect.DeepEqual(bogus, o4) {
		t.Fatalf("KN4 判据失明：空结果档构造不对（比较器射程可疑）")
	}
}

// KN4 腿② 单独再验一次（M0316 靶）：走 Client.Call 的 HTTP 层，不复用上面的纯函数断言，
// 确保「切读后不可达必须 503」这条腿没有被四档综合测试覆盖掉。
func TestKN4CutoverUnavailableNeverSilent(t *testing.T) {
	c := knowledge.NewClient(knowledge.Config{
		BaseURL:     "http://127.0.0.1:1",
		Token:       "x",
		ServiceRead: true, // 切读档
	})
	c.HTTPClient = &http.Client{Transport: w3FailTransport{}, Timeout: time.Second}
	out, err := c.Call(context.Background(), contract.PathI18nList,
		contract.I18nListRequest{TenantID: 1}, &contract.I18nListResponse{})
	if err != nil {
		t.Fatalf("KN4 腿② Call 不该把不可达吞成普通错误: %v", err)
	}
	if out.HTTPStatus != http.StatusServiceUnavailable {
		t.Fatalf("KN4 腿② 切读不可达必须 503（不许回落/假装空）——M0316 在此红: %+v", out)
	}
	if out.FallbackToDirect || out.EmptyArray {
		t.Fatalf("KN4 腿② 不许回落直读或返回空结果: %+v", out)
	}
}
