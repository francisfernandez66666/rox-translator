// ============ 本文件职责中文说明 ============
// S2「结构页」接线闸门：`structure_v3.py` 真跑 → stdout → Go 的 `parseStructureJSON`，
// 每一腿各配一枚**反证**（★ 2026-10-28 计价与成本批，方案 §6.1）。
//
// 为什么必须有这一层（判据级代替不了守卫级，AGENTS §三）：
//
//	`structure_page_selftest.py` 只证明脚本自己吐的形状对；它能证明的边界止于 Python 进程。
//	而这条链真正的风险在**接缝**上——脚本吐的字节 Go 认不认、Go 认的时候有没有守住「零文本字段」、
//	脚本退回空结构时上层会不会当成"这页真没版面"。这三件只有让**真子进程**跑一遍才现形。
//
// 六条腿（各带反证）：
//
//	G1 接线绿：真跑 page ⇒ DetectStructure 拿到 4 块 / 1 表 / 合并单元格 / 阅读顺序
//	G2 零文本边界（守卫级）：摘掉脚本那道防御腿 ⇒ Go 侧必须判 BadInput（而不是把文本吃进来）
//	G3 阅读顺序真的来自重排：删掉脚本的 rows.sort ⇒ 顺序退回模型返回序（证明 G1 那条有射程）
//	G4 三档失败各报各的：BadInput / Unavailable / Timeout（墙钟）＋ Timeout（脚本内部预算）
//	G5 空结构不许当成功：脚本回 `{"blocks":[],"tables":[]}` ⇒ ErrStructureEmpty
//	G6 自证腿全集 + probe 读数：`STRUCTSELFTEST_ALL_OK=1`、LEG 集合等值、`layout=`/`table=` 现读现出
//
// 假 paddleocr = `testdata/structure_fake/paddleocr.py`（只认协议：构造吃 model_dir、
// predict 吃路径字符串、回 `Result.json`），本文件与 `structure_page_selftest.py` 共用同一份。
// ⚠️ 反证一律走**临时目录里的变异副本**，绝不改仓库里的真件（改真件再改回来，一旦中途 panic
//
//	留下的就是"脚本被改坏"的工作区，下一个人会以为产品坏了）。
//
// ============================================
package fileproc

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	structureScriptFile   = "structure_v3.py"
	structureSelftestFile = "structure_page_selftest.py"
	structureFakePkgDir   = "testdata/structure_fake"
)

// structWireLegs 自证腿的全集（★ 与 structure_page_selftest.py 的 @leg 注册**双向等值**）。
// 写成常量是刻意的：有人删掉一条腿时，Python 那边照样打 ALL_OK（少跑一条谁看得见？），
// 只有这里的集合等值会让闸门当场红。
var structWireLegs = []string{"L01", "L02", "L03", "L04", "L05", "L06", "L07", "L08"}

// ============ 夹具 ============

// structWirePy 取可用解释器，并**把它钉进 FILEPROC_PYTHON_BIN**。
// 为什么必须钉：`pyBin()` 只认生产 venv 与 PATH 上的 python3，而 `findPython()` 认这个 env。
// 不钉就会出现「探测用一个解释器、执行用另一个」——本机 venv 装了 Pillow 而系统 python3 没装时，
// 表现是"探针说结构腿就绪、page 却说模块找不到"，而那正是这条链最容易被误判成产品缺陷的形态。
func structWirePy(t *testing.T) string {
	t.Helper()
	py := findPython()
	if py == "" {
		t.Fatal("findPython() 返回空：本机没有可用 python3，S2 接线闸门完全跑不到 ⇒ 判红。" +
			"装 python3（含 Pillow）或用 FILEPROC_PYTHON_BIN 指到 ~/.venvs/langcross-fileproc/bin/python3" +
			"（§二 口径：只许补依赖，不许放宽或跳过）")
	}
	t.Setenv("FILEPROC_PYTHON_BIN", py)
	return py
}

