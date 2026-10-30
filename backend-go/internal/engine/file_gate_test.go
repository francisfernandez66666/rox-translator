// 漏译率判定与写回交付形态的纯函数测试：
//   - leakedLang：某语言未译出段数占比 >5%（2026-10-24 用户令：50%→5%）返回语言代码，
//     作为「触发缺失段补译」的信号；补不回也不判失败（见 HandleFile 收尾）。
//   - writebackDelivery：三档（inplace/image/audio）。★ 2026-10-30 三事合一批起
//     「srt/vtt/json/yaml/yml → xlsx 双语对照表」这一档**已退役**（那五种格式同时撤出准入），
//     本文件的期望按新口径翻转，翻转理由写在 TestWritebackDelivery 的注释里。
package engine

import (
	"testing"

	"translator/internal/fileproc"
)

// TestProtectSourceByLang 翻译前品牌保护：源文品牌名替换为规定译法（长词优先防拆残），
// 无术语匹配时不改动；返回第二个值标记是否发生过替换。
func TestProtectSourceByLang(t *testing.T) {
	terms := map[string]string{"极石汽车": "ROX", "极石": "ROX"}
	cases := []struct {
		name  string
		in    []string
		terms map[string]string
		want  []string
	}{
		{"长词优先不拆残", []string{"极石汽车驰骋全球山海。"}, terms, []string{"ROX驰骋全球山海。"}},
		{"短词单独替换", []string{"极石是高端品牌。"}, terms, []string{"ROX是高端品牌。"}},
		{"不含品牌原样", []string{"今天天气不错。"}, terms, []string{"今天天气不错。"}},
		{"无术语映射原样", []string{"极石汽车。"}, nil, []string{"极石汽车。"}},
		{"多段各自替换", []string{"极石汽车A", "极石B"}, terms, []string{"ROXA", "ROXB"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := protectSourceByLang(c.in, c.terms)
			for i := range c.want {
				if got[i] != c.want[i] {
					t.Fatalf("protectSourceByLang(%v)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
				}
			}
		})
	}
}

// TestLeakedLang 漏译率判定：>5% 触发、恰 5% 不触发、0 不触发、total=0 不触发。
func TestLeakedLang(t *testing.T) {
	t.Setenv("FILE_LEAK_REPAIR_RATIO", "0.05")
	cases := []struct {
		name   string
		lang   string
		remain int
		total  int
		want   string
	}{
		{"全部未译出触发", "en", 18, 18, "en"},
		{"10%触发", "ar", 2, 20, "ar"},
		{"恰5%不触发", "ru", 1, 20, ""},
		{"4%不触发", "ja", 4, 100, ""},
		{"6%触发", "ja", 6, 100, "ja"},
		{"remain为0不触发", "ko", 0, 10, ""},
		{"total为0不触发", "en", 0, 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := leakedLang(c.lang, c.remain, c.total); got != c.want {
				t.Errorf("leakedLang(%q, %d, %d) = %q, want %q", c.lang, c.remain, c.total, got, c.want)
			}
		})
	}
}

// TestWritebackDelivery 写回交付形态三档（inplace / image / audio）＋ 已撤格式的行为口径。
//
// ⚠️ 这里原先有一整段「srt/vtt/json/yaml/yml → "xlsx"」的期望，★ 2026-10-30 三事合一批
// 随该档交付退役而**翻转**（翻转理由必须写在原地——只删断言的话，下一个人会照旧口径装回来）：
// 这五种格式在建单口与执行口都已被拒（那两口由 fileproc/text_formats_test.go 判），
// 但**存量在飞单**（升级前建、升级后才轮到 worker 跑）仍会走到本函数。
// 对那种单，按 txt 行对齐写回是**故意留的行为**：钱已扣、译文已在库里，
// 这时候回一句「不支持」比交出一个纯文本件更坏（AGENTS §19「回落原文＋点名」同一取向）。
func TestWritebackDelivery(t *testing.T) {
	retired := []string{".srt", ".vtt", ".json", ".yaml", ".yml"}
	for _, ext := range retired {
		if fileproc.IsTranslateFormat(ext, false) || fileproc.IsTranslateFormat(ext, true) {
			t.Errorf("%s 仍准入 ⇒ 会走回那条已删除的对照表分支", ext)
		}
		if got := writebackDelivery(ext); got != "inplace" {
			t.Errorf("writebackDelivery(%q) = %q, want \"inplace\"（存量在飞单按行对齐写回，不报错）", ext, got)
		}
	}
	// 全量负向：准入名单里**任何一个**格式都不该再判成 "xlsx"（对照表档整个不存在）。
	for _, ext := range fileproc.TranslateFormatOrder {
		if got := writebackDelivery(ext); got == "xlsx" {
			t.Errorf("writebackDelivery(%q) = xlsx：这一档已于 2026-10-30 退役", ext)
		}
	}
	inplaceFormats := []string{".pdf", ".docx", ".pptx", ".txt", ".csv", ".md", ".xlsx", ""}
	for _, ext := range inplaceFormats {
		if got := writebackDelivery(ext); got != "inplace" {
			t.Errorf("writebackDelivery(%q) = %q, want \"inplace\"", ext, got)
		}
	}
	// ★ P1a：图片格式经 OCR 投喂翻译，无原格式可原位回写，交付形态为 "image"（译后图＋译文 .md）。
	imageFormats := []string{".png", ".jpg", ".jpeg", ".bmp", ".tif", ".tiff", ".webp", ".heic"}
	for _, ext := range imageFormats {
		if got := writebackDelivery(ext); got != "image" {
			t.Errorf("writebackDelivery(%q) = %q, want \"image\"", ext, got)
		}
	}
	// ★ P2：音频经 ASR 转写投喂翻译，译文是文本塞不回音轨，交付 "audio"。
	// ★ 2026-10-30：产物＝**译文 .md ＋ 同容器译后配音两件**（字幕 .srt 与原音频副本一并取消）。
	audioFormats := []string{".mp3", ".m4a", ".wav", ".ogg", ".flac", ".aac", ".opus"}
	for _, ext := range audioFormats {
		if got := writebackDelivery(ext); got != "audio" {
			t.Errorf("writebackDelivery(%q) = %q, want \"audio\"", ext, got)
		}
	}
	// ★ 反向对照：交付形态必须两两互斥——.md 是原位回写档，不能被误判成图片/音频降级档，
	//   否则不同形态会在同一条分支里互相串位（只测「该对的对了」测不出这种漂移，必须有反向对照）。
	if got := writebackDelivery(".md"); got != "inplace" {
		t.Errorf("writebackDelivery(\".md\") = %q, want \"inplace\"（反向对照：md 不是音频降级档）", got)
	}
}

