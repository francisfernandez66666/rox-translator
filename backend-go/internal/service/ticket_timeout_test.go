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
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

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

func TestLongTaskBudgetsRaised(t *testing.T) {
	if ticketJobTimeout < 60*time.Minute || fileJobTimeout < 120*time.Minute ||
		mediaImageJobTimeout < 120*time.Minute || mediaAudioJobTimeout < 180*time.Minute {
		t.Fatalf("长任务超时未整体加长：文本=%v 文件=%v 图片=%v 音频=%v",
			ticketJobTimeout, fileJobTimeout, mediaImageJobTimeout, mediaAudioJobTimeout)
	}
}

func TestJobExecTimeoutFileNotBoundByTextBudget(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	st, err := store.New(db)
	if err != nil {
		t.Fatal(err)
	}
	tkt, err := st.CreateTicket(1, 1, "file job", "", "/tmp/a.md", "he")
	if err != nil {
		t.Fatal(err)
	}
	id := tkt.ID
	if _, err := st.AddTicketFile(&store.TicketFile{TenantID: 1, TicketID: id, FileName: "a.md", FilePath: "/tmp/a.md"}); err != nil {
		t.Fatal(err)
	}
	s := &TicketService{Store: st}
	job := &queue.Job{Type: "ticket_run", Payload: queue.NewTicketPayload(id)}
	if got := s.jobExecTimeout(job); got != fileJobTimeout {
		t.Fatalf("文件工单执行上限=%v，want %v（25 分钟会撞上 199 行翻译实测耗时）", got, fileJobTimeout)
	}
	if fileJobTimeout <= ticketJobTimeout {
		t.Fatal("文件工单上限必须严格大于文本工单上限")
	}
}
