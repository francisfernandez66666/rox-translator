// ============ cmd/ocrbench · 职责说明 ============
// OCR 精度基准计分器（主方案 §15 测量门）。与 scripts/ocr_bench_gen.py 配对：
// 生成器出「图 + 真值清单」，本命令在**部署机**上把每张图真的过一遍 ocr_recognize.py，
// 算出字符错误率（CER）与噪声行命中数，输出 JSON 读数。
//
// 为什么不做成单测：精度要靠真权重（assets/ocr/*.onnx），而权重不进 git、只在部署机；
// 本机跑不了不是"跳过"，是环境事实——所以这条腿由部署机执行，读数回填方案 §15 的默认值。
//
// 用法：
//
//	go run ./cmd/ocrbench -manifest /tmp/ocrbench/manifest.json
//	go build -o /opt/translator/bin/ocrbench ./cmd/ocrbench
//	/opt/translator/bin/ocrbench -manifest /tmp/ocrbench/manifest.json -out /tmp/ocrbench/report.json
//
// 退出码：0 全部跑通（是否达标看 ok 字段）；1 有图跑失败（子进程非零/JSON 解析失败）；2 参数/环境错。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// manifest 生成器写出的真值清单。
type manifest struct {
	Lang   string `json:"lang"`
	Rows   int    `json:"rows"`
	Font   string `json:"font"`
	Images []struct {
		Image    string   `json:"image"`
		Expected []string `json:"expected"`
		Noise    []string `json:"noise"`
	} `json:"images"`
}

// ocrOut ocr_recognize.py image 子命令的 stdout（只取本命令要用的三个字段）。
type ocrOut struct {
	Text  string      `json:"text"`
	Boxes [][]float64 `json:"boxes"`
	Confs []float64   `json:"confs"`
}

// imageReport 单张图的读数。
type imageReport struct {
	Image    string  `json:"image"`
	CER      float64 `json:"cer"`
	Edits    int     `json:"edits"`
	Chars    int     `json:"chars"`
	NoiseHit int     `json:"noise_hits"`
	Lines    int     `json:"lines"`
	Millis   int64   `json:"ms"`
	Error    string  `json:"error,omitempty"`
}

// main 解析参数并按 manifest 逐张图跑一遍 OCR，汇总 CER 与噪声命中数。
// 退出码：0 全部跑通（是否达标看 ok）；1 有图跑失败；2 参数/环境错。
func main() {
	var (
		manifestPath = flag.String("manifest", "", "gen 脚本产出的 manifest.json")
		python       = flag.String("python", "", "Python 解释器（缺省 env FILEPROC_PYTHON_BIN 或 python3）")
		script       = flag.String("script", "", "ocr_recognize.py（缺省 env OCR_SCRIPT 或可执行文件同目录）")
		outPath      = flag.String("out", "", "报告输出路径（缺省 stdout）")
		maxCER       = flag.Float64("max-cer", 0.05, "CER 达标线（默认 5%）")
	)
	flag.Parse()
	if strings.TrimSpace(*manifestPath) == "" {
		fmt.Fprintln(os.Stderr, "ERR:Usage: -manifest <manifest.json>")
		os.Exit(2)
	}
	raw, err := os.ReadFile(*manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERR:Manifest:%v\n", err)
		os.Exit(2)
	}
	var mf manifest
	if err := json.Unmarshal(raw, &mf); err != nil {
		fmt.Fprintf(os.Stderr, "ERR:Manifest:%v\n", err)
		os.Exit(2)
	}
	py := firstNonEmpty(*python, os.Getenv("FILEPROC_PYTHON_BIN"), "python3")
	sc := firstNonEmpty(*script, os.Getenv("OCR_SCRIPT"), filepath.Join(filepath.Dir(os.Args[0]), "ocr_recognize.py"))
	if _, err := os.Stat(sc); err != nil {
		fmt.Fprintf(os.Stderr, "ERR:Script:找不到 ocr_recognize.py（%s）\n", sc)
		os.Exit(2)
	}
	base := filepath.Dir(*manifestPath)

	reports := make([]imageReport, 0, len(mf.Images))
	totalEdits, totalChars, noiseHit, failed := 0, 0, 0, 0
	for _, it := range mf.Images {
		started := time.Now()
		rep := imageReport{Image: it.Image}
		imgPath := it.Image
		if !filepath.IsAbs(imgPath) {
			imgPath = filepath.Join(base, it.Image)
		}
		text, err := runOCR(py, sc, imgPath)
		rep.Millis = time.Since(started).Milliseconds()
		if err != nil {
			rep.Error = err.Error()
			failed++
			reports = append(reports, rep)
			continue
		}
		exp := strings.Join(it.Expected, "\n")
		rep.Edits = levenshtein([]rune(exp), []rune(text))
		rep.Chars = len([]rune(exp))
		rep.Lines = len(nonEmptyLines(text))
		if rep.Chars > 0 {
			rep.CER = float64(rep.Edits) / float64(rep.Chars)
		}
		for _, n := range it.Noise {
			if n != "" && strings.Contains(text, n) {
				rep.NoiseHit += strings.Count(text, n)
			}
		}
		totalEdits += rep.Edits
		totalChars += rep.Chars
		noiseHit += rep.NoiseHit
		reports = append(reports, rep)
	}
	cer := 0.0
	if totalChars > 0 {
		cer = float64(totalEdits) / float64(totalChars)
	}
	ok := failed == 0 && cer <= *maxCER && noiseHit == 0
	report := map[string]interface{}{
		"ok":           ok,
		"images":       len(mf.Images),
		"failed":       failed,
		"cer":          cer,
		"max_cer":      *maxCER,
		"noise_hits":   noiseHit,
		"total_edits":  totalEdits,
		"total_chars":  totalChars,
		"lang":         mf.Lang,
		"font":         mf.Font,
		"per_image":    reports,
		"generated_at": time.Now().Format(time.RFC3339),
	}
	body, _ := json.MarshalIndent(report, "", "  ")
	if *outPath != "" {
		if err := os.WriteFile(*outPath, body, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "ERR:Out:%v\n", err)
			os.Exit(2)
		}
		fmt.Printf("OCRBENCH ok=%v cer=%.4f noise=%d images=%d report=%s\n", ok, cer, noiseHit, len(mf.Images), *outPath)
	} else {
		fmt.Println(string(body))
	}
	if failed > 0 {
		os.Exit(1)
	}
}

// runOCR 调一次 ocr_recognize.py image，返回识别文本（失败时带 stderr 摘要）。
func runOCR(py, script, img string) (string, error) {
	cmd := exec.Command(py, script, "image", img)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	var r ocrOut
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &r); err != nil {
		return "", fmt.Errorf("OCR 输出非法 JSON: %w", err)
	}
	return r.Text, nil
}

// nonEmptyLines 非空行计数（只用于读数）。
func nonEmptyLines(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if strings.TrimSpace(ln) != "" {
			out = append(out, ln)
		}
	}
	return out
}

// levenshtein 编辑距离（rune 级；合成集是已知文本，字符级足够支撑 CER）。
func levenshtein(a, b []rune) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// min3 三数取小。
func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// firstNonEmpty 取第一个非空串。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
