// ============ user_import_f54_test.go · 职责说明 ============
// 2026-09-26 发布前 UAT 修复批 I-6 的 F-54 回归断言（user_import.go 邮箱两道闸 + 回执口径）。
//
// 本轮实跑复现的事故（证据：UAT报告_20260926.md F-54 / 证据/实测笔记.md R6）：
// 管理员下载「批量导入」模板后**一个字都不改**直接上传，系统照样建号成功——
// 示例行的邮箱是 example.com（RFC 2606 永不投递的保留域），于是生产库里多出一个
// zhangsan 账号，并向一个不存在的地址「寄」出含初始密码的邮件（回执还写着已通过邮件通知）。
// 旧实现只有两层缺失：① 邮箱列不过任何格式/语义校验（任意字符串照收，SetUserEmail 成功、
// 发信静默失败）；② 回执以「Send 无错」为成功口径（enqueueMail 入队即 return nil）。
//
// 锁清单：
//
//	F54-A) importEmailRejection 表锁（格式闸＋保留域闸）：留空放行、真域放行、
//	       无点/双 @ 等假邮箱走「格式」文案、example.*／.invalid／.test／.localhost 走「永不送达」文案；
//	       ★ 反向必锁 myexample.com / invalid-domain.com 不得被误杀（整段匹配 vs 前缀匹配的分水岭）；
//	F54-B) 模板原样上传全链锁：created=0、失败回执点名「保留域」，且**库里不得出现示例账号**
//	       （这条就是 zhangsan 事故的复现锁）；附带修复文档要求的正则锁——
//	       模板示例邮箱不得命中 @example\.(com|org|net|edu)$；
//	F54-C) 拒差不连坐：一份表里好行/保留域行/畸形行/留空行混排，合法行照常建号，
//	       非法行各自拿到自己的原因（防止把闸做成「整单报废」的另一种事故）；
//	F54-D) 通道在线（队列可用）态下的 handler 回执＝「已提交发送，稍后送达」，
//	       负向不得出现完成时「已通过邮件通知」；并以假队列计数证明「入队次数＝建号成功数」的
//	       受理语义，且被邮箱闸拒收的行一次都不占发信。
//
// 方言：自钉 SQLite 内存库（AGENTS.md §一·4），探针复用 user_import_f23_test.go 的
// newF23ImportProbe / writeImportXlsxFromRows / callUserBulkImportXlsx。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestF54
// =============================================
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"translator/internal/queue"
	"translator/internal/service"
)

// exampleDomainInRow 修复文档要求的正则锁判据：可投递的示例域形态。
var exampleDomainInRow = regexp.MustCompile(`@example\.(com|org|net|edu)$`)

// TestF54_ImportEmailRejectionTable 锁 F54-A：邮箱两道闸的逐例等值。
// 判据分两族：格式族必须说「格式不合法」，保留域族必须说「永远不会送达」——
// 管理员看到后者才知道「不是打错了，是这个地址根本不存在」，才会去删示例行而不是改大小写。
func TestF54_ImportEmailRejectionTable(t *testing.T) {
	cases := []struct {
		email string
		want  string // ""=放行；否则回执必须包含的关键词
	}{
		// —— 放行侧（留空是合法形态：不绑邮箱、走线下转告初始密码）——
		{"", ""},
		{"   ", ""},
		{"imp1@acme-corp.com", ""},
		{"Imp1@ACME-CORP.com", ""},
		{"ops@lexicorn.cn", ""},
		// ★ 反向误杀锁：若实现写成 HasSuffix("example.com")，这些真域会被一起拒掉
		{"a@myexample.com", ""},
		{"a@notexample.org", ""},
		{"a@mail.invalid-domain.com", ""},
		{"a@contest.test.com", ""}, // .test 只在**末段**才算保留域
		// —— 格式族（旧实现全收，SetUserEmail 成功、发信静默失败，管理员看不到异常）——
		{"zhangsan@examplecom", "格式不合法"}, // 无点
		{"abc@@x.com", "格式不合法"},          // 双 @
		{"plain-address-no-at", "格式不合法"}, // 无 @
		{"a b@x.com", "格式不合法"},           // 含空格
		{"a@x.", "格式不合法"},                // 点后为空
		// —— 保留域族（RFC 2606 + localhost：邮件永远不会出门，等于把初始密码丢进黑洞）——
		{"a@example.com", "永远不会送达"},
		{"A@EXAMPLE.COM", "永远不会送达"}, // 大小写归一后才比较
		{"a@exAmple.Net", "永远不会送达"},
		{"a@example.org", "永远不会送达"},
		{"a@example.edu", "永远不会送达"},
		{"sample@example.invalid", "永远不会送达"}, // ★ 现模板示例行就落在这里
		{"a@anyhost.invalid", "永远不会送达"},
		{"a@mail.test", "永远不会送达"},
		{"a@foo.localhost", "永远不会送达"},
	}
	for _, c := range cases {
		got := importEmailRejection(c.email)
		if c.want == "" {
			if got != "" {
				t.Errorf("邮箱 %q 应放行，实被拒: %q（误杀真域＝管理员只能线下改表）", c.email, got)
			}
			continue
		}
		if got == "" {
			t.Errorf("邮箱 %q 应拒收（原因含 %q），实得放行", c.email, c.want)
			continue
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("邮箱 %q 的拒收原因须含 %q，实得 %q", c.email, c.want, got)
		}
		// 两族文案不得串台：格式问题不该被说成保留域，反之亦然
		if (c.want == "格式不合法") != strings.HasPrefix(got, "邮箱格式不合法") {
			t.Errorf("邮箱 %q 拒收文案族别错位（期望 %q 族）: %q", c.email, c.want, got)
		}
	}
}