// structWireFakePath 假 paddleocr 所在目录的绝对路径（子进程按 PYTHONPATH 找它）。
func structWireFakePath(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(structureFakePkgDir)
	if err != nil {
		t.Fatalf("解析假件目录失败：%v", err)
	}
	if _, err := os.Stat(filepath.Join(abs, "paddleocr.py")); err != nil {
		t.Fatalf("假 paddleocr 不在 %s：%v ⇒ 接线闸门没有靶子（不许改成 skip）", abs, err)
	}
	return abs
}

// structWireEnv 子进程可见的全部形状开关：假件路径 + 脚本路径 + 两路权重目录 + 开关。
// 每次都从零设定，避免上一条腿的 env 泄漏到下一条（`STRUCT_FAKE_*` 一旦残留，
// 反证的形态会串到正测里，读数就没人信了）。
func structWireEnv(t *testing.T, script string, layoutDir, tableDir string) {
	t.Helper()
	pp := structWireFakePath(t)
	if old := os.Getenv("PYTHONPATH"); old != "" {
		pp = pp + string(os.PathListSeparator) + old
	}
	t.Setenv("PYTHONPATH", pp)
	t.Setenv("STRUCTURE_SCRIPT", script)
	t.Setenv("OCR_STRUCTURE", "on")
	t.Setenv("STRUCTURE_LAYOUT_MODEL_DIR", layoutDir)
	t.Setenv("STRUCTURE_TABLE_MODEL_DIR", tableDir)
	for _, k := range []string{
		"STRUCT_FAKE_TEXT_KEY", "STRUCT_FAKE_BAD_SHAPE", "STRUCT_FAKE_SLEEP_MS",
		"STRUCT_FAKE_TABLE_MODE", "STRUCTURE_PAGE_BUDGET_SEC",
	} {
		t.Setenv(k, "")
	}
}

// structWireWeights 造分档权重树（layout/ 与 table/ 各一份 0 字节 .onnx）。
// 空文件是够用的：假件只校验 `model_dir` 是目录，真权重不参与任何判定（自证问的是形状，不是精度）。
func structWireWeights(t *testing.T) (layoutDir, tableDir string) {
	t.Helper()
	root := t.TempDir()
	layoutDir = filepath.Join(root, "layout")
	tableDir = filepath.Join(root, "table")
	for _, d := range []string{layoutDir, tableDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("建权重目录 %s 失败：%v", d, err)
		}
		if err := os.WriteFile(filepath.Join(d, "inference.onnx"), nil, 0o644); err != nil {
			t.Fatalf("落权重 %s 失败：%v", d, err)
		}
	}
	return layoutDir, tableDir
}

// structWirePNG 用标准库现造一张 PNG（不依赖 Pillow 造图：造图能力不该成为闸门的前提）。
func structWirePNG(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 500, 400))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	p := filepath.Join(t.TempDir(), "page.png")
	f, err := os.Create(p)
	if err != nil {
		t.Fatalf("建 PNG 失败：%v", err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("写 PNG 失败：%v", err)
	}
	return p
}

// structWireScriptVariant 把仓库里的脚本拷成变异副本并返回其路径。
// mutate=nil ⇒ 逐字副本（反证的**对照腿**：同一份夹具喂原稿必须绿，才证明红是变异带来的）。
func structWireScriptVariant(t *testing.T, mutate func(string) string) string {
	t.Helper()
	src, err := os.ReadFile(structureScriptFile)
	if err != nil {
		t.Fatalf("读不到 %s：%v ⇒ 脚本必须随仓库提交，本闸门才有被锁的对象（不许改成 skip）", structureScriptFile, err)
	}
	body := string(src)
	if mutate != nil {
		body = mutate(body)
		if body == string(src) {
			// 变异没落到任何字节上 ⇒ 这条反证是空转的（它"证明"的只是原稿本来就绿）。
			t.Fatal("变异副本与原文逐字相同：反证的替换串已经漂了，请按当前源码重挑锚点")
		}
	}
	dir := t.TempDir()
	p := filepath.Join(dir, structureScriptFile)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatalf("写变异副本失败：%v", err)
	}
	return p
}

