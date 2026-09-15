// k6 容量探针（★ 2026-09-14 商业化 D-1 定案用，替换旧 500VU 理想版）
// 目标：真实链路（同步短文翻译 → 闸 → LLM 上游）下找「无过载雪崩」的安全并发，
//       产出各台阶成功率/P95/P99/限流数 → 容量预告与超卖预案依据。
// 用法（单台阶一个 VU 档，逐档跑）：
//   k6 run -e BASE=https://<host> -e TOKEN=<api-key> -e VUS=8 -e DURATION=75s deploy/loadtest/k6.js
// 口径：命中 /openapi/v1/translate（同步，≤5000 字符）；每请求注入随机串绕 TM 缓存；
//       rate_limited（HTTP 200 业务码或 429/503）=过载信号单独计数，不算系统错误。
import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend, Counter } from 'k6/metrics';


const BASE = __ENV.BASE || 'http://127.0.0.1:8787';
const TOKEN = __ENV.TOKEN || '';
const VUS = parseInt(__ENV.VUS || '4', 10);
const DURATION = __ENV.DURATION || '75s';

const errRate = new Rate('app_errors'); // 5xx/网络错误/业务未知错误
const busyRate = new Rate('overloaded'); // 过载信号样本占比（仅被 add 的失败面）
const success = new Rate('translate_success'); // 译文交付成功
const limited = new Counter('translate_limited'); // 限流总次数
const latency = new Trend('translate_ms', true);

// ★ P1 压测基线量化（2026-09-15，见《P0P2待办核实报告_20260915.md》P1-3）：
//   阈值预算默认对齐《容量预告与超卖预案_20260914.md》实测定案——
//   安全档（VU≤8）P99≈10s（单请求真实模型时延 7–8s + 排队），预算取 P99<15s（约 1.5 倍余量）；
//   系统错误率<1%（09-14 实测恒 0）；可用 env 覆盖：P99_MS=8000 k6 run ...
const P99_MS = parseInt(__ENV.P99_MS || '15000', 10);
const ERR_RATE_MAX = parseFloat(__ENV.ERR_RATE_MAX || '0.01');

export const options = {
  scenarios: {
    probe: { executor: 'constant-vus', vus: VUS, duration: DURATION, exec: 'default' },
  },
  // ★ 量化验收阈值：超预算的台阶在退出码上判 FAIL（k6 非零退出），可直接做发布/容量闸门。
  thresholds: {
    app_errors: [`rate<${Math.max(ERR_RATE_MAX, 0.10)}`], // 粗闸保留（09-14 口径）
    translate_ms: [`p(99)<${P99_MS}`],                    // P99 时延预算
    translate_success: [`rate>${1 - ERR_RATE_MAX}`],      // 成功交付率（限流拒绝不计为失败交付面？——见 handleSummary 口径说明）
  },
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

// 约 40 汉字 → 英文：单请求成本最低、又穿透完整链路
const BASE_SENT = '智能驾驶域控制器固件升级已完成，蓝牙钥匙绑定状态同步成功，请提醒用户在下次上车前重新校准迎宾灯语与座椅记忆位置。';

export default function () {
  const payload = JSON.stringify({
    text: BASE_SENT + `（批次编号 R${__ITER}-${(Math.floor(Math.random() * 900000) + 100000)}）`,
    target_lang: 'en',
    source_lang: 'zh',
  });
  const res = http.post(`${BASE}/openapi/v1/translate`, payload, {
    headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + TOKEN },
    timeout: '60s',
  });
  const s = res.status;
  check(res, { 'status ok': () => s === 200 });
  if (s === 200) {
    try {
      const b = JSON.parse(res.body);
      const tr = b.translations || {};
      const has = b.success === true && Object.values(tr).some((v) => !!v);
      if (has) { errRate.add(0); success.add(1); }
      else if (b.error_code === 'rate_limited') { errRate.add(0); busyRate.add(1); limited.add(1); success.add(0); }
      else if (b.error_code === 'insufficient_balance') { errRate.add(0); } // 预算耗尽：单独归因，不算系统错误
      else { errRate.add(1); success.add(0); }
    } catch (e) { errRate.add(1); success.add(0); }
  } else if (s === 429 || s === 503 || s === 422) {
    errRate.add(0); busyRate.add(1); limited.add(1); success.add(0);
  } else {
    errRate.add(1); success.add(0);
  }
  if (res.timings && res.timings.duration) latency.add(res.timings.duration);
  sleep(1);
}

export function handleSummary(data) {
  const m = data.metrics;
  const g = (name, key) => (m[name] && m[name].values ? m[name].values[key] : null);
  const samples = (name) => (m[name] ? (m[name].values.passes || 0) + (m[name].values.fails || 0) : 0);
  const out = {
    generated_at: new Date().toISOString(),
    base: BASE,
    vus: VUS, duration: DURATION,
    budget_p99_ms: P99_MS,
    requests: m.http_reqs ? m.http_reqs.values.count : 0,
    success_samples: samples('translate_success'),
    success_rate: m.translate_success ? m.translate_success.values.rate : null,
    limited_count: m.translate_limited ? m.translate_limited.values.count : 0,
    app_errors_rate: m.app_errors ? m.app_errors.values.rate : null,
    // ★ 阈值判定结果快照（k6 评估顺序：thresholds 在 summary 前，FAIL 时退出码非零）
    thresholds_passed: data.thresholds ? Object.values(data.thresholds).every((t) => (Array.isArray(t) ? t.every((x) => x.ok) : true)) : null,
    http_ms: { avg: g('http_req_duration', 'avg'), p95: g('http_req_duration', 'p(95)'), p99: g('http_req_duration', 'p(99)'), max: g('http_req_duration', 'max') },
    translate_ms: { avg: g('translate_ms', 'avg'), p95: g('translate_ms', 'p(95)'), p99: g('translate_ms', 'p(99)'), max: g('translate_ms', 'max') },
  };
  const ts = new Date().toISOString().replace(/[:.]/g, '-');
  // ★ 结果归档（P1 基线留痕）：默认写 deploy/loadtest/results/（RESULTS_DIR 可覆盖），
  //   全量 JSON 一份 + 单台阶 CSV 一份；多台阶矩阵由 run_capacity_matrix.sh 合并为总表。
  const dir = __ENV.RESULTS_DIR || 'deploy/loadtest/results';
  const header = 'generated_at,base,vus,requests,success_rate,p99_ms,limited,err_rate,passed\n';
  const csvRow = `${out.generated_at},${BASE.replace(/https?:\/\//, '')},${VUS},${out.requests},${out.success_rate},${out.translate_ms.p99},${out.limited_count},${out.app_errors_rate},${out.thresholds_passed}\n`;
  return {
    stdout: '\n===== 容量探针 VU=' + VUS + ' 阈值P99<' + P99_MS + 'ms =====\n' + JSON.stringify(out, null, 2) + '\n',
    [`${dir}/k6_capacity_${VUS}vu_${ts}.json`]: JSON.stringify(data, null, 2),
    [`${dir}/k6_capacity_${VUS}vu_${ts}.csv`]: header + csvRow,
  };
}
