// ============ dispatch_capacity.go · 职责说明 ============
// 派发能力上限（2026-11-26，《施工方案_工单动态派发与上传闸门抬升_20261125.md》§3.7）。
//
// 为什么要独立于 fileproc.MaxUploadBytes：
//   上传闸 200 MiB 只回答"客户能不能把文件传上来"，不回答"广州执行机跑不跑得动"。
//   按 24 MB/32 页真机基准（apply 55s、RSS 92 MB）外推到广州 fpd.slice 的 0.3 核 /
//   768M 帽 / 615s 墙钟：60 MiB 已临界、100 MiB 破超时、200 MiB 撞内存帽。
//   所以另设一道**派发能力上限**（默认 100 MiB），超出的件由规则 2b 判 local，
//   既保住"大件能上传"的收益，又不把跑不动的件派给广州机。
//
// 三把尺子各管一件事，不许互相靠拢：
//   · MaxUploadBytes（200 MiB）—— 用户上传口
//   · dispatchMinBytes（20 MiB） —— 值不值得派（盈亏平衡）
//   · dispatchMaxPayloadBytes（100 MiB）—— 派得动派不动（算力上限）
// =============================================
package fileproc

import (
	"os"
	"strconv"
)

// envDispatchMaxMB 派发能力上限环境变量（FILEPROC_DISPATCH_MAX_MB）。
// 命名与 envDispatchMinMB 同一族，默认档见 dispatchMaxPayloadBytes 注释。
const envDispatchMaxMB = "FILEPROC_DISPATCH_MAX_MB"

// dispatchMaxPayloadBytes 派发能力上限（字节）。默认 100 MiB。
//
// ★ 为什么是 100 而不是 200：见文件头推算表——现资源帽（CPU 30%/768M/615s）
// 下 60 MiB 临界、100 MiB 需抬超时到 1200s、200 MiB 撞 768M 帽。
// 真要派 200 MiB 属"升级档"（CPU 60%/内存 1.2G/超时 2400s），需另行拍板，
// 届时 env 覆盖这一个数即可，不必改代码。
//
// 下限钳制：若运维误配成小于 dispatchMinBytes，会导致"够档的件全判超容量"，
// 这里取两者较大值，保证能力上限至少不低于分流下限（否则派发闸彻底空转）。
func dispatchMaxPayloadBytes() int64 {
	mb := 100 // 默认档（2026-11-26 实测推算，见文件头）
	if v := os.Getenv(envDispatchMaxMB); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			mb = n
		}
	}
	capBytes := int64(mb) << 20
	if floor := dispatchMinBytes(); capBytes < floor {
		return floor
	}
	return capBytes
}
