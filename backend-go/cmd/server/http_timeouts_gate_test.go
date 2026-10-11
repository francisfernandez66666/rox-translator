// 本文件锁 P1④ 的**接线级**判据（2026-10-11 慢 SQL 批）：主站 http.Server 必须带
// WriteTimeout 与 IdleTimeout 兜底，且**不许**设 ReadTimeout。
// 为什么是静态源码锁：这些字段只影响真实监听的超时行为，httptest 里读不回来；
// 「字段被谁删掉」正是这一族退化的唯一形态（行为测试看不见，源码锁当场红）。
// ReadTimeout 那条负向是刻意的：大文件上传在慢线路上是合法长请求，
// body 读超时会把正常上传成套 4xx——现网上传面只有比它更短的合法预算，没有比它更长的非法请求需要它拦。
package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestMainServerTimeoutWiring(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("读 main.go 失败: %v", err)
	}
	body := string(src)
	i := strings.Index(body, "s := &http.Server{")
	if i < 0 {
		t.Fatal("找不到主站 http.Server 字面量（接线锁的锚点先失效了）")
	}
	seg := body[i:]
	if end := strings.Index(seg, "\n\t}"); end > 0 {
		seg = seg[:end]
	}
	if !strings.Contains(seg, "WriteTimeout:") {
		t.Fatal("主站 http.Server 没有 WriteTimeout ⇒ 挂死 handler 继续无限占连接（P1④ 兜底被摘）")
	}
	if !strings.Contains(seg, "IdleTimeout:") {
		t.Fatal("主站 http.Server 没有 IdleTimeout ⇒ keep-alive 空闲连接不回收（P1④ 兜底被摘）")
	}
	// 负向（配正向对照防"整段没匹配到所以恒不含"的恒真空转——上面两条正向已钉死同一字面量在段内存在）：
	if re := regexp.MustCompile(`\bReadTimeout:`); re.MatchString(seg) {
		t.Fatal("主站 http.Server 设了 ReadTimeout ⇒ 慢线路的大文件上传会被正常掐死（口径见函数头注释）")
	}
}
