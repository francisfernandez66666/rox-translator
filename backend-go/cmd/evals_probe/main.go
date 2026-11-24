// evals_probe · tier3 主判/复核探针：对照 GLM-4-9B 与候选模型的判别力和稳定性。
//
// 背景（2026-11-24 凌晨）：tier3 语义闸"HY 回译 + evals 打分(阈值70)"在清洗 job 上误伤率过高，
// 抽查看到的打分几乎与译文对错无关（回译逐字相同的 长视频 打 0/0；回译取错义项的 卖家 打 95/90）。
// 已确认不是 JSON 解析 bug（原始输出是干净的 ```json 块，ExtractJSON 能解析），而是打分腿本身。
//
// 本探针在同一模型上做三臂对照，同一批样本、同一批参数（温度 0）：
//
//	old     = 旧版 qualityjudge 提示词（含 ליד / צ'רי 两个希伯来语举例）
//	clean   = 当前产品提示词（无举例、明确评分锚点、回译只当参考不当证据）
//	nothink = clean + thinking:{type:disabled}（推理系模型关掉思维链，避免预算被思考吃光）
//
// 每臂默认重复 2 次 —— 同一输入、同一提示词、温度 0 下如果两次分数不同，那叫"模型抖动"，
// 不是"提示词没写好"；这一维是"prompt 可修 vs 模型不适配"的关键判据。
//
// 输出：逐次 TSV（R 行，可直接贴进表格）+ 末尾汇总（应过/应拒命中率、抖动数）。
// 用法（在部署机上，与现网同库同 Key）：
//
//	env DB_DRIVER=postgres DB_DSN="$(...)" /tmp/evals_probe -reps 2 > /tmp/evals_probe2.log 2>&1
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"translator/internal/config"
	"translator/internal/db"
	"translator/internal/llm"
	"translator/internal/qualityjudge"
	"translator/internal/store"
)

// rec 一次打分调用的读数。
type rec struct {
	arm    string
	grp    string
	src    string
	tgt    string
	want   int // 1=应通过(>=阈值) -1=应拒绝 0=观察样本
	rep    int
	term   float64
	sem    float64
	total  float64
	finish string
	parsed bool
	raw    string
}

