// ============ structure_v3.go · 职责说明 ============
// PP-StructureV3 接入（S2）：只取版面/表格几何，**不取识别文本**。
//
// ★ 2026-10-28 计价与成本批：`structure_v3.py` 的 `page` 已接真实推理（版面 + 表格两档模型），
//
//	协议级自证见 `structure_page_selftest.py`（零权重、零 GPU）与 `structure_page_wire_gate_test.go`
//	（脚本真跑一遍、stdout 喂给下面的解析器）。
//	⚠️ 开关**仍然默认 off**：真机上 S0–S5 六条读数（权重体积/许可/耗时/内存/三档失败）没跑完之前
//	不许在这台机器上打开——本批完成的是"代码 + 协议级自证"那一半。
//
// 三条硬口径：
//  1. 默认关：`OCR_STRUCTURE=on|1|true` 才启用；未设/其它值一律视为关（配置面回滚，不换二进制）；
//  2. 降级明确：脚本/权重缺失 ⇒ ErrStructureUnavailable（调用方走几何重建，**不判失败**）；
//     空结构但没报错 ⇒ ErrStructureEmpty（空结构不可能是"这页没版面"）；
//  3. 输出零文本：解析时递归扫描任何 text/rec_text/rec_texts 键即判 BadInput——
//     那等于把识别结果从后门带进来，换掉我们刚统一的六条识别口径。
package fileproc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 结构腿的四种降级档（对外排障契约，改字必须同步单测与文档）。
var (
	ErrStructureUnavailable = errors.New("structure unavailable")
	ErrStructureTimeout     = errors.New("structure timeout")
	ErrStructureBadInput    = errors.New("structure bad input")
	ErrStructureEmpty       = errors.New("structure empty")
)

// StructureScriptEnv 覆盖脚本路径（开发/测试用，同其余脚本约定）。
const StructureScriptEnv = "STRUCTURE_SCRIPT"

// StructureEnabled 结构腿是否启用：只有显式 on/1/true 才开（默认关，等 spike 全绿）。
func StructureEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("OCR_STRUCTURE")))
	return v == "on" || v == "1" || v == "true"
}

