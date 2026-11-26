// ============ dispatch_chunk_run.go · 工单块级派发主流程 ============
// 2026-11-26，方案 §四·C-6：把"整份 PDF 送远端做 extract+apply"改为
// "主站提取→分块→远端翻译→zip 回传→主站解压校验"。
//
// 复用 DispatchRun 的 put/stat/get 协议，不新增 fpdexec mode；三次 run 分别是：
//   ① extract：texts 落 w/<sid>/texts.json，用 outputs 自动回拉主站
//   ② translate：payload 带块边界/Key/术语快照，远端打 result.zip，outputs 自动回拉
//   ③ assemble：仅超限接手时由远端本地写回（本文件不主动调用，由上层编排触发）
// =============================================
package fileproc

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// ChunkRunRequest 块级派发的入参。Key/Base/Model 来自主站 llmsource.Current（唯一读点）。
type ChunkRunRequest struct {
	SessionID  string
	InputPDF   string
	Lang       string
	Bounds     [][2]int
	Key        string
	Base       string
	Model      string
	Terms      map[string]string
	OutputZip  string // 主站落点（result.zip）
	TextsOut   string // 主站 texts.json 落点
}

// ChunkRunResult 回执（tokens_used 回传主站计费；Chunks 供主站构造真值表）。
type ChunkRunResult struct {
	Texts      []string
	ChunkCount int
	TokensUsed int64
	QualityHit []string
	// Translations 原文→译文映射（从 zip 各 chunk 解压出的源/译文配对）。
	// 不序列化进响应/日志（客户内容），仅供工单落 ticket_segments 真值表。
	Translations map[string]string
}

// DispatchChunkRun 执行 extract→translate→回拉→解压校验的完整远端链。
// 任何一步失败都返回 error（调用方决定降级/重试），本函数自身不重试（硬约定 2）。
func DispatchChunkRun(ctx context.Context, req ChunkRunRequest) (ChunkRunResult, error) {
	var out ChunkRunResult
	if !DispatchEnabled() {
		return out, errors.New("派发未启用")
	}
	if req.InputPDF == "" || req.OutputZip == "" || req.TextsOut == "" {
		return out, errors.New("块级派发入参不齐")
	}

	// ① extract：产物声明为 texts.json（远端落文件，避开 4MiB stdout 截断）
	// outputs 口径：key=远端相对名，value=主站本地落点（见 DispatchRun 的 outRemote 构造）
	textsOutputs := map[string]string{"texts.json": req.TextsOut}
	if _, err := DispatchRun(ctx, req.SessionID, "chunk_translate.py",
		[]string{"chunk_translate.py", "extract", req.InputPDF},
		nil, textsOutputs); err != nil {
		return out, fmt.Errorf("块级 extract 失败: %w", err)
	}
	raw, err := os.ReadFile(req.TextsOut)
	if err != nil {
		return out, fmt.Errorf("读 texts.json 失败: %w", err)
	}
	var extracted struct {
		Success bool     `json:"success"`
		Texts   []string `json:"texts"`
	}
	if err := json.Unmarshal(raw, &extracted); err != nil || !extracted.Success || len(extracted.Texts) == 0 {
		return out, fmt.Errorf("texts.json 不可解析或零段")
	}
	out.Texts = extracted.Texts

	// ② translate：payload 带块边界/Key/术语快照（只走 stdin，不进 argv/文件）
	payload, err := json.Marshal(map[string]interface{}{
		"key": req.Key, "base": req.Base, "model": req.Model,
		"target_lang": req.Lang, "bounds": req.Bounds,
		"terms": req.Terms,
	})
	if err != nil {
		return out, err
	}
	zipOutputs := map[string]string{"result.zip": req.OutputZip}
	if _, err := DispatchRun(ctx, req.SessionID, "chunk_translate.py",
		[]string{"chunk_translate.py", "translate", req.InputPDF},
		payload, zipOutputs); err != nil {
		return out, fmt.Errorf("块级 translate 失败: %w", err)
	}

	// ③ 解压 + 逐块 sha 复算（校验位置＝主站，方案 §3.2）
	return verifyChunkZip(ctx, req, out)
}

