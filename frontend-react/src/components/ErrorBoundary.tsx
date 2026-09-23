// ============================================================================
// components/ErrorBoundary.tsx — 全局错误边界
// 捕获子树渲染期抛出的异常：避免单个面板抛错导致整页白屏（React 默认行为）。
// 提供「重试」（本地复位）与「返回首页」（window.location.href='/'）两条恢复路径；
// fallback 可自定义兜底 UI（传了就不走内置卡片）。
// ============================================================================
import { Component, type ReactNode } from 'react'
import { Icon } from '@/ui/langcross/src'

/** 错误边界入参：children=被保护子树；fallback=出错时自定义兜底（缺省内置卡片） */
interface Props {
  children: ReactNode
  fallback?: ReactNode
}

/** 内部状态：hasError 标记是否进入异常态（供 render 分支），error 保留原始错误 */
interface State {
  hasError: boolean
  error: Error | null
}

/** ErrorBoundary 错误边界：把渲染异常转成可见的「页面出错了」卡片 + 重试按钮
 *  必须是 class 组件：React 只提供 componentDidCatch / getDerivedStateFromError 两个
 *  类生命周期钩子，函数组件没有等价物（要函数式只能引第三方 react-error-boundary）。 */
export default class ErrorBoundary extends Component<Props, State> {
  constructor(props: Props) {
    super(props)
    this.state = { hasError: false, error: null }
  }

  /** 由 React 自动调用：把抛出的异常转为 hasError=true 的内部状态（等价「熔断」） */
  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error }
  }

  // 崩溃卡片之外的唯一线索来源：把 componentStack 落 console，便于用户复述时能定位到具体面板。
  // ⚠ 这里只上报到控制台——边界只覆盖「渲染期同步抛错」，请求/Promise 异常走不到这儿（那些由 runGuarded 兜），
  //   所以别把它当成前端的统一错误上报口
  componentDidCatch(error: Error, info: React.ErrorInfo) {
    console.error('[ErrorBoundary]', error, info.componentStack)
  }

  /** handleRetry 本地复位异常态：不清历史，仅允许 React 重新渲染子树
   *  （若错误源于同样的渲染路径，重试会立刻再次触发边界，所以必须留着「返回首页」这条出口） */
  handleRetry = () => {
    this.setState({ hasError: false, error: null })
  }

  /** render 异常态走兜底卡片（优先自定义 fallback），正常态原样渲染子树 */
  render() {
    if (this.state.hasError) {
      if (this.props.fallback) return this.props.fallback
      return (
        // 兜底卡片占满整个视口：崩溃时页面布局已不可信，全屏居中比「嵌在原位的一块红框」更容易被看到
        // 配色走 --lc-* 纯黑单色令牌；崩溃页在 i18n 初始化前也可能渲染，故保留硬编码中文兜底文案
        // ★ 每个 var() 都带字面兜底值（如 var(--lc-text-1, #E7E9EA)）：本页可能就是令牌层
        //   （theme.css / 组件库 CSS）随模块一起崩掉的那一次，变量取不到时也不能退成浏览器默认的黑字。
        //   兜底值同时不受 readability.test.ts 的「#68 描边禁写死暗值」闸门影响——
        //   该锁显式跳过含 var( 的声明，只拦真正的写死字面值。
        <div style={{
          display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
          minHeight: '100vh', gap: 16, fontFamily: 'system-ui, sans-serif', padding: 24, textAlign: 'center',
        }}>
          <div style={{ fontSize: 48 }}><Icon n="alert" /></div>
          <h2 style={{ margin: 0, fontSize: 20, color: 'var(--lc-text-1, #E7E9EA)' }}>页面出现异常</h2>
          {/* 直接把 error.message 摊出来：这页已经不会有人二次操作，信息多一点比美观重要 */}
          <p style={{ margin: 0, fontSize: 16, color: 'var(--lc-text-3, #9AA0AA)', maxWidth: 480 }}>
            {this.state.error?.message || '未知错误，请尝试刷新页面'}
          </p>
          <div style={{ display: 'flex', gap: 12, marginTop: 8 }}>
            {/* 一主一次：主按钮反白填充（描边跟着填充走白，故取 --lc-text-1 而不是描边令牌，
                否则边比底暗，按钮会「缺一个角」）；次按钮透明底 + 描边令牌。 */}
            <button
              onClick={this.handleRetry}
              style={{
                padding: '8px 24px', borderRadius: 6, border: '1.2px solid var(--lc-fill-white, #FFFFFF)',
                background: 'var(--lc-fill-white, #FFFFFF)', color: '#000', fontSize: 16, cursor: 'pointer',
              }}
            >
              重试
            </button>
            <button
              // 硬跳转（不是 navigate）：整页重载才能顺带丢掉可能已经脏掉的 store / 模块级单例状态
              onClick={() => { window.location.href = '/' }}
              style={{
                padding: '8px 24px', borderRadius: 6, border: '1.2px solid var(--lc-border-pill, #424956)',
                background: 'transparent', color: 'var(--lc-text-1, #E7E9EA)', fontSize: 16, cursor: 'pointer',
              }}
            >
              返回首页
            </button>
          </div>
        </div>
      )
    }
    return this.props.children
  }
}
