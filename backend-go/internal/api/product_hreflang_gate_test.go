// ============ 本文件职责中文说明 ============
// O 组（2026-11-06「注册登录后没产品就引导」）的两把判据：
//
//	W1 hreflang 只铺**卖家声明过**的那几语；未声明时保持站点全表。
//	W2 声明里全是脏码（站点根本不支持）时宁可铺全表，也不铺一串点了 404 的 hreflang。
//	W3 target_langs 的三态（nil＝没表态／空切片＝显式清空／非空＝整份替换）必须各判一次，
//	   这一条与 store 侧 TestProductTargetLangs* 配对：这里验的是 **api 出栈那一面**。
//
// 为什么"没声明就铺全表"不是偷懒：把空值读成"只支持中文"或"跟随访客界面语种"，
// 等于系统替客户决定他对外说哪些话——那是本仓「缺省不许冒充客户表态」那一族的反面。
// ========================================
package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"translator/internal/store"
)

// hreflangCodes 从直出面里把 hreflang 的 lang= 参数按出现顺序取出来（判据只问顺序与集合，不比整段 HTML）。
func hreflangCodes(t *testing.T, html string) []string {
	t.Helper()
	var out []string
	for _, seg := range strings.Split(html, `<link rel="alternate"`) {
		i := strings.Index(seg, "?lang=")
		if i < 0 {
			continue
		}
		rest := seg[i+len("?lang="):]
		j := strings.IndexAny(rest, `"'&<>`)
		if j > 0 {
			out = append(out, rest[:j])
		}
	}
	return out
}

func TestProductHreflangFollowsDeclaredLangs(t *testing.T) {
	// ★ 这里**刻意不调 loadSEOMeta()**：真值直接从磁盘上那份 embed 源文件现读。
	//   上一版让测试自己先 ensure，结果是把"产品侧忘了 ensure"这件事盖掉——
	//   变异体（摘掉 productHreflangLangs 里那行 ensure）跑出来 lock-stayed-green，
	//   而屏幕上几条判据全都"对得上"（空表 == 空表）。判据要吃自己的靶子，不能吃实现喂给它的。
	all := embeddedLangs(t)
	mk := func(csv string) store.Product {
		return store.Product{Status: store.ProductStatusLive, SeoIndexable: 1, TargetLangs: csv}
	}

	// W1a：没声明 ⇒ 全表（今天的形态一字不变，这条同时是"改动没顺手缩小覆盖面"的对照）
	if got := productHreflangLangs(mk("")); strings.Join(got, ",") != strings.Join(all, ",") {
		t.Fatalf("W1a：未声明必须保持站点全表，实得 %v", got)
	}

	// W1b：声明了子集 ⇒ 只铺那几语，且**保持提交顺序**（顺序给 hreflang 当主次，不许被字典序重排）
	// ★ 这里**不许 Skip**：Skip 与"判据空转"在屏幕上长得一模一样。
	//   码表短到没有这三个靶子＝本条判据失明，直接判红并点名，而不是留一条绿色的"没测到"。
	if len(all) < 4 {
		t.Fatalf("射程自证失败：站点码表只读到 %d 个语种 ⇒ hreflang 判据是空转（embed 副本没解出来？）", len(all))
	}
	for _, c := range []string{"ja", "en", "de"} {
		if !containsLang(all, c) {
			t.Fatalf("射程自证失败：码表里没有靶子 %q（现读 %v）", c, all)
		}
	}
	got := productHreflangLangs(mk("ja,en,de"))
	if strings.Join(got, ",") != "ja,en,de" {
		t.Fatalf("W1b：声明子集必须按提交顺序铺，实得 %v", got)
	}

	// W1c：声明里混了站点不支持的码 ⇒ 那一个不进（**不是**整份退回全表，因为其余是有效声明）
	got = productHreflangLangs(mk("ja,xx-nope,en"))
	if strings.Join(got, ",") != "ja,en" {
		t.Fatalf("W1c：不支持的码要单独剔除、有效声明保留，实得 %v", got)
	}

	// W2：全是脏码 ⇒ 退回全表（宁多铺也不铺一串 404）
	got = productHreflangLangs(mk("zz-1,zz-2"))
	if len(got) != len(all) {
		t.Fatalf("W2：声明全脏必须退回站点全表，实得 %v", got)
	}

	// W3：直出面验一次接线——hreflang 真的按声明铺，而不是函数对了、调用点还吃全表。
	srv, _ := newProductAPIServer(t)
	// 对外页是"后端直出的 SPA 壳"：不挂 dist 就必然 404，而这条判据测的是 hreflang 不是找壳
	srv.Dist = newProductDist(t)
	p, err := srv.Store.CreateProduct(1, store.ProductInput{Name: "整机", UserID: 1})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	langs := []string{"ja", "en"}
	if err := srv.Store.UpdateProductProfile(1, p.ID, store.ProductInput{TargetLangs: &langs}); err != nil {
		t.Fatalf("W3：写目标语种失败: %v", err)
	}
	if err := srv.Store.SetProductStatus(1, p.ID, store.ProductStatusLive); err != nil {
		t.Fatalf("W3：切 live 失败: %v", err)
	}
	row, ok, err := srv.Store.GetProductScoped(1, p.ID)
	if err != nil || !ok {
		t.Fatalf("W3：读回产品失败 ok=%v err=%v", ok, err)
	}
	if row.TargetLangs != "ja,en" {
		t.Fatalf("W3：库里 target_langs 必须是归一后的 \"ja,en\"，实得 %q", row.TargetLangs)
	}
	code0, html := callHTML(t, srv, "/p/"+row.TenantCode+"/"+row.PageCode)
	if code0 != 200 {
		t.Fatalf("W3：对外页直出应 200，实得 %d", code0)
	}
	codes := hreflangCodes(t, html)
	if strings.Join(codes, ",") != "ja,en" {
		t.Fatalf("W3：直出面 hreflang 必须只铺声明的两语（接线腿），实得 %v", codes)
	}

	// W3b：公开口出栈同样要带声明（前端语言切换只能读这一份，不许按界面语种猜）
	code, body := callProductAPI(t, srv, http.MethodGet,
		"/api/product/public?tenant_code="+row.TenantCode+"&page_code="+row.PageCode, "", "")
	if code != http.StatusOK {
		t.Fatalf("W3b：公开口应 200，实得 %d", code)
	}
	prod, _ := body["product"].(map[string]interface{})
	tl, _ := prod["target_langs"].([]interface{})
	if len(tl) != 2 || tl[0] != "ja" || tl[1] != "en" {
		t.Fatalf("W3b：公开口必须原样出栈声明语种（空也要出 []，不出 null），实得 %v", prod["target_langs"])
	}
}

// containsLang 判站点码表里有没有这个码（W1b 的靶子先要在位，否则那条判据是空转）。
func containsLang(list []string, code string) bool {
	for _, c := range list {
		if c == code {
			return true
		}
	}
	return false
}

// embeddedLangs 从磁盘现读 embed 的那份 seo_meta.json 取语种码表（不经过被测函数、也不触发它的懒加载）。
func embeddedLangs(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("seo_meta.json")
	if err != nil {
		t.Fatalf("读不到嵌入的 seo_meta.json（判据对象不在位）: %v", err)
	}
	var doc struct {
		Languages []string `json:"languages"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("解析语种码表失败: %v", err)
	}
	return doc.Languages
}
