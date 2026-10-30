// ============ chat_image_limit_test.go · 职责说明 ============
// 组4 N4/N7 的判据锁（★ 2026-10-31 补）：
//
//	N4 静默截断必须消失：落盘函数抄满硬顶后要**按实际尺寸判超限**，
//	   不许"抄到哪算哪"把半截图送进 OCR（旧形态响应仍然成功，客户拿到糊图）。
//	N7 图片语种诚实读数：出参必须带 image_langs_rendered，且只有真出图时才置 1。
//
// =============================================
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

// N4：小文件正常落盘；超过硬顶必须回 errChatFileTooLarge（而不是静默截断）。
func TestChatImageStage_RejectsOversize(t *testing.T) {
	// 小文件：正常落盘，内容逐字节等值。
	small := bytes.Repeat([]byte("a"), 1024)
	path, cleanup, err := stageChatImage(bytes.NewReader(small), ".png")
	if err != nil {
		t.Fatalf("N4：小文件落盘失败：%v", err)
	}
	got, rerr := os.ReadFile(path)
	cleanup()
	if rerr != nil || !bytes.Equal(got, small) {
		t.Fatalf("N4：落盘内容不等值（rerr=%v, len=%d）", rerr, len(got))
	}
	// 超硬顶 1 字节：必须判超限，且临时文件被清掉。
	big := make([]byte, chatImageMaxBytes+1)
	_, cleanup2, err2 := stageChatImage(bytes.NewReader(big), ".png")
	if !errors.Is(err2, errChatFileTooLarge) {
		t.Fatalf("N4：超硬顶必须回 errChatFileTooLarge，实得 %v", err2)
	}
	if cleanup2 != nil {
		cleanup2()
	}
}

// N7：出参字段存在且形态稳定（前端与排障按它区分"只出一版"与"这版也没出来"）。
func TestChatImageResp_CarriesRenderedLangs(t *testing.T) {
	b, err := json.Marshal(chatImageResp{ImageLangsRendered: 1})
	if err != nil {
		t.Fatalf("N7：序列化失败：%v", err)
	}
	if !strings.Contains(string(b), `"image_langs_rendered":1`) {
		t.Fatalf("N7：出参缺 image_langs_rendered，实得 %s", string(b))
	}
	// 降级（默认 0）也要出这个键——缺键会让前端把"没出图"读成"字段不存在"。
	b0, _ := json.Marshal(chatImageResp{})
	if !strings.Contains(string(b0), `"image_langs_rendered":0`) {
		t.Fatalf("N7：降级出参必须带 0，实得 %s", string(b0))
	}
	// 接线：成功分支必须真把 1 写进去（只声明字段不赋值＝永远 0）。
	src, rerr := os.ReadFile("chat_image.go")
	if rerr != nil {
		t.Fatalf("N7：读源码失败：%v", rerr)
	}
	if !strings.Contains(string(src), "resp.ImageLangsRendered = 1") {
		t.Fatal("N7：成功分支没有写 ImageLangsRendered=1（字段恒 0＝读数与事实相反）")
	}
}
