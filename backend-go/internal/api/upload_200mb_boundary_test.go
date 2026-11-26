// ============ upload_200mb_boundary_test.go · 上传闸门 200MiB 抬档门禁 ============
// 2026-11-26 工单动态派发批：把上传体积上限从 40MiB 抬到 200MiB。
//
// 本文件钉住两件事：
//   ① 现读 fileproc.MaxUploadBytes 恰为 200<<20（2^20 基数，不是 200*10^6）；
//   ② 边界判据形状 = MaxBytesReader 语义（size <= limit 放行、> limit 超限），
//      用纯函数模拟，不真发 HTTP（避免把网络层的偶然行为写进门禁）。
//
// 反证腿：用临时字符串夹具把期望写回 40<<20，同一套判据必须判红——
// 否则"现读等于 200"只是一条恒真的自比，改回去也没人知道。
// ============================================
package api

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"translator/internal/fileproc"
)

// boundaryReadFile 读相对本包目录（backend-go/internal/api）的源码；读不到直接判红，
// 不许退成"没读到所以没命中所以干净"（那是所有负向锁最怕的假绿形态）。
func boundaryReadFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("读 %s 失败（判据按相对本包目录的字面路径走）：%v", rel, err)
	}
	return string(b)
}

// uploadSizeAllowed 模拟 http.MaxBytesReader 的阈值语义：
// 恰好等于上限放行（<=），超出一个字节就超限。用纯函数表达，避免真发请求。
func uploadSizeAllowed(size, limit int64) bool {
	return size <= limit
}

// uploadMaxLiteralRe 认唯一事实源里的赋值形态 `MaxUploadBytes int64 = N << 20`。
// 只认 2^20 基数写法：40<<20（旧档）与 200<<20（新档）都能现读，MB/10^6 写法判红。
var uploadMaxLiteralRe = regexp.MustCompile(`MaxUploadBytes\s+int64\s*=\s*(\d+)\s*<<\s*20`)

// uploadMaxFixtureOK 判一段源码夹具里写的档位是否等于 200<<20。
// 正例喂真实 formats.go 必须 true；反例喂 40<<20 夹具必须 false。
func uploadMaxFixtureOK(src string) bool {
	m := uploadMaxLiteralRe.FindStringSubmatch(src)
	if m == nil {
		return false
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return false
	}
	return n<<20 == 200<<20
}

// TestUpload200MBBoundary 钉住 200MiB 抬档与 MaxBytesReader 的边界语义。
func TestUpload200MBBoundary(t *testing.T) {
	// ① 现读唯一事实源：恰好 200<<20，不是近似值也不是 10^6 基数。
	if got, want := fileproc.MaxUploadBytes, int64(200<<20); got != want {
		t.Fatalf("fileproc.MaxUploadBytes = %d，期望 %d（200MiB 抬档未落地或被改回旧档）", got, want)
	}

	// ② 边界判据形状：-1 放行、+1 超限（MaxBytesReader 的 <= 语义）。
	limit := fileproc.MaxUploadBytes
	if !uploadSizeAllowed(limit-1, limit) {
		t.Errorf("上限-1 应放行：size=%d limit=%d", limit-1, limit)
	}
	if uploadSizeAllowed(limit, limit) == false {
		t.Errorf("恰好等于上限应放行（MaxBytesReader 是 > 才拒）：size=%d limit=%d", limit, limit)
	}
	if uploadSizeAllowed(limit+1, limit) {
		t.Errorf("上限+1 应超限：size=%d limit=%d", limit+1, limit)
	}

	// ③ 真实源码夹具：同一套判据必须把 formats.go 认成 200<<20。
	realSrc := boundaryReadFile(t, "../fileproc/formats.go")
	if !uploadMaxFixtureOK(realSrc) {
		t.Error("唯一事实源 formats.go 里的档位没被认成 200<<20 ⇒ 判据射程失效（下面反证会变成假绿）")
	}

	// ④ 反证：把期望写回 40<<20 的假夹具必须判红。
	fake := strings.Replace(realSrc, "200 << 20", "40 << 20", 1)
	if fake == realSrc {
		t.Fatal("反证构造失败：formats.go 里没有 `200 << 20`（说明锚点漂了，③ 已扫不到东西）")
	}
	if uploadMaxFixtureOK(fake) {
		t.Error("反证落空：档位写回 40<<20 仍判通过 ⇒ 这条门禁抓不住任何回退")
	}
}