// main 运行打分模型对照探针，输出逐次评分、抖动与分组命中率。
func main() {
	reps := flag.Int("reps", 2, "每臂每样本重复次数（>=2 才能量抖动）")
	modelOverride := flag.String("model", "", "覆盖打分模型（默认读 stage_models.initial_evals）")
	armsFlag := flag.String("arms", "old,clean,nothink", "基础臂（逗号分隔；候选模型臂用 -candidates）")
	candidates := flag.String("candidates", "", "候选打分模型（逗号分隔），每档跑同一份 clean 提示词")
	candTok := flag.Int("candtok", 64, "候选模型臂的 max_tokens（默认与生产打分腿一致）")
	listModels := flag.Bool("listmodels", false, "只列出该打分档 base 上可用的模型名，不跑样本")
	dim5 := flag.Bool("dim5", false, "改跑 5 维评估链（internal/evals 提示词，200 tokens）而非 term/semantic 打分链")
	// 打分档默认 initial_evals；-stage review 取 review 那一档的 base/key。
	// 不同档可能使用不同网关与 Key，换档时必须连 base+key 一起换。
	stageFlag := flag.String("stage", config.StageInitialEvals, "取哪一档 stage_models 的 base/key/model（如 review）")
	// 有些网关（intern-ai 那台）有严格 QPS/并发档，连发就 429；
	// 429 与"模型判错"在汇总里同形（都是 0/0），所以必须能限速再判。
	delayMs := flag.Int("delay", 0, "每次调用之间停多少毫秒（网关 QPS 严格时用，如 1200）")
	// 生产 term/semantic 腿是 64 tokens；候选臂默认同为 64。
	// 要判"这个模型在现网参数下能不能活"，必须能把所有臂统一压到 64 复跑一次。
	tokOverride := flag.Int("tok", 0, "覆盖所有臂的 max_tokens（0=不覆盖；判生产可用性时给 64）")
	flag.Parse()

	cfg := config.Default()
	rawDB, err := db.Open(db.Config{Driver: db.DriverPostgres, DSN: os.Getenv("DB_DSN")})
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL open db: %v\n", err)
		os.Exit(2)
	}
	defer rawDB.Close()
	st, err := store.New(rawDB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL store: %v\n", err)
		os.Exit(2)
	}

	base, key, model, resolveErr := resolveScorer(st, *stageFlag)
	if resolveErr != nil {
		fmt.Fprintf(os.Stderr, "FATAL 打分档读取失败: %v\n", resolveErr)
		os.Exit(2)
	}
	if *modelOverride != "" {
		model = *modelOverride
	}
	if base == "" || key == "" || model == "" {
		fmt.Fprintf(os.Stderr, "FATAL 打分档配置不全 base=%q key_empty=%v model=%q\n", base, key == "", model)
		os.Exit(2)
	}
	minScore := float64(st.ConfigIntAllowZero(qualityjudge.ConfigKeyMinScore, int(qualityjudge.DefaultMinScore)))
	if v, err := st.GetConfig(qualityjudge.ConfigKeyMinScore); err == nil {
		fmt.Printf("META\tmodel=%s\tbase=%s\tmin_score_cfg=%s\treps=%d\n", model, base, v, *reps)
	} else {
		fmt.Printf("META\tmodel=%s\tbase=%s\treps=%d\n", model, base, *reps)
	}

	client := llm.NewClient(cfg)
	ctx := context.Background()
	cases := probeCases()
	arms := selectArms(*armsFlag, *candidates, *candTok)
	if *tokOverride > 0 {
		for i := range arms {
			arms[i].maxTokens = *tokOverride
		}
	}

	if *listModels {
		listRemoteModels(ctx, base, key)
		return
	}

	var recs []rec
	if *dim5 {
		// 5 维链（evals.Evaluate）用同一批候选模型跑一遍：这条链吃同一个 stage key，
		// 换模型必须同时对它成立，否则"换对了打分腿、换坏了评估腿"。
		arms = dim5Arms(strings.Split(*candidates, ","), 200)
		if len(arms) == 0 {
			fmt.Fprintln(os.Stderr, "FATAL -dim5 需要 -candidates 指定模型")
			os.Exit(2)
		}
	}
	for _, a := range arms {
		useModel := model
		if a.model != "" {
			useModel = a.model
		}
		// 把第一样本真正发出去的 user 提示词打一行（%q 保留换行）——
		// "探针自己把话说错"（如语言名写死）与产品缺陷在分数上同形，只能靠这一行分开。
		if len(cases) > 0 {
			fmt.Printf("P\t%s\t%s\t%q\n", a.name, useModel, a.user(cases[0]))
		}
		for _, c := range cases {
			for i := 1; i <= *reps; i++ {
				if *delayMs > 0 {
					time.Sleep(time.Duration(*delayMs) * time.Millisecond)
				}
				callCtx := ctx
				if len(a.extra) > 0 {
					callCtx = llm.WithChatExtraParams(ctx, a.extra)
				}
				msgs := []map[string]string{
					{"role": "system", "content": a.sys},
					{"role": "user", "content": a.user(c)},
				}
				out, finish, cerr := client.CallChat(callCtx, base, key, useModel, msgs, a.maxTokens, false, 0.0)
				r := rec{arm: a.name, grp: c.grp, src: c.src, tgt: c.tgt, want: c.want, rep: i, finish: finish}
				if cerr != nil {
					r.raw = "ERR: " + cerr.Error()
				} else {
					r.term, r.sem, r.total, r.parsed = parseProbeScores(out, finish, strings.HasPrefix(a.name, "d5:"))
					r.raw = strings.ReplaceAll(strings.ReplaceAll(out, "\n", "\\n"), "\t", " ")
				}
				fmt.Printf("R\t%s\t%s\t%d\t%.0f\t%.0f\t%s\t%s\n",
					r.arm, r.src, r.rep, r.term, r.sem, r.finish, r.raw)
				recs = append(recs, r)
			}
		}
	}
	summarize(arms, cases, recs, *reps, minScore)
}