// chunkIndex 块序号 → 文件名尾缀（避免引入额外依赖，与 Python 的 "chunk_%d.json" 同形）。
func chunkIndex(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	k := len(b)
	for i > 0 {
		k--
		b[k] = byte('0' + i%10)
		i /= 10
	}
	return string(b[k:])
}

// verifyChunkZip 主站解压并复算逐块/整件 sha；缺块/重块判红。
func verifyChunkZip(ctx context.Context, req ChunkRunRequest, out ChunkRunResult) (ChunkRunResult, error) {
	z, err := zip.OpenReader(req.OutputZip)
	if err != nil {
		return out, fmt.Errorf("result.zip 打不开: %w", err)
	}
	defer z.Close()

	// 解压炸弹预检（§3.8）：声明总解压体积 > 输入×2 判红
	inputSize := int64(0)
	if fi, e := os.Stat(req.InputPDF); e == nil {
		inputSize = fi.Size()
	}
	var declared int64
	for _, f := range z.File {
		declared += int64(f.UncompressedSize64)
	}
	if inputSize > 0 && declared > inputSize*2 {
		return out, fmt.Errorf("result.zip 解压体积 %d 超输入 %d 的两倍，拒绝解压", declared, inputSize)
	}

	tmp, err := os.MkdirTemp("", "chunkzip_")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(tmp)
	var manifest struct {
		ChunkCount int `json:"chunk_count"`
		Chunks     []struct {
			Name   string `json:"name"`
			SHA256 string `json:"sha256"`
		} `json:"chunks"`
		TokensUsed int64    `json:"tokens_used"`
		QualityHit []string `json:"quality_hits"`
	}
	found := map[string]string{}
	for _, f := range z.File {
		if f.Name == "manifest.json" {
			rc, e := f.Open()
			if e != nil {
				return out, e
			}
			e = json.NewDecoder(rc).Decode(&manifest)
			rc.Close()
			if e != nil {
				return out, fmt.Errorf("manifest.json 不可解析: %w", e)
			}
			continue
		}
		if !strings.HasPrefix(f.Name, "chunk_") || !strings.HasSuffix(f.Name, ".json") {
			continue
		}
		rc, e := f.Open()
		if e != nil {
			return out, e
		}
		h := sha256.New()
		if _, e = io.Copy(h, rc); e != nil {
			rc.Close()
			return out, e
		}
		rc.Close()
		found[f.Name] = hex.EncodeToString(h.Sum(nil))
	}
	if manifest.ChunkCount != len(req.Bounds) {
		return out, fmt.Errorf("块数不符：manifest=%d bounds=%d", manifest.ChunkCount, len(req.Bounds))
	}
	for _, c := range manifest.Chunks {
		got, ok := found[c.Name]
		if !ok || got != c.SHA256 {
			return out, fmt.Errorf("块 %s sha 不等值（缺块或被改）", c.Name)
		}
	}
	out.ChunkCount = manifest.ChunkCount
	out.TokensUsed = manifest.TokensUsed
	out.QualityHit = manifest.QualityHit

	// 构造原文→译文映射（供工单真值表）。块序号来自 manifest 的 bounds，与主站切块同一口径。
	var srcTexts []string
	for _, f := range z.File {
		if f.Name == "texts.json" {
			rc, e := f.Open()
			if e == nil {
				var t struct {
					Texts []string `json:"texts"`
				}
				_ = json.NewDecoder(rc).Decode(&t)
				rc.Close()
				srcTexts = t.Texts
			}
			break
		}
	}
	out.Translations = make(map[string]string)
	for i, b := range req.Bounds {
		var lines []string
		for _, f := range z.File {
			if f.Name == "chunk_"+chunkIndex(i)+".json" {
				rc, e := f.Open()
				if e == nil {
					_ = json.NewDecoder(rc).Decode(&lines)
					rc.Close()
				}
				break
			}
		}
		for j, line := range lines {
			idx := b[0] + j
			if idx < len(srcTexts) && line != "" {
				out.Translations[srcTexts[idx]] = line
			}
		}
	}
	return out, nil
}