// structureTimeout 结构腿墙钟预算（与 OCR 的 60s 整件预算互不占用）。
func structureTimeout() time.Duration {
	if v := strings.TrimSpace(os.Getenv("STRUCTURE_TIMEOUT_SEC")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 20 * time.Second
}

// structureScriptPath 定位 structure_v3.py（与可执行文件同目录）。
func structureScriptPath() string {
	if v := strings.TrimSpace(os.Getenv(StructureScriptEnv)); v != "" {
		return v
	}
	return filepath.Join(filepath.Dir(os.Args[0]), "structure_v3.py")
}

// structureAssetsDir 权重目录（相对脚本同目录，随 sync_fileproc_assets.sh 同步）。
func structureAssetsDir() string {
	return filepath.Join(filepath.Dir(structureScriptPath()), "assets", "structure")
}

// StructureProbe 结构腿的自检读数。
type StructureProbe struct {
	OK      bool   // probe 是否回 OK
	Version string // paddleocr 版本（OK 时非空）
	Models  int    // 权重份数（平铺与分档两种投放形态都算）
	// Layout / Table 是**实际会被推理用到的那个模型目录名**（★ 2026-10-28 S2）。
	// 排障时它回答的是「这台机器加载的是哪一份权重」——版本换了但 probe 仍 OK 的那种漂移，
	// 靠这两个读数才看得出来。
	Layout string
	Table  string
	Reason string // 不就绪原因（MISSING 行的原文，已去前缀）
}

// probeRe 解析 probe 的两态输出（只在 Go 侧解析，不把脚本输出直接透给健康面）。
var probeRe = regexp.MustCompile(`^(OK|MISSING):\s*(.*)$`)

// parseStructureProbe 纯函数：把 probe 的 stdout 解析成结构体（空/畸形输出 ⇒ 未就绪）。
func parseStructureProbe(out string) StructureProbe {
	p := StructureProbe{}
	for _, line := range strings.Split(out, "\n") {
		m := probeRe.FindStringSubmatch(strings.TrimSpace(line))
		if len(m) != 3 {
			continue
		}
		if m[1] == "MISSING" {
			p.Reason = m[2]
			return p
		}
		p.OK = true
		for _, kv := range strings.Fields(m[2]) {
			if strings.HasPrefix(kv, "version=") {
				p.Version = strings.TrimPrefix(kv, "version=")
			}
			if strings.HasPrefix(kv, "models=") {
				if n, err := strconv.Atoi(strings.TrimPrefix(kv, "models=")); err == nil {
					p.Models = n
				}
			}
			// 未知键一律忽略（向后兼容：脚本将来多打几个读数不许把解析打坏）。
			if strings.HasPrefix(kv, "layout=") {
				p.Layout = strings.TrimPrefix(kv, "layout=")
			}
			if strings.HasPrefix(kv, "table=") {
				p.Table = strings.TrimPrefix(kv, "table=")
			}
		}
		return p
	}
	p.Reason = "probe 输出无法解析"
	return p
}

// ProbeStructure 跑一次 probe（不缓存；健康面自己带 TTL）。
func ProbeStructure(ctx context.Context) StructureProbe {
	script := structureScriptPath()
	if _, err := os.Stat(script); err != nil {
		return StructureProbe{Reason: "structure_v3.py 未随包部署"}
	}
	cctx, cancel := context.WithTimeout(ctx, structureTimeout())
	defer cancel()
	bin, argv := wrapNice(pyBin(), []string{script, "probe"})
	cmd := exec.CommandContext(cctx, bin, argv...)
	out, err := cmd.Output()
	p := parseStructureProbe(string(out))
	if err != nil && p.OK {
		// 退出码非零却打了 OK：按"读数不可信"处理（同 probe 只认状态词的口径）。
		p.OK = false
		p.Reason = "probe 退出码非零"
	}
	return p
}

// structureHealthCache 健康面 TTL 缓存（probe 起子进程，不能每个 /api/health 都跑）。
var (
	structureHealthMu     sync.Mutex
	structureHealthAt     time.Time
	structureHealthProbe  StructureProbe
	structureHealthProbed bool
)

// structureHealthTTL probe 缓存时长（与派发探针的 TTL 口径一致：不是 sync.Once）。
const structureHealthTTL = 600 * time.Second

// structureONNXCount 现数权重份数（顶层平铺 + 一层子目录，与脚本 probe 的 models= 同一把尺子）。
func structureONNXCount() int {
	n := 0
	dir := structureAssetsDir()
	top, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	for _, e := range top {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(strings.ToLower(e.Name()), ".onnx") {
			n++
		}
	}
	for _, e := range top {
		if !e.IsDir() {
			continue
		}
		if sub, serr := os.ReadDir(filepath.Join(dir, e.Name())); serr == nil {
			for _, f := range sub {
				if !f.IsDir() && strings.HasSuffix(strings.ToLower(f.Name()), ".onnx") {
					n++
				}
			}
		}
	}
	return n
}

// StructureHealthWord 健康面状态词：off / ok / degraded，以及实际引擎档。
//
// degraded 与 off 必须分开：前者是"配了但不可用"（该有人去修），后者是"主动没开"（正常态）。
func StructureHealthWord(ctx context.Context) (word, engine string) {
	if !StructureEnabled() {
		return "off", "-"
	}
	structureHealthMu.Lock()
	p, at, probed := structureHealthProbe, structureHealthAt, structureHealthProbed
	structureHealthMu.Unlock()
	if !probed || time.Since(at) > structureHealthTTL {
		p = ProbeStructure(ctx)
		structureHealthMu.Lock()
		structureHealthProbe, structureHealthAt, structureHealthProbed = p, time.Now(), true
		structureHealthMu.Unlock()
	}
	if !p.OK {
		return "degraded", "-"
	}
	// 引擎档：权重若是 onnx 转出来的（本方案首选 B 路线）报 onnx；否则按 paddle。
	// ★ 2026-10-28：分档投放（`layout/`、`table/` 各一份）也要认——只扫顶层会把分档部署读成 paddle。
	engine = "paddle"
	if structureONNXCount() > 0 {
		engine = "onnx"
	}
	return "ok", engine
}

// DetectStructure 单页/整件结构识别（唯一出口：换引擎/换模型只改这里，调用方零改动）。
//
// 调用方按 §5.2 的降级表处理——四档失败都走几何重建，**不判失败、不退款**：
// Unavailable（没开/缺权重/版本形状不匹配）、Timeout、BadInput（坏图）、Empty（这页真没块）。
// 本函数只做"把脚本读数换成结构体"，任何形状判断都在脚本那一侧（唯一适配层）。
func DetectStructure(ctx context.Context, path string) (StructureDoc, error) {
	doc := StructureDoc{}
	if !StructureEnabled() {
		return doc, ErrStructureUnavailable
	}
	script := structureScriptPath()
	if _, err := os.Stat(script); err != nil {
		return doc, ErrStructureUnavailable
	}
	cctx, cancel := context.WithTimeout(ctx, structureTimeout())
	defer cancel()
	bin, argv := wrapNice(pyBin(), []string{script, "page", path})
	cmd := exec.CommandContext(cctx, bin, argv...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if cctx.Err() == context.DeadlineExceeded {
		return doc, ErrStructureTimeout
	}
	if err != nil {
		return doc, structureError(string(stderr.String()))
	}
	parsed, perr := parseStructureJSON(out)
	if perr != nil {
		return doc, perr
	}
	if len(parsed.Blocks) == 0 && len(parsed.Tables) == 0 {
		return parsed, ErrStructureEmpty
	}
	return parsed, nil
}

// structureError 把脚本 stderr 的 ERR:Structure:<variant> 映射成哨兵错误。
func structureError(stderr string) error {
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "ERR:Structure:") {
			continue
		}
		variant := strings.SplitN(strings.TrimPrefix(line, "ERR:Structure:"), ":", 2)[0]
		switch variant {
		case "Unavailable":
			return ErrStructureUnavailable
		case "Timeout":
			return ErrStructureTimeout
		case "BadInput", "Usage":
			return ErrStructureBadInput
		}
	}
	return ErrStructureUnavailable
}