// structWireRun 跑一次真实子进程（本文件唯一的执行口）。
// 直接跑脚本而不是绕 Go 侧，是为了让**反证**也能拿到原始产物（接线绿那条才走 DetectStructure）。
func structWireRun(t *testing.T, py string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, py, args...)
	var ob, eb strings.Builder
	cmd.Stdout, cmd.Stderr = &ob, &eb
	err := cmd.Run()
	code = 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("执行 %v 失败：%v（stdout=%s stderr=%s）", args, err, ob.String(), eb.String())
	}
	stdout, stderr = ob.String(), eb.String()
	// 依赖缺失要**点名的判红**：否则「本机 python 没装 Pillow」会被读成「结构页形状不对」。
	if strings.Contains(strings.ToLower(stdout+stderr), "no module named") {
		t.Fatalf("解释器 %s 的依赖缺失 ⇒ 判红不判通过（§二：只许补依赖，不许放宽或跳过）。\nstdout=%s\nstderr=%s",
			py, stdout, stderr)
	}
	return stdout, stderr, code
}

// ============ G1 接线绿 ============

// TestStructureWireGreen 真子进程 → Go 解析：块/表/合并单元格/阅读顺序逐条对齐。
func TestStructureWireGreen(t *testing.T) {
	py := structWirePy(t)
	_ = py
	layoutDir, tableDir := structWireWeights(t)
	structWireEnv(t, structureScriptFile, layoutDir, tableDir)
	t.Setenv("STRUCT_FAKE_TABLE_MODE", "merged") // 首行跨两列：钉住 rowspan/colspan 的映射
	img := structWirePNG(t)

	doc, err := DetectStructure(context.Background(), img)
	if err != nil {
		t.Fatalf("结构页应成功，实际 %v ⇒ 脚本与 Go 解析器的接线断了（先看 G4 的 BadInput 是否也绿）", err)
	}
	if len(doc.Blocks) != 4 {
		t.Fatalf("版面块应为 4，实际 %d（%+v）", len(doc.Blocks), doc.Blocks)
	}
	labels := map[string]bool{}
	for _, b := range doc.Blocks {
		labels[b.Label] = true
	}
	for _, want := range []string{"title", "figure", "table", "text"} {
		if !labels[want] {
			t.Fatalf("缺 %q 这一块（tags=%v）⇒ label 映射漏了", want, labels)
		}
	}
	// 阅读顺序：假件刻意乱序返回，order 必须按 (y, x) 重排。
	ordered := append([]StructureBlock(nil), doc.Blocks...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Order < ordered[j].Order })
	var order []string
	for _, b := range ordered {
		order = append(order, b.Label)
	}
	if got, want := strings.Join(order, ","), "title,figure,table,text"; got != want {
		t.Fatalf("阅读顺序应为 %s，实际 %s ⇒ 按 (y, x) 重排没生效（见 G3 反证）", want, got)
	}
	if ordered[0].Box != [4]float64{10, 20, 300, 60} {
		t.Fatalf("title 的框应为 [10 20 300 60]，实际 %v ⇒ 框坐标在接缝上被改写了", ordered[0].Box)
	}

	if len(doc.Tables) != 1 {
		t.Fatalf("应恰好解析出 1 张表，实际 %d", len(doc.Tables))
	}
	tb := doc.Tables[0]
	if tb.Rows != 2 || tb.Cols != 2 {
		t.Fatalf("表格网格应为 2x2，实际 %dx%d", tb.Rows, tb.Cols)
	}
	if tb.Box != [4]float64{10, 120, 400, 220} {
		t.Fatalf("表格框应沿用版面那一块，实际 %v", tb.Box)
	}
	if len(tb.Cells) != 3 {
		t.Fatalf("logic_points 档应有 3 个单元格，实际 %d", len(tb.Cells))
	}
	first := tb.Cells[0]
	if first.Row != 0 || first.Col != 0 || first.RowSpan != 1 || first.ColSpan != 2 {
		t.Fatalf("首行跨两列必须解成 rowspan=1/colspan=2，实际 %+v", first)
	}
	if first.Box != [4]float64{0, 0, 200, 20} {
		t.Fatalf("单元格框应同序映射，实际 %v", first.Box)
	}
}

