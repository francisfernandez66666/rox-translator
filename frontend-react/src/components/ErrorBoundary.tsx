import { Component, type ReactNode } from 'react'

interface Props {
  children: ReactNode
  fallback?: ReactNode
}

interface State {
  hasError: boolean
  error: Error | null
}

export default class ErrorBoundary extends Component<Props, State> {
  constructor(props: Props) {
    super(props)
    this.state = { hasError: false, error: null }
  }

  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error }
  }

  componentDidCatch(error: Error, info: React.ErrorInfo) {
    console.error('[ErrorBoundary]', error, info.componentStack)
  }

  handleRetry = () => {
    this.setState({ hasError: false, error: null })
  }

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
