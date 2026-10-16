// 漏译率硬闸与写回交付形态的纯函数测试（2026-09-09 可靠性改造回归）：
//   - leakedLang：某语言未译出段数 >50% 返回语言代码（工单失败），否则 ""
//   - writebackDelivery：srt/vtt/json/yaml/yml 走 xlsx 对照表，其余走原格式写回
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

// TestLeakedLang 漏译率硬闸判定：超 50% 触发、恰 50% 不触发、0 不触发、负数不触发
func TestLeakedLang(t *testing.T) {
	cases := []struct {
		name   string
		lang   string
		remain int
		total  int
		want   string
	}{
		{"全部未译出触发", "en", 18, 18, "en"},
		{"超过50%触发", "ar", 11, 20, "ar"},
		{"恰50%不触发", "ru", 10, 20, ""},
		{"低于50%不触发", "ja", 9, 20, ""},
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

// TestWritebackDelivery 写回交付形态：无回写能力的格式走 xlsx，其余走原格式
func TestWritebackDelivery(t *testing.T) {
	xlsxFormats := []string{".srt", ".vtt", ".json", ".yaml", ".yml"}
	for _, ext := range xlsxFormats {
		if got := writebackDelivery(ext); got != "xlsx" {
			t.Errorf("writebackDelivery(%q) = %q, want \"xlsx\"", ext, got)
		}
	}
	inplaceFormats := []string{".pdf", ".docx", ".pptx", ".txt", ".csv", ".md", ".xlsx", ""}
	for _, ext := range inplaceFormats {
		if got := writebackDelivery(ext); got != "inplace" {
			t.Errorf("writebackDelivery(%q) = %q, want \"inplace\"", ext, got)
		}
	}
	// ★ P1a：图片格式经 OCR 投喂翻译，无原格式可原位回写，交付形态为 "image"（产出译文 .md）。
	imageFormats := []string{".png", ".jpg", ".jpeg", ".bmp", ".tif", ".tiff", ".webp", ".heic"}
	for _, ext := range imageFormats {
		if got := writebackDelivery(ext); got != "image" {
			t.Errorf("writebackDelivery(%q) = %q, want \"image\"", ext, got)
		}
	}
	// ★ P2：音频格式经 ASR 转写投喂翻译，译文是文本塞不回音轨，交付 "audio"（.srt + .md + 原音频）。
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
