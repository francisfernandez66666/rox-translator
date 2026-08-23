// ============ 本文件职责中文说明 ============
// 四期数据层单元测试：句包发放折算 token（句包外壳→token 底层兑换点）、
// 用户反馈 CRUD 与每日限流计数。
package store

import (
	"testing"
)

// TestGrantPackageChargesTokens 发放 100 句包应同时充入 100×换算率 token；句数镜像照常记录。
func TestGrantPackageChargesTokens(t *testing.T) {
	s := newTestStoreWithTenants(t)
	if err := s.SetConfig("estimate_tokens_per_sentence", "500"); err != nil {
		t.Fatal(err)
	}
	pkg := &Package{Code: "p_month", Name: "包月包", PType: PackagePaid, Sentences: 100, PriceMoney: 99, DurationDays: 30, Enabled: 1}
	if _, err := s.CreatePackage(pkg); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GrantPackageSentences(1, pkg); err != nil {
		t.Fatal(err)
	}
	bal, err := s.GetBalance(1)
	if err != nil {
		t.Fatal(err)
	}
	if bal.Balance != 100*500 {
		t.Fatalf("发放 100 句应折算充入 50000 token，实得 %d", bal.Balance)
	}
	perms, _ := s.GetTenantPerms(1)
	if perms.SentenceBalance != 100 {
		t.Fatalf("句数镜像应为 100，实得 %d", perms.SentenceBalance)
	}
	// 增量包：追加 token，不动订阅状态
	inc := &Package{Code: "inc_1", Name: "增量包", PType: PackageIncrement, Sentences: 10, PriceMoney: 9, Enabled: 1}
	if _, err := s.CreatePackage(inc); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GrantPackageSentences(1, inc); err != nil {
		t.Fatal(err)
	}
	bal, _ = s.GetBalance(1)
	if bal.Balance != (100+10)*500 {
		t.Fatalf("增量包应追加 5000 token，实得 %d", bal.Balance)
	}
}

// TestTokenSentenceRateDefault 未配置时换算率默认 500。
func TestTokenSentenceRateDefault(t *testing.T) {
	s := newTestStoreWithTenants(t)
	if r := s.TokenSentenceRate(); r != 500 {
		t.Fatalf("默认换算率应为 500，实得 %d", r)
	}
}

// TestFeedbackCreateAndResolve 反馈创建/列表/处理全链路。
func TestFeedbackCreateAndResolve(t *testing.T) {
	s := newTestStoreWithTenants(t)
	f := &Feedback{TenantID: 1, UserID: 2, TargetType: "text", Content: "术语翻错了", WithContext: true, SourceText: "制动系统"}
	if err := s.CreateFeedback(f); err != nil {
		t.Fatal(err)
	}
	if f.ID <= 0 || f.Status != "open" {
		t.Fatalf("反馈应成功创建且初始 open")
	}
	list, err := s.ListFeedbacks("")
	if err != nil || len(list) != 1 || !list[0].WithContext {
		t.Fatalf("列表查询失败或上下文标记丢失")
	}
	if err := s.ResolveFeedback(f.ID, "已修正术语"); err != nil {
		t.Fatal(err)
	}
	list, _ = s.ListFeedbacks("resolved")
	if len(list) != 1 || list[0].HandleNote != "已修正术语" {
		t.Fatalf("处理后状态/备注不符")
	}
	if list, _ = s.ListFeedbacks("open"); len(list) != 0 {
		t.Fatalf("处理后 open 列表应为空")
	}
}

// TestCountFeedbacksToday 每日限流计数随新增递增。
func TestCountFeedbacksToday(t *testing.T) {
	s := newTestStoreWithTenants(t)
	for i := 0; i < 3; i++ {
		_ = s.CreateFeedback(&Feedback{TenantID: 1, UserID: 2, TargetType: "text", Content: "x"})
	}
	if n := s.CountFeedbacksToday(2); n != 3 {
		t.Fatalf("当日计数应为 3，实得 %d", n)
	}
	if n := s.CountFeedbacksToday(99); n != 0 {
		t.Fatalf("其他用户计数应为 0")
	}
}