// ============ G2 零文本边界（三层，逐层反证） ============

// TestStructureWireRejectsTextKey 「只取结构不取识别」在这条链上有三道闸，逐道给一枚读数：
//
//	① **映射层白名单**：模型框里带着 `text` 键时，`_layout_rows` 只挑 label/box/score ⇒ 出栈天然干净；
//	② **脚本防御腿**：万一哪天有人把模型结果整段透传，`_has_text_key(out)` 必须当场报 Unavailable；
//	③ **Go 解析器**：即便脚本把文本吐出来，`parseStructureJSON` 也必须判 BadInput。
//
// 三道里任何一道单独存在都不够：①是常态路径（最常被改动的就是它），②只在①被绕过时才起作用
// （所以必须**人为绕过**才验得到），③是最后一道——它才是"客户屏幕上不会出现模型原话"的保证。
// 只测①的写法会让②③常年空转（脚本压根不吐文本，红不了），本仓把这种锁叫"没射程的绿灯"。
func TestStructureWireRejectsTextKey(t *testing.T) {
	structWirePy(t)
	layoutDir, tableDir := structWireWeights(t)
	img := structWirePNG(t)

	t.Run("映射层白名单挡在源头", func(t *testing.T) {
		structWireEnv(t, structureScriptFile, layoutDir, tableDir)
		t.Setenv("STRUCT_FAKE_TEXT_KEY", "1")
		stdout, stderr, code := structWireRun(t, structWirePy(t), structureScriptFile, "page", img)
		if code != 0 {
			t.Fatalf("模型带 text 键时结构页仍应成功（白名单是常态路径，不该让整单降级），exit=%d stderr=%s",
				code, stderr)
		}
		if hits := structWireTextKeys(t, stdout); len(hits) > 0 {
			t.Fatalf("出栈 JSON 里出现了文本键 %v ⇒ 映射层白名单漏了（模型原话会被带到客户屏幕）", hits)
		}
	})

	t.Run("脚本防御腿挡住透传", func(t *testing.T) {
		// 模拟"有人把模型结果整段透传"：出栈 dict 里真带上 text 键，防御腿必须当场拒绝。
		passthrough := structWireScriptVariant(t, func(s string) string {
			return strings.Replace(s,
				`    out = {"blocks": blocks, "tables": tables}`,
				`    out = {"blocks": blocks, "tables": tables, "text": "模型原话"}`, 1)
		})
		structWireEnv(t, passthrough, layoutDir, tableDir)
		_, err := DetectStructure(context.Background(), img)
		if !errors.Is(err, ErrStructureUnavailable) {
			t.Fatalf("出栈带 text 键时脚本必须自报 Unavailable，实际 %v ⇒ 防御腿被摘掉/写成恒假了", err)
		}
	})

	t.Run("Go解析器挡住越界字节", func(t *testing.T) {
		// 防御腿 + 白名单都不动，只把防御腿本身关掉：文本真的到解析器面前，Go 侧必须判 BadInput。
		leaky := structWireScriptVariant(t, func(s string) string {
			body := strings.Replace(s,
				`    out = {"blocks": blocks, "tables": tables}`,
				`    out = {"blocks": blocks, "tables": tables, "text": "模型原话"}`, 1)
			return strings.Replace(body, "    if _has_text_key(out):", "    if False and _has_text_key(out):", 1)
		})
		structWireEnv(t, leaky, layoutDir, tableDir)
		stdout, _, code := structWireRun(t, structWirePy(t), leaky, "page", img)
		if code != 0 {
			t.Fatalf("防御腿被关掉后脚本应正常退出（好让文本真到解析器面前），exit=%d", code)
		}
		if hits := structWireTextKeys(t, stdout); len(hits) == 0 {
			t.Fatalf("变异副本的 stdout 里没有文本键 ⇒ 这条反证形态没造出来（夹具漂了）：%s", stdout)
		}
		if _, err := DetectStructure(context.Background(), img); !errors.Is(err, ErrStructureBadInput) {
			t.Fatalf("带 text 键的结构必须判 BadInput，实际 %v ⇒ Go 侧零文本边界被摘掉/放宽了"+
				"（那等于把识别结果从后门带进来，换掉统一口径）", err)
		}
	})
}

