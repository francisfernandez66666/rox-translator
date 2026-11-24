// probe_util.go · 探针的臂选择与模型清单读取（与打分逻辑无关的工具腿）。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"translator/internal/qualityjudge"
)

// parseProbeScores 对二维与五维回执分别严格解析；截断、缺字段、越界都不算有效样本。
func parseProbeScores(content, finish string, dim5 bool) (term, semantic, total float64, parsed bool) {
	if strings.EqualFold(strings.TrimSpace(finish), "length") {
		return 0, 0, 0, false
	}
	if !dim5 {
		term, semantic, err := qualityjudge.ParseJudgeScores(content)
		return term, semantic, 0, err == nil
	}
	var s struct {
		Term     *float64 `json:"term"`
		Grammar  *float64 `json:"grammar"`
		Semantic *float64 `json:"semantic"`
		NumUnit  *float64 `json:"numunit"`
		Style    *float64 `json:"style"`
		Total    *float64 `json:"total"`
	}
	raw := qualityjudge.ExtractJSON(content)
	if json.Unmarshal([]byte(raw), &s) != nil || s.Term == nil || s.Grammar == nil || s.Semantic == nil || s.NumUnit == nil || s.Style == nil {
		return 0, 0, 0, false
	}
	values := []float64{*s.Term, *s.Grammar, *s.Semantic, *s.NumUnit, *s.Style}
	for _, v := range values {
		if v < 0 || v > 100 {
			return 0, 0, 0, false
		}
	}
	total = 0.3*(*s.Term) + 0.2*(*s.Grammar) + 0.3*(*s.Semantic) + 0.1*(*s.NumUnit) + 0.1*(*s.Style)
	if s.Total != nil {
		if *s.Total < 0 || *s.Total > 100 {
			return 0, 0, 0, false
		}
		total = *s.Total
	}
	return *s.Term, *s.Semantic, total, true
}

// selectArms 按 -arms（逗号分隔的基础臂名）过滤 probeArms，再追加 -candidates 的候选模型臂。
// 名字不认识时报错退出（不静默跑空集：空集会打出全绿汇总，是最坏的一种假绿）。
func selectArms(armsFlag, candidates string, candTok int) []arm {
	base := probeArms()
	byName := make(map[string]arm, len(base))
	for _, a := range base {
		byName[a.name] = a
	}
	var out []arm
	for _, name := range strings.Split(armsFlag, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		a, ok := byName[name]
		if !ok {
			fmt.Fprintf(os.Stderr, "FATAL 未知臂 %q（可选 old/clean/nothink）\n", name)
			os.Exit(2)
		}
		out = append(out, a)
	}
	out = append(out, candidateArms(strings.Split(candidates, ","), candTok)...)
	if len(out) == 0 {
		fmt.Fprintln(os.Stderr, "FATAL 没有选中任何臂（-arms 与 -candidates 都为空）")
		os.Exit(2)
	}
	return out
}

// listRemoteModels 打印该打分档 base 上可用的模型 id（只读；用于挑候选，不参与判定）。
func listRemoteModels(ctx context.Context, base, key string) {
	url := strings.TrimRight(base, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL 构造请求: %v\n", err)
		os.Exit(2)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	c := &http.Client{Timeout: 30 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL 请求模型清单: %v\n", err)
		os.Exit(2)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "FATAL 模型清单 HTTP %d: %.200s\n", resp.StatusCode, body)
		os.Exit(2)
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL 解析模型清单: %v\n", err)
		os.Exit(2)
	}
	for _, m := range out.Data {
		fmt.Println("MODEL\t" + m.ID)
	}
	fmt.Printf("MODELS_TOTAL\t%d\n", len(out.Data))
}
