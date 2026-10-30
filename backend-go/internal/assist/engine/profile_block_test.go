// ============================================================================
// profile_block_test.go — 画像进 system 前缀的形态与边界（★ 2026-10-30 组7 §8.2，G27/G28）
//
// 覆盖：
//   - 匿名 ⇒ 提示词里**零**画像段（不是「画像：无」）；
//   - 登录 ⇒ 称呼/职业/租户/行业/付费状态/余额/到期/任务奖励齐全；
//   - 两个不同用户的 ctx 并发构建提示词，A 的字段一次都不出现在 B 的提示词里（P-4 串号腿）；
//   - 邮箱是脱敏形态，提示词里零 token/密码类字样（G28）；
//   - 拼接点只有一个：非测试源码里 `visitorProfileBlock(` 只允许出现在 profile.go（定义）
//     与 engine.go（buildSystemPrompt 调用）两处（P-6）。
//
// ============================================================================
package engine

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"translator/internal/assist/profile"
)

func sampleVisitor() *profile.Visitor {
	return &profile.Visitor{
		UID:             42,
		DisplayName:     "张三",
		JobRole:         "前端工程师",
		PreferredLang:   "en",
		Status:          "active",
		Email:           "z***@example.com",
		TenantName:      "极石汽车",
		Industry:        "汽车",
		IsPersonal:      false,
		BillingEnforced: true,
		PackageCode:     "pro",
		PackageExpires:  "2026-12-01T00:00:00Z",
		AutoRenew:       true,
		PointsBalance:   320,
		PointsUsedToday: 12,
		PointsUsedMonth: 300,
		HitWall:         true,
		TaskRewards:     []profile.TaskReward{{Title: "每日登录", Today: 1, Week: 3, Total: 12}},
	}
}

// slowlessCtx 取消的 ctx：让 buildSystemPrompt 里的现值/档位两段立刻放弃网络请求，
// 本文件的断言只关心画像段本身（否则每条断言都要等 3 秒超时）。
func slowlessCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestVisitorProfileBlockAnonymousIsAbsent(t *testing.T) {
	e := newTestEngine(t)
	if got := visitorProfileBlock(context.Background()); got != "" {
		t.Fatalf("匿名必须零画像段，实际 %q", got)
	}
	p := e.buildSystemPrompt(slowlessCtx(), nil, "zh")
	if strings.Contains(p, "访客画像") {
		t.Fatalf("匿名提示词不得出现画像段：%s", p)
	}
}

func TestVisitorProfileBlockLoggedInFields(t *testing.T) {
	ctx := WithVisitorProfile(context.Background(), sampleVisitor())
	got := visitorProfileBlock(ctx)
	for _, want := range []string{
		"访客画像", "张三", "前端工程师", "极石汽车", "汽车", "pro",
		"320 分", "12 分", "300 分", "2026-12-01", "每日登录", "z***@example.com",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("画像段缺少 %q：\n%s", want, got)
		}
	}
	// G28：脱敏与零敏感**值**（说明句里出现「密码」二字是指令，不是数据；判据按字段形态取）。
	if strings.Contains(got, "zhangsan@") || strings.Contains(got, "密码：") ||
		strings.Contains(got, "password:") || strings.Contains(got, "sk-") {
		t.Fatalf("画像段出现敏感值：\n%s", got)
	}
}

// TestVisitorProfileBlockConcurrentNoCrossTalk G27 串号腿：
// 两个独立 ctx 并发构建提示词，各自只出现自己的称呼/租户。
func TestVisitorProfileBlockConcurrentNoCrossTalk(t *testing.T) {
	a := sampleVisitor()
	a.UID, a.DisplayName, a.TenantName = 1001, "甲用户", "甲公司"
	b := sampleVisitor()
	b.UID, b.DisplayName, b.TenantName = 1002, "乙用户", "乙公司"
	var wg sync.WaitGroup
	results := make([]string, 2)
	for i, v := range []*profile.Visitor{a, b} {
		wg.Add(1)
		go func(i int, v *profile.Visitor) {
			defer wg.Done()
			results[i] = visitorProfileBlock(WithVisitorProfile(context.Background(), v))
		}(i, v)
	}
	wg.Wait()
	if !strings.Contains(results[0], "甲用户") || strings.Contains(results[0], "乙用户") {
		t.Fatalf("A 的画像串到了 B：\n%s", results[0])
	}
	if !strings.Contains(results[1], "乙用户") || strings.Contains(results[1], "甲用户") {
		t.Fatalf("B 的画像串到了 A：\n%s", results[1])
	}
}

// TestVisitorProfileNotInCanned G28 前半：欢迎词/chips 的拼装路径不吃画像 ctx。
// 它们走 localize，不经 buildSystemPrompt——这条断言锁住「即使 ctx 里带着画像，
// 直出文案也不会把画像字段带出去」。
func TestVisitorProfileNotInCanned(t *testing.T) {
	e := newTestEngine(t)
	ctx := WithVisitorProfile(context.Background(), sampleVisitor())
	got := e.LocalizeGreeting(ctx, "你好呀", "zh")
	if strings.Contains(got, "张三") || strings.Contains(got, "极石汽车") {
		t.Fatalf("canned 欢迎词里出现了画像字段：%q", got)
	}
}

// TestVisitorProfileBlockSingleJoinPoint P-6：拼接点只有一个。
// 按包内**非测试**源码扫 `visitorProfileBlock(`，只允许 profile.go 的定义与 engine.go 的调用。
func TestVisitorProfileBlockSingleJoinPoint(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	total := 0
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		n := strings.Count(string(b), "visitorProfileBlock(")
		if name == "engine.go" {
			if n != 1 {
				t.Fatalf("engine.go 应恰好一处调用，实际 %d", n)
			}
		} else if name != "profile.go" && n > 0 {
			t.Fatalf("画像拼接点出现在 %s（只允许 engine.go 调、profile.go 定义）", name)
		}
		total += n
	}
	// profile.go 定义 1 + engine.go 调用 1。
	if total != 2 {
		t.Fatalf("拼接点总数应为 2（定义+调用），实际 %d", total)
	}
}