// TestWritebackDeliveryAgreesWithFormatSource ★ 同源等价锁（2026-10-16）：
// writebackDelivery 的"图片/音频"两档**委托** fileproc 的集合判定（IsImageFormat / IsAudioFormat），
// 所以这里必须拿**准入名单全量**逐条比对——两份列表一旦各自漂（历史上 engine 那份加过 .heic、
// fileproc 那份没加），表现是"上传口放行、这里判 inplace"，图片被送进 Office 回写分支。
//
// 判据三腿：① 名单里每个后缀的分类与 fileproc 谓词一致；② image/audio 分类**恰好**等于谓词集合
// （既不许漏，也不许把别的格式塞进媒体档）；③ 名单里每个后缀都真的被这三档之一接住（不许有孤儿）。
func TestWritebackDeliveryAgreesWithFormatSource(t *testing.T) {
	order := fileproc.TranslateFormatOrder
	if len(order) < 20 {
		// 射程自证：名单忽然变短 ⇒ 本腿在稀疏集合上恒真，等于失明。
		t.Fatalf("准入名单只有 %d 项 ⇒ 派生失败，本等价锁已失明", len(order))
	}
	var wantImage, wantAudio []string
	for _, ext := range order {
		got := writebackDelivery(ext)
		switch {
		case fileproc.IsImageFormat(ext):
			wantImage = append(wantImage, ext)
			if got != "image" {
				t.Fatalf("图片格式 %s 被判成 %q（fileproc 谓词说是图片）⇒ 两份列表已漂", ext, got)
			}
		case fileproc.IsAudioFormat(ext):
			wantAudio = append(wantAudio, ext)
			if got != "audio" {
				t.Fatalf("音频格式 %s 被判成 %q（fileproc 谓词说是音频）⇒ 两份列表已漂", ext, got)
			}
		default:
			if got != "inplace" && got != "xlsx" {
				t.Fatalf("非媒体格式 %s 被判成 %q ⇒ 分类越界", ext, got)
			}
			if got == "image" || got == "audio" {
				t.Fatalf("非媒体格式 %s 被判成媒体档 %q ⇒ 谓词与分类抢了同一格", ext, got)
			}
		}
	}
	// ② 两个集合都必须非空（否则"谓词恒假 + 分类恒 inplace"这一对能一起骗过上面每一条）。
	if len(wantImage) == 0 || len(wantAudio) == 0 {
		t.Fatalf("媒体集合为空：image=%d audio=%d ⇒ 谓词已失明", len(wantImage), len(wantAudio))
	}
}

// TestLeakRatioExceeded F-01 服务层「质检存疑」判据：与 leakedLang 同阈值（5%）。
func TestLeakRatioExceeded(t *testing.T) {
	t.Setenv("FILE_LEAK_REPAIR_RATIO", "0.05")
	cases := []struct {
		name string
		un   map[string]int
		tot  int
		want bool
	}{
		{"恰5%不标记", map[string]int{"en": 1}, 20, false},
		{"10%标记", map[string]int{"en": 2}, 20, true},
		{"多语言任一超限即标记", map[string]int{"en": 0, "ja": 6}, 100, true},
		{"零缺失不标记", map[string]int{}, 20, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := LeakRatioExceeded(c.un, c.tot); got != c.want {
				t.Fatalf("LeakRatioExceeded(%v,%d)=%v, want %v", c.un, c.tot, got, c.want)
			}
		})
	}
}
