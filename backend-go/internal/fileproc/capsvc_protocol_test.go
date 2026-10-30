// ============================================================================
// capsvc_protocol_test.go —— 常驻服务帧协议的机械锁（方案二 A1）。
//
// 协议只有一份：uint32 **大端**长度 + UTF-8 JSON，单帧上限 8 MiB，一个连接只跑一个请求。
// 这些腿刻意不碰 OCR 逻辑：它们问的是"帧边界对不对"，任何一条错都会表现为
// 「服务在、但客户端一直回落」——而那在现网是**零日志、零报错**的形态（回落是设计）。
//
// 反证：把 writeWarmFrame 的 BigEndian 改成 LittleEndian ⇒ 往返腿必红（服务端按大端读，
// 长度会变成一个天文数字 ⇒ 超限断开）。最后一条 `LittleEndianHeader` 腿把这个事实
// 直接钉在测试里，避免"哪天有人两处一起改成小端、于是互相自洽、现网全挂"。
// ============================================================================
package fileproc

import (
	"encoding/binary"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

// dialFakeCapsvc 连上测试服务（带 5s 预算，避免用例在坏帧上挂死）。
func dialFakeCapsvc(t *testing.T, sock string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("unix", sock, 3*time.Second)
	if err != nil {
		t.Fatalf("连不上测试服务: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	return conn
}

func TestCapsvcProtocolRoundTrip(t *testing.T) {
	sock := startFakeCapsvc(t)
	conn := dialFakeCapsvc(t, sock)

	body, err := json.Marshal(warmRequest{ID: "rt-1", Cap: "ocr", Cmd: "image",
		Args: []string{"ok.png"}, TimeoutMS: 5000})
	if err != nil {
		t.Fatalf("编码请求失败: %v", err)
	}
	if err := writeWarmFrame(conn, body); err != nil {
		t.Fatalf("写帧失败: %v", err)
	}
	raw, err := readWarmFrame(conn)
	if err != nil {
		t.Fatalf("读帧失败（长度字段字节序若被改成小端，这里就是第一个红点）: %v", err)
	}
	var resp warmResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if resp.ID != "rt-1" {
		t.Fatalf("id 必须原样回显，实得 %q", resp.ID)
	}
	if !resp.OK || resp.Variant != "" {
		t.Fatalf("期望成功响应，实得 ok=%v variant=%q stderr=%s", resp.OK, resp.Variant, resp.Stderr)
	}
	if !strings.Contains(resp.Stdout, "ok.png") {
		t.Fatalf("stdout 内容不对: %s", resp.Stdout)
	}
	if resp.Cold != 1 {
		t.Fatalf("第一次调用应 cold=1（触发了能力加载），实得 %d", resp.Cold)
	}
}

// 超长帧：**直接断开 + 记 oversize**，不读 body、不回包。
func TestCapsvcProtocolRejectsOversizeFrame(t *testing.T) {
	sock := startFakeCapsvc(t)
	conn := dialFakeCapsvc(t, sock)
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(capsvcFrameMax+1))
	if _, err := conn.Write(hdr[:]); err != nil {
		t.Fatalf("写头失败: %v", err)
	}
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	// 服务端应当收摊（EOF 或 reset）；收到任何一帧都算判据失守。
	if _, err := readWarmFrame(conn); err == nil {
		t.Fatalf("超长帧不应得到响应（应当断开）")
	}
	// 断开之后服务仍可用（超限不是崩溃）。
	c2 := dialFakeCapsvc(t, sock)
	body, _ := json.Marshal(warmRequest{ID: "after-oversize", Cap: "ocr", Cmd: "image", Args: []string{"ok.png"}, TimeoutMS: 3000})
	if err := writeWarmFrame(c2, body); err != nil {
		t.Fatalf("写帧失败: %v", err)
	}
	raw, err := readWarmFrame(c2)
	if err != nil {
		t.Fatalf("超长帧之后服务不可用: %v", err)
	}
	var resp warmResponse
	_ = json.Unmarshal(raw, &resp)
	if resp.ID != "after-oversize" {
		t.Fatalf("超长帧之后响应 id 不对: %q", resp.ID)
	}
}

// 半包：只发两个字节就断开，服务不许 panic，下一条连接照常。
func TestCapsvcProtocolHalfPacketDoesNotPanic(t *testing.T) {
	sock := startFakeCapsvc(t)
	c := dialFakeCapsvc(t, sock)
	if _, err := c.Write([]byte{0x00, 0x00}); err != nil {
		t.Fatalf("写半包失败: %v", err)
	}
	c.Close()

	c2 := dialFakeCapsvc(t, sock)
	body, _ := json.Marshal(warmRequest{ID: "after-half", Cap: "ocr", Cmd: "image", Args: []string{"ok.png"}, TimeoutMS: 3000})
	if err := writeWarmFrame(c2, body); err != nil {
		t.Fatalf("写帧失败: %v", err)
	}
	raw, err := readWarmFrame(c2)
	if err != nil {
		t.Fatalf("半包之后服务不可用（多半是 panic 了）: %v", err)
	}
	var resp warmResponse
	_ = json.Unmarshal(raw, &resp)
	if resp.ID != "after-half" || !resp.OK {
		t.Fatalf("半包之后响应不对: %+v", resp)
	}
}

// 坏 JSON ⇒ BadRequest；未知能力 ⇒ Unsupported。两档必须分开（一个怪调用方、一个怪能力档）。
func TestCapsvcProtocolBadRequestAndUnsupported(t *testing.T) {
	sock := startFakeCapsvc(t)

	c := dialFakeCapsvc(t, sock)
	if err := writeWarmFrame(c, []byte("{not json")); err != nil {
		t.Fatalf("写坏 JSON 失败: %v", err)
	}
	raw, err := readWarmFrame(c)
	if err != nil {
		t.Fatalf("坏 JSON 应当得到**一帧错误响应**而不是断开: %v", err)
	}
	var resp warmResponse
	_ = json.Unmarshal(raw, &resp)
	if resp.OK || resp.Variant != "BadRequest" {
		t.Fatalf("坏 JSON 期望 variant=BadRequest，实得 ok=%v variant=%q", resp.OK, resp.Variant)
	}

	c2 := dialFakeCapsvc(t, sock)
	body, _ := json.Marshal(warmRequest{ID: "u1", Cap: "nope", Cmd: "image", Args: []string{"ok.png"}, TimeoutMS: 2000})
	if err := writeWarmFrame(c2, body); err != nil {
		t.Fatalf("写帧失败: %v", err)
	}
	raw, err = readWarmFrame(c2)
	if err != nil {
		t.Fatalf("未知能力应当得到一帧 Unsupported: %v", err)
	}
	_ = json.Unmarshal(raw, &resp)
	if resp.Variant != "Unsupported" {
		t.Fatalf("未知能力期望 variant=Unsupported，实得 %q", resp.Variant)
	}
}

// 小端长度字段（反证腿）：同样的 body 用小端写头，服务端按大端读 ⇒ 长度天文数字 ⇒ 断开。
// 这条腿存在的意义：证明"字节序"是这个协议里**被断言过**的事实，
// 而不是"两处碰巧都写成同一个字节序"。
func TestCapsvcProtocolLittleEndianHeaderIsRejected(t *testing.T) {
	sock := startFakeCapsvc(t)
	c := dialFakeCapsvc(t, sock)
	body, _ := json.Marshal(warmRequest{ID: "le-1", Cap: "ocr", Cmd: "image", Args: []string{"ok.png"}, TimeoutMS: 3000})
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(len(body)))
	if _, err := c.Write(append(hdr[:], body...)); err != nil {
		t.Fatalf("写帧失败: %v", err)
	}
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if raw, err := readWarmFrame(c); err == nil {
		var resp warmResponse
		_ = json.Unmarshal(raw, &resp)
		t.Fatalf("小端长度不应被接受，却收到了响应 id=%q ok=%v", resp.ID, resp.OK)
	}
}
