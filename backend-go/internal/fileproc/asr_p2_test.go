// ============ asr_p2_test.go · 职责说明 ============
// P2（音频/ASR）自转写产物链路的单元测试。四条必守口径：
//
//	① **时间轴必须原样进产物**——用户拿 .srt 是要挂到原音轨上播的，
//	   把 00:01:02,500 写成 00:00:00,000 就整份废（这是音频翻译与文档翻译最本质的区别）；
//	② **顺序必须稳定**——不能因为 Go map 迭代随机而让相邻两段互换位置；
//	③ **译文第一**——某段没译出就保留源文落盘，绝不让一条缺失变成一份空文件；
//	④ **降级不是失败**——拿不到时间戳（进程重启/缓存淘汰）也要出 .srt，只是退化为顺序编号。
//
// =============================================
package fileproc

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestFormatSrtTime SRT 时间轴格式的等值锁（毫秒三位、不足补零、跨小时进位）。
// ★ 负数那条是 `if sec < 0 { sec = 0 }` 的守卫：素材时长出现负值是浮点误差与脏元数据的常态，
//
//	不钳零会写出 "-00:00:05" 这种连 ffmp/播放器都拒绝解析的时间戳。
func TestFormatSrtTime(t *testing.T) {
	cases := []struct {
		sec  float64
		want string
	}{
		{0, "00:00:00,000"},
		{1.5, "00:00:01,500"},
		{61.25, "00:01:01,250"},
		{3661.007, "01:01:01,007"},
		{-5, "00:00:00,000"}, // 负数（素材时长为负/浮点误差）钳零，不能产出 "-00:00:05"
	}
	for _, c := range cases {
		if got := formatSrtTime(c.sec); got != c.want {
			t.Errorf("formatSrtTime(%v) = %q, want %q", c.sec, got, c.want)
		}
	}
}

// writeSrtToTemp 调用 WriteTranslationSrt 落临时文件并读回内容。
func writeSrtToTemp(t *testing.T, cues []SubtitleCue, texts []string, tr map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "out.srt")
	if err := WriteTranslationSrt(p, cues, texts, tr); err != nil {
		t.Fatalf("WriteTranslationSrt 失败: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读产物失败: %v", err)
	}
	return string(b)
}

// TestWriteTranslationSrtKeepsTimeline ① 时间轴原样保留 + ③ 未译出保留源文。
func TestWriteTranslationSrtKeepsTimeline(t *testing.T) {
	cues := []SubtitleCue{
		{Index: 1, Start: 0.0, End: 2.5, Text: "欢迎收听本期节目"},
		{Index: 2, Start: 2.5, End: 5.75, Text: "今天我们来聊聊翻译"},
	}
	tr := map[string]string{"欢迎收听本期节目": "Welcome to today's episode"}
	got := writeSrtToTemp(t, cues, []string{"欢迎收听本期节目", "今天我们来聊聊翻译"}, tr)
	want := "1\n00:00:00,000 --> 00:00:02,500\nWelcome to today's episode\n\n" +
		"2\n00:00:02,500 --> 00:00:05,750\n今天我们来聊聊翻译\n\n"
	if got != want {
		t.Fatalf("译文 srt 产物不符：\n got=%q\nwant=%q", got, want)
	}
}

// TestWriteTranslationSrtDeterministicOrder ② 顺序稳定：同一份输入跑两次必须逐字节相同。
// ★ 为什么必须这条：WriteTranslationSrt 的退化分支若写成遍历 map（历史上一度如此），
//
//	不同 Go 版本/不同机器会得到不同行序，而 t.Fatalf 里比对字符串只会「偶发红」，
//	排查成本高到不合理 —— 用「跑两次」把它钉死在第一次。
func TestWriteTranslationSrtDeterministicOrder(t *testing.T) {
	texts := []string{"第一段", "第二段", "第三段", "第四段"}
	tr := map[string]string{}
	for _, s := range texts {
		tr[s] = s + "-EN"
	}
	first := writeSrtToTemp(t, nil, texts, tr)
	for i := 0; i < 5; i++ {
		if got := writeSrtToTemp(t, nil, texts, tr); got != first {
			t.Fatalf("无时间轴退化分支输出顺序不稳定（第 %d 次）:\n%q\n%q", i+2, got, first)
		}
	}
}

// TestWriteTranslationSrtFallbackOrdered ④ 无时间戳时按 texts 顺序编号（不是按 map 顺序）。
func TestWriteTranslationSrtFallbackOrdered(t *testing.T) {
	texts := []string{"甲句", "乙句"}
	tr := map[string]string{"甲句": "A", "乙句": "B"}
	got := writeSrtToTemp(t, nil, texts, tr)
	want := "1\n00:00:00,000 --> 00:00:00,000\nA\n\n2\n00:00:00,000 --> 00:00:00,000\nB\n\n"
	if got != want {
		t.Fatalf("退化分支输出不符:\n got=%q\nwant=%q", got, want)
	}
}

