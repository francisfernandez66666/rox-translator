// ============================================================================
// capsvc_variant_parity_test.go —— 回落**不改变用户可见错误档位**（方案二 A10）。
//
// 这一条是组 6 的真判据之一：常驻腿和子进程腿是同一份识别逻辑的两个入口，
// 所以同一个底层失败必须给出**逐字相同**的 `ERR:<Variant>` 与用户话术。
// 只要有一档漂了，现网的表现就是「同一张坏图，有的单报"资产缺失"、有的单报"识别失败"」，
// 而两条腿都"成功回落"、日志都不红。
//
// 夹具口径：假脚本/假模块**只按协议回应**（打印 `ERR:<Variant>:` 并退 2 ／ 吐 JSON），
// 不模拟真实实现细节——协议变了才该红。
// ============================================================================
package fileproc

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeVariantScript 写一个只按文件名后缀吐档位的假识别脚本（子进程腿的对照靶子）。
func writeVariantScript(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "fake_ocr_variant.py")
	src := `import json, os, sys
name = os.path.basename(sys.argv[2] if len(sys.argv) > 2 else "")
if name.startswith("asset"):
    print("ERR:Asset:fake 资产缺失", file=sys.stderr); sys.exit(2)
if name.startswith("corrupt"):
    print("ERR:ImageCorrupt:fake 坏图", file=sys.stderr); sys.exit(2)
if name.startswith("slow"):
    print("ERR:Timeout:fake 预算不足", file=sys.stderr); sys.exit(2)
print(json.dumps({"text": "ok", "boxes": [[0, 0, 10, 10]], "confs": [0.9]}))
`
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("写假脚本失败: %v", err)
	}
	return p
}

// rawWarmCall 直接按协议发一帧（绕开客户端的共用 deadline，用于单独验证**服务端**产生的档位）。
func rawWarmCall(t *testing.T, sock, capName, cmd string, args []string, timeoutMS int) warmResponse {
	t.Helper()
	conn, err := net.DialTimeout("unix", sock, 3*time.Second)
	if err != nil {
		t.Fatalf("连不上测试服务: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	body, _ := json.Marshal(warmRequest{ID: "parity-1", Cap: capName, Cmd: cmd, Args: args, TimeoutMS: timeoutMS})
	if err := writeWarmFrame(conn, body); err != nil {
		t.Fatalf("写帧失败: %v", err)
	}
	raw, err := readWarmFrame(conn)
	if err != nil {
		t.Fatalf("读帧失败: %v", err)
	}
	var resp warmResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("响应不是 JSON: %v", err)
	}
	return resp
}

func TestCapsvcVariantParityWithSubprocess(t *testing.T) {
	py := capsvcTestPython(t)
	sock := startFakeCapsvc(t)
	script := writeVariantScript(t, t.TempDir())
	t.Setenv(OcrScriptEnv, script)
	t.Setenv("FILEPROC_PYTHON_BIN", py)

	// ① 脚本里就有的档位（Asset / ImageCorrupt）：两条腿的错误文本必须逐字相同。
	for _, tc := range []struct {
		file    string
		variant string
	}{
		{"asset.png", "Asset"},
		{"corrupt.png", "ImageCorrupt"},
	} {
		res, ok := warmOcrImage(context.Background(), tc.file, 5*time.Second)
		if !ok {
			t.Fatalf("%s: 常驻腿应当回一帧结果（不是回落信号）", tc.variant)
		}
		if res.Variant != tc.variant {
			t.Fatalf("%s: 常驻腿 variant 期望 %q，实得 %q（stderr=%s）", tc.variant, tc.variant, res.Variant, res.Stderr)
		}
		warmErr := ocrWarmError(res)

		e := &Extractor{}
		subErr := extractImageOCRSubprocess(context.Background(), tc.file, e)
		if subErr == nil {
			t.Fatalf("%s: 子进程腿期望失败，实得成功", tc.variant)
		}
		if warmErr.Error() != subErr.Error() {
			t.Fatalf("%s: 两条腿话术漂了\n常驻: %s\n子进程: %s", tc.variant, warmErr, subErr)
		}
	}

	// ② 超时：常驻腿这一档由**服务端自己**产生（推理被预算掐断、stderr 为空），
	//    子进程腿由脚本 ERR:Timeout 产生；两边用户话术必须一致。
	resp := rawWarmCall(t, sock, "ocr", "image", []string{"slow.png"}, 150)
	if resp.Variant != "Timeout" {
		t.Fatalf("服务端超时期望 variant=Timeout，实得 %q（stderr=%s）", resp.Variant, resp.Stderr)
	}
	warmErr := ocrWarmError(&warmResult{Variant: resp.Variant, Stderr: []byte(resp.Stderr)})

	e := &Extractor{}
	subErr := extractImageOCRSubprocess(context.Background(), "slow.png", e)
	if subErr == nil {
		t.Fatalf("子进程腿 ERR:Timeout 期望失败，实得成功")
	}
	if warmErr.Error() != subErr.Error() {
		t.Fatalf("Timeout 档话术漂了\n常驻: %s\n子进程: %s", warmErr, subErr)
	}
	if !strings.Contains(subErr.Error(), "超时") {
		t.Fatalf("Timeout 档话术应当带「超时」二字（人看得懂档位）: %s", subErr)
	}
}