// structWireTextKeys 独立地递归列出 JSON 里的文本键名（**只问键不问值**）。
// 刻意不复用产品侧的 hasTextKey：这条腿要判的是"脚本吐的字节干不干净"，
// 用被测实现去判被测产物，改坏一处两边一起坏、读数照样绿。
// ⚠️ 也不许做子串匹配：版面块的 `"label":"text"` 是**合法值**，按子串判会把它误报成文本键（本批真踩）。
func structWireTextKeys(t *testing.T, raw string) []string {
	t.Helper()
	var v any
	line := strings.TrimSpace(raw)
	if i := strings.Index(line, "{"); i > 0 {
		line = line[i:]
	}
	if err := json.Unmarshal([]byte(line), &v); err != nil {
		t.Fatalf("脚本 stdout 不是合法 JSON：%v（%q）", err, raw)
	}
	var hits []string
	var walk func(any)
	walk = func(x any) {
		switch y := x.(type) {
		case map[string]any:
			for k, child := range y {
				switch strings.ToLower(k) {
				case "text", "rec_text", "rec_texts":
					hits = append(hits, k)
				}
				walk(child)
			}
		case []any:
			for _, child := range y {
				walk(child)
			}
		}
	}
	walk(v)
	return hits
}

// ============ G3 阅读顺序反证 ============

// TestStructureWireOrderIsReranked 删掉 `rows.sort` ⇒ 顺序退回模型返回序。
// 这条腿证明 G1 的顺序断言**有射程**：不是"随便什么顺序都能通过"。
func TestStructureWireOrderIsReranked(t *testing.T) {
	structWirePy(t)
	layoutDir, tableDir := structWireWeights(t)
	img := structWirePNG(t)

	mutated := structWireScriptVariant(t, func(s string) string {
		return strings.Replace(s, "    rows.sort(key=lambda r: (r[\"box\"][1], r[\"box\"][0]))\n", "", 1)
	})
	structWireEnv(t, mutated, layoutDir, tableDir)
	doc, err := DetectStructure(context.Background(), img)
	if err != nil {
		t.Fatalf("删掉重排不该让整条链失败（它只影响顺序），实际 %v", err)
	}
	ordered := append([]StructureBlock(nil), doc.Blocks...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Order < ordered[j].Order })
	var order []string
	for _, b := range ordered {
		order = append(order, b.Label)
	}
	got := strings.Join(order, ",")
	if got == "title,figure,table,text" {
		t.Fatalf("删掉重排后顺序仍与期望一致 ⇒ G1 的顺序断言没有射程（判据失效，不是产品变好）")
	}
	if want := "text,table,title,figure"; got != want {
		t.Fatalf("删掉重排后应退回模型返回序 %s，实际 %s ⇒ 变异锚点漂了（当前脚本的重排语句改了形状）", want, got)
	}
}

// ============ G4 三档失败 ============

