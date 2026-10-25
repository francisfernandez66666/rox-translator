// ============ api_openapi_artifact_name_gate_test.go · 职责说明 ============
// 对外契约三面（JSON 规范／中文文档／英文文档）里「译文产物文件名带 `-<lang>`」这条的等值锁
// （★ 2026-10-25 O-03 收口批，AGENTS §一·14「三面同改，缺一面就是客户照文档发却被判不支持」）。
//
// 为什么这条值得单独一把锁，而不是"文档改过了"就算完：
//
//	产物文件名是**客户端唯一能拿到语种归属**的地方——多语种任务一次下整包 zip 时，
//	八个语种若都叫 `手册.docx`，解压后端序覆盖，客户看到的是"少给了几份译文"，
//	而服务端每一行都是成功的。所以这一句写在哪几面、有没有写全，是对外语义问题不是排版问题。
//
// 三面读的是**源码**而不是运行时展开结果：这一族最坏的形态是"代码里真拼了后缀、
// 文档里那行没写"，运行时判据对它是盲的（接口照常 200，客户端照常把同名文件叠在一起）。
//
// 五条腿（每一面**点名自己要携带的锚点**，判据函数只有一份）：
//
//	A1 三面源码里都必须出现锚点 `-<lang>`（逐面点名，缺一面即红）；
//	A2 中文面必须交代"后缀取产物真实 basename"（客户端不必自己拼），英文面同义；
//	A3 应急回退档 `artifact_lang_suffix` 只要求**运维看得见的那一面**（中文文档页）点名——
//	   它是服务端开关，不是客户端契约，写进 JSON 规范反而会把"应急档"当默认形态推给客户；
//	A4 射程自证：合法文本放行、摘掉任一锚点的副本必须判缺该锚点（防止把判据写成恒真）；
//	A5 **切面射程**自证：两面各占一份原始字符串块，且中文锚点不许出现在英文块、反之亦然
//	   （没有这一条，"两面"其实是同一份文件全文，摘掉中文面那行、英文面同串还在，判据恒绿——
//	    本批真踩第一次就是这种假绿）。
//
// ⚠️ 加文档行时的形状约束：`defaultDocsMDZh`／`defaultDocsMDEn` 是**反引号原始字符串**，
//
//	块内出现一个裸反引号就把 Go 文件切成语法错（本批第一次改完 `go build` 当场报
//	`syntax error: unexpected <, expected expression`）。这一族的行内代码一律写成裸文本。
//
// 方言：本文件不碰库。
// 运行：go test ./internal/api/ -run 'TestOpenAPIArtifactName|TestOpenAPIDocsFaces'
// =============================================
package api

import (
	"os"
	"strings"
	"testing"
)

// artifactNameAnchor 三面共用的锚点字面量（写文档时不许换成同义改写：判据按这一串取）。
const artifactNameAnchor = "-<lang>"

// artifactNameZhAnchor 中文面的语义锚点：后缀来自产物真实名字（不是客户端自己拼的）。
const artifactNameZhAnchor = "真实 basename"

// artifactNameEnAnchor 英文面的同义锚点。
const artifactNameEnAnchor = "real basename"

// artifactNameFallbackKey 应急回退档的配置键名（只要求中文文档页面点名）。
const artifactNameFallbackKey = "artifact_lang_suffix"

// 两面内置文档的起始锚（Golang 原始字符串字面量，块内不得出现裸反引号）。
const (
	docsZhBlockStart = "const defaultDocsMDZh = `"
	docsEnBlockStart = "const defaultDocsMDEn = `"
)

// openAPIDocsFaceText 从 admin_openapi.go 全文里切出**一面**内置文档的正文。
// 参数 t: 测试上下文；whole: 文件全文；startMarker: 该面 `const … = ` 的起始锚。
// 返回: 该面原始字符串里的正文（不含首尾反引号）。
//
// 为什么必须切面而不是拿整份文件当"两面"：两面在**同一个 Go 文件**里，
// 整份文件做靶子时"中文面写了、英文面没写"与"两面都写了"读数完全相同
// （本批实测：摘掉中文面的回退键，英文面那行同串还在全文件里，判据照样绿）。
// 三面等值锁的前提是**三面真的能被分别问到**。
func openAPIDocsFaceText(t *testing.T, whole, startMarker string) string {
	t.Helper()
	i := strings.Index(whole, startMarker)
	if i < 0 {
		t.Fatalf("找不到内置文档起始锚 %q ⇒ 文档常量被改名或搬走，本锁已失明", startMarker)
	}
	rest := whole[i+len(startMarker):]
	j := strings.Index(rest, "\n`\n")
	if j < 0 {
		t.Fatalf("文档块 %q 没找到结束反引号 ⇒ 原始字符串形状变了，本锁已失明", startMarker)
	}
	return rest[:j]
}

// artifactNameMissing 判据本体（A1–A3 与 A4 自证共用同一份，禁止再抄第二把尺子）。
// 参数 anchors＝**调用方点名的这一面必须携带的锚点**；返回缺失清单（空＝这一面写全了）。
// 锚点集合放在调用方而不是函数里的 switch，是因为三面的读者不同（规范给客户、文档页给开发、
// 应急档给运维）；判据形态本身仍然只有一份，新增一面只要在自己的清单里点名。
func artifactNameMissing(text string, anchors ...string) []string {
	var miss []string
	for _, a := range anchors {
		if !strings.Contains(text, a) {
			miss = append(miss, a)
		}
	}
	return miss
}

