// ============ admin_packages_qr_test.go · 职责说明 ============
// G14（2026-10-19）：静态收款码配置写入侧的落盘校验 `validateStaticQRImageRef`。
//
// 缺陷形态（D23）：读侧 handleQRImage 只认 `<UploadDir>/_qr/<basename>`，而写侧允许存任意字符串 ⇒
// 手工粘贴/历史批次写进库的原始截图名在磁盘上不存在，保存成功、收银台四处 404。
//
// 判据：本机相对路径形态必须落盘可得（缺件即报错）；base64 dataURI 与外部 http(s) URL
// 不是本机路径，原样放行（不越权猜外部可达性）；名字含路径分隔符/为空一律拦。
//
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestValidateStaticQRImageRef
// =============================================
package api

import (
	"os"
	"path/filepath"
	"testing"

	"translator/internal/config"
)

// TestValidateStaticQRImageRef G14 主判据＋放行对照。
func TestValidateStaticQRImageRef(t *testing.T) {
	dir := t.TempDir()
	// 落一枚真件到 _qr/，用于"命中放行"那一腿。
	qrDir := filepath.Join(dir, "_qr")
	if err := os.MkdirAll(qrDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(qrDir, "real_123.PNG"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Server{Cfg: &config.Config{UploadDir: dir}}

	// ① 命中已落盘件 ⇒ 放行
	if err := s.validateStaticQRImageRef("/api/qr-image/real_123.PNG"); err != nil {
		t.Fatalf("已落盘件应放行，实际报错：%v", err)
	}
	// ② 未落盘 ⇒ 必须拦（D23 本体）
	if err := s.validateStaticQRImageRef("/api/qr-image/IMG_2516_1788143198498618891.PNG"); err == nil {
		t.Fatal("未落盘的本机相对路径必须被拦，否则保存成功、读侧 404")
	}
	// ③ 路径分隔符/空名 ⇒ 拦
	for _, v := range []string{"/api/qr-image/", "/api/qr-image/a/b.png", `/api/qr-image/a\b.png`} {
		if err := s.validateStaticQRImageRef(v); err == nil {
			t.Errorf("%q 应被拦（非法名）", v)
		}
	}
	// ④ base64 dataURI / 外部 URL / 空串 ⇒ 放行（非本机路径，读侧本就不命中本目录）
	for _, v := range []string{
		"data:image/png;base64,iVBORw0KGgo=",
		"https://pay.example.com/qr.png",
		"",
	} {
		if err := s.validateStaticQRImageRef(v); err != nil {
			t.Errorf("%q 应放行，实际报错：%v", v, err)
		}
	}
}
