# 翻译助手 langcross TypeScript SDK

与 Python SDK 完全一致的开放 API 客户端。

```bash
npm install
npm run build   # 产出 dist/（CommonJS + d.ts）
```

```ts
import { TranslatorClient } from "@langcross/translator-sdk";

const c = new TranslatorClient("https://translator.example.com", "YOUR_API_KEY");
const task = await c.translateAndWait("Hello world", ["zh"]);
console.log(task.status, task.files);
```

约束（同 Python SDK）：
- 成功响应**不含** `success` 字段，以 `task_id` 存在为准；
- 业务错误为 `{success:false, error_code, message}` 或 HTTP 4xx/5xx；
- 所有计量按 token 计费，受租户余额与每日配额约束。
