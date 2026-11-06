package api

// ============ product_shape_gate_test.go · 职责说明 ============
// 一期产品面「同一个字段的出栈形状只有一种」的机械闸（★ 2026-11-06，e2e 抓到真缺陷后新立）。
//
// 缺陷本体（新加的 e2e/onboarding_guided.spec.ts 第一次跑就撞上）：
//   `store.Product.TargetLangs` 是库内那一列的 CSV 字符串，而它当时带着 `json:"target_langs"`
//   ⇒ `/api/product/list` 把 **"en"** 这种裸字符串吐了出去；
//   而 `/api/product/public` 走的是 `p.TargetLangList()` ⇒ 吐的是 **["en"]**。
//   同一个字段两副面孔，消费方（前端、SDK、手写脚本）必须各判一次；
//   更坏的是字符串 "en" 对 `langs.includes('n')` 这类调用**看起来还能用**——
//   它不报错，只是把"没选中的语种"判成"选中了"（AGENTS §一·5「列表类接口的分页与空值形态」
//   那一族在产品面的第二次发作）。
//
// 判据取向（三条，一条都不许省）：
//  ① **两个口各判一次**：list（卖家侧）与 public（访客侧）都必须出数组——只判 public 等于什么都没判，
//     因为当初正是 public 对、list 错；
//  ② **空态也必须是数组**：未声明语种的产品，两个口都要出 `[]`，不许出 `""`／`null`
//     （`[]` 让消费方一次判都不必做；null 与 "" 各要一处兜底，兜底迟早分叉）；
//  ③ **射程自证**：判据函数喂"字符串形态"必须判红——否则这条锁只是在读一个恒真的形状。
//
// 闸门：本文件（`go test ./internal/api/`）。修法在 store：CSV 列仍照存，
// 但序列化只走派生好的 `TargetLangsList []string`（唯一写点 `(*Product).fillLangsList`）。

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// targetLangsShape 从一段响应体里取 product.target_langs 的**JSON 类型**与值。
// 参数：body=接口响应体；返回：kind（array/string/number/bool/object/null/missing）与原文片段。
func targetLangsShape(t *testing.T, body map[string]interface{}) (string, interface{}) {
	t.Helper()
	p, ok := body["product"].(map[string]interface{})
	if !ok {
		// list 那一支把产品放在 products[0] 里
		if arr, ok2 := body["products"].([]interface{}); ok2 && len(arr) > 0 {
			p, _ = arr[0].(map[string]interface{})
		}
	}
	if p == nil {
		return "no-product", nil
	}
	v, present := p["target_langs"]
	if !present {
		return "missing", nil
	}
	switch v.(type) {
	case []interface{}:
		return "array", v
	case string:
		return "string", v
	case nil:
		return "null", nil
	default:
		return "other", v
	}
}

// arrayShapeProblem 是判据**本体**（返回问题描述，空串＝形状对）。
// 为什么返回而不是直接 t.Fatalf：真接口与合成件都要喂同一个函数（口径③），
// 而 t.Fatalf 走的是 runtime.Goexit——用它做"反证必须红"的判据会既打印红字又让 recover 拿到 nil，
// 于是这条射程自证自己就是那条"判据绿着而判据是坏的"（本批首跑真踩，写在这是为了下次别再试）。
// 参数：kind=形状名，v=原值，where=出口名；返回：问题描述（空串＝通过）。
func arrayShapeProblem(kind string, v interface{}, where string) string {
	if kind == "array" {
		return ""
	}
	return where + "：target_langs 出栈必须是数组（空也要 []），实得形态=" + kind +
		" ⇒ 消费方要各判一次；而字符串 \"en\" 对上 includes('n') 这类调用还看不出错"
}

// assertArrayShape 是给真接口用的薄包装：把"有问题"翻成 Fatalf。
func assertArrayShape(t *testing.T, kind string, v interface{}, where string) {
	t.Helper()
	if msg := arrayShapeProblem(kind, v, where); msg != "" {
		t.Fatalf("%s", msg)
	}
}

