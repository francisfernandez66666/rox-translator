// ============ asr_p2_test.go · 职责说明 ============
// P2（音频/ASR）转写链的单元测试。
//
// ★ 2026-10-30 三事合一批改档（AGENTS §一·14）：音频交付从「.srt 字幕 ＋ .md ＋ 配音 ＋ 原音轨副本」
// 收成**两件**（译文 .md ＋ 同容器配音），所以本文件里那四条服务字幕产物的测试
// （时间轴格式化、字幕写盘、退化编号、跨阶段缓存）随实现一起删除——
// **不是放松判据**：`.srt` 已经不存在，留着就是一条对着空气的断言。
//
// 现役四条必守口径：
//
//	① 段序必须稳定——配音的行序按提取顺序 texts 走（engine/file.go 音频分支），
//	   同一份音频跑两次得到的段序列必须逐字相同（顺序错＝译文与音画错位）；
//	② 空段/无语音必须**报错**而不是交空产物（「没识别出来」与「产出一个空文件」是两件事）；
//	③ 每个 ERR: variant 都必须落到用户话术，未知 variant 才回空串给原始错误；
//	④ `asr_engine` 出的是**有效路由档**，判据与 asr_transcribe.py 的 _build_recognizer 同序。
//
// 两件产物本身（md 内容、配音容器回落）的断言在 engine 包：
// dubWithFallback／WriteTranslationMd 各自有锁，别在这里重复数一遍（两把尺子迟早打架）。
// =============================================
package fileproc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// TestParseAsrJSON 解析锁定（口径 ①②）：带 segments 优先用 segments（时间戳为准），
// segments 缺失时按 \n 切成伪段；全空必须报错而不是交空产物。
// ★ 「空段丢弃」那一腿同时钉住段序：丢弃后 Index 仍按**原 segment 顺序**递增，
// 下游按序读到的就是音频里的说话顺序——配音缝合读的就是这个序。
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

// TestParseAsrJSONDeterministic ★ 口径 ①：同一份 stdout 解析五次，段序与内容必须逐字相同。
// 为什么单独钉这一条：实现里的退化分支曾经写成「遍历 map 拼行」，
// 那样只有偶发红（不同 Go 版本/机器行序不同），排查成本极高——用「跑多次」把它钉死在第一次。
func TestParseAsrJSONDeterministic(t *testing.T) {
	out := `{"text":"第一段\n第二段\n第三段\n第四段"}`
	first, err := parseAsrJSON(out)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(first) != 4 {
		t.Fatalf("应切成 4 段，实际 %d: %+v", len(first), first)
	}
	for i := 0; i < 5; i++ {
		again, aerr := parseAsrJSON(out)
		if aerr != nil {
			t.Fatalf("第 %d 次解析失败: %v", i+2, aerr)
		}
		for j := range again {
			if again[j].Text != first[j].Text || again[j].Index != first[j].Index {
				t.Fatalf("第 %d 次解析段序漂移：[%d] got %q/%d, want %q/%d",
					i+2, j, again[j].Text, again[j].Index, first[j].Text, first[j].Index)
			}
		}
	}
}

// TestAsrCueTimelineNoCache ★ 2026-10-30 改档的接线判据：转写段的时间轴**只随返回值走**，
// 进程里不再留任何缓存（字幕取消后没有第二个读者）。
// 判据问的是「结构」：asr.go 的代码行里不许再出现缓存本体的三个名字——
// 留着就是一条「重启会丢」的伪契约，而下一条实现会顺手把它当现成件复用回去。
// ⚠️ 靶子必须先剥整行注释（`stripGoBareComments`）：本文件与 asr.go 的说明注释里
// 就写着这三个名字（记录"为什么删"），拿裸串判会把注释当实现（本仓反复踩过的那一族）。
func TestAsrCueTimelineNoCache(t *testing.T) {
	b, err := os.ReadFile("asr.go")
	if err != nil {
		t.Fatalf("读 asr.go 失败（静态锁必须以包内源码为靶子）: %v", err)
	}
	code := stripGoBareComments(string(b))
	for _, dead := range []string{"rememberAsrCues", "AudioCues(", "asrCueCache"} {
		if strings.Contains(code, dead) {
			t.Fatalf("asr.go 的代码行里仍有 %s：缓存本体已随字幕产物删除，不该再有读者", dead)
		}
	}
	// 射程自证 A：探测器必须扫到现役入口（扫不到＝读到的是空文本，上面三条恒真空转）。
	if !strings.Contains(code, "func TranscribeAudio") {
		t.Fatal("射程自证失败：asr.go 里连 TranscribeAudio 都扫不到，说明靶子取错了")
	}
	// 射程自证 B：把缓存那一行喂回同一个判据，必须被判红（证明这条负向锁真能抓住回潮）。
	mutated := code + "\nvar asrCueCache []asrCueEntry\n"
	caught := false
	for _, dead := range []string{"rememberAsrCues", "AudioCues(", "asrCueCache"} {
		if strings.Contains(stripGoBareComments(mutated), dead) {
			caught = true
		}
	}
	if !caught {
		t.Fatal("反证失败：喂回缓存本体却没抓到，这条锁射程为零")
	}
}

// TestAsrEngineFromAssets ★ 2026-10-19 G12：asr_engine 出的是**有效路由档**，
// 判据必须与 asr_transcribe.py 的 _build_recognizer 同序（SenseVoice ＞ Transducer ＞ Whisper ＞ 单权重兜底）。
// 这里用临时目录喂四档形态，不必装 sherpa-onnx/权重——判据是"资产形状 → 档位词"这一层。
func TestAsrEngineFromAssets(t *testing.T) {
	write := func(dir string, names ...string) {
		for _, n := range names {
			if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
				t.Fatalf("写夹具 %s 失败: %v", n, err)
			}
		}
	}
	cases := []struct {
		name  string
		files []string
		want  string
	}{
		{"sense", []string{"model.onnx", "tokens.txt"}, "sense_voice"},
		{"transducer", []string{"encoder.onnx", "decoder.onnx", "joiner.onnx", "tokens.txt"}, "transducer"},
		{"whisper", []string{"whisper_encoder.onnx", "whisper_decoder.onnx", "tokens.txt"}, "whisper"},
		{"single-fallback", []string{"foo.onnx", "tokens.txt"}, "transducer"},
		{"no-tokens", []string{"model.onnx"}, ""},
		{"empty", nil, ""},
	}
	for _, c := range cases {
		dir := t.TempDir()
		write(dir, c.files...)
		if got := asrEngineFromAssets(dir); got != c.want {
			t.Errorf("%s: asrEngineFromAssets = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestFriendlyAsrErrorNoSpeech ★ G11 映射腿：脚本的 ERR:NoSpeech 必须落成人话，
// 且这句话**逐字等于** AsrNoSpeechMessage（api/asrReason 靠同一常量前缀判档，两处各写一份就会漂移）。
func TestFriendlyAsrErrorNoSpeech(t *testing.T) {
	got := friendlyAsrError("ERR:NoSpeech:no_speech 未检测到有效语音（输入能量低于门限，可能是纯静音或极低音量）")
	if got != AsrNoSpeechMessage {
		t.Fatalf("NoSpeech 人话 = %q, want %q", got, AsrNoSpeechMessage)
	}
}
