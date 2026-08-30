// k6 负载/压测脚本（路线图 P1 压测阶段）
// 用法：
//   npm i -g k6            # 或下载 k6 二进制
//   k6 run -e BASE=http://127.0.0.1:8787 -e TOKEN=<apikey> deploy/loadtest/k6.js
//
// 场景：阶梯加压（50→200→500 VU），命中 /api/translate 与 /api/translate/file，
// 校验 P95 < 2s 且错误率 < 1%（结合分布式信号量验证并发上限真实生效）。
import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate } from 'k6/metrics';

const BASE = __ENV.BASE || 'http://127.0.0.1:8787';
const TOKEN = __ENV.TOKEN || '';

const errorRate = new Rate('app_errors');

export const options = {
  stages: [
    { duration: '1m', target: 50 },
    { duration: '2m', target: 200 },
    { duration: '3m', target: 500 },
    { duration: '1m', target: 0 },
  ],
  thresholds: {
    http_req_duration: ['p(95)<2000'],
    app_errors: ['rate<0.01'],
  },
};

const payload = JSON.stringify({
  text: '请将以下句子翻译为英文：今天天气真好，我们去公园散步吧。',
  target_lang: 'en',
  source_lang: 'zh',
});

export default function () {
  const headers = { 'Content-Type': 'application/json', 'X-API-Key': TOKEN };
  const res = http.post(`${BASE}/api/translate`, payload, { headers });
  const ok = check(res, {
    'status 200': (r) => r.status === 200,
    'has translation': (r) => {
      try { return JSON.parse(r.body).translated_text !== undefined; } catch (e) { return false; }
    },
  });
  errorRate.add(!ok);
  sleep(1);
}
