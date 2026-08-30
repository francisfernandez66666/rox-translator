# 翻译助手 langcross Java SDK

与 Python / TypeScript SDK 完全一致的开放 API 客户端（Java 11+，jackson-databind）。

```bash
mvn package   # 产出 target/translator-sdk-1.0.0.jar
```

```java
import com.langcross.sdk.TranslatorClient;
import static com.langcross.sdk.TranslatorClient.langs;

TranslatorClient c = new TranslatorClient("https://translator.example.com", "YOUR_API_KEY");
var task = c.translateAndWait("Hello world", langs("zh"));
System.out.println(task.path("status").asText());
```

约束（同 Python SDK）：成功响应不含 `success` 字段，以 `task_id` 为准；业务错误抛 `TranslatorError`（含 HTTP 状态与 `error_code`）。
