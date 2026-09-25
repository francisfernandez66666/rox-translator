# LangCross · User Guide

> Audience: personal-plan users, and all members of an enterprise/team tenant (User, Dept Admin, Org Admin)
> Scope: features visible and usable inside a tenant only. Platform-operations functions, and anything related to plans, top-ups, invoicing or promotional activities, are intentionally excluded.
> UI names follow the product's actual labels; where this guide and the UI differ, trust the UI.

---

## Table of contents

1. [Product overview](#1-product-overview)
2. [Getting started](#2-getting-started)
3. [Roles and permissions](#3-roles-and-permissions)
4. [Instant Translation (workspace)](#4-instant-translation-workspace)
5. [Translation Jobs (document translation)](#5-translation-jobs-document-translation)
6. [Side-by-Side Editor](#6-side-by-side-editor)
7. [Knowledge Base and Translation Memory](#7-knowledge-base-and-translation-memory)
8. [Account and personal settings](#8-account-and-personal-settings)
9. [Organization and member management (Org Admin)](#9-organization-and-member-management-org-admin)
10. [External calls and integrations (Org Admin)](#10-external-calls-and-integrations-org-admin)
11. [Notifications, feedback and AI Assistant](#11-notifications-feedback-and-ai-assistant)
12. [FAQ](#12-faq)
13. [Appendix](#13-appendix)

---

## 1. Product overview

**LangCross (能言)** is an AI translation collaboration platform for enterprises and teams. It combines three translation workflows with one corporate language-asset system:

| Capability | Description |
|------------|-------------|
| Instant Translation | Chat-style text translation — type and translate, one input to many target languages at once |
| Translation Jobs | Upload docx / pptx / xlsx / pdf files and run them through "create job → approval → translation → delivery", with translations returned in the original format |
| Side-by-Side Editor | Source/translation paragraph-by-paragraph review: edit, approve or reject segments with notes |
| Knowledge Base / Translation Memory (KB/TM) | Company terminology, confirmed bitext and fixed brand names live in the KB and are applied automatically during translation |

Highlights:

- **40+ languages**, with the interface itself in 12 languages (Simplified & Traditional Chinese, English, Japanese, Korean, German, French, Spanish, Portuguese, Russian, Arabic — right-to-left layout, and Thai).
- **Two translation modes**: *Fast* for lightweight, high-throughput work; *Pro* for knowledge-base term calibration, quality evaluation and hard-check gates — suited to publishable content.
- **Enterprise isolation**: each tenant's data, knowledge bases and members are separate; within an organization, knowledge bases and budgets are further isolated by department.
- **One voice across surfaces**: besides the web app there is a browser selection-translation extension, a Word add-in, a VS Code extension, and an Open API/SDK — all sharing the same terminology and memory as the web.

---

## 2. Getting started

### 2.1 Sign in

Open the address your company provides (the platform domain or your company's dedicated subdomain) and enter your **username** and **password** on the login page.

- Forgot password: click "Forgot password" and reset it with a verification code sent to your bound email.
- If your company has single sign-on enabled, the login card shows "Or sign in with your enterprise identity provider" — one click signs you in with your company account.
- First sign-in: if the admin who created your account left the email empty, a non-dismissible "Bind Your Email" dialog appears — complete it first. Accounts created in bulk are required to change their password on first login.

### 2.2 Register / join a company

Two paths:

- **Join an existing company**: at registration, enter the organization code + an **invite code** (required — ask your company admin) to join the matching department as a User. Registering through your company's dedicated subdomain (`code.domain`) pre-fills the company info; that entry only creates ordinary members of that company.
- **Create a new company**: leave the invite code empty to create a new organization — you become its Org Admin and can then invite colleagues (see section 9).

Registration can be guided by an AI assistant: account type (personal/company) → company role (admin creates a company / member joins with an invite code) → industry and job role → account details. Follow the prompts; you can step back at any time. Choosing *personal* opens a Personal plan account — see [3.1 Personal-plan users](#31-personal-plan-users).

### 2.3 Meet the interface

After signing in you land in the workspace:

| Area | Contents |
|------|----------|
| Top bar | Three workspace tabs: **Instant Translation**, **Translation Jobs**, **Side-by-Side Editor**; on the right: credits balance badge, notification bell, language selector, account menu; Dept Admins and above also see **Upload Knowledge Base** and the Admin Console entry |
| "More" drawer | Self-service pages such as My Account, plus the footer |
| Main area | The active tab's work surface |
| "AI Assistant" (bottom right) | An always-available assistant widget (see section 11) |

- **Switching the interface language**: the top-bar selector lists each language in its own script (e.g. 「日本語」「한국어」); it applies instantly site-wide. On first visit your browser language is auto-detected.
- **Mobile**: the app adapts to phone/tablet browsers; on narrow screens admin sidebars become a drawer.
- **Account menu** (avatar, top right): Admin Console (Dept Admin and above) / Back to Workspace, Change Password, Change Email, My job role, Logout.

### 2.4 Your first translation

1. Paste text into the Instant Translation input box;
2. Confirm the source language (default "Auto detect") and pick one or more target languages (candidates are grouped as "KB Languages / Other Languages");
3. Choose a mode (Fast / Pro) and click "Translate";
4. The translation streams into chat bubbles — copy it, show the source, or keep chatting to adjust style.

For files, see section 5.

---

## 3. Roles and permissions

There are three in-tenant roles:

| Role | Who | What they see |
|------|-----|---------------|
| **User** | Every member | Instant Translation, Translation Jobs (create/view/download), Side-by-Side Editor, notification center, AI Assistant, My Account; interface language and job-role settings |
| **Dept Admin** | Appointed by an Org Admin | Everything a User has, plus the basic admin menus: **Overview** (incl. usage detail), **Feedback / Approval** (approve jobs, reply to feedback), **Knowledge Base** (department & cross-dept packs), top-bar Upload Knowledge Base |
| **Org Admin** | At least one per company (creator or delegate) | Everything a Dept Admin has, plus **Org & Members** (department tree, members, invite codes, budgets), **External Calls** (API keys, webhooks, SDKs), **Knowledge Base** in all pack types (org/dept/cross-dept), brand fixed-translation maintenance, and SCIM provisioning |

Notes:

- This guide covers the roles above only; platform-operations features are out of scope.
- Actions labelled "Memory Review" or "complete feedback (archive)" on the Feedback screen belong to platform-side processes and do not appear for tenants.
- For permission questions, contact your company's Org Admin.

### 3.1 Personal-plan users

Choosing the **personal** account type at registration (no organization code or invite code needed) opens a **Personal plan** account — the top-bar identity badge reads "Personal plan". The account is managed solely by you; there are no members or departments.

What a personal account can use:

| Feature | Notes |
|---------|-------|
| The three workspaces | Instant Translation, Translation Jobs, Side-by-Side Editor — identical to the enterprise experience |
| Admin Console | Available from the account menu: **Knowledge Base** (upload and maintain your own terminology, visible only to you), **External Calls** (self-service API keys — required for the browser extension, Word add-in and SDKs), **Overview** (your usage detail) |
| My job role | Same as enterprise — a preset professional role tuned to your work context (see 7.5) |
| Self-service pages | More → My Account; change password/email, notification center, account deactivation |

Differences from an enterprise tenant:

- **No "Org & Members"** — departments, member provisioning and dept budgets simply don't exist;
- **No approval step on jobs** — a created Translation Job goes straight to the queue and never waits in "Pending approval";
- No dedicated company subdomain or shared company KB; platform-wide industry and language-culture packs still apply automatically.

If your personal quota or balance runs low, follow the on-screen prompt.

---

## 4. Instant Translation (workspace)

The default page after sign-in: a full-screen dialog — history on top, input docked at the bottom.

### 4.1 The input area (composer toolbar)

One input card holding an auto-growing input line and a single-row toolbar:

| Control | Description |
|---------|-------------|
| Source · Auto detect | Choose the source language; detection is on by default |
| Target language | Opens the picker; languages are grouped into "KB Languages" and "Other Languages (AI)", with "More languages…" for full search. Selected languages show as chips — beyond 3 they collapse into "+n"; expand to remove individually |
| Pro / Fast | Translation mode. *Fast* = AI draft + proofread, low cost, high throughput; *Pro* = full pipeline with KB term calibration, quality evaluation and hard gates — for publishable content |
| Condense | Constrains translation length — handy for titles, button labels and other tight character budgets |
| Translate (primary button) | Starts translation; "Stop" cancels generation mid-flight |

An estimate is shown before sending. If the balance or quota runs out, the UI says so and points you to your administrator.

### 4.2 Conversation and results

- Source and translation appear in one bubble column; translations stream in segment by segment.
- Each translation is tagged with how it was produced: **Exact match / Fuzzy match / Semantic match** (from the knowledge base) or **AI translation**; matched KB terms are highlighted.
- Bubble actions: copy translation, show source; keep sending instructions to adjust tone or style — context carries over.
- Pipeline stages (Term retrieval → Machine translation → Term calibration → Translation complete) are surfaced as progress hints.

### 4.3 Session management

The dialog header offers:

- **Search this chat** (Ctrl/Cmd+K);
- **Export conversation** (Markdown file);
- **Clear chat**.

### 4.4 Usage display

Consumption is shown uniformly in **credits**, converted to an approximate sentence count. Near the composer you'll see: balance (credits ≈ sentences), today's spend, and — if you belong to a budgeted department — the dept budget progress.

---

## 5. Translation Jobs (document translation)

All **file translation** starts here and follows the full flow: create → (if your company requires it) approval → translation → delivery.

### 5.1 Creating a job

Click "Create Translation Job" and pick the Text or File tab:

- **Text**: paste long text, choose target languages and mode, submit as a job.
- **File**: upload one or more files; select several target languages at once (deliverables arrive as a zip).

Key fields:

| Field | Description |
|-------|-------------|
| Delivery | **Restore Format** (default): translated file in the original layout, plus a text-only .md fallback artifact; **Text Only**: body text extracted locally, translated and delivered as .md with no layout guarantee (for legacy doc/ppt/xls, odt/epub/rtf etc.) |
| Mode | Fast (no KB) / Pro Review |
| Target languages | Multiple selection |
| Estimate | "Est. {n} sentences · balance {n}" before submitting |

Format support:

| Category | Formats | Delivery |
|----------|---------|----------|
| Layout-restored | docx, pptx, xlsx, pdf, txt, csv, md | Same-format translated file |
| Bilingual table | srt, vtt, json, yaml, etc. | Source/translation xlsx |
| Text only | legacy doc/ppt/xls family, odt/ods/odp, rtf, epub | Translated .md |

Limits:

- Per-job file count and size are capped; PDFs over 40 MB or 120 pages are rejected with a hint to convert to docx first.
- Scanned PDFs (all images) are not supported; text inside images is not translated by design.
- If layout write-back fails for one file, it automatically degrades to .md delivery with an in-app notice — the whole job never fails for that reason.

### 5.2 Job states and workflow

"My Jobs" is filterable by state:

```
Queued → Translating → Completed
Pending approval → Approved → Translating …   (when approval is enabled)
                 └→ Rejected
Any in-progress state → Cancelled
```

- In-progress jobs can be **cancelled**; finished ones can be deleted.
- If your company enables approval, new jobs land in "Pending approval" and a Dept/Org Admin **Approves** or **Rejects** them (with a reason) at the Approval Desk in the admin console.

### 5.3 Progress and downloads

Open a job to see its Progress stepper — Extract → KB match → AI draft → Pro review → Hard gate → Culture check → Write back (steps shown depend on mode and file type).

- When finished, click Download; the text-only fallback is available via "Download translation text (.md)".
- Multi-language / multi-file jobs download as a zip.

### 5.4 Quality report

Completed Pro jobs carry a Quality Report: overall verdict (QA passed / QA failed / QA flagged), evaluation scores out of 100, and segment notes. Disagree with a result? Use "Submit feedback" on the job detail — admins see and answer it in the console.

### 5.5 Hand-off to the editor

Any completed job can be opened directly in the Side-by-Side Editor for human review — next section.

---

## 6. Side-by-Side Editor

Manual final review of job translations:

1. Jump from a job's detail page, or "Load" a job by ID on the Side-by-Side Editor page;
2. Source on the left, translation on the right, segment by segment, with KB terms highlighted;
3. Per segment: **approve** / **reject** (with a note/rejection reason) / edit the translation directly;
4. "Save" to keep your revisions.

Human-confirmed segments are prime material for translation memory — your KB admins can import them (section 7).

---

## 7. Knowledge Base and Translation Memory

The KB makes translations speak your company's language: consistent terminology, reuse of confirmed translations, and locked-down compliance wording.

### 7.1 Four layers

| Layer | Content | Typical use |
|-------|---------|-------------|
| L1 Terms | Term pairs (incl. fixed brand/product names) | Enforced consistent wording |
| L2 Translation Memory | Confirmed source/target sentence pairs | Whole-sentence or fragment reuse |
| L3 Safety phrases | Style rules / forbidden terms / replacement pairs | Hard culture gates: block or auto-replace in output |
| L4 Fragments | Loose expressions and pattern references | Example context for fuzzy matching |

Match priority during translation: packs on your organization chain (own department → parents → root, nearest wins) > company pack > industry/language-culture packs; cross-department shared packs participate as a fallback layer only when an admin enables sharing.

### 7.2 Who can maintain what

| Action | Minimum role |
|--------|--------------|
| Top-bar Upload Knowledge Base (recognize file → pick pack → import) | Dept Admin |
| Create/maintain **Department** and **Cross-dept** packs | Dept Admin |
| Create/maintain the **company (org) pack**, cross-dept sharing switch, per-pack grants | Org Admin |
| Maintain brand fixed translations ("Brand names") | Any role that can reach the KB panel |
| Import bitext / TMX, export TMX | Dept Admin |
| Maintain Language Culture Rules (safety phrases) | Dept Admin |

### 7.3 Pack types

- **Org pack (company)**: applies enterprise-wide;
- **Department pack**: visible only inside the department and its organization chain — department-level term isolation;
- **Cross-dept shared pack**: private by default; effective for other departments only when both the pack-level "Cross-dept: On" switch and the company policy allow.

### 7.4 Ways to import

In the admin console under Knowledge Base (Dept Admin and above):

- **File import**: upload a glossary/document; the system recognizes it and writes entries into the chosen pack;
- **Bitext / TMX import**: bulk-write into translation memory, compatible with TMX exported by mainstream CAT tools (Trados/memoQ etc.);
- **TMX export**: take company memory back into your existing toolchain.

The platform also auto-provisions generic industry and language-culture packs for your sector — no maintenance needed; they participate in translation automatically.

### 7.5 My job role (everyone)

Account menu → "My job role": pick your position from 15+ preset professional roles. The matching role pack is assembled automatically at translation time, tuning terminology and tone to your work context.

---

## 8. Account and personal settings

Entry points: the top-right account menu and the "More" drawer.

| Item | Where | Notes |
|------|-------|-------|
| My Account | More → My Account | Username/email/role/tenant; Org Admins and above also see the **SCIM provisioning** card here |
| Change Password | Account menu | May be enforced at first login |
| Change Email | Account menu | Used for password reset and notifications |
| My job role | Account menu | See 7.5 |
| Notifications | Top-bar bell | In-app messages: job status, approvals, delivery downgrades; mark one/all as read |
| Deactivate Account | Account menu (User role) | Self-service closure, with confirmation |
| Interface language | Top-bar selector | 12 languages, instant |

---

## 9. Organization and member management (Org Admin)

Entry: account menu → Admin Console → "Org & Members".

### 9.1 Structure

- **New Org / New Department**: maintain the multi-level company tree; renaming supported.
- **Dept Budget**: "Allocate monthly budget" per department to cap that department's translation usage; members see the budget progress in their UI.

### 9.2 Members

The "Members" page manages every account in the company:

- **Add User**: create one member with a role (User / Dept Admin / Org Admin) and organization placement;
- **Bulk Import Users (Excel)**: provision many members at once — initial passwords arrive by email and must be changed at first login;
- Existing members: **reset password**, **enable/disable**, change role and department.

### 9.3 Inviting colleagues

"Invite Staff" generates **invite codes** (multiple allowed, each bound to a level of the org tree). Give colleagues the organization code + invite code; entering them at registration joins the right department.

### 9.4 SCIM provisioning (optional)

The SCIM card on "More → My Account": with an identity provider such as Okta or Entra ID, copy the SCIM endpoint and token to automate member provisioning/deprovisioning; once SSO is on, staff sign in via the login-page identity button.

---

## 10. External calls and integrations (Org Admin)

Entry: admin console → "External Calls", with three sub-pages: **Open API**, **Webhooks**, **Official SDKs**.

### 10.1 API keys

- **Create an API key**: the plaintext is shown **once** at creation — save it immediately;
- Keys support **enable/disable, rotation, daily limits, deletion**;
- A key is bound to your company and its credit balance — API usage bills to the same account as the web UI.

The Open API provides: asynchronous translation tasks (create → poll → download, multi-file & multi-language, zip deliverables), synchronous short-text translation (≤5,000 chars, ≤5 languages), balance & usage queries, and KB statistics. Interactive docs are at `/openapi/docs` on your site.

### 10.2 Webhooks

Register a callback URL and subscribe to events (e.g. `translation.completed`) to have LangCross push results into your systems. Signature verification (X-Signature), test sends, delivery history and retries are all provided.

### 10.3 SDKs and plug-ins

The "Official SDKs" page carries install guides and downloads:

| Surface | How to get it | What it does |
|---------|---------------|--------------|
| Python | Download the `.whl` (or source `.tar.gz`) from the site's "Official SDK" page, then `pip install <downloaded file>` (hosted under `/sdk/` on this site; not published to external registries) | Programmatic translation, batch jobs  |
| TypeScript | Download the `.tgz` from the site's "Official SDK" page, then `npm install <downloaded file>` (hosted under `/sdk/` on this site) | Node/front-end integration  |
| Java | Maven source distribution | Enterprise system integration |
| Browser extension | zip download on that page (no app-store channel) | Select text on any web page → floating "译" button → translation bubble with one-click copy; shortcut Alt+Shift+T (Alt+T on macOS) |
| Word add-in | manifest served by the site; in Word use "Insert → Get Add-ins → Upload My Add-in" to side-load | Translate the selection inside Word and insert the result back |
| VS Code extension | vscode-extension in the repository | In-place translation of selections, term lookup |

**Configuring extension/add-ins** (the browser popup and Word task pane share the same fields): **server address** (site root URL), **API key** (from 10.1), **target languages**, and **mode** (Fast/Pro). Terminology and memory match the web experience.

⚠ After an extension upgrade the style may look unchanged: overwrite the **same directory** with the new zip, then hit **Reload** on the extension's card in `chrome://extensions`.

### 10.4 For Dept Admins

The External Calls menu is Org-Admin-only — ask your Org Admin to issue an API key for departmental integrations.

---

## 11. Notifications, feedback and AI Assistant

### 11.1 Notification center (everyone)

The top-bar bell polls for unread in-app messages (jobs completed, approvals, delivery downgrade notices…). Mark single or all as read from the dropdown.

### 11.2 Feedback loop

- **Everyone**: on a completed job, "Submit feedback" with the problematic segments and expectations.
- **Dept/Org Admins**: the admin console's "Feedback / Approval" menu lists company feedback with **reply**, and hosts the Approval Desk for pending jobs.
- "Overview" (Dept Admin and above): company health dashboard and usage detail (CSV export available).

### 11.3 AI Assistant widget (everyone, even visitors)

The bottom-right "AI Assistant" button opens a chat for product questions, guidance and quick jumps. Conversations survive page refreshes (local cache → server-side history restore).

---

## 12. FAQ

**Q1: The Translate button is dead / quota messages appear?**
The balance or department budget is exhausted. Users: contact your company admin; admins can check usage detail in the console.

**Q2: Where do I translate a file? The chat box no longer takes attachments?**
File translation now lives exclusively in Translation Jobs → File. The chat is for text only.

**Q3: My PDF job was refused?**
PDFs over 40 MB / 120 pages must first be converted to docx; scanned (image-only) PDFs are not supported.

**Q4: The translation ignores my company's terminology?**
Check that the terms are imported into a KB pack visible to your department (section 7), and use *Pro* mode for publishable content.

**Q5: The delivered docx looks different from the original?**
Restore Format preserves layout in the vast majority of cases; occasional complex layouts degrade to .md text delivery with an in-app notice. Review in the Side-by-Side Editor or submit feedback.

**Q6: The balance didn't move right after a translation?**
Metering is committed synchronously at completion — refresh the page once; if it persists, file feedback.

**Q7: Can I use it on my phone or tablet?**
Yes via the mobile browser; the layout adapts. Extensions/add-ins are desktop-focused.

**Q8: Text is clipped or looks odd in some language (e.g. Arabic)?**
Switch languages to confirm it's a single string, then send feedback with a screenshot.

---

## 13. Appendix

### A. UI naming cheat sheet (English / 简体中文)

| English | 简体中文 |
|---------|----------|
| Instant Translation | 即时翻译 |
| Translation Jobs | 翻译工单 |
| Side-by-Side Editor | 对照编辑 |
| More | 更多 |
| My Account | 我的账号 |
| Upload Knowledge Base | 上传知识库 |
| Admin Console | 进入管理后台 |
| Notifications | 通知中心 |
| Pro / Fast | 专业校对 / 快速 |
| Condense | 缩翻 |
| Auto detect | 自动检测 |
| KB Languages / Other Languages (AI) | 知识库语言 / 其他语言（AI翻译） |
| Create Translation Job | 创建翻译工单 |
| Delivery: Restore Format / Text Only | 交付方式：还原文件模式 / 纯文案模式 |
| My Jobs | 我的工单 |
| Queued / Translating / Pending approval / Approved / Rejected / Completed / Cancelled | 排队中 / 翻译中 / 待审批 / 已批准 / 已驳回 / 已完成 / 已取消 |
| Progress | 执行进度 |
| Quality Report | 质检报告 |
| Approval Desk / Approve / Reject | 审批台 / 批准 / 驳回 |
| Knowledge Base | 知识库 |
| Tenant / Department / Cross-dept pack | 组织包（本企业）/ 部门包（本部门）/ 跨部门包（共享） |
| File Import / Bitext · TMX Import | 文件导入 / 双语语料 · TMX 导入 |
| Language Culture Rules (Safety Phrases) | 语言文化规范（安全句） |
| Org & Members | 组织与成员 |
| Organization / Invite Staff / Members | 组织结构 / 邀请员工 / 成员账户 |
| Dept Budget | 部门预算 |
| Add User / Bulk Import Users (Excel) | 开通用户 / Excel 批量导入用户 |
| Invite Codes / Generate Code | 邀请码管理 / 生成邀请码 |
| User / Dept Admin / Org Admin | 普通用户 / 部门管理员 / 组织管理员 |
| External Calls / Open API / Webhooks / Official SDKs | 外部调用 / 开放 API / 回调通知 / 官方 SDK |
| My job role | 我的职业角色 |
| Change Password / Change Email / Deactivate Account | 修改密码 / 修改邮箱 / 注销账号 |
| credits | 积分 |

### B. Interface languages (12)

English · 简体中文 · 繁體中文 · 日本語 · 한국어 · Deutsch · Français · Español · Português · Русский · العربية (RTL layout) · ไทย

### C. Shortcuts

| Shortcut | Function |
|----------|----------|
| Ctrl/Cmd + K | Instant Translation: search this chat |
| Alt+Shift+T (macOS: Alt+T) | Browser extension: translate selection |
| Cmd/Ctrl+Alt+T | VS Code extension: translate selection in place |

### D. File format support

See the table in [5.1 Creating a job](#51-creating-a-job).

---

*This guide covers tenant-facing product features. For commercial or billing matters, contact your company administrator or the LangCross service team.*
