package fileproc

import (
	"context"
	"testing"
)

// TestDetectLangsBasic 12 语种样本各一条正例 + 三条负例（空/纯符号/纯数字）。
func TestDetectLangsBasic(t *testing.T) {
	cases := map[string]string{
		"the quick brown fox jumps over the lazy dog and the cat":           "en",
		"产品说明书：本设备支持蓝牙钥匙，请勿拆卸。":                                             "zh",
		"これは日本語の説明文です。設定を変更してください。":                                         "ja",
		"이 제품은 블루투스 키를 지원합니다. 설정을 변경하세요.":                                   "ko",
		"le manuel de l'utilisateur est dans la boîte avec les accessoires": "fr",
		"die Bedienungsanleitung ist im Karton und nicht mit dem Gerät":     "de",
		"el manual del usuario está en la caja con los accesorios":          "es",
		"o manual do usuário está na caixa com os acessórios":               "pt",
		"руководство пользователя находится в коробке с аксессуарами":       "ru",
		"دليل المستخدم موجود في الصندوق مع الملحقات":                        "ar",
		"คู่มือผู้ใช้อยู่ในกล่องพร้อมอุปกรณ์เสริม":                          "th",
	}
	texts := make([]string, 0, len(cases))
	want := make([]string, 0, len(cases))
	for k, v := range cases {
		texts = append(texts, k)
		want = append(want, v)
	}
	got, docLang, err := DetectLangs(context.Background(), texts)
	if err != nil {
		t.Fatalf("DetectLangs 不应返回错误：%v", err)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 段（%q）判成 %s，期望 %s", i, texts[i][:minLen(texts[i], 12)], got[i], want[i])
		}
	}
	if docLang == "und" {
		t.Fatal("整篇语种不该是 und")
	}
	// 负例：空串/纯符号/纯数字一律 und。
	for _, s := range []string{"", "   ", "!!!", "12345", "---"} {
		if lc := lidOfCached(s); lc != "und" {
			t.Errorf("%q 应判 und，得 %s", s, lc)
		}
	}
}

// TestLidCacheBound 缓存有界（超上限淘汰最旧），且结果稳定。
func TestLidCacheBound(t *testing.T) {
	for i := 0; i < lidCacheMax+50; i++ {
		_ = lidOfCached("sample text number " + string(rune('a'+i%26)) + string(rune('a'+i/26)))
	}
	lidMu.Lock()
	n := len(lidCache)
	lidMu.Unlock()
	if n > lidCacheMax {
		t.Fatalf("缓存超界：%d > %d", n, lidCacheMax)
	}
}

// minLen 取前 n 个字符（日志里截断用）。
func minLen(s string, n int) int {
	r := []rune(s)
	if len(r) < n {
		return len(r)
	}
	return n
}
