// ============ promise_media_gate_test.go · 职责说明 ============
// 「挂件承诺的媒体格式」与「准入清单」的跨包等值锁（★ 2026-10-15，改造方案 §六 第 5 条）。
//
// 为什么要这条锁：
//
//	【承诺边界】第一段现在要模型直接对客户说清「图片与扫描件（png、jpg…）」「录音和音频（mp3、m4a…）」
//	能翻。这两串扩展名是**对外承诺**，而唯一的准入事实源是 `internal/fileproc/formats.go`。
//	两边一旦漂开，坏的方向是确定的两种，而且都很难看：
//	  · 清单加了 .gif 而承诺文案没加 ⇒ 客户上传得进去、系统也翻，但挂件会当着人面说"这个不支持"
//	    （功能存在却对外否认，本批刚把旧文里那句"不做识别提取"清掉，就是同一族缺陷的反面）；
//	  · 文案写了 .heic 而清单没有 ⇒ 客户被承诺一个建单当场拒收的格式（比错译更难收场）。
//	所以判据是**等值**，不是"承诺不许更宽"的单向锁（AGENTS §一·5：单向锁会把设计一路推离真值）。
//
// ★ 为什么不 import fileproc：assist 是自包含子服务（engine 只依赖 assist/llm、assist/store
//
//	与两个基础包），为了一条文案锁把整个文件处理层拉进它的依赖图是本末倒置。
//	所以这里读的是**源码文本**，判据函数是纯函数，反证喂的就是同一个函数。
//
// 三条判据：
//
//	M1 射程腿：formats.go 那两个 set 的字面量解析必须非空（锚点漂了要报「闸门已失效」，
//	   不许退化成"解析出空集合 ⇒ 与文案比出差集"那种把失明读成配置问题）。
//	M2 等值：承诺文案里那两串扩展名与清单**逐字等值**（顺序无关，集合相等）。
//	M3 结构腿：两串都必须各自存在（缺一串＝该媒体族从承诺里整体消失，M2 的集合差抓不到"整段没了"，
//	   因为空串与空集在只比差集时可能看起来"没问题"）。
//
// =============================================
package engine

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// mediaFormatListsFromFile 从 fileproc/formats.go 源码里解析图片与音频两个格式集合。
// 锚点＝`imageFormatSet`／`audioFormatSet` 那两处闭包字面量里的 `[]string{...}`。
// 返回值第三项是"解析是否成立"：不成立时调用方必须报射程失效，而不是拿空集合继续比。
func mediaFormatListsFromFile(src string) (image []string, audio []string, parsed bool) {
	pick := func(name string) []string {
		// 只在该 set 的构造函数体内取第一个 []string{…} 字面量
		i := strings.Index(src, "var "+name+" = func() map[string]bool {")
		if i < 0 {
			return nil
		}
		j := strings.Index(src[i:], "[]string{")
		if j < 0 {
			return nil
		}
		block := src[i+j:]
		end := strings.Index(block, "}")
		if end < 0 {
			return nil
		}
		re := regexp.MustCompile(`"(\.[a-z0-9]+)"`)
		var out []string
		for _, m := range re.FindAllStringSubmatch(block[:end], -1) {
			out = append(out, m[1])
		}
		return out
	}
	image, audio = pick("imageFormatSet"), pick("audioFormatSet")
	return image, audio, len(image) > 0 && len(audio) > 0
}

