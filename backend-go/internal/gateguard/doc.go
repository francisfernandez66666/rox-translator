// Package gateguard 是 meta 闸门的覆盖锁所在包（主方案《闸门射程与反证登记》§3）。
//
// 为什么单独开一个包：它横跨前后端（同时扫 Go 的 *_gate_test.go 与 TS 的 *.lock.test.ts），
// 不依赖任何产品包；塞进 internal/fileproc 会让那个"基础包"的边界变脏（AGENTS §一·1）。
// 本包只有一处判据：**新增的家族级锁必须登记 gate_mutants.tsv**——
// 存量锁按 M1 落地时的基线台账豁免（只减不增），避免一次性补 80+ 条变异体把人劝退。
package gateguard
