// ============================================================================
// api/errmsg_test.go — ★ P0-3 错误脱敏回归锁（2026-09-21）
// 锁三件事：①内部实现特征（SQL/路径/网络/TLS/序列化/裸英文库文案）绝不外泄，
// 一律折成固定文案；②业务话术（*APIError、含中文、白名单短词）原样透出——
// 前端直接展示与 UAT 中文断言依赖这一条；③中文前缀拼接内部错误（"…: sql: …"）
// 这类混合体必须按内部错误处理。
// 运行：go test ./internal/api/ -run TestPublicErrMessage
// ============================================================================
package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	apierrors "translator/internal/errors"
)

// TestPublicErrMessage 脱敏出口分类判定（钉死方言无关，纯函数）
func TestPublicErrMessage(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string // "" 表示断言「原样透出 err.Error()」；"MASKED" 表示断言折成固定文案
	}{
		{"APIError 透出设计文案", apierrors.New(apierrors.ErrInsufficientBalance, "余额不足，请先充值"), "余额不足，请先充值"},
		{"中文业务话术原样", errors.New("邀请码已被使用"), ""},
		{"白名单英文短词原样", errors.New("unauthorized"), ""},
		{"SQL 错误脱敏", errors.New(`pq: relation "users" does not exist`), "MASKED"},
		{"sqlite 驱动错误脱敏", errors.New("no such table: information_schema.tables"), "MASKED"},
		{"文件路径脱敏", errors.New("open /opt/translator/data/x.json: permission denied"), "MASKED"},
		{"网络错误脱敏", errors.New("dial tcp 10.0.0.8:5432: connect: connection refused"), "MASKED"},
		{"TLS 错误脱敏", errors.New("x509: certificate signed by unknown authority"), "MASKED"},
		{"序列化错误脱敏", errors.New("json: cannot unmarshal object into Go value type"), "MASKED"},
		{"裸英文库文案按内部处理", errors.New("unexpected EOF"), "MASKED"},
		{"中文前缀拼内部错误", errors.New("读取用户失败: sql: no rows in result set"), "MASKED"},
		{"凭据字样脱敏", errors.New("missing Authorization bearer token in headers"), "MASKED"},
	}
	for _, c := range cases {
		got := publicErrMessage(context.Background(), c.err)
		switch c.want {
		case "":
			if got != c.err.Error() {
				t.Errorf("%s: 业务文案被误脱敏 %q → %q", c.name, c.err.Error(), got)
			}
		case "MASKED":
			if got != serviceBusyMessage {
				t.Errorf("%s: 内部错误未脱敏，原文外泄 %q → %q", c.name, c.err.Error(), got)
			}
			if strings.Contains(got, "sql") || strings.Contains(got, "/opt/") {
				t.Errorf("%s: 脱敏文案仍含内部特征", c.name)
			}
		default: // 指定期望值（如 APIError 只透出 Message 不含 code 前缀）
			if c.want != "" && got != c.want {
				t.Errorf("%s: 期望 %q 实得 %q", c.name, c.want, got)
			}
		}
	}
	if publicErrMessage(context.Background(), nil) == "" {
		t.Error("nil error 应给非空占位文案")
	}
}

// TestNoRawErrLeakInHandlers 闸门级防回归：扫描本包全部非测试源码，任何 handler 再写出
// `"message": err.Error()` 直挂即红灯——强制后来者走 publicErrMessage 统一出口。
// （2026-09-21 整改基线：306 处裸回显已全部收敛，此处清零后不得回升）
func TestNoRawErrLeakInHandlers(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var bad []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, `"message": err.Error()`) || strings.Contains(line, `"error": err.Error()`) {
				bad = append(bad, f+":"+strconv.Itoa(i+1))
			}
		}
	}
	if len(bad) > 0 {
		t.Errorf("发现裸 err.Error() 出参（应改用 publicErrMessage）：%v", bad)
	}
}

// （注：本包已有 itoa 工具见 packscraper.go，此处直接用标准库 strconv 避免耦合）