// TestF54_TemplateSampleRowIsNotDeliverable 锁 F54-B 的读侧一半：
// 模板示例行的邮箱列**必须不是**可投递地址（.invalid 保留域），且必被邮箱闸拒收。
// 这条与下一条全链锁互为因果：示例行既要「一眼看出是占位」，
// 又要「就算管理员没看出来、原样上传也建不出号」。
func TestF54_TemplateSampleRowIsNotDeliverable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tpl_f54.xlsx")
	f := buildUserImportTemplate()
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("模板落盘失败: %v", err)
	}
	sheet := f.GetSheetName(0)
	email, err := f.GetCellValue(sheet, "E2")
	if err != nil {
		t.Fatalf("读模板示例行邮箱列失败: %v", err)
	}
	email = strings.TrimSpace(email)
	if email == "" {
		t.Fatalf("模板示例行应带邮箱列示例（教会管理员这一列填什么）")
	}
	if exampleDomainInRow.MatchString(strings.ToLower(email)) {
		t.Fatalf("模板示例邮箱又回到可投递的 example.* 形态（%q）——旧事故复发：原样上传即建号并寄出含密码的邮件", email)
	}
	if reason := importEmailRejection(email); reason == "" {
		t.Fatalf("模板示例邮箱 %q 必须被邮箱闸拒收（否则「不改一字直接上传」仍会建号）", email)
	} else if !strings.Contains(reason, "永远不会送达") {
		t.Fatalf("模板示例邮箱 %q 应命中保留域文案，实得 %q", email, reason)
	}
	// 填写说明（F2）必须同时讲清「示例行要删」与「保留域会被拒收」，否则闸成了哑谜
	note, err := f.GetCellValue(sheet, "F2")
	if err != nil {
		t.Fatalf("读模板填写说明失败: %v", err)
	}
	if !strings.Contains(note, "示例行") || !strings.Contains(note, "保留域") {
		t.Fatalf("模板填写说明须预告「删除示例行 + 保留域会被拒收」，实得 %q", note)
	}
}

// f54ImportResp 批量导入回执解码结构。
type f54ImportResp struct {
	Success bool `json:"success"`
	Created int  `json:"created"`
	Failed  int  `json:"failed"`
	Total   int  `json:"total"`
	Results []struct {
		Username string `json:"username"`
		OK       bool   `json:"ok"`
		Message  string `json:"message"`
	} `json:"results"`
}

// templateRowUsernames 从模板工作簿读出示例行的用户名列（供全链锁做落库负证据）。
func templateRowUsernames(t *testing.T, path string) []string {
	t.Helper()
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	rows, err := f.GetRows(f.GetSheetName(0))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for i, r := range rows {
		if i == 0 || len(r) == 0 {
			continue // 表头行不算示例
		}
		if un := strings.TrimSpace(r[0]); un != "" {
			out = append(out, un)
		}
	}
	if len(out) == 0 {
		t.Fatalf("模板应至少含 1 行示例: %+v", rows)
	}
	return out
}

