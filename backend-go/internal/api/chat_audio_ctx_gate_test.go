// ============ chat_audio_ctx_gate_test.go · 职责说明 ============
// /api/chat/tts 合成调用的**上下文来源**静态锁（★ 2026-10-21 UAT 收口 M8）。
//
// 由来：旧写法把 `context.Background()` 传给 fileproc.SpeakToWav，于是客户端断开、浏览器放弃、
// 用户点了朗读又关掉页面之后，那个 Python 子进程仍会继续吃掉一整个 CPU 核直到 60s 预算烧完。
// 单测与 UAT 都看不见这一层（它们从不中途断开请求），而 TTS 最贵的资源恰好就是 CPU——
// 这是「有自动降级的链路，产物能打开不算验收」那一族的同一形态：**没有症状的浪费**。
//
// 判据只有两条，都读源码（剥注释后）：
//
//	C1 SpeakToWav 的首参必须是 `r.Context()`；
//	C2 该文件里不许再出现 `context.Background()`（未来给 ASR 也补同一条时，这条会自动守住）。
//
// ★ 反证（本文件 C3）：把同一枚判据函数喂给"改回 context.Background()"的源码副本必须判红，
//
//	否则"文件里恰好没有那串"会让这条锁在实现被写坏时照样绿灯（空转锁）。
//
// =============================================
package api

import (
	"os"
	"strings"
	"testing"
)

// stripGoLineComments 去掉整行 `//` 注释，保留其余字节。
// ★ 必须剥注释：本文件的说明与你即将写下的理由里都会出现 `context.Background()` 这串字面，
//
//	拿裸串判会把「记录事故的注释」当成缺陷（同 §一·17 brotli 大窗口那条判据的先例）。
func stripGoLineComments(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// chatTTSUsesRequestContext 是这条锁的唯一判据（源码进、违规原因出；空串＝合规）。
// 反证测试与正测共用它，保证"判据本身"也被判过。
func chatTTSUsesRequestContext(raw string) string {
	src := stripGoLineComments(raw)
	if !strings.Contains(src, "SpeakToWav(r.Context(") {
		return "SpeakToWav 的首参必须是 r.Context()（客户端断开后要能停掉合成子进程）"
	}
	if strings.Contains(src, "context.Background()") {
		return "chat_audio.go 里不许再出现 context.Background()（合成必须跟请求同生共死）"
	}
	return ""
}

// TestChatTTSUsesRequestContext C1+C2：现役源码必须合规。
func TestChatTTSUsesRequestContext(t *testing.T) {
	data, err := os.ReadFile("chat_audio.go")
	if err != nil {
		t.Fatalf("读 chat_audio.go 失败：%v", err)
	}
	if why := chatTTSUsesRequestContext(string(data)); why != "" {
		t.Fatalf("chat_audio.go 违反 M8 上下文口径：%s", why)
	}
}

// TestChatTTSContextGateCounterProof C3：把判据喂给"旧形态"必须判红。
// ★ 这一步同时证明剥注释腿没有把判据剥瞎：下面那份副本里 context.Background() 出现在**代码行**上。
func TestChatTTSContextGateCounterProof(t *testing.T) {
	broken := "package api\n" +
		"// 说明注释里允许出现 context.Background() 这串字（记录事故用）\n" +
		"func f() { _ = fileproc.SpeakToWav(context.Background(), text, lang, p) }\n"
	if why := chatTTSUsesRequestContext(broken); why == "" {
		t.Fatal("反证失败：改回 context.Background() 的源码必须判红")
	}
	// 正向对照：同一份副本只把首参换回 r.Context() 后必须放行（否则判据射程指向了别处）。
	ok := "package api\n" +
		"func f() { _ = fileproc.SpeakToWav(r.Context(), text, lang, p) }\n"
	if why := chatTTSUsesRequestContext(ok); why != "" {
		t.Fatalf("正向对照失败（判据射程指向了注释或别的字符串）：%s", why)
	}
}
