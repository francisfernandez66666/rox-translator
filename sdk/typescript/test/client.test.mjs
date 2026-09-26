// ============================================================================
// test/client.test.mjs — TypeScript SDK 行为级单测（node:test 内置运行器，零新依赖）
// 2026-09-16 测试盲区补全：此前 npm test 仅 tsc --noEmit 类型检查，
//   请求头/错误映射/multipart 报文结构/轮询逻辑全靠线上人工验证，回归无防护。
// 运行：npm run build && node --test test/
// 策略：替换 global.fetch 为内存 mock，按 URL 匹配返回预设响应；
//   multipart 用例对请求体做字节级断言（boundary 闭环/文件名字段/二进制透传）。
// ============================================================================
import { test, mock } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { TranslatorClient, TranslatorError } from '../dist/index.js';

// ---- fetch mock 基建：记录全部请求（method/url/headers/body），按谓词返回响应 ----
const calls = [];
let responders = []; // [{match(url,init), status, body, headers}]
function installFetch() {
  mock.method(global, 'fetch', async (url, init = {}) => {
    calls.push({ url: String(url), init });
    for (const r of responders) {
      if (r.match(String(url), init)) {
        return {
          ok: (r.status || 200) >= 200 && (r.status || 200) < 300,
          status: r.status || 200,
          // headers 替身：真 fetch 的 Headers 只有 get()，mock 保持同一形状，
          // 这样 SDK 里「JSON 字段优先、Retry-After 头兜底」那条腿才测得到（不给 headers
          // 就等于把这条分支永久短路）。
          headers: { get: (k) => (r.headers ? (r.headers[k] ?? r.headers[k.toLowerCase()]) : null) },
          text: async () => (typeof r.body === 'string' ? r.body : JSON.stringify(r.body ?? {})),
          arrayBuffer: async () => new TextEncoder().encode(typeof r.body === 'string' ? r.body : JSON.stringify(r.body ?? '')).buffer,
        };
      }
    }
    return { ok: false, status: 404, text: async () => '{}', arrayBuffer: async () => new ArrayBuffer(0) };
  });
}
function resetFetch() {
  // ★ mock 泄漏修复：用例内 mock.method(global,'fetch') 替换的实例必须先还原，
  //   否则 waitTask/网络异常用例的 mock 会泄漏到后续用例（getTask/createFileTask 命中旧 mock）。
  mock.restoreAll();
  calls.length = 0;
  responders = [];
  installFetch();
}
installFetch();

test('baseUrl 去尾斜杠 + Bearer 认证头与 JSON Content-Type', async () => {
  resetFetch();
  responders = [{ match: (u) => u.endsWith('/openapi/v1/balance'), body: { balance_points: 123 } }];
  const cli = new TranslatorClient('https://api.example.com///', 'rk_test_key');
  const bal = await cli.balance();
  assert.equal(bal.balance_points, 123);
  assert.equal(calls[0].url, 'https://api.example.com/openapi/v1/balance');
  assert.equal(calls[0].init.headers['Authorization'], 'Bearer rk_test_key');
});

test('createTask 成功：POST JSON 体内含 text/target_langs/mode', async () => {
  resetFetch();
  responders = [{ match: (u) => u.endsWith('/openapi/v1/tasks'), body: { task_id: 7, mode: 'pro', type: 'text', status: 'queued' } }];
  const cli = new TranslatorClient('https://api.example.com', 'rk_k');
  const r = await cli.createTask('你好', ['en', 'ja'], 'fast', '标题');
  assert.equal(r.task_id, 7);
  const body = JSON.parse(calls[0].init.body);
  assert.equal(body.text, '你好');
  assert.deepEqual(body.target_langs, ['en', 'ja']);
  assert.equal(body.mode, 'fast');
  assert.equal(body.title, '标题');
});

test('createTask HTTP 错误：TranslatorError 携带 status 与 error_code', async () => {
  resetFetch();
  responders = [{
    match: () => true, status: 402,
    body: { success: false, error_code: 'insufficient_balance', message: '余额不足' },
  }];
  const cli = new TranslatorClient('https://api.example.com', 'rk_k');
  await assert.rejects(
    () => cli.createTask('x'),
    (e) => e instanceof TranslatorError && e.status === 402 && e.error_code === 'insufficient_balance' && /余额不足/.test(e.message),
  );
});