// TestF54_UnmodifiedTemplateCreatesNothing 锁 F54-B 的全链一半（★ zhangsan 事故复现锁）：
// 把**未改一字的官方模板**当 Excel 上传 → 200 但 created=0，
// 失败回执点名保留域且库里一行账号都不产生。
// 旧实现在此必红（created=1 + 「已通过邮件通知」）；闸被删掉同样必红。
func TestF54_UnmodifiedTemplateCreatesNothing(t *testing.T) {
	s, tok := newF23ImportProbe(t)
	t.Setenv("MAIL_ENABLED", "") // 离线态：本锁只判「建没建号」，不依赖通道
	tplPath := filepath.Join(t.TempDir(), "template.xlsx")
	if err := buildUserImportTemplate().SaveAs(tplPath); err != nil {
		t.Fatalf("模板落盘失败: %v", err)
	}
	rec := callUserBulkImportXlsx(t, s, tok, tplPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("批量导入应 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
	var out f54ImportResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || !out.Success {
		t.Fatalf("导入响应解码失败: %v (%s)", err, rec.Body.String())
	}
	if out.Created != 0 || out.Failed != 1 || out.Total != 1 {
		t.Fatalf("未改一字的模板不得建号：created=%d failed=%d total=%d（%s）", out.Created, out.Failed, out.Total, rec.Body.String())
	}
	if len(out.Results) != 1 || out.Results[0].OK {
		t.Fatalf("应恰好 1 行失败回执: %s", rec.Body.String())
	}
	if msg := out.Results[0].Message; !strings.Contains(msg, "保留域") || !strings.Contains(msg, "永远不会送达") {
		t.Fatalf("失败回执须点明「示例保留域、邮件永远不会送达」，实得 %q", msg)
	}
	// 落库侧负证据：示例账号不得存在（GetUserByUsernameGlobal 返回同名切片，无则空）
	for _, un := range templateRowUsernames(t, tplPath) {
		got, err := s.Store.GetUserByUsernameGlobal(un)
		if err != nil {
			t.Fatalf("查示例账号失败: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("示例行 %q 竟然建出了账号（事故复发）", un)
		}
	}
}

// TestF54_RejectionDoesNotPoisonBatch 锁 F54-C：拒收按行生效、不连坐。
// 一份表里 4 行：真域（应建号）／example.com（保留域）／bob@@x.com（畸形）／留空邮箱（应建号）。
// 要求 created=2、failed=2，且两个失败行各自拿到自己那一族的原因。
// 防的是另一种事故：闸做成整单抛错，管理员改一个邮箱就要重传全表。
func TestF54_RejectionDoesNotPoisonBatch(t *testing.T) {
	s, tok := newF23ImportProbe(t)
	t.Setenv("MAIL_ENABLED", "")
	xlsxPath := filepath.Join(t.TempDir(), "mixed.xlsx")
	writeImportXlsxFromRows(t, xlsxPath, [][]string{
		{"用户名称", "姓名", "部门", "角色", "邮箱"},
		{"f54ok1", "真邮箱一", "", "普通用户", "f54ok1@acme-corp.com"},
		{"f54bad1", "示例域二", "", "", "f54bad1@example.com"},
		{"f54bad2", "畸形三", "", "", "bob@@x.com"},
		{"f54ok2", "留空四", "", "", ""},
	})
	rec := callUserBulkImportXlsx(t, s, tok, xlsxPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("应 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
	var out f54ImportResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || !out.Success {
		t.Fatalf("解码失败: %v (%s)", err, rec.Body.String())
	}
	if out.Created != 2 || out.Failed != 2 || out.Total != 4 {
		t.Fatalf("合法行不得被连坐：created=%d failed=%d total=%d（%s）", out.Created, out.Failed, out.Total, rec.Body.String())
	}
	byName := map[string]struct {
		ok  bool
		msg string
	}{}
	for _, rr := range out.Results {
		byName[rr.Username] = struct {
			ok  bool
			msg string
		}{rr.OK, rr.Message}
	}
	if len(byName) != 4 {
		t.Fatalf("四行都该有回执，实得 %d 行: %s", len(out.Results), rec.Body.String())
	}
	for _, un := range []string{"f54ok1", "f54ok2"} {
		rr, e := byName[un]
		if !e || !rr.ok {
			t.Fatalf("合法行 %s 应建号成功，实得 %+v", un, rr)
		}
		// 离线态（Noop 通道）→ 两行都必须走线下转告版
		if !strings.Contains(rr.msg, "线下转告") {
			t.Fatalf("离线态下 %s 回执须走线下转告文案: %q", un, rr.msg)
		}
	}
	if rr := byName["f54bad1"]; rr.ok || !strings.Contains(rr.msg, "保留域") {
		t.Fatalf("保留域行须拿到「保留域」原因且不建号，实得 %+v", rr)
	}
	if rr := byName["f54bad2"]; rr.ok || !strings.Contains(rr.msg, "格式不合法") {
		t.Fatalf("畸形邮箱须拿到「格式不合法」原因且不建号，实得 %+v", rr)
	}
	// 落库等值：真域那行邮箱确实绑上，留空那行邮箱为空
	u1s, e1 := s.Store.GetUserByUsernameGlobal("f54ok1")
	u2s, e2 := s.Store.GetUserByUsernameGlobal("f54ok2")
	if e1 != nil || len(u1s) != 1 {
		t.Fatalf("真域行应恰有一行落库: n=%d err=%v", len(u1s), e1)
	}
	if u1s[0].Email != "f54ok1@acme-corp.com" {
		t.Fatalf("真域行邮箱未绑上: %q", u1s[0].Email)
	}
	if e2 != nil || len(u2s) != 1 {
		t.Fatalf("留空邮箱行应恰有一行落库: n=%d err=%v", len(u2s), e2)
	}
	if u2s[0].Email != "" {
		t.Fatalf("留空邮箱行不该被绑上邮箱: %q", u2s[0].Email)
	}
}

// countingMailQueue 只实现「入队计数」这一件事的假队列：
// 让 mailLive() 判为通道可用（TicketSvc.Queue != nil），同时把 EnqueueMail 变成可数的——
// 回执写「已提交发送」时，我们要能证明它确实只入队、没投递，
// 而且被邮箱闸拒收的行一次都不占发信。
type countingMailQueue struct{ enqueued int }

func (q *countingMailQueue) Enqueue(ctx context.Context, jobType string, payload []byte, maxAttempts int) (int64, error) {
	q.enqueued++
	return int64(q.enqueued), nil
}
func (q *countingMailQueue) Reserve(ctx context.Context, workerID string, leaseSec int) (*queue.Job, error) {
	return nil, nil
}
func (q *countingMailQueue) MarkDone(ctx context.Context, jobID int64) error { return nil }
func (q *countingMailQueue) MarkFailed(ctx context.Context, jobID int64, errMsg string) error {
	return nil
}
func (q *countingMailQueue) Heartbeat(ctx context.Context, jobID int64, workerID string) error {
	return nil
}
func (q *countingMailQueue) RecoverStale(ctx context.Context) (int64, error) { return 0, nil }

// TestF54_LiveChannelReceiptSaysAcceptedNotDelivered 锁 F54-D：通道在线（队列可用）态的 handler 回执。
// 两行合法用户（都带真域邮箱）+ 一行 example.com：created=2、入队次数恰 2
// （拒收行不占发信）、成功回执必须是「已提交发送，稍后送达」，
// 且整份响应里不得出现完成时的「已通过邮件通知」。
//
// 为什么这条必须存在：enqueueMail 入队即 return nil，「Send 无错」＝「已受理」而非「已送达」；
// 旧文案替系统许下系统自己不知道的果（与「发码 success ≠ 投递」同一条纪律）。
func TestF54_LiveChannelReceiptSaysAcceptedNotDelivered(t *testing.T) {
	s, tok := newF23ImportProbe(t)
	t.Setenv("MAIL_ENABLED", "") // 通道在线判据走队列，不靠 SMTP 三件套（避免真连网）
	q := &countingMailQueue{}
	s.TicketSvc = &service.TicketService{Queue: q}
	if !s.mailLive() {
		t.Fatalf("队列可用时 mailLive 应判通道在线（本锁的前置）")
	}
	xlsxPath := filepath.Join(t.TempDir(), "live.xlsx")
	writeImportXlsxFromRows(t, xlsxPath, [][]string{
		{"用户名称", "姓名", "部门", "角色", "邮箱"},
		{"f54live1", "在线一", "", "普通用户", "f54live1@acme-corp.com"},
		{"f54live2", "在线二", "", "", "f54live2@lexicorn.cn"},
		{"f54sample", "示例域", "", "", "x@example.com"},
	})
	rec := callUserBulkImportXlsx(t, s, tok, xlsxPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("应 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
	var out f54ImportResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || !out.Success {
		t.Fatalf("解码失败: %v (%s)", err, rec.Body.String())
	}
	if out.Created != 2 || out.Failed != 1 {
		t.Fatalf("应建号 2 行、示例域那行被拒，实得 created=%d failed=%d: %s", out.Created, out.Failed, rec.Body.String())
	}
	if q.enqueued != 2 {
		t.Fatalf("入队次数应恰为 2（两行成功各一封，拒收行不发信），实得 %d", q.enqueued)
	}
	body := rec.Body.String()
	if strings.Contains(body, "已通过邮件通知") {
		t.Fatalf("回执不得把「已入队」写成「已通知」（F-54② 复发）: %s", body)
	}
	checked := 0
	for _, rr := range out.Results {
		if !rr.OK {
			continue
		}
		checked++
		if !strings.Contains(rr.Message, "已提交发送") || !strings.Contains(rr.Message, "线下转告") {
			t.Fatalf("在线态成功回执须「已提交发送＋兜底线下转告」，行 %s 实得 %q", rr.Username, rr.Message)
		}
	}
	if checked != 2 {
		t.Fatalf("应有 2 行成功回执参与文案等值，实得 %d", checked)
	}
}
