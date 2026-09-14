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

export const options = {
  scenarios: {
    probe: { executor: 'constant-vus', vus: VUS, duration: DURATION, exec: 'default' },
  },
  thresholds: { app_errors: ['rate<0.10'] },
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
    requests: m.http_reqs ? m.http_reqs.values.count : 0,
    success_samples: samples('translate_success'),
    success_rate: m.translate_success ? m.translate_success.values.rate : null,
    limited_count: m.translate_limited ? m.translate_limited.values.count : 0,
    app_errors_rate: m.app_errors ? m.app_errors.values.rate : null,
    http_ms: { avg: g('http_req_duration', 'avg'), p95: g('http_req_duration', 'p(95)'), p99: g('http_req_duration', 'p(99)'), max: g('http_req_duration', 'max') },
    translate_ms: { avg: g('translate_ms', 'avg'), p95: g('translate_ms', 'p(95)'), p99: g('translate_ms', 'p(99)'), max: g('translate_ms', 'max') },
  };
  const ts = new Date().toISOString().replace(/[:.]/g, '-');
  return {
    stdout: '\n===== 容量探针 VU=' + VUS + ' =====\n' + JSON.stringify(out, null, 2) + '\n',
    [`/tmp/k6_capacity_${ts}.json`]: JSON.stringify(data, null, 2),
  };
}
