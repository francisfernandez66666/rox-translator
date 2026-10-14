// ============ notifications_ref.go · 职责说明 ============
// 站内信「关联类型」（notifications.ref_type）的**唯一名单**。
//
// 为什么要单独成文件（★ #46，2026-10-14 用户令「采集与告警类通知收敛成一把尺子」）：
// ref_type 不是一个自描述字符串，它是**前端铃铛点击落点的路由键**
// （frontend-react/src/lib/noticeRouting.ts 按它决定去哪个后台面板）。
// 过去它由各投递点随手写字面量（"ticket"/"feedback"/"quota"/"tenant"/"kb_scrape"/"pay_manual"），
// 于是"加了一类通知"这件事在仓库里没有单一痕迹：前端不知道该多一个落点，
// 现网形态就是采集通知正文写着「请前往数据源面板查看」，点下去却进工单面板。
//
// 纪律：新增一类站内信 ⇒ 必须①在本文件补一条常量并进 NotifyRefTypes 名单、
// ②在 noticeRouting.ts 补该类型的落点。两侧由跨语言等值锁机械拦截
// （noticeRouting.lock.test.ts：名单里有而映射里没有＝红，映射里有而名单里没有＝红）。
// =============================================
package store

// 站内信关联类型名单（值即 notifications.ref_type 列的字面量，历史已落库，**禁止改名**）。
const (
	NotifyRefTicket    = "ticket"     // 工单生命周期（完成/失败/产物过期），ref_id=工单 ID
	NotifyRefFeedback  = "feedback"   // 问题反馈与销售线索（同一条 BBS 通道），ref_id=反馈单 ID
	NotifyRefQuota     = "quota"      // 部门/组织预算墙命中，ref_id=组织或租户 ID
	NotifyRefTenant    = "tenant"     // 租户管理员面（订阅/额度/品牌），ref_id=租户 ID
	NotifyRefKbScrape  = "kb_scrape"  // 行业包采集轮次完成，ref_id 恒 0（没有单条对象可定位）
	NotifyRefPayManual = "pay_manual" // 静态码支付待人工确认，ref_id=订单 ID
)

// NotifyRefTypes 名单全集（派生值，闸门与前端锁都从这里派生，禁止再抄一份数组字面量）。
var NotifyRefTypes = []string{
	NotifyRefTicket,
	NotifyRefFeedback,
	NotifyRefQuota,
	NotifyRefTenant,
	NotifyRefKbScrape,
	NotifyRefPayManual,
}