// TestStructureWireFailureVariants 四档映射各一条：BadInput / Unavailable / Timeout×2。
// 混成一档的后果：上层分不清"该换权重"（Unavailable）与"这台机器太慢"（Timeout），
// 前者会有人去修，后者只会被当成偶发抖动放过。
func TestStructureWireFailureVariants(t *testing.T) {
	structWirePy(t)
	layoutDir, tableDir := structWireWeights(t)
	img := structWirePNG(t)

	t.Run("坏图判BadInput", func(t *testing.T) {
		structWireEnv(t, structureScriptFile, layoutDir, tableDir)
		_, err := DetectStructure(context.Background(), filepath.Join(t.TempDir(), "nope.png"))
		if !errors.Is(err, ErrStructureBadInput) {
			t.Fatalf("不存在的图片应判 BadInput，实际 %v", err)
		}
	})

	t.Run("缺权重判Unavailable并点名", func(t *testing.T) {
		// 无 env 覆盖 + 副本旁没有 assets/structure ⇒ 走"缺哪个报哪个"那一支。
		dead := structWireScriptVariant(t, nil)
		structWireEnv(t, dead, "", "")
		_, err := DetectStructure(context.Background(), img)
		if !errors.Is(err, ErrStructureUnavailable) {
			t.Fatalf("缺权重应判 Unavailable，实际 %v ⇒ 缺权重被当成坏图/超时，排障会指错方向", err)
		}
		stderr := structWirePageStderr(t, dead, img)
		if !strings.Contains(stderr, "layout") {
			t.Fatalf("缺权重必须点名缺的是哪一路，stderr=%q", stderr)
		}
	})

	t.Run("墙钟超时判Timeout", func(t *testing.T) {
		structWireEnv(t, structureScriptFile, layoutDir, tableDir)
		t.Setenv("STRUCTURE_TIMEOUT_SEC", "1")
		t.Setenv("STRUCT_FAKE_SLEEP_MS", "2500")
		start := time.Now()
		_, err := DetectStructure(context.Background(), img)
		if !errors.Is(err, ErrStructureTimeout) {
			t.Fatalf("超出 STRUCTURE_TIMEOUT_SEC 应判 Timeout，实际 %v（耗时 %s）", err, time.Since(start).Round(time.Millisecond))
		}
	})

	t.Run("脚本内部预算判Timeout", func(t *testing.T) {
		structWireEnv(t, structureScriptFile, layoutDir, tableDir)
		t.Setenv("STRUCTURE_PAGE_BUDGET_SEC", "0.0001")
		t.Setenv("STRUCT_FAKE_SLEEP_MS", "30")
		_, err := DetectStructure(context.Background(), img)
		if !errors.Is(err, ErrStructureTimeout) {
			t.Fatalf("脚本自报 ERR:Structure:Timeout 必须映成 ErrStructureTimeout，实际 %v ⇒ stderr 到哨兵错误的映射漏了"+
				"（那会把\"慢\"归到 Unavailable 那一档，运维去换权重）", err)
		}
	})
}

// structWirePageStderr 跑一次 page 拿 stderr（只看失败文案时用；判据不依赖它，只做排障读数）。
func structWirePageStderr(t *testing.T, script, img string) string {
	t.Helper()
	_, stderr, _ := structWireRun(t, structWirePy(t), script, "page", img)
	return stderr
}

// ============ G5 空结构不许当成功 ============

// TestStructureWireEmptyIsNotSuccess 脚本回 `{"blocks":[],"tables":[]}` ⇒ ErrStructureEmpty。
// 为什么单列一腿：空结构在协议上"合法"，而在语义上不可能——一页连一个块都没有，
// 只可能是识别没跑/模型坏了。把它当成功，上层就会认为"这页没版面"，然后把整页的结构信息丢掉。
func TestStructureWireEmptyIsNotSuccess(t *testing.T) {
	structWirePy(t)
	layoutDir, tableDir := structWireWeights(t)
	img := structWirePNG(t)

	mutated := structWireScriptVariant(t, func(s string) string {
		return strings.Replace(s,
			`    out = {"blocks": blocks, "tables": tables}`,
			`    out = {"blocks": [], "tables": []}`, 1)
	})
	structWireEnv(t, mutated, layoutDir, tableDir)
	_, err := DetectStructure(context.Background(), img)
	if !errors.Is(err, ErrStructureEmpty) {
		t.Fatalf("空结构必须判 ErrStructureEmpty，实际 %v ⇒ \"没做完/没跑到\"会被伪装成\"这页没版面\"", err)
	}
}

// ============ G6 自证腿全集 + probe 读数 ============

