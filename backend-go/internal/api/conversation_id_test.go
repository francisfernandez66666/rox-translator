// conversation_id_test.go — 聊天会话主键的格式锁（〇-AR 挂账：旧格式串末段 %012x 喂 8 字节
// 实际吐 16 位、且段与段重叠 ⇒ 整串 40 字符不合 RFC 4122）。锁按 UUID v4 规范形态判。
package api

import (
	"regexp"
	"testing"
)

// uuidV4Re UUID v4 规范形态：8-4-4(版本 4)-4(variant 8/9/a/b)-12，整串 36 字符。
var uuidV4Re = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// TestConversationIDIsCanonicalUUIDV4 生成的会话 id 必须逐字符落在 UUID v4 形态上。
// 反证（本轮实测口径）：把 newConversationID 的格式串写回旧形态
// "%08x-%04x-%04x-%04x-%012x"（配 b[0:4],b[2:4],b[4:6],b[6:8],b[8:16]），
// 长度立刻变 40、末段 16 位 ⇒ 本用例判红。
func TestConversationIDIsCanonicalUUIDV4(t *testing.T) {
	for i := 0; i < 200; i++ {
		id := newConversationID()
		if len(id) != 36 {
			t.Fatalf("会话 id 长度应为 36（8-4-4-4-12），实际 %d：%s", len(id), id)
		}
		if !uuidV4Re.MatchString(id) {
			t.Fatalf("会话 id 不合 UUID v4 规范形态: %s", id)
		}
	}
}

// TestConversationIDSlicesAreDisjoint 用**已知字节**做等值锁：16 个字节 0x00..0x0f 必须
// 一字不差地铺成 8-4-4-4-12，且版本位/variant 落在第 15、第 20 个字符上。
// 旧形态把 b[2:4] 同时喂进第一段与第二段（重叠＝白扔 8 位熵）、末段 %012x 喂 8 字节吐 16 位，
// 按这一条逐字符等值判据写回来当场红。
func TestConversationIDSlicesAreDisjoint(t *testing.T) {
	b := make([]byte, 16)
	for i := range b {
		b[i] = byte(i)
	}
	want := "00010203-0405-4607-8809-0a0b0c0d0e0f"
	if got := formatConversationID(b); got != want {
		t.Fatalf("五段切片等值不符\n期望 %s\n实际 %s", want, got)
	}
}

// TestConversationIDUniqueAcrossCalls 主键唯一性：1000 次生成零重复。
func TestConversationIDUniqueAcrossCalls(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		id := newConversationID()
		if seen[id] {
			t.Fatalf("会话 id 重复: %s", id)
		}
		seen[id] = true
	}
}
