// ============ ticket_timeout_test.go · 职责说明 ============
// 2026-10-26 用户令：图片/音频处理时间不按 25 分钟翻译预算卡。
//
// 锁两条：
//
//	① 媒体文件的专属上限：图片 60 分钟、音频 90 分钟（音频优先）；
//	② 无媒体/无任务/无 Store 时回落文本档 25 分钟（fail-soft，不因一次库读失败把任务判短命）。
//
// 反证：把 mediaTimeoutForFiles 改回恒 0 ⇒ 本文件第一条红；把 jobExecTimeout 的 nil 分支去掉 ⇒ 第二条红。
// ============================================
package service

import (
	"testing"
	"time"

	"translator/internal/queue"
	"translator/internal/store"
)

func TestMediaTimeoutForFiles(t *testing.T) {
	cases := []struct {
		name  string
		files []*store.TicketFile
		want  time.Duration
	}{
		{"无文件", nil, 0},
		{"文本", []*store.TicketFile{{FileName: "a.txt"}}, 0},
		{"图片", []*store.TicketFile{{FileName: "a.png"}}, mediaImageJobTimeout},
		{"音频", []*store.TicketFile{{FileName: "a.mp3"}}, mediaAudioJobTimeout},
		{"图片+音频（音频优先）", []*store.TicketFile{{FileName: "a.png"}, {FileName: "b.wav"}}, mediaAudioJobTimeout},
		{"FileName 缺省回落 FilePath", []*store.TicketFile{{FilePath: "/tmp/x.webp"}}, mediaImageJobTimeout},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mediaTimeoutForFiles(c.files); got != c.want {
				t.Fatalf("mediaTimeoutForFiles=%v want %v", got, c.want)
			}
		})
	}
}

func TestJobExecTimeoutFallsBackToText(t *testing.T) {
	s := &TicketService{}
	if got := s.jobExecTimeout(nil); got != ticketJobTimeout {
		t.Fatalf("nil job 应回落 %v，got %v", ticketJobTimeout, got)
	}
	// Store 为空时不得 panic，仍回落文本档。
	if got := s.jobExecTimeout(&queue.Job{}); got != ticketJobTimeout {
		t.Fatalf("无 Store 应回落 %v，got %v", ticketJobTimeout, got)
	}
}