// TestStructureSelftestLegSet 跑 `structure_page_selftest.py`：判绿只认末行
// `STRUCTSELFTEST_ALL_OK=1`，且 LEG 集合必须与 `structWireLegs` **等值**。
// 集合等值防的是"少跑一条腿"这种静默退化：Python 侧删掉一条腿照样会打 ALL_OK。
func TestStructureSelftestLegSet(t *testing.T) {
	py := structWirePy(t)
	structWireEnv(t, structureScriptFile, "", "")
	stdout, stderr, code := structWireRun(t, py, structureSelftestFile)
	if code != 0 {
		t.Fatalf("自证脚本应退 0，实际 %d。\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "STRUCTSELFTEST_ALL_OK=1") {
		t.Fatalf("判绿只认末行 STRUCTSELFTEST_ALL_OK=1，实际输出：\n%s", stdout)
	}
	ok := map[string]bool{}
	for _, line := range strings.Split(stdout, "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && f[0] == "LEG" && f[2] == "OK" {
			ok[f[1]] = true
		}
		if len(f) >= 3 && f[0] == "LEG" && f[2] == "FAIL" {
			t.Fatalf("自证腿 %s 判红：%s", f[1], line)
		}
	}
	var missing, extra []string
	for _, id := range structWireLegs {
		if !ok[id] {
			missing = append(missing, id)
		}
	}
	want := map[string]bool{}
	for _, id := range structWireLegs {
		want[id] = true
	}
	for id := range ok {
		if !want[id] {
			extra = append(extra, id)
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("自证腿集合与 Go 侧台账不等值：缺 %v / 多 %v ⇒ 有人删了腿（ALL_OK 照样绿）或加了腿没登记",
			missing, extra)
	}
}

// TestStructureProbeReadings probe 的 `layout=`/`table=`/`models=` 必须**现读现出**，
// 且与 Go 侧结构体逐字对上；缺权重时必须是 MISSING 而不是 OK。
func TestStructureProbeReadings(t *testing.T) {
	structWirePy(t)
	// 造一个"脚本旁边真有分档权重"的部署形态（probe 的 models= 数的是脚本同级 assets/structure）。
	dir := t.TempDir()
	script := filepath.Join(dir, structureScriptFile)
	body, err := os.ReadFile(structureScriptFile)
	if err != nil {
		t.Fatalf("读不到脚本：%v", err)
	}
	if err := os.WriteFile(script, body, 0o755); err != nil {
		t.Fatalf("写脚本副本失败：%v", err)
	}
	for kind := range map[string]bool{"layout": true, "table": true} {
		d := filepath.Join(dir, "assets", "structure", kind)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("建权重目录失败：%v", err)
		}
		if err := os.WriteFile(filepath.Join(d, "inference.onnx"), nil, 0o644); err != nil {
			t.Fatalf("落权重失败：%v", err)
		}
	}
	// 无 env 覆盖：让脚本按"脚本同级 assets/structure"自己解析（那才是部署机形态）。
	structWireEnv(t, script, "", "")

	p := ProbeStructure(context.Background())
	if !p.OK {
		t.Fatalf("分档权重齐备时 probe 应 OK，实际 %+v（reason=%q）", p, p.Reason)
	}
	if p.Models != 2 {
		t.Fatalf("分档形态应数到 2 份权重，实际 %d ⇒ 计数只扫了顶层（分档部署会被读成\"没权重\"）", p.Models)
	}
	if p.Layout != "layout" || p.Table != "table" {
		t.Fatalf("layout/table 读数应为 layout/table，实际 %q/%q ⇒ 排障时答不出\"这台加载的是哪份权重\"",
			p.Layout, p.Table)
	}

	t.Run("未知键不许把解析打坏", func(t *testing.T) {
		q := parseStructureProbe("OK: version=9.9 future=x models=3 layout=L table=T\n")
		if !q.OK || q.Models != 3 || q.Layout != "L" || q.Table != "T" {
			t.Fatalf("多出来的读数键应被忽略、已知键照常解析，实际 %+v", q)
		}
		// 旧版脚本只打 version/models ⇒ 两个新字段留空，但不许把 OK 判成未就绪。
		old := parseStructureProbe("OK: version=3.2.0 models=2\n")
		if !old.OK || old.Models != 2 || old.Layout != "" || old.Table != "" {
			t.Fatalf("旧版 probe 行必须仍判就绪，实际 %+v", old)
		}
	})
}
