package fileproc

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeTestZip 造一个 result.zip：manifest 声明 n 个块的 sha，chunk_<n>.json 为真块。
func makeTestZip(t *testing.T, dir string, n int, tokens int64) (string, [][2]int) {
	t.Helper()
	zpath := filepath.Join(dir, "result.zip")
	z, err := os.Create(zpath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(z)
	manifest := map[string]interface{}{
		"kind": "langcross_chunks", "chunk_count": n,
		"chunks": []map[string]string{}, "tokens_used": tokens,
		"quality_hits": []string{"structure_fingerprint"},
	}
	chunks := manifest["chunks"].([]map[string]string)
	for i := 0; i < n; i++ {
		name := "chunk_" + chunkItoa(i) + ".json"
		body := []byte(`{"text":"t` + chunkItoa(i) + `"}`)
		h := sha256.Sum256(body)
		w, e := zw.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		w.Write(body)
		chunks = append(chunks, map[string]string{"name": name, "sha256": hex.EncodeToString(h[:])})
	}
	manifest["chunks"] = chunks
	mw, _ := zw.Create("manifest.json")
	b, _ := json.Marshal(manifest)
	mw.Write(b)
	zw.Close()
	z.Close()
	return zpath, makeBounds(n)
}

func chunkItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func makeBounds(n int) [][2]int {
	out := make([][2]int, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, [2]int{i, i + 1})
	}
	return out
}

func TestVerifyChunkZipHappyPath(t *testing.T) {
	dir := t.TempDir()
	pdf := filepath.Join(dir, "in.pdf")
	if err := os.WriteFile(pdf, make([]byte, 1<<20), 0644); err != nil {
		t.Fatal(err)
	}
	zpath, bounds := makeTestZip(t, dir, 3, 42)
	req := ChunkRunRequest{InputPDF: pdf, OutputZip: zpath, Bounds: bounds}
	got, err := verifyChunkZip(context.Background(), req, ChunkRunResult{})
	if err != nil {
		t.Fatal(err)
	}
	if got.ChunkCount != 3 || got.TokensUsed != 42 {
		t.Errorf("回执错：%+v", got)
	}
	if len(got.QualityHit) == 0 {
		t.Error("quality_hits 应回传主站供 F-1 断言")
	}
}

func TestVerifyChunkZipRejectsMissingChunk(t *testing.T) {
	dir := t.TempDir()
	pdf := filepath.Join(dir, "in.pdf")
	os.WriteFile(pdf, make([]byte, 1<<20), 0644)
	// 造 3 块的 zip，但 bounds 声明 4 ⇒ 块数不符判红
	zpath, _ := makeTestZip(t, dir, 3, 42)
	req := ChunkRunRequest{InputPDF: pdf, OutputZip: zpath, Bounds: makeBounds(4)}
	if _, err := verifyChunkZip(context.Background(), req, ChunkRunResult{}); err == nil {
		t.Error("缺块（manifest 块数与 bounds 不符）应判红")
	}
}

func TestVerifyChunkZipRejectsBomb(t *testing.T) {
	dir := t.TempDir()
	pdf := filepath.Join(dir, "in.pdf")
	os.WriteFile(pdf, make([]byte, 1<<10), 0644) // 1 KB 输入
	zpath := filepath.Join(dir, "bomb.zip")
	f, _ := os.Create(zpath)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("manifest.json")
	w.Write([]byte(`{"chunk_count":0,"chunks":[],"tokens_used":0}`))
	// 声明 10 MB 解压体积（远超输入 1KB ×2）
	w2, _ := zw.Create("chunk_0.json")
	w2.Write(make([]byte, 10<<20))
	zw.Close()
	f.Close()
	req := ChunkRunRequest{InputPDF: pdf, OutputZip: zpath, Bounds: makeBounds(1)}
	if _, err := verifyChunkZip(context.Background(), req, ChunkRunResult{}); err == nil {
		t.Error("解压炸弹应判红")
	}
}

// TestDispatchChunkZipSingleGet 钉住 C-4：块译文打包为一个 zip，主站只 get 一次。
// 形态断言：DispatchChunkRun 源码里 outputs 只声明一个 result.zip，没有逐块 get 循环。
func TestDispatchChunkZipSingleGet(t *testing.T) {
	b, err := os.ReadFile("dispatch_chunk_run.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"result.zip": req.OutputZip`) {
		t.Error("DispatchChunkRun 未声明单个 result.zip 产物")
	}
	// 反证：假源码里出现逐块 stat+get 形态应被识别（这里是探测器自证，不是真生产代码）
	fake := `for _, chunk := range chunks { dispatchStat(ctx, chunk); dispatchGet(ctx, chunk) }`
	if !strings.Contains(fake, "dispatchGet(ctx, chunk)") {
		t.Error("探测器失效：识别不出逐块 get 形态")
	}
	if strings.Contains(s, "dispatchGet(ctx, chunk") || strings.Contains(s, "dispatchStat(ctx, chunk") {
		t.Error("生产代码出现逐块 get/stat ⇒ 违反'一次 get'口径")
	}
}