// resolveScorer 按产品口径读取指定阶段模型；档位缺失时直接报错，绝不回落 online 假测另一条链。
func resolveScorer(st *store.Store, stage string) (base, key, model string, err error) {
	raw, gerr := st.GetConfig("stage_models")
	if gerr != nil || raw == "" {
		return "", "", "", fmt.Errorf("stage_models 未配置")
	}
	var m config.StageModels
	if json.Unmarshal([]byte(raw), &m) != nil {
		return "", "", "", fmt.Errorf("stage_models 解析失败")
	}
	sm, ok := m[stage]
	if !ok || sm.APIBase == "" || sm.Model == "" {
		return "", "", "", fmt.Errorf("模型档位 %s 未配置", stage)
	}
	key = sm.APIKey
	if key != "" {
		key = store.DecryptSecret(key)
	}
	if key == "" {
		ek, _ := st.GetConfig("online_api_key")
		key = store.DecryptSecret(ek)
	}
	if key == "" {
		return "", "", "", fmt.Errorf("模型档位 %s 的 API Key 不可用", stage)
	}
	return sm.APIBase, key, sm.Model, nil
}

// summarize 按（臂 × 样本组）汇总：JSON 成功数、应过/应拒命中、同输入抖动。
//
// ★ 分组的理由是"判据不同源"：prod/ctrl 是手工样本（含回译取错义项的两条），
// real 是现网真实被丢弃的 15 条（回译读数从 translator.log 抄来，一条不改）——
// 两组合起来算命中率会把"回译本身错"的样本算到模型头上。
func summarize(arms []arm, cases []probeCase, recs []rec, reps int, minScore float64) {
	fmt.Println("---- SUMMARY ----")
	groups := []string{}
	seenGrp := map[string]bool{}
	for _, c := range cases {
		if !seenGrp[c.grp] {
			seenGrp[c.grp] = true
			groups = append(groups, c.grp)
		}
	}
	for _, a := range arms {
		for _, g := range groups {
			var ok, passed, wantPass, wantRej, rejOK, unstable, observed int
			for _, c := range cases {
				if c.grp != g {
					continue
				}
				verdicts := map[bool]int{}
				var scores []string
				var seen []string
				for _, r := range recs {
					if r.arm != a.name || r.grp != c.grp || r.src != c.src || r.tgt != c.tgt {
						continue
					}
					if !r.parsed {
						continue
					}
					ok++
					pass := r.term >= minScore && r.sem >= minScore
					if strings.HasPrefix(a.name, "d5:") {
						pass = r.total >= minScore
					}
					verdicts[pass]++
					scores = append(scores, fmt.Sprintf("%.0f/%.0f", r.term, r.sem))
					seen = append(seen, fmt.Sprintf("%v", pass))
				}
				if c.want == 1 {
					wantPass++
					if verdicts[true] > 0 && verdicts[false] == 0 {
						passed++
					}
				} else if c.want == -1 {
					wantRej++
					if verdicts[false] > 0 && verdicts[true] == 0 {
						rejOK++
					}
				}
				// 抖动：同一输入多次判定不一致（判定翻转或分数不同）
				if reps >= 2 {
					differ := false
					for i := 1; i < len(scores); i++ {
						if scores[i] != scores[0] {
							differ = true
						}
					}
					for i := 1; i < len(seen); i++ {
						if seen[i] != seen[0] {
							differ = true
						}
					}
					if differ {
						unstable++
					}
				}
				if len(scores) > 0 {
					observed++
					fmt.Printf("C\t%s\t[%s]\t%s\twant=%d\t%s\n", a.name, g, c.src, c.want, strings.Join(scores, " "))
				}
			}
			fmt.Printf("A\t%s\t[%s]\tjson_ok=%d/%d\t应过命中=%d/%d\t应拒命中=%d/%d\t抖动=%d/%d\n",
				a.name, g, ok, countGroup(cases, g)*reps, passed, wantPass, rejOK, wantRej, unstable, observed)
		}
	}
	fmt.Println("DONE")
}

// countGroup 该组的样本条数（汇总分母用；写成派生量，加样本不用改分母）。
func countGroup(cases []probeCase, grp string) int {
	n := 0
	for _, c := range cases {
		if c.grp == grp {
			n++
		}
	}
	return n
}
