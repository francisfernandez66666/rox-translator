// ============ 本文件职责中文说明 ============
// DEF-01（2026-10-24 云端 UAT）指令回显判据单测。
// 样本取自 evidence/FIX_followup/deliverable_115.md：第 2 段源文被替换成货币/纯度尾注的英译。
// 判据目标：回显判不可用（走极简指令重译/原文回落），合法翻译与「源文本身讲规范」不误杀。
// ============================================
package engine

import (
	"context"
	"strings"
	"testing"
)

const echoSampleSrc = "第一段：这是用于验收的中文样例文本，包含行业术语「销售线索」。"
const echoSampleOut = "The amount and currency should be presented accurately: If the original amount does not specify the currency, the default is Renminbi (CNY). The translation must maintain the use of Renminbi (¥/CNY/RMB/yuan) and should not replace or convert the currency symbol to any other currency symbols. The amount value should remain unchanged. Do not repeat the original text, nor should the original term be noted in the translation in parentheses or in any other form."

func TestFileTranslationRejectsInstructionEcho(t *testing.T) {
	e := &Engine{}
	if e.fileTranslationUsable(context.Background(), echoSampleSrc, echoSampleOut, "en") {
		t.Fatal("货币/纯度尾注的英译仍被判可用——DEF-01 判据没生效")
	}
	if isProseInstructionEcho(echoSampleSrc, echoSampleOut, "en", "zh") != true {
		t.Fatal("4-gram/特征词判据没有识别出指令回显")
	}
}

func TestFileTranslationKeepsLegitTranslation(t *testing.T) {
	e := &Engine{}
	legit := "First paragraph: This is a Chinese sample text for acceptance, containing the industry term Sales Leads."
	if !e.fileTranslationUsable(context.Background(), echoSampleSrc, legit, "en") {
		t.Fatal("合法译文被误判为指令回显")
	}
}

func TestFileTranslationExemptsSourceAboutRules(t *testing.T) {
	e := &Engine{}
	src := "金额币种保真：原文金额未指明币种时默认人民币，译文须保持人民币口径（¥/CNY/RMB/yuan）。"
	out := "Currency rule: if the original amount does not specify the currency, the default is Renminbi (CNY/RMB/yuan)."
	if !e.fileTranslationUsable(context.Background(), src, out, "en") {
		t.Fatal("源文本身讲货币规范时被误杀（源文-语料差分应豁免）")
	}
}

func TestFileTranslationCatchesLongExpansion(t *testing.T) {
	e := &Engine{}
	out := strings.Repeat("This is an unrelated explanatory sentence that should never be delivered as a translation. ", 8)
	if e.fileTranslationUsable(context.Background(), echoSampleSrc, out, "en") {
		t.Fatal("32 字源文吐出数百字说明，长文爆炸判据没有拦住")
	}
}

func TestFileEchoGuardCanBeDisabled(t *testing.T) {
	t.Setenv("FILE_ECHO_GUARD", "0")
	e := &Engine{}
	// 关闭后仍保留 IsTranslationUsable；回显不是同文/尖括号，故放行（应急回滚语义）。
	if !e.fileTranslationUsable(context.Background(), echoSampleSrc, echoSampleOut, "en") {
		t.Fatal("FILE_ECHO_GUARD=0 时不应再判回显")
	}
}
