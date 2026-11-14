package knowledge

import "testing"

// KN12 双 DSN 必须不等：空 / 相等 / 归一化后相等一律拒；真正不等才放行。
// ★ 「相等必 err」与「不等必 nil」两条腿都在：
//   把断言改成 `if false`（M0315）⇒ 相等档红；改成永不放过 ⇒ 不等档红。
func TestKN12AssertDistinct(t *testing.T) {
	cases := []struct {
		name    string
		main    string
		know    string
		wantErr bool
	}{
		{"空串", "", "", true},
		{"相等", "postgres://u:p@h:5432/db", "postgres://u:p@h:5432/db", true},
		{"大小写差异", "POSTGRES://U:P@H/DB", "postgres://u:p@h/db", true},
		{"尾斜杠差异", "postgres://u:p@h/db", "postgres://u:p@h/db/", true},
		{"localhost同库", "postgres://u:p@localhost:5432/db", "postgres://u:p@127.0.0.1:5432/db", true},
		{"默认端口写法", "postgres://u:p@h/db", "postgres://u:p@h:5432/db", true},
		{"不同库名", "postgres://u:p@h/langcross", "postgres://u:p@h/langcross_demo", false},
		{"不同端口", "postgres://u:p@h:5432/db", "postgres://u:p@h:5433/db", false},
	}
	for _, c := range cases {
		err := AssertDistinct(c.main, c.know)
		if (err != nil) != c.wantErr {
			t.Fatalf("KN12 档[%s] 判错：main=%q know=%q err=%v wantErr=%v（把断言改成 if false ⇒ M0315 在此红）",
				c.name, c.main, c.know, err, c.wantErr)
		}
	}
}

// StartupGuard 接线在：配置里双 DSN 相等也必须拒启。
func TestKN12StartupGuardWired(t *testing.T) {
	cfg := Config{MainDSN: "postgres://u@h/db", KnowledgeDSN: "postgres://u@h/db"}
	if err := StartupGuard(cfg); err == nil {
		t.Fatalf("KN12 StartupGuard 未拦住同 DSN 配置")
	}
	cfg.KnowledgeDSN = "postgres://u@h/other"
	if err := StartupGuard(cfg); err != nil {
		t.Fatalf("KN12 不同 DSN 不该拦: %v", err)
	}
}
