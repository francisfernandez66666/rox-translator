// b7_webhook_secret_test.go · ★ B7 webhook secret 静态加密回归：落库密文、读取明文、更新幂等。
package store

import (
	"strconv"
	"strings"
	"testing"
)

func TestWebhookSecretAtRestEncrypted(t *testing.T) {
	st := newTestStore(t)
	w := &Webhook{TenantID: 1, URL: "https://example.com/cb", Secret: "s3cr3t-abc", Events: "translation.completed", Enabled: 1, MaxRetries: 3, RetryInterval: 60}
	if err := st.UpsertWebhook(w); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := st.DB().QueryRow("SELECT secret FROM webhooks WHERE id=" + itoa(w.ID)).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, SecretEncPrefix) {
		t.Fatalf("库内应为 enc:v1: 密文，实得 %q", raw)
	}
	if strings.Count(raw, SecretEncPrefix) != 1 {
		t.Fatalf("不得双重包裹: %q", raw)
	}
	// 读取链路透明解密（签名用明文）
	list, err := st.ListWebhooks(1)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListWebhooks: %v %d", err, len(list))
	}
	if list[0].Secret != "s3cr3t-abc" {
		t.Fatalf("读取应还原明文，实得 %q", list[0].Secret)
	}
	// 更新（结构体仍是明文）幂等：密文前缀不叠加
	if err := st.UpsertWebhook(list[0]); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRow("SELECT secret FROM webhooks WHERE id=" + itoa(w.ID)).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Count(raw, SecretEncPrefix) != 1 {
		t.Fatalf("更新后仍须单层包裹: %q", raw)
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
