// ============================================================================
// components/guidedchat/GuidedChatFrame.tsx — 对话式引导外壳的**唯一**视图（H1，2026-11-06）
//
// 与 guidedChatMachine.ts（状态机）＋ guidedChatStyles.ts（样式）合成"一套外壳"：
// 顶部栏（×／标题／在线点）→ 进度（步骤名＋上一步＋进度条）→ 聊天流（气泡＋打字机）→ 场景自己挂的内容。
//
// 三条口径：
//  ① **打字机的 ghost 撑高在这里，不在场景里**：真身绝对定位、ghost 同文本 visibility:hidden，
//     窄屏逐字打字不跳高。场景各自实现一遍必然出现"有的场景跳、有的不跳"。
//  ② 气泡之外的内容（选项组／表单／chips／检查点／摘要卡）**一律经 renderBody 由场景投喂**，
//     外壳不认识注册或产品的字段；这样加第三个场景（门店/私域）不改这里。
//  ③ 进度条用整宽元素做负位移"填充"：transform 只走合成层，不像 width 那样每帧重排（交付档原样）。
//
// 类名沿用注册面那一份 `.ar-*`（见 guidedChatStyles 文件头），改名会同时推翻交付真值与既有闸门。
// ============================================================================
import type { ReactNode } from 'react'
import { CSS_GUIDED } from './guidedChatStyles'
import type { GuidedMsgBase } from './guidedChatMachine'

/** 外壳入参：话术与回调全部由场景给（外壳不取词、不认识任何业务字段） */
export interface GuidedFrameProps<M extends GuidedMsgBase> {
  /** 顶部栏标题（场景自己取词） */
  title: string
  /** 顶部栏右侧"在线"读数文案（不分场景，同一句话） */
  onlineLabel: string
  /** × 按钮的 aria-label：注册面是「返回登录」，完善信息/引导流是「暂时跳过」——语义不同，必须由各场景自己给 */
  closeLabel: string
  onClose: () => void
  stepLabel: string
  trackPct: number
  backLabel: string
  backHidden: boolean
  backDisabled?: boolean
  onBack: () => void
  /** AI 气泡署名行：徽标文字（恒 AI）＋ 名字（品牌名或场景标题） */
  badgeLabel: string
  whoLabel: string
  msgs: M[]
  /** 气泡下方由场景自己挂的内容（选项/表单/摘要卡…） */
  renderBody: (m: M) => ReactNode
  chatRef: React.MutableRefObject<HTMLDivElement | null>
}

/** 对话式引导外壳：注册引导、完善信息与引导建产品**共用这一份**（第二份必须被锁判红） */
export function GuidedChatFrame<M extends GuidedMsgBase>(props: GuidedFrameProps<M>) {
  const {
    title, onlineLabel, closeLabel, onClose, stepLabel, trackPct, backLabel, backHidden,
    backDisabled, onBack, badgeLabel, whoLabel, msgs, renderBody, chatRef,
  } = props
  return (
    <div className="ar-root">
      <style>{CSS_GUIDED}</style>
      <div className="ar-panel">
        {/* 顶部栏 */}
        <div className="ar-nav">
          <button className="ar-close" type="button" aria-label={closeLabel} onClick={onClose}>
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round"><path d="M6 6l12 12M18 6L6 18" /></svg>
          </button>
          <span className="ar-nav-t">{title}</span>
          <span className="ar-online"><i />{onlineLabel}</span>
        </div>
        {/* 进度（带名字） */}
        <div className="ar-prog">
          <div className="ar-prog-top">
            <span className="ar-prog-label">{stepLabel}</span>
            <button className="ar-back" type="button" hidden={backHidden} disabled={backDisabled} onClick={onBack}>{backLabel}</button>
          </div>
          <div className="ar-track"><i style={{ transform: `translateX(${-(100 - trackPct)}%)` }} /></div>
        </div>
        {/* 聊天流 */}
        <div className="ar-chat" ref={chatRef}>
          {msgs.map((m) => (
            <div key={m.id} className={`ar-msg ar-msg--${m.side}`}>
              {m.side === 'ai' && (
                <div className="ar-who"><span className="ar-badge">{badgeLabel}</span><span>{whoLabel}</span></div>
              )}
              <div className="ar-body">
                {m.text !== undefined && (
                  <div className="ar-bubble">
                    {m.full && m.text !== m.full ? (
                      <span className="ar-type">
                        <span className="ar-ghost">{m.full}</span>
                        <span className="ar-real">{m.text}</span>
                        {m.typing && <span className="ar-caret" />}
                      </span>
                    ) : (m.full ?? m.text)}
                  </div>
                )}
                {renderBody(m)}
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