// TestProductTargetLangsShapeIsArrayOnBothFaces 卖家侧 list 与访客侧 public 必须同一种形状。
func TestProductTargetLangsShapeIsArrayOnBothFaces(t *testing.T) {
	srv, tok := newProductAPIServer(t)

	// 建一份**没声明语种**的产品：空态那一档先判（最容易出 null/"" 的就是这里）
	code := "shape-empty-" + strings.ReplaceAll(t.Name(), "/", "_")
	createBody := map[string]interface{}{"name": "形状闸-未声明", "code": code}
	raw, _ := json.Marshal(createBody)
	_, out := callProductAPI(t, srv, http.MethodPost, "/api/product/save", tok, string(raw))
	if out["success"] != true {
		t.Fatalf("建档失败，本锁无从判起：%#v", out)
	}
	id, _ := out["id"].(string)
	if id == "" {
		t.Fatalf("建档没回 id：%#v", out)
	}
	pv, _ := out["product"].(map[string]interface{})
	if pv == nil {
		t.Fatalf("建档响应里没有 product：%#v", out)
	}
	// save 那一支也是出栈面之一：它自己也必须出数组（三处出口一把尺子）
	kind, v := targetLangsShape(t, out)
	assertArrayShape(t, kind, v, "POST /api/product/save（未声明）")

	// ① 卖家侧列表
	_, list := callProductAPI(t, srv, http.MethodGet, "/api/product/list", tok, "")
	kind, v = targetLangsShapeList(t, list, id)
	assertArrayShape(t, kind, v, "GET /api/product/list（未声明）")

	// 声明两个语种后再判一次非空数组（顺序＝提交顺序，不许被排成字典序）
	updBody := map[string]interface{}{"id": id, "name": "形状闸-已声明", "target_langs": []string{"ja", "en", "ja", ""}}
	rawU, _ := json.Marshal(updBody)
	if _, o := callProductAPI(t, srv, http.MethodPost, "/api/product/save", tok, string(rawU)); o["success"] != true {
		t.Fatalf("声明语种失败：%#v", o)
	}
	_, list2 := callProductAPI(t, srv, http.MethodGet, "/api/product/list", tok, "")
	kind, v = targetLangsShapeList(t, list2, id)
	assertArrayShape(t, kind, v, "GET /api/product/list（已声明）")
	got, _ := v.([]interface{})
	if len(got) != 2 || got[0] != "ja" || got[1] != "en" {
		t.Fatalf("语种必须去重保序出栈（提交 ja,en,ja,\"\" ⇒ [ja,en]），实得 %#v", got)
	}

	// ② 访客侧公开口（当初它对、list 错 ⇒ 两个面必须各判一次）
	pcode, _ := list2["products"].([]interface{})[0].(map[string]interface{})["page_code"].(string)
	tcode := productGateTenantCode(t, list2, id)
	_, pub := callProductAPI(t, srv, http.MethodGet, "/api/product/public?tenant_code="+tcode+"&page_code="+pcode, "", "")
	kind, v = targetLangsShape(t, pub)
	assertArrayShape(t, kind, v, "GET /api/product/public（已声明）")
}

// targetLangsShapeList 从 list 响应里按 id 取那一份产品的 target_langs 形态。
func targetLangsShapeList(t *testing.T, body map[string]interface{}, id string) (string, interface{}) {
	t.Helper()
	arr, ok := body["products"].([]interface{})
	if !ok {
		t.Fatalf("list 响应里没有 products 数组：%#v", body)
	}
	for _, it := range arr {
		m, _ := it.(map[string]interface{})
		if m == nil || m["id"] != id {
			continue
		}
		v, present := m["target_langs"]
		if !present {
			return "missing", nil
		}
		switch v.(type) {
		case []interface{}:
			return "array", v
		case string:
			return "string", v
		case nil:
			return "null", nil
		}
		return "other", v
	}
	t.Fatalf("list 里找不到刚建的 %s（建档后立刻读不到＝写读不同源）", id)
	return "", nil
}

// productGateTenantCode 取该产品自己的租户段（公开口要用，不许从记账上下文猜，见 §29 P10）。
func productGateTenantCode(t *testing.T, body map[string]interface{}, id string) string {
	t.Helper()
	for _, it := range body["products"].([]interface{}) {
		m, _ := it.(map[string]interface{})
		if m != nil && m["id"] == id {
			s, _ := m["tenant_code"].(string)
			if s == "" {
				t.Fatalf("产品没有 tenant_code：%#v", m)
			}
			return s
		}
	}
	t.Fatalf("list 里找不到 %s", id)
	return ""
}

// TestProductTargetLangsShapeJudgeHasReach 口径③：判据函数必须能认出"字符串形态"是错的。
func TestProductTargetLangsShapeJudgeHasReach(t *testing.T) {
	bad := map[string]interface{}{"product": map[string]interface{}{"target_langs": "en"}}
	kind, v := targetLangsShape(t, bad)
	if kind != "string" {
		t.Fatalf("射程自证失败：坏形态应被识别为 string，实得 %s（值 %#v）", kind, v)
	}
	msg := arrayShapeProblem(kind, v, "合成件（坏形态）")
	if msg == "" {
		t.Fatal("射程自证失败：喂字符串形态时判据函数必须给出问题（否则这条锁只是在读一个恒真的形状）")
	}
	// 反向对照：数组形态必须**不**给问题，否则判据会一路红（同 §一·5「负向锁要配正向对照」）
	if m := arrayShapeProblem("array", []interface{}{"en"}, "合成件（好形态）"); m != "" {
		t.Fatalf("正向对照失败：数组形态被判成坏形状：%s", m)
	}
}
