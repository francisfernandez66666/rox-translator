// ============ chat_audio_capability_order_gate_test.go · 职责说明 ============
// ★ 2026-11-01 UAT 收口批（A6b/A7/T71 那轮红项里唯一一条**产品侧**改动）：
//
//	`/api/chat/asr` 的**能力判定必须排在时长探测之前**，且那句"这台没装 ASR"的话术只有一份。
//
// 现网/UAT 实证：本机没装 ffmpeg 也没投放 ASR 权重，客户端拿到的却是
//
//	{"error":"无法读取这段录音的时长，请换一段录音或交给翻译工单","reason":"unreadable"}
//
// ——把**平台缺能力**说成**客户的录音有问题**。AGENTS §19 族一把话说死了：交付面必须点清
// 是哪一族（上游断／本来不是文字／模型坏形态／能力档）。方向错的读数比没有读数更坏，
// 因为它会把运营支去"让客户重录"，而真凶是这台机器压根没有这项能力。
//
// 三条判据（一条都不能省）：
//
//	C1 位置锁：能力闸那一行必须**早于** AudioDuration 那一行（换回旧顺序必红）；
//	C2 单源锁：那句中文只许出现在 fileproc/asr.go 一处（api 侧抄一份＝迟早漂移，
//	   而漂移的表现是 asrReason 的前缀判据认不出档位、capability_missing 静默退成 failed）；
//	C3 行为锁：就绪时 AsrUnavailableErr()==nil（否则每次请求都被判"没能力"，那是更坏的形态）；
//	   并 asrReason(那条错误) 必须正好是 capability_missing。
//
// ⚠️ 单测自钉 sqlite 方言由 newEstCfgFixture 那套承担；本文件不碰库，只读源码与两个纯函数。
// ==========================================
package api

import (
	"errors"
	"os"
	"strings"
	"testing"

	"translator/internal/fileproc"
)

// readGoCode 读源文件并**按行剥掉 // 注释**——本仓反复踩的坑：说明注释里就写着靶子字样，
// 拿裸词判会被自己的注释喂饱（假绿）或假红。
func readGoCodeForCapOrder(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s 失败 ⇒ 锚点搬家了、本文件三条锁全部失明（判红而不是跳过）：%v", path, err)
	}
	out := make([]string, 0, 64)
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		t.Fatalf("%s 剥注释后一行不剩 ⇒ 判据射程为零", path)
	}
	return out
}

func TestAsrCapabilityGateComesBeforeDurationProbe(t *testing.T) {
	lines := readGoCodeForCapOrder(t, "chat_audio.go")
	capIdx, probeIdx := -1, -1
	for i, l := range lines {
		if capIdx < 0 && strings.Contains(l, "fileproc.AsrUnavailableErr()") {
			capIdx = i
		}
		if probeIdx < 0 && strings.Contains(l, "fileproc.AudioDuration(") {
			probeIdx = i
		}
	}
	if capIdx < 0 {
		t.Fatal("没找到能力闸（fileproc.AsrUnavailableErr）⇒ 这条顺序锁的靶子消失了，判红")
	}
	if probeIdx < 0 {
		t.Fatal("没找到时长探测（fileproc.AudioDuration）⇒ 本文件判据已失焦，判红而不是放过")
	}
	// 判据本体抽成纯函数：正例与"旧顺序"那枚反向对照**共用它**
	//（不抽出来就得把比较再抄一遍，两份比较迟早一份漏判——§三 那条"判据级代替不了守卫级"）。
	if capOrderViolated(capIdx, probeIdx) {
		t.Fatalf("能力闸排在时长探测**之后**（第 %d 行 vs 第 %d 行）⇒ 没装 ASR 的机器会把客户支去重录", capIdx, probeIdx)
	}
	// 射程自证：把两个下标对调（＝旧顺序那个形态）必须被同一把尺子判"违反"，
	// 否则上面那句绿灯可能只是"两个索引都找不到"的另一种写法。
	if !capOrderViolated(probeIdx, capIdx) {
		t.Fatalf("反向对照落空：对调 cap=%d probe=%d 竟仍判合规 ⇒ 这条顺序锁是摆设", probeIdx, capIdx)
	}
}

