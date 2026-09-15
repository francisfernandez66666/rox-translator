// ============================================================================
// components/ErrorBoundary.tsx — 全局错误边界
// 捕获子树渲染期抛出的异常：避免单个面板抛错导致整页白屏（React 默认行为）。
// 提供「重试」（本地复位）与「刷新」两条恢复路径；fallback 可自定义兜底 UI。
// ============================================================================
import { Component, type ReactNode } from 'react'

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

/** ErrorBoundary 错误边界：把渲染异常转成可见的「页面出错了」卡片 + 重试按钮 */
export default class ErrorBoundary extends Component<Props, State> {
  constructor(props: Props) {
    super(props)
    this.state = { hasError: false, error: null }
  }

  /** 由 React 自动调用：把抛出的异常转为 hasError=true 的内部状态（等价「熔断」） */
  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error }
  }

  componentDidCatch(error: Error, info: React.ErrorInfo) {
    console.error('[ErrorBoundary]', error, info.componentStack)
  }

  /** handleRetry 本地复位异常态：不清历史，仅允许 React 重新渲染子树 */
  handleRetry = () => {
    this.setState({ hasError: false, error: null })
  }

  /** render 异常态走兜底卡片（优先自定义 fallback），正常态原样渲染子树 */
  render() {
    if (this.state.hasError) {
      if (this.props.fallback) return this.props.fallback
      return (
        <div style={{
          display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
          minHeight: '100vh', gap: 16, fontFamily: 'system-ui, sans-serif', padding: 24, textAlign: 'center',
        }}>
          <div style={{ fontSize: 48 }}>⚠️</div>
          <h2 style={{ margin: 0, fontSize: 20, color: '#333' }}>页面出现异常</h2>
          <p style={{ margin: 0, fontSize: 14, color: '#666', maxWidth: 480 }}>
            {this.state.error?.message || '未知错误，请尝试刷新页面'}
          </p>
          <div style={{ display: 'flex', gap: 12, marginTop: 8 }}>
            <button
              onClick={this.handleRetry}
              style={{
                padding: '8px 24px', borderRadius: 6, border: '1px solid #2f47f5',
                background: '#2f47f5', color: '#fff', fontSize: 14, cursor: 'pointer',
              }}
            >
              重试
            </button>
            <button
              onClick={() => { window.location.href = '/' }}
              style={{
                padding: '8px 24px', borderRadius: 6, border: '1px solid #ddd',
                background: '#fff', color: '#333', fontSize: 14, cursor: 'pointer',
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
