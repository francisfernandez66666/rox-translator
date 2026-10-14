// ============ boot_timer.go · 职责说明 ============
// 启动阶段计时器（★ #41 S3 第一步：先有读数，才谈瘦身）。
//
// 为什么必须先有这一份日志：《零停机发版方案_亚秒级第一阶段》的 S3 是「把启动路径压进亚秒」，
// 而验收口径里最硬的一条就是**按真数排序投入**——此前仓库里没有任何一行日志记录
// 「bind 之前每一步花了多少毫秒」，于是所有收益预估都只是按代码结构推的估计。
// 低配机器（1.6G 宿主机、已在吃 swap）上最容易犯的错，就是在最便宜的环节优化最贵的地方。
//
// 三条口径：
//
//	① **起点是包变量初始化时刻，不是 main 第一行**：Go 运行时装载与各包 init 也在这条关键路径上
//	   （embed 资源、驱动注册），从 main 起算会把那一段白漏掉；
//	② **只出声、不参与任何判定**：这一份是观测面，绝不做成启动闸门——启动慢不是启动失败，
//	   拿计时器拦启动会把一次发版打成停站（同 AGENTS §一·3「判据别把可用性交押金」那条口径）；
//	③ **日志走 observability（slog JSON），不走标准库 log.Printf**：
//	   `cmd/` 根下的 log.Printf 存量受棘轮基线约束（AGENTS §一·2），新观测行不该去顶那条账。
//
// 用法：main 里在每一段边界调一次 `boot.stage("段名")`，出的读数是该段耗时＋自启动累计；
// 后台化的那几段（如 fileproc 自检）不许共用同一个 stager（会与主 goroutine 抢写 last），
// 由各自用自己的起止时间单独出一行「后台完成」。
// =============================================
package main

import (
	"context"
	"time"

	"translator/internal/observability"
)

// bootProcessStart 进程启动路径的计时起点（包级变量初始化＝早于 main 第一行）。
// 与 boot_timer.go 同包，因此 main 里的 newBootStager() 与这里必然是同一时刻，
// 不存在「某个调用方自己 now 一次、另一个也 now 一次」的两把尺子。
var bootProcessStart = time.Now()

// bootStager 启动阶段计时器。非并发安全：只在主 goroutine 顺序推进。
type bootStager struct {
	start time.Time // 进程起点（bootProcessStart）
	last  time.Time // 上一段结束时刻
}

// newBootStager 建一个起点为 bootProcessStart、当前段为「从进程起来到现在」的计时器。
func newBootStager() *bootStager {
	now := time.Now()
	return &bootStager{start: bootProcessStart, last: now}
}

// stage 结束当前段并出声：stage＝段名（人读的中文，运维看板直接按它排序找瓶颈）、
// ms＝本段耗时、total_ms＝自进程起点累计、since_main_ms 由调用方不必关心。
// nil 接收者直接返回：让测试与提前退出的分支都能安全调用，不必处处判空。
func (b *bootStager) stage(name string) {
	if b == nil {
		return
	}
	now := time.Now()
	observability.Info(context.Background(), "启动阶段计时",
		"stage", name,
		"ms", now.Sub(b.last).Milliseconds(),
		"total_ms", now.Sub(b.start).Milliseconds())
	b.last = now
}

// bootAsyncDone 后台启动腿的完成读数（各腿用自己的起点，不与主流程的 stager 共享状态）。
// 出这一行的意义是「这条腿虽然不在 bind 之前了，但它到底花了多久、什么时候好」——
// 少了它，异步化就变成把读数扔掉，而不是把读数挪到不挡路的地方。
func bootAsyncDone(name string, since time.Time) {
	observability.Info(context.Background(), "启动后台腿完成",
		"stage", name,
		"ms", time.Since(since).Milliseconds(),
		"total_ms", time.Since(bootProcessStart).Milliseconds())
}