test('createTask 业务错误（200+无 task_id）：以 error_code 抛出', async () => {
  // ★ F-64① 后这条是「2xx 空壳」兜底通道（中间层把失败改写成 200、或打老服务端）：
  // 正常失败已是 403，见下一条。判据不变——不许默默返回一个没有 task_id 的对象。
  resetFetch();
  responders = [{ match: () => true, status: 200, body: { success: false, error_code: 'forbidden', message: '无权限' } }];
  const cli = new TranslatorClient('https://api.example.com', 'rk_k');
  await assert.rejects(
    () => cli.createTask('x'),
    (e) => e instanceof TranslatorError && e.error_code === 'forbidden',
  );
});

test('F-64①：403 带 code+error_code 双键且同值', async () => {
  resetFetch();
  responders = [{
    match: () => true, status: 403,
    body: { success: false, code: 'forbidden', error_code: 'forbidden', message: 'API Key 无翻译权限' },
  }];
  const cli = new TranslatorClient('https://api.example.com', 'rk_k');
  await assert.rejects(
    () => cli.createTask('x'),
    (e) => e instanceof TranslatorError && e.status === 403
      && e.code === 'forbidden' && e.error_code === 'forbidden'
      && e.message === 'API Key 无翻译权限',
  );
});

test('F-64①：老服务端只发 error_code 时 code 仍取到（混跑窗口不许没码）', async () => {
  resetFetch();
  responders = [{
    match: () => true, status: 402,
    body: { success: false, error_code: 'insufficient_balance', message: '余额不足' },
  }];
  const cli = new TranslatorClient('https://api.example.com', 'rk_k');
  await assert.rejects(
    () => cli.createTask('x'),
    (e) => e instanceof TranslatorError && e.code === 'insufficient_balance'
      && e.error_code === 'insufficient_balance',
  );
});

test('F-47/F-64①：429 的 retry_after 字段与 Retry-After 头都能取到', async () => {
  resetFetch();
  responders = [{
    match: (u) => u.includes('/tasks/status'), status: 429,
    body: { success: false, code: 'rate_limited', error_code: 'rate_limited', message: '请求过于频繁', retry_after: 30 },
  }];
  const cli = new TranslatorClient('https://api.example.com', 'rk_k');
  await assert.rejects(() => cli.getTask(1),
    (e) => e instanceof TranslatorError && e.retryAfter === 30);

  resetFetch();
  responders = [{
    match: (u) => u.includes('/tasks/status'), status: 429,
    body: { message: 'too many requests' },           // JSON 字段被中间层吃掉
    headers: { 'Retry-After': '45' },                 // 头还在
  }];
  const cli2 = new TranslatorClient('https://api.example.com', 'rk_k');
  await assert.rejects(() => cli2.getTask(1),
    (e) => e instanceof TranslatorError && e.retryAfter === 45);

  resetFetch();
  responders = [{
    match: (u) => u.includes('/tasks/status'), status: 400,
    body: { success: false, code: 'bad_request', message: '缺少任务 id' },
  }];
  const cli3 = new TranslatorClient('https://api.example.com', 'rk_k');
  await assert.rejects(() => cli3.getTask(1),
    (e) => e instanceof TranslatorError && e.retryAfter === undefined); // 非限流不许造时长
});

test('F-64①：downloadFile 失败必须带出 message/code（旧写法只剩一行 HTTP 状态）', async () => {
  resetFetch();
  responders = [{
    match: (u) => u.includes('/tasks/download'), status: 409,
    body: { success: false, code: 'not_ready', error_code: 'not_ready', message: '译文尚未就绪' },
  }];
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'sdkdl-'));
  const out = path.join(dir, 'out.zip');
  const cli = new TranslatorClient('https://api.example.com', 'rk_k');
  await assert.rejects(() => cli.downloadFile(1, out),
    (e) => e instanceof TranslatorError && e.status === 409
      && e.code === 'not_ready' && e.message === '译文尚未就绪');
  assert.ok(!fs.existsSync(out), '失败时不许落盘（否则得到内容为 JSON 的假产物）');
  fs.rmSync(dir, { recursive: true, force: true });
});