// capOrderViolated 是唯一的方向判据：能力闸必须在时长探测之前（相等也判违反，保守方向）。
func capOrderViolated(capLine, probeLine int) bool { return capLine >= probeLine }

func TestAsrCapabilityMessageIsSingleSource(t *testing.T) {
	const msg = "音频语音识别（ASR）不可用"
	apiLines := readGoCodeForCapOrder(t, "chat_audio.go")
	for i, l := range apiLines {
		if strings.Contains(l, msg) {
			t.Fatalf("api 侧第 %d 行抄了一份能力话术 ⇒ 与 fileproc 那一份迟早漂移（漂移后 asrReason 认不出档位）：%s", i+1, strings.TrimSpace(l))
		}
	}
	proc := readGoCodeForCapOrder(t, "../fileproc/asr.go")
	hits := 0
	for _, l := range proc {
		if strings.Contains(l, msg) {
			hits++
		}
	}
	if hits == 0 {
		t.Fatal("fileproc/asr.go 里反而找不到那句能力话术 ⇒ 单源已经搬走，本锁的锚点需随迁（不许直接摘掉判据）")
	}
	// ★ 诚实边界（登记而不是假装没有）：fileproc 内部本来就有**三条**形状相近的 ASR 能力诊断句
	//
	//	（probeAsr 的"库未安装"／"模型资产缺失"与 healthcheck 的"资产缺失或加载失败"），
	//	它们是**运维诊断**话术、各有读者，本批不合并（合并会把 health 与请求面写成一句，读者不同）。
	//	本批真正收口的"只有一份"是这三件，缺一件就说明单源又散开了：
	//	① 客户面那一句的**前缀常量**只有一个定义；② 能力档**谓词**只有一个函数；
	//	③ 调用方（api）一个字都不抄。
	for _, def := range []string{"const asrUnavailableHead =", "func IsAsrUnavailableMessage("} {
		n := 0
		for _, l := range proc {
			if strings.Contains(l, def) {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("fileproc 里 %q 出现 %d 次（应为 1）⇒ 单源又散开了", def, n)
		}
	}
}

func TestAsrUnavailableErrBehaviourAndReasonTier(t *testing.T) {
	// 两条一致性判据（各抓一边的反方向错法）：
	//   · 就绪却报"不可用"＝能力闸把整条 ASR 链打死（麦克风点了永远说没装，health 一切正常）；
	//   · 没就绪却报 nil＝本闸形同不存在，客户继续往下撞 ffmpeg 那句"读不出时长"。
	avail := fileproc.AsrAvailable()
	err := fileproc.AsrUnavailableErr()
	if avail && err != nil {
		t.Fatalf("这台明确就绪（AsrAvailable=true）却仍被判不可用 ⇒ 能力闸把整条 ASR 链打死：%v", err)
	}
	if !avail && err == nil {
		t.Fatal("AsrAvailable=false 而 AsrUnavailableErr 回 nil ⇒ 这道闸形同不存在（顺序修了也没用）")
	}
	// 档位映射：拿一条同形错误喂 asrReason，必须是 capability_missing（不是 failed）。
	r := asrReason(errors.New("音频语音识别（ASR）不可用：服务器未安装 sherpa-onnx 或未部署 ASR 模型资产（assets/asr/*.onnx）"))
	if r != "capability_missing" {
		t.Fatalf("能力话术的档位应判 capability_missing，实得 %q ⇒ 前端按档位取词典会取到笼统失败句", r)
	}
	// 反向对照：一句真失败绝不能被判成能力缺失（否则运维支去重装资产）。
	if r2 := asrReason(errors.New("ASR 识别失败: exit status 1")); r2 != "failed" {
		t.Fatalf("普通识别失败应判 failed，实得 %q", r2)
	}
}
