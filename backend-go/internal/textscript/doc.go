// Package textscript 提供「一个字符属于哪个书写体系」的唯一实现。
//
// 为什么需要它（本仓历史形态）：汉字/假名/西里尔等码位区间此前在 5 处各写一份手抄区间
// （engine 的 isLoneKana / assist 的 hanResidueRuns / PostProcess 的 zhCharRe / python 的
// _is_lone_nonascii），每一份都漏扩展区、且改一处不会带动另一处。本包把这张表收成一份，
// 纯标准库、零第三方依赖、不 import 任何产品包，因此 fileproc / engine / assist 都能引，
// 也不会把「读一个字符属于哪个体系」这件事绑上存储层或模型层。
//
// 三条口径（改本包前必读）：
//  1. 「体系」按 Unicode Script 属性判，不按码位区间手抄；Common 字符（长音符 ー、中点 ・、
//     叠字符 々 等）由 scriptExtend 显式挂到最近的体系上——它们常常决定日文行会不会被
//     误判成「含 Other」。
//  2. 统计占比的分母只数**字母**（unicode.IsLetter）；数字、标点、空白不进分母。
//     否则「一行三格数字」就会把书写体系占比稀释成噪声。
//  3. unicode.Version 会随 Go 版本变（升 Go 就可能静默改变本包全部判据），
//     所以有一条 TestUnicodeVersionPinned 把版本钉在实测值上：那条锁变红时的正确动作是
//     「回来复核字符判据」，不是改锁。
package textscript