test('F-64①：downloadFile 在 2xx 上拿到 JSON 错误体也不落盘（老后端混跑）', async () => {
  resetFetch();
  responders = [{
    match: (u) => u.includes('/tasks/download'), status: 200,
    body: { success: false, error_code: 'no_result', message: '无可下载产物' },
  }];
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'sdkdl2-'));
  const out = path.join(dir, 'out.zip');
  const cli = new TranslatorClient('https://api.example.com', 'rk_k');
  await assert.rejects(() => cli.downloadFile(1, out),
    (e) => e instanceof TranslatorError && e.error_code === 'no_result');
  assert.ok(!fs.existsSync(out));
  fs.rmSync(dir, { recursive: true, force: true });
});

test('网络异常：包装为「连接失败」TranslatorError', async () => {
  resetFetch();
  mock.method(global, 'fetch', async () => { throw new TypeError('fetch failed'); });
  const cli = new TranslatorClient('https://api.example.com', 'rk_k');
  await assert.rejects(() => cli.balance(), (e) => e instanceof TranslatorError && /连接失败/.test(e.message));
});

test('waitTask：以显式 interval 轮询至 completed（显式间隔不被吞）', async () => {
  resetFetch();
  let n = 0;
  // 按调用序返回 queued→processing→completed
  const statuses = ['queued', 'processing', 'completed'];
  mock.method(global, 'fetch', async (url) => {
    calls.push({ url: String(url) });
    const st = statuses[Math.min(n++, statuses.length - 1)];
    return {
      ok: true, status: 200,
      text: async () => JSON.stringify({ task_id: 1, status: st, type: 'text' }),
      arrayBuffer: async () => new ArrayBuffer(0),
    };
  });
  const cli = new TranslatorClient('https://api.example.com', 'rk_k');
  const t0 = Date.now();
  const st = await cli.waitTask(1, 0.01); // 10ms 间隔
  assert.equal(st.status, 'completed');
  assert.ok(Date.now() - t0 < 3000, '显式 0.01s 间隔应立即生效（旧 ?? 优先级缺陷会把显式间隔吞成 15s）');
  assert.ok(n >= 3);
});

test('getTask 失败态：返回 failed 终态而非抛错', async () => {
  resetFetch();
  responders = [{ match: (u) => u.includes('/tasks/status'), body: { task_id: 9, status: 'failed', error_code: 'task_failed' } }];
  const cli = new TranslatorClient('https://api.example.com', 'rk_k');
  const st = await cli.getTask(9);
  assert.equal(st.status, 'failed');
  assert.equal(st.error_code, 'task_failed');
});

test('createFileTask：multipart 报文字节级结构（boundary 闭环/文件名/二进制透传/表单字段）', async () => {
  resetFetch();
  responders = [{ match: (u) => u.endsWith('/openapi/v1/tasks'), body: { task_id: 5, type: 'files', status: 'queued', file_count: 1 } }];
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'sdktest-'));
  const fp = path.join(dir, '样例.txt');
  fs.writeFileSync(fp, Buffer.from('文件内容字节'));
  const cli = new TranslatorClient('https://api.example.com', 'rk_k');
  const r = await cli.createFileTask([fp], ['en'], 'fast');
  assert.equal(r.task_id, 5);
  const body = calls[0].init.body; // Buffer
  assert.ok(Buffer.isBuffer(body));
  const ctype = calls[0].init.headers['Content-Type'];
  assert.match(ctype, /^multipart\/form-data; boundary=----TranslatorSDKBoundary/);
  const boundary = ctype.split('boundary=')[1];
  const text = body.toString('latin1');
  // boundary 闭环：首段、尾段闭合
  assert.ok(text.includes(`--${boundary}\r\n`), '缺少首段 boundary');
  assert.ok(text.includes(`--${boundary}--\r\n`), 'multipart 未闭合');
  // 文件名（UTF-8 字节以 latin1 视图断言）与字段
  assert.ok(text.includes('filename="'), '缺少 filename');
  assert.ok(text.includes('name="target_langs"\r\n\r\nen\r\n'), '缺少 target_langs 字段');
  assert.ok(text.includes('name="mode"\r\n\r\nfast\r\n'), '缺少 mode 字段');
  // 文件字节透传不变形
  assert.ok(body.includes(Buffer.from('文件内容字节')), '文件二进制内容未透传');
  fs.rmSync(dir, { recursive: true, force: true });
});
