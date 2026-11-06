// ============================================================================
// components/guidedchat/guidedChatStyles.ts — 对话式引导外壳的**唯一**样式表（H1，2026-11-06）
//
// 这份 CSS 是从 AiRegisterFlow.tsx 整段搬过来的，**类名一字未改**：
// 注册面的交付真值（卡宽 400 / 圆角 28 / 气泡 15.5px / 描边 1.2px / 断点 640）与
// 既有闸门（pixel_uat、Login.*.dom、AiRegisterFlow.dom 里按 `.ar-*` 取点的断言）都按这些名字判定，
// 改名等于同时把交付档与闸门一起推翻（AGENTS §一·5「界面规格＝UI 交付真值」）。
// ⇒ 新场景（引导建产品）复用这一份，**禁止**再造第二套 ar-/gc- 样式。
// ============================================================================

export const CSS_GUIDED = `
.ar-root{position:fixed;inset:0;z-index:60;display:grid;place-items:center;padding:16px;background:rgba(0,0,0,.72);color:var(--lc-text);font-family:var(--lc-font);animation:lc-mo-fade var(--lc-mo-enter) var(--lc-mo-out) both;}
/* ★ 比例按演示稿（demo-register-ai-motion.html 的 .phone）：竖版卡、
   高 min(780, 100dvh-32)、圆角 28、居中 —— 不是全屏接管（2026-09-18 用户裁定）。
   ★ 2026-10-21 IAM 重做 P1-2：宽 390 → 400，与三张认证卡同档（AuthCard 交付档 400）。
   同一段注册流程里卡片宽度来回跳 10px，用户看到的是"面板换了一张"，
   而交付稿的认证面本来就只有一档宽度。 */
.ar-panel{width:400px;max-width:100%;height:min(780px,calc(100dvh - 32px));min-height:520px;display:flex;flex-direction:column;background:var(--lc-bg);border:1.2px solid var(--lc-border-card);border-radius:28px;overflow:hidden;box-shadow:0 24px 64px rgba(0,0,0,.55);animation:lc-mo-pop var(--lc-mo-enter) var(--lc-mo-out) both;--lc-mo-origin:50% 92%;}
@media (max-width:640px){ /* ★ 批3 断点归一：480 → 640（S 档上缘） */
  .ar-root{padding:0;background:var(--lc-bg);}
  .ar-panel{width:100%;height:100dvh;min-height:0;border:0;border-radius:0;box-shadow:none;}
}
.ar-nav{height:52px;flex:none;display:flex;align-items:center;gap:12px;padding:0 16px;border-bottom:1px solid var(--lc-border-faint);}
.ar-close{display:flex;align-items:center;justify-content:center;width:28px;height:28px;border-radius:8px;background:none;border:0;color:var(--lc-text);cursor:pointer;}
.ar-nav-t{font-size:18px;font-weight:600;color:var(--lc-text);flex:1;}
.ar-online{display:flex;align-items:center;gap:6px;font-size:13px;color:var(--lc-text-3);}
.ar-online i{width:7px;height:7px;border-radius:50%;background:var(--lc-text);box-shadow:0 0 0 3px rgba(255,255,255,.08);}
.ar-prog{flex:none;display:flex;flex-direction:column;gap:9px;padding:14px 16px 12px;border-bottom:1px solid var(--lc-border-faint);}
.ar-prog-top{display:flex;align-items:center;justify-content:space-between;gap:12px;}
.ar-prog-label{font-size:14px;color:var(--lc-text-3);}
.ar-prog-label b{color:var(--lc-text);font-weight:600;}
.ar-back{flex:none;font-size:13px;padding:4px 11px;border-radius:999px;background:var(--lc-raised);border:1.2px solid var(--lc-border-pill);color:var(--lc-text-2);cursor:pointer;font-family:var(--lc-font);}
.ar-back:active{transform:scale(.97);}
.ar-back[disabled]{opacity:.5;cursor:not-allowed;}
.ar-track{height:2px;border-radius:1px;background:var(--lc-border-faint);overflow:hidden;}
.ar-track i{display:block;height:100%;width:100%;background:var(--lc-text-2);transform:translateX(-101%);transition:transform .5s cubic-bezier(.32,.72,.28,1);}
.ar-chat{flex:1;min-height:0;overflow-y:auto;padding:20px 16px;display:flex;flex-direction:column;gap:20px;scrollbar-width:none;}
.ar-chat::-webkit-scrollbar{display:none;}
.ar-msg{display:flex;flex-direction:column;opacity:0;transform:translateY(8px);animation:ar-in .36s cubic-bezier(.32,.72,.28,1) forwards;}
.ar-msg--user{align-items:flex-end;}
.ar-who{display:flex;align-items:center;gap:8px;font-size:13px;color:var(--lc-text-4);margin-bottom:8px;}
.ar-badge{width:20px;height:20px;border-radius:7px;background:var(--lc-raised);border:1.2px solid var(--lc-border-card);display:flex;align-items:center;justify-content:center;font-family:var(--lc-font-mono);font-size:11px;font-weight:600;color:var(--lc-text);}
.ar-body{display:flex;flex-direction:column;gap:10px;}
.ar-msg--ai .ar-body{margin-inline-start:28px;}
.ar-bubble{padding:12px 14px;border-radius:14px;font-size:15.5px;line-height:1.7;}
.ar-msg--ai .ar-bubble{background:var(--lc-inset);border:1.2px solid var(--lc-border-card);color:var(--lc-text-2);border-top-left-radius:6px;align-self:stretch;}
.ar-msg--user .ar-bubble{background:#FFFFFF;color:#000;font-weight:500;border-top-right-radius:6px;}
.ar-type{position:relative;display:block;}
.ar-type .ar-ghost{visibility:hidden;}
.ar-type .ar-real{position:absolute;inset:0;}
.ar-caret{display:inline-block;width:1px;height:1em;background:var(--lc-text-3);vertical-align:-.15em;margin-inline-start:2px;animation:ar-caret 1s steps(1) infinite;}
@keyframes ar-caret{50%{opacity:0;}}
.ar-opts{display:flex;flex-direction:column;gap:8px;}
.ar-opt{display:flex;flex-direction:column;gap:4px;padding:12px 14px;border-radius:12px;text-align:start;background:var(--lc-panel);border:1.2px solid var(--lc-border-card);cursor:pointer;font-family:var(--lc-font);
  transition:border-color var(--lc-mo-release) var(--lc-mo-out),background var(--lc-mo-release) var(--lc-mo-out),opacity var(--lc-mo-sink) var(--lc-mo-out);}
.ar-opt:hover:not(:disabled){border-color:var(--lc-border-pill);}
.ar-opt:disabled{opacity:.6;cursor:not-allowed;}
/* 选项确认：被点中的提亮并留在原位、其余压暗退场 —— 一次看得见的交代（原则 7） */
.ar-opt--picked{border-color:var(--lc-fill-white);background:var(--lc-raised);}
.ar-opts--locked{pointer-events:none;}
.ar-opts--locked .ar-opt:not(.ar-opt--picked){opacity:.34;}
.ar-opt-t{font-size:15px;font-weight:500;color:var(--lc-text);}
.ar-opt-d{font-size:13px;color:var(--lc-text-4);line-height:1.5;}
.ar-chips{display:flex;flex-wrap:wrap;gap:8px;}
.ar-chip{display:flex;align-items:baseline;gap:6px;padding:7px 11px;border-radius:8px;background:var(--lc-raised);border:1.2px solid var(--lc-border-card);}
.ar-chip-k{font-size:12.5px;color:var(--lc-text-4);}
.ar-chip-v{font-size:14px;color:var(--lc-text);font-weight:500;}
.ar-form{display:flex;flex-direction:column;gap:14px;padding:20px 16px;border-radius:14px;background:var(--lc-panel);border:1.2px solid var(--lc-border-card);box-shadow:var(--lc-panel-highlight);align-self:stretch;}
.ar-fgroup{display:flex;flex-direction:column;gap:8px;}
/* ★ P1-3：口令框 + 右端眼睛（与认证卡 .auth-pwd-eye 同形态同尺寸，只换定位方式） */
.ar-pwd{position:relative;display:block;}
.ar-pwd .lc-input{width:100%;padding-inline-end:38px;box-sizing:border-box;}
.ar-pwd-eye{position:absolute;inset-inline-end:6px;top:50%;transform:translateY(-50%);display:flex;align-items:center;justify-content:center;background:none;border:0;color:var(--lc-text-3);cursor:pointer;padding:4px;line-height:0;}
.ar-pwd-eye:hover{color:var(--lc-text);}
/* Turnstile 挂载容器：widget 固定 300px 宽，居中摆放；预留 65px 高度，
   避免脚本晚到时表单内容突然下移（AI 面板是逐条追加的，跳动很显眼） */
/* ★ D-02：widget 固有宽 300px，容器必须能收缩（选档失效时也不许把面板顶出横向滚动）；
   装载提示占一行小字，用现成的次级文字档，不新造色。 */
.ar-ts{display:flex;justify-content:center;min-height:65px;max-width:100%;overflow-x:auto;}
.ar-ts-note{margin:0;color:var(--lc-text-3);font-size:14px;line-height:1.5;display:flex;align-items:center;gap:8px;flex-wrap:wrap;justify-content:center;}
.ar-flabel{font-size:14px;color:var(--lc-text-3);}
.ar-flabel i{font-style:normal;color:var(--lc-text-4);font-size:13px;}
/* ★ 2026-10-21 IAM 重做 P1-4：协议勾选链接与登录/传统注册卡的 .auth-link 同档（次级文字色、悬停下划线） */
.ar-link{background:none;border:0;color:var(--lc-text-3);font-size:14px;cursor:pointer;font-family:var(--lc-font);text-decoration:none;}
.ar-link:hover{color:var(--lc-text);text-decoration:underline;}
.ar-otp-row{display:flex;gap:8px;align-items:center;min-width:0;}
.ar-otp-cells{display:flex;gap:7px;flex:1;min-width:0;}
/* ★ min-width:0 必须给：<input> 的默认尺寸（size≈20 字符 ≈170px）来自内容，
   flex 子项默认 min-width:auto 会拒绝收缩 —— 六格在 390 宽上直接撑破表单，
   实测整行右缘溢出卡片 126px、页面出现横向滚动。 */
.ar-otp{flex:1;min-width:0;width:100%;height:46px;border-radius:10px;background:var(--lc-inset);border:1.2px solid var(--lc-border-input);color:var(--lc-text);text-align:center;font-family:var(--lc-font-latin);font-size:18px;font-weight:600;
  transition:border-color var(--lc-mo-release) var(--lc-mo-out),background var(--lc-mo-release) var(--lc-mo-out);}
/* 窄屏：发送按钮让出整行，六格拿满宽度（390 下格子能到 ~44px，否则只有 24px 太挤） */
@media (max-width: 640px){ /* ★ 批3 断点归一：480 → 640（S 档上缘） */
  .ar-otp-row{flex-wrap:wrap;}
  .ar-otp-cells{flex:1 1 100%;}
  .ar-otp-row > .lc-btn{flex:1 1 100%;justify-content:center;}
}
.ar-otp:focus{outline:none;border-color:var(--lc-border-strong);}
/* 已填格：描边提到完成态、底提一档，六格填满时整排能一眼读出来 */
.ar-otp--filled{border-color:var(--lc-border-done);background:var(--lc-raised);}
.ar-ckpts{display:flex;flex-direction:column;gap:10px;padding:14px;border-radius:12px;background:var(--lc-inset);border:1.2px solid var(--lc-border-card);}
.ar-ckpt{display:flex;align-items:center;gap:10px;font-size:15px;color:var(--lc-text-2);}
.ar-ck{width:18px;height:18px;border-radius:50%;background:var(--lc-text);border:1.2px solid var(--lc-text);display:flex;align-items:center;justify-content:center;flex:none;}
.ar-done{display:flex;flex-direction:column;gap:12px;padding:20px 16px;border-radius:14px;background:var(--lc-panel);border:1.2px solid var(--lc-border-done);box-shadow:var(--lc-panel-highlight);align-self:stretch;}
.ar-done-t{font-size:15px;font-weight:600;color:var(--lc-text);}
/* ★ B4-d（§6.5 第 4 条「受邀落地页告知」）：完成摘要卡里的「可切回个人身份」一句。
   与 .ar-done 卡同色阶、只做次级说明，抬字号会抢过上面的身份/邮箱读数。 */
.ar-done-note{font-size:14px;color:var(--lc-text-3);line-height:1.5;}
.ar-kv{display:flex;gap:12px;font-size:14px;line-height:1.5;}
.ar-kv-k{width:72px;flex:none;color:var(--lc-text-4);}
.ar-kv-v{color:var(--lc-text-2);font-family:var(--lc-font-latin);}
.ar-busy{color:var(--lc-text-2)!important;pointer-events:none;}
.ar-spin{display:inline-block;width:12px;height:12px;border:1.2px solid var(--lc-border-faint);border-top-color:var(--lc-text-2);border-radius:50%;margin-inline-end:8px;vertical-align:-2px;animation:ar-spin .8s linear infinite;}
@keyframes ar-spin{to{transform:rotate(360deg);}}
@keyframes ar-in{to{opacity:1;transform:none;}}
@media (prefers-reduced-motion: reduce){
  .ar-msg{animation:none!important;opacity:1!important;transform:none!important;}
  .ar-track i{transition:none!important;}
  .ar-caret,.ar-spin{animation:none!important;}
  .ar-root,.ar-panel{animation:none!important;}
}
`