// TestFriendlyAsrError 错误 variant → 用户话术的映射锁（每种 ERR: 都必须有对应的人话，
// 落到 default 返回空串就等于把 Python 的原始堆栈抛给用户）。
func TestFriendlyAsrError(t *testing.T) {
	cases := map[string]bool{ // variant → 是否必须有话术
		"ModelLoad":              true,
		"Asset":                  true,
		"AudioOpen":              true,
		"Decode":                 true,
		"Unsupported":            true,
		"Empty":                  true,
		"Import":                 true,
		"FFmpeg":                 true,
		"WhateverUnknownVariant": false, // 未知 variant：返回空串，由调用方给原始错误
	}
	for variant, wantMsg := range cases {
		got := friendlyAsrError("ERR:" + variant + ":某处细节")
		if wantMsg && got == "" {
			t.Errorf("friendlyAsrError(%s) 返回空串 ⇒ 该 variant 没有落到用户话术", variant)
		}
		if !wantMsg && got != "" {
			t.Errorf("friendlyAsrError(%s) 不该有话术，实际 %q", variant, got)
		}
	}
	if got := friendlyAsrError("没有 ERR 前缀的普通 stderr"); got != "" {
		t.Errorf("非 ERR: 行应返回空串，实际 %q", got)
	}
}

// TestAsrCueCacheRoundTrip 时间轴跨阶段传递：转写侧记住 → 写回侧取回；
// 不同 key 互不干扰（★ 并发多工单场景下「A 的音频拿到 B 的时间轴」是灾难级串位）。
func TestAsrCueCacheRoundTrip(t *testing.T) {
	asrCueMu.Lock()
	saved := asrCueCache
	asrCueCache = nil
	asrCueMu.Unlock()
	t.Cleanup(func() {
		asrCueMu.Lock()
		asrCueCache = saved
		asrCueMu.Unlock()
	})

	a := []SubtitleCue{{Index: 1, Start: 0, End: 1, Text: "甲"}}
	b := []SubtitleCue{{Index: 1, Start: 0, End: 9, Text: "乙"}}
	rememberAsrCues("/tmp/a.mp3", a)
	rememberAsrCues("/tmp/b.mp3", b)

	if got := AudioCues("/tmp/a.mp3"); len(got) != 1 || got[0].Text != "甲" {
		t.Fatalf("AudioCues(/tmp/a.mp3) = %+v, want 甲的 cue", got)
	}
	if got := AudioCues("/tmp/b.mp3"); len(got) != 1 || got[0].Text != "乙" {
		t.Fatalf("AudioCues(/tmp/b.mp3) = %+v, want 乙的 cue", got)
	}
	if got := AudioCues("/tmp/missing.mp3"); got != nil {
		t.Fatalf("未缓存的键应返回 nil（写回侧据此退化为无时间轴 srt），实际 %+v", got)
	}
}

// TestAsrCueCacheEviction 缓存必须有界：长跑进程不能无限增长（队列上限淘汰最早的）。
func TestAsrCueCacheEviction(t *testing.T) {
	asrCueMu.Lock()
	saved := asrCueCache
	asrCueCache = nil
	asrCueMu.Unlock()
	t.Cleanup(func() {
		asrCueMu.Lock()
		asrCueCache = saved
		asrCueMu.Unlock()
	})

	for i := 0; i < asrCueCacheMax+8; i++ {
		p := "/tmp/audio_" + strconv.Itoa(i) + ".mp3"
		rememberAsrCues(p, []SubtitleCue{{Index: 1, Text: p}})
	}
	asrCueMu.Lock()
	n := len(asrCueCache)
	asrCueMu.Unlock()
	if n > asrCueCacheMax {
		t.Fatalf("缓存条数 %d 超过上限 %d ⇒ 内存无界", n, asrCueCacheMax)
	}
}

// TestParseAsrJSON 解析锁定：带 segments 优先用 segments（时间戳为准），
// segments 缺失时按 \n 切成伪段；全空必须报错而不是交空产物。
func TestParseAsrJSON(t *testing.T) {
	cues, err := parseAsrJSON(`{"text":"全文","segments":[{"start":1,"end":2,"text":"第一句"},{"start":2,"end":3,"text":"  "},{"start":3,"end":4,"text":"第二句"}]}`)
	if err != nil {
		t.Fatalf("parseAsrJSON 失败: %v", err)
	}
	if len(cues) != 2 {
		t.Fatalf("空段应被丢弃，实际 %d 段: %+v", len(cues), cues)
	}
	if cues[1].End != 4 {
		t.Fatalf("第 2 段结束时间 = %v, want 4", cues[1].End)
	}

	cues, err = parseAsrJSON(`{"text":"甲\n乙"}`)
	if err != nil {
		t.Fatalf("退化形态不应报错: %v", err)
	}
	if len(cues) != 2 || cues[0].Text != "甲" {
		t.Fatalf("退化形态应切成 2 段（甲/乙），实际 %+v", cues)
	}

	if _, err := parseAsrJSON(`{"text":"","segments":[]}`); err == nil {
		t.Fatal("全空输入应报错（这里是「没识别出来」与「产空文件」的分水岭）")
	}
	if _, err := parseAsrJSON(`这不是 JSON`); err == nil {
		t.Fatal("非 JSON 应报错")
	}
}