// promiseMediaExts 从【承诺边界】正文里抠出那两串扩展名（图片串与音频串）。
// 锚点是文案里那两句的括号内容：「图片与扫描件（png、jpg、…）里的文字…」／
// 「录音和音频文件（mp3、m4a、…）能转成文字再翻…」。
// 判 "png"/"jpg" 而不是 ".png"：对外文案不写点号，这是设计与清单形态的唯一差异，
// 归一在这里做一次，M2 比的就是纯集合。
func promiseMediaExts(promise string) (image []string, audio []string) {
	pick := func(lead string) []string {
		i := strings.Index(promise, lead)
		if i < 0 {
			return nil
		}
		rest := promise[i:]
		open := strings.Index(rest, "（")
		close := strings.Index(rest, "）")
		if open < 0 || close < 0 || close < open {
			return nil
		}
		var out []string
		for _, part := range strings.Split(rest[open+len("（"):close], "、") {
			if s := strings.TrimSpace(part); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return pick("图片与扫描件"), pick("录音和音频文件")
}

// sameSet 两个集合等值（忽略顺序与清单侧的前导点号）。
func sameSet(want []string, got []string) bool {
	norm := func(in []string) []string {
		out := make([]string, 0, len(in))
		for _, s := range in {
			out = append(out, strings.TrimPrefix(s, "."))
		}
		sort.Strings(out)
		return out
	}
	a, b := norm(want), norm(got)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPromiseMediaFormatsMatchAdmissionList(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "fileproc", "formats.go"))
	if err != nil {
		t.Fatalf("读不到 fileproc/formats.go（本锁的事实源换了位置）：%v", err)
	}
	imgList, audioList, parsed := mediaFormatListsFromFile(string(src))
	if !parsed { // M1 射程腿
		t.Fatalf("formats.go 的 imageFormatSet／audioFormatSet 字面量解析为空或不全（图片 %d 项／音频 %d 项）"+
			"⇒ 锚点漂了、本闸门已失效，去核两处 set 的构造函数形状，别把这条判据摘掉", len(imgList), len(audioList))
	}
	imgPromise, audioPromise := promiseMediaExts(defaultPromiseRules)
	if len(imgPromise) == 0 || len(audioPromise) == 0 { // M3 结构腿
		t.Fatalf("【承诺边界】里没找到那两串媒体扩展名（图片 %d 项／音频 %d 项）——整段消失比少一项更坏",
			len(imgPromise), len(audioPromise))
	}
	if !sameSet(imgList, imgPromise) { // M2 等值
		t.Fatalf("图片格式承诺与准入清单不等值：清单 %v ／ 文案 %v（清单加了文案没加＝挂件对外否认已有功能；"+
			"文案加了清单没有＝承诺一个建单会拒收的格式）", imgList, imgPromise)
	}
	if !sameSet(audioList, audioPromise) {
		t.Fatalf("音频格式承诺与准入清单不等值：清单 %v ／ 文案 %v", audioList, audioPromise)
	}
	t.Logf("✅ 媒体承诺与准入清单等值：图片 %d 项／音频 %d 项", len(imgList), len(audioList))
}

// TestPromiseMediaGateSelfProof 反证：同一组判据必须真的抓得住三种坏形态。
// 不配反证的话，上面的绿灯可能只是"解析出空集合 + 空集合相等"的摆设。
func TestPromiseMediaGateSelfProof(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "fileproc", "formats.go"))
	if err != nil {
		t.Fatalf("读不到 formats.go：%v", err)
	}
	imgList, audioList, parsed := mediaFormatListsFromFile(string(src))
	if !parsed {
		t.Fatal("射程自证的前置就失败：formats.go 解析为空")
	}
	// ① 清单加一项而文案没加 ⇒ 必须判不等值
	more := append(append([]string{}, imgList...), ".gif")
	if sameSet(more, imgList) {
		t.Fatal("清单多出一项却被判等值 ⇒ 集合比较失明")
	}
	// ② 文案少一项 ⇒ 同样要判死（这就是"对外否认功能"那一形态）
	imgPromise, audioPromise := promiseMediaExts(defaultPromiseRules)
	dropped := imgPromise[:len(imgPromise)-1]
	if sameSet(imgList, dropped) {
		t.Fatal("承诺文案少一项却被判等值 ⇒ 本锁抓不到对外否认")
	}
	// ③ 整段消失（把两句里的括号删掉）⇒ 走的是 M3 结构腿而不是差集
	mutated := strings.Replace(defaultPromiseRules, "图片与扫描件（", "图片与扫描件【", 1)
	if ip, _ := promiseMediaExts(mutated); len(ip) != 0 {
		t.Fatal("括号形态被换掉后仍解析出扩展名 ⇒ 锚点判据在吃别处的括号，M3 会漏判")
	}
	// 干净对照：原样必须过
	if !sameSet(imgList, imgPromise) || !sameSet(audioList, audioPromise) {
		t.Fatal("干净文本被判不等值 ⇒ 上面主判据的绿是运气")
	}
}
