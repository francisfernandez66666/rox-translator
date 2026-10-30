// ============ warm_caps.go · 职责说明 ============
// 能力注册表：把「能力名 + 子命令」映射到**既有的那份脚本**，并给出该能力的并发档。
//
// 为什么单独一个文件：常驻腿与子进程腿必须是同一份实现的两个入口（AGENTS §一·17
// 「能力脚本随批落盘、只有一份事实源」）。这张表就是"哪一份"的落点——
// 新增能力只在这里加一条，**不许**在 warm.go/调用点里各写一份脚本名或 worker 数。
//
// 一期只接 ocr（ExtractImageOCR）。asr / tts 二期接入时：
//
//	① 本表把 `Warm` 置 true 并填 WorkerEnv；
//	② capsvc.py 的 build_default_factories() 把那两个 None 换成真工厂；
//	③ 调用点（asr.go / tts.go）按 ocr.go 的形态薄替换（签名不变）。
//
// 注意两期共同的边界：远端派发 `fpdexec` **不接 capsvc**（协议不同，见 warm.go 头）。
// =============================================
package fileproc

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"
)

// warmCapSpec 一个能力的常驻化规格。
type warmCapSpec struct {
	Name           string // 能力名（协议里的 cap）
	Module         string // capsvc 侧 import 的脚本模块名（与 ScriptCapability 同源）
	Script         string // 脚本文件名（子进程路径用；warm 路径只作锚点，不执行）
	WorkerEnv      string // 并发档 env（与 capsvc.py 的 workers_for 同源）
	DefaultWorkers int
	Warm           bool // 是否已接入常驻（false＝二期留位，调用点仍走子进程）
}

// warmCapRegistry 能力注册表。**名字即对外契约**（cap 字段出现在协议、日志、健康面）。
var warmCapRegistry = map[string]warmCapSpec{
	"ocr": {
		Name: "ocr", Module: "ocr_recognize", Script: "ocr_recognize.py",
		WorkerEnv: "CAPSVC_OCR_WORKERS", DefaultWorkers: 2, Warm: true,
	},
	// 二期留位（本次不做，但注册表先占住名字，免得将来"加一个能力"变成改协议）：
	"asr": {
		Name: "asr", Module: "asr_transcribe", Script: "asr_transcribe.py",
		WorkerEnv: "CAPSVC_ASR_WORKERS", DefaultWorkers: 1, Warm: false,
	},
	"tts": {
		Name: "tts", Module: "tts_speak", Script: "tts_speak.py",
		WorkerEnv: "CAPSVC_TTS_WORKERS", DefaultWorkers: 1, Warm: false,
	},
}

// warmCapSpecFor 取能力规格；未登记的能力返回 false（调用方一律走子进程）。
func warmCapSpecFor(name string) (warmCapSpec, bool) {
	spec, ok := warmCapRegistry[strings.ToLower(strings.TrimSpace(name))]
	return spec, ok
}

// capsvcWorkerCount 该能力的并发档（与 capsvc.py 的 workers_for 同源）。
// 只用于客户端侧的礼貌排队；真正的并发上限在服务端。
func capsvcWorkerCount(name string) int {
	spec, ok := warmCapSpecFor(name)
	if !ok {
		return 1
	}
	if v := strings.TrimSpace(os.Getenv(spec.WorkerEnv)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	if spec.DefaultWorkers > 0 {
		return spec.DefaultWorkers
	}
	return 1
}

// warmOcrImage OCR 识别走常驻腿。
//
// 返回值语义与 warmCall 一致：(res,true) 是服务端给出的**权威结果**（含失败档位），
// (nil,false) 才是"常驻腿不可用、请回落子进程一次"。
// 闸：只取 capGate("ocr")，**不进 procGate**（回落那一跳自己会走 procGate）。
func warmOcrImage(ctx context.Context, path string, timeout time.Duration) (*warmResult, bool) {
	spec, ok := warmCapSpecFor("ocr")
	if !ok || !spec.Warm {
		return nil, false
	}
	release := acquireCapGateCtx(ctx, spec.Name)
	defer release()
	return warmCall(ctx, spec.Name, "image", []string{path}, timeout)
}