// TestOpenAPIDocsFacesAreSeparateBlocks 切面射程自证：两面各自必须能独占一句本面文案。
// 摘掉这一条，上面的等值锁就退化成"整份文件里出现过就算两面都写了"。
func TestOpenAPIDocsFacesAreSeparateBlocks(t *testing.T) {
	docs, err := os.ReadFile("admin_openapi.go")
	if err != nil {
		t.Fatalf("读 admin_openapi.go 失败: %v", err)
	}
	zh := openAPIDocsFaceText(t, string(docs), docsZhBlockStart)
	en := openAPIDocsFaceText(t, string(docs), docsEnBlockStart)
	if len(zh) == 0 || len(en) == 0 {
		t.Fatalf("文档面切出空正文 zh=%d en=%d ⇒ 起始锚或结束锚不成立", len(zh), len(en))
	}
	// 逐面等值：中文面的说明在中文块里、英文面的说明在英文块里，且**互不越界**。
	if !strings.Contains(zh, artifactNameZhAnchor) {
		t.Error("中文文档块里没有「真实 basename」⇒ 切面切错了或中文面确实缺这句")
	}
	if strings.Contains(zh, artifactNameEnAnchor) {
		t.Errorf("中文文档块里出现英文锚点 %q ⇒ 两面被切成同一份文本，等值锁失去意义", artifactNameEnAnchor)
	}
	if !strings.Contains(en, artifactNameEnAnchor) {
		t.Error("英文文档块里没有「real basename」⇒ 切面切错了或英文面确实缺这句")
	}
	if strings.Contains(en, artifactNameZhAnchor) {
		t.Errorf("英文文档块里出现中文锚点 %q ⇒ 两面被切成同一份文本", artifactNameZhAnchor)
	}
}

// TestOpenAPIArtifactNameDocumentedOnAllThreeFaces 三面等值锁（A1/A2/A3）。
func TestOpenAPIArtifactNameDocumentedOnAllThreeFaces(t *testing.T) {
	spec, err := os.ReadFile("openapi.v1.json")
	if err != nil {
		t.Fatalf("读 openapi.v1.json 失败: %v", err)
	}
	docs, err := os.ReadFile("admin_openapi.go")
	if err != nil {
		t.Fatalf("读 admin_openapi.go 失败: %v", err)
	}
	faces := []struct {
		name    string
		text    string
		anchors []string
	}{
		{"openapi.v1.json（规范）", string(spec),
			[]string{artifactNameAnchor, artifactNameZhAnchor}},
		{"admin_openapi.go 中文文档面", openAPIDocsFaceText(t, string(docs), docsZhBlockStart),
			[]string{artifactNameAnchor, artifactNameZhAnchor, artifactNameFallbackKey}},
		{"admin_openapi.go 英文文档面", openAPIDocsFaceText(t, string(docs), docsEnBlockStart),
			[]string{artifactNameAnchor, artifactNameEnAnchor}},
	}
	for _, f := range faces {
		if miss := artifactNameMissing(f.text, f.anchors...); len(miss) > 0 {
			t.Errorf("%s 缺产物文件名说明锚点 %v（三面缺一面＝客户照文档自己拼后缀、与实际产物打架）", f.name, miss)
		}
	}
}

// TestOpenAPIArtifactNameJudgeHasRange 射程自证（A4）：合法文本放行，摘掉锚点的副本必须判缺。
// 没有这一条，"三面全绿"与"判据恒真"是同一个读数（AGENTS §三 那条反证纪律）。
func TestOpenAPIArtifactNameJudgeHasRange(t *testing.T) {
	baseZh := "产物文件名带 " + artifactNameAnchor + "，后缀取产物真实 basename；应急回退见 " + artifactNameFallbackKey + "=0"
	baseEn := "artifacts carry " + artifactNameAnchor + "; the suffix comes from the artifact's real basename"
	zhAnchors := []string{artifactNameAnchor, artifactNameZhAnchor, artifactNameFallbackKey}
	enAnchors := []string{artifactNameAnchor, artifactNameEnAnchor}
	if len(artifactNameMissing(baseZh, zhAnchors...)) != 0 {
		t.Fatal("合法中文面被判缺 ⇒ 判据本身有缺陷，A1–A3 全绿不可信")
	}
	if len(artifactNameMissing(baseEn, enAnchors...)) != 0 {
		t.Fatal("合法英文面被判缺 ⇒ 判据本身有缺陷")
	}
	cuts := []struct {
		name    string
		text    string
		anchors []string
		want    string
	}{
		{"中文面摘掉锚点", strings.Replace(baseZh, artifactNameAnchor, "后缀", 1), zhAnchors, artifactNameAnchor},
		{"中文面摘掉 basename 说明", strings.Replace(baseZh, artifactNameZhAnchor, "名字", 1), zhAnchors, artifactNameZhAnchor},
		{"中文面摘掉回退键", strings.Replace(baseZh, artifactNameFallbackKey, "开关", 1), zhAnchors, artifactNameFallbackKey},
		{"英文面摘掉锚点", strings.Replace(baseEn, artifactNameAnchor, "suffix", 1), enAnchors, artifactNameAnchor},
		{"英文面摘掉 basename 说明", strings.Replace(baseEn, artifactNameEnAnchor, "name", 1), enAnchors, artifactNameEnAnchor},
	}
	for _, c := range cuts {
		got := artifactNameMissing(c.text, c.anchors...)
		if len(got) == 0 || got[0] != c.want {
			t.Errorf("变异「%s」没被抓到：期望判缺 %q，实得 %v", c.name, c.want, got)
		}
	}
}