// structureWire 脚本输出形状（只增不改；零文本字段是硬约束）。
type structureWire struct {
	Blocks []struct {
		Label string    `json:"label"`
		Box   []float64 `json:"box"`
		Order int       `json:"order"`
	} `json:"blocks"`
	Tables []struct {
		Box   []float64 `json:"box"`
		Rows  int       `json:"rows"`
		Cols  int       `json:"cols"`
		Cells []struct {
			R       int       `json:"r"`
			C       int       `json:"c"`
			RowSpan int       `json:"rowspan"`
			ColSpan int       `json:"colspan"`
			Box     []float64 `json:"box"`
		} `json:"cells"`
		HTML string `json:"html"`
	} `json:"tables"`
}

// parseStructureJSON 解析并做**零文本字段**校验（L51 的行为级判据）。
func parseStructureJSON(out []byte) (StructureDoc, error) {
	doc := StructureDoc{}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return doc, ErrStructureEmpty
	}
	if i := strings.Index(raw, "{"); i > 0 {
		raw = raw[i:]
	}
	var generic interface{}
	if err := json.Unmarshal([]byte(raw), &generic); err != nil {
		return doc, ErrStructureBadInput
	}
	if hasTextKey(generic) {
		// 结构腿只许带几何：出现任何识别文本键即判坏——那等于换掉我们统一的识别口径。
		return doc, ErrStructureBadInput
	}
	var w structureWire
	if err := json.Unmarshal([]byte(raw), &w); err != nil {
		return doc, ErrStructureBadInput
	}
	for _, b := range w.Blocks {
		if len(b.Box) < 4 {
			continue
		}
		doc.Blocks = append(doc.Blocks, StructureBlock{
			Label: b.Label, Order: b.Order,
			Box: [4]float64{b.Box[0], b.Box[1], b.Box[2], b.Box[3]},
		})
	}
	for _, t := range w.Tables {
		if len(t.Box) < 4 {
			continue
		}
		st := StructureTable{Rows: t.Rows, Cols: t.Cols, HTML: t.HTML,
			Box: [4]float64{t.Box[0], t.Box[1], t.Box[2], t.Box[3]}}
		for _, c := range t.Cells {
			if len(c.Box) < 4 {
				continue
			}
			st.Cells = append(st.Cells, StructureCell{
				Row: c.R, Col: c.C, RowSpan: c.RowSpan, ColSpan: c.ColSpan,
				Box: [4]float64{c.Box[0], c.Box[1], c.Box[2], c.Box[3]},
			})
		}
		doc.Tables = append(doc.Tables, st)
	}
	return doc, nil
}

// hasTextKey 递归扫描 JSON 里是否出现 text/rec_text/rec_texts 键。
func hasTextKey(v interface{}) bool {
	switch x := v.(type) {
	case map[string]interface{}:
		for k, child := range x {
			lk := strings.ToLower(k)
			if lk == "text" || lk == "rec_text" || lk == "rec_texts" {
				return true
			}
			if hasTextKey(child) {
				return true
			}
		}
	case []interface{}:
		for _, child := range x {
			if hasTextKey(child) {
				return true
			}
		}
	}
	return false
}

// structureErrName 把哨兵错误映射成日志/健康面的档位名（未知回空串，不硬猜）。
func structureErrName(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrStructureUnavailable):
		return "unavailable"
	case errors.Is(err, ErrStructureTimeout):
		return "timeout"
	case errors.Is(err, ErrStructureBadInput):
		return "bad_input"
	case errors.Is(err, ErrStructureEmpty):
		return "empty"
	}
	return fmt.Sprintf("unknown:%v", err)
}
