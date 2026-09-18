// ============================================================================
// ui/langcross/src/useReveal.ts — 滚动揭示
//
// 用法：
//   const ref = useReveal<HTMLDivElement>()
//   return <div ref={ref}>
//     <section className="lc-reveal">…</section>
//     <section className="lc-reveal" data-reveal-delay="120">…</section>
//   </div>
//
// 约定（对应动效十原则 7「仪器不能早于被测对象出现」）：
//   - 只在真进入视口时才现身，元素一到位即 unobserve，不做来回重放；
//   - rootMargin 下缘收 12%，避免元素刚探出头就开始动（那是"仪器先到"）；
//   - prefers-reduced-motion / 无 IntersectionObserver（jsdom 单测）时直接落终态。
//
// ★ 死区兜底（2026-09-18 实测踩到）：
//   负的 rootMargin 会把观察区的下边缘向上抬 12%，于是**文档最末尾**那些矮元素
//   （如收尾 CTA 的按钮）即使滚到底也永远够不到那条边 —— 实测首页 16 个揭示元素
//   有 3 个永远停在 opacity:0。这里在"滚到文档末尾"时补一次强制点亮修掉该死区。
// ============================================================================
import { useEffect, useRef } from 'react'

/** 观察 root 内所有 `.lc-reveal`，进入视口后加 `.is-in`（一次性） */
export function useReveal<T extends HTMLElement = HTMLDivElement>(deps: unknown[] = []) {
  const ref = useRef<T>(null)

  useEffect(() => {
    const root = ref.current
    if (!root) return

    const els = Array.from(root.querySelectorAll<HTMLElement>('.lc-reveal'))
    if (els.length === 0) return

    const revealAll = () => els.forEach((el) => el.classList.add('is-in'))

    // 偏好减少动效：不注册观察器，直接落终态
    const reduced = typeof window !=='undefined'&& !!window.matchMedia
      && window.matchMedia('(prefers-reduced-motion: reduce)').matches
    if (reduced) { revealAll(); return }

    // 无 IntersectionObserver（老旧环境 / jsdom 单测）时同样直接落终态，
    // 否则元素会永远停在 opacity:0 —— 这是"测试里看不见内容"的经典坑。
    if (typeof IntersectionObserver === 'undefined') { revealAll(); return }

    const io = new IntersectionObserver(
      (entries) => {
        for (const en of entries) {
          if (!en.isIntersecting) continue
          en.target.classList.add('is-in')
          io.unobserve(en.target)
        }
      },
      { rootMargin: '0px 0px -12% 0px', threshold: 0.08 },
    )
    els.forEach((el) => io.observe(el))

    // —— 死区兜底：只在「已滚到文档末尾」时才补，不影响正常节拍 ——
    let raf = 0
    const onScroll = () => {
      if (raf) return
      raf = requestAnimationFrame(() => {
        raf = 0
        const doc = document.documentElement
        const atBottom = window.innerHeight + window.scrollY >= doc.scrollHeight - 4
        if (!atBottom) return
        els.forEach((el) => {
          if (el.classList.contains('is-in')) return
          el.classList.add('is-in')
          io.unobserve(el)
        })
      })
    }
    window.addEventListener('scroll', onScroll, { passive: true })
    window.addEventListener('resize', onScroll, { passive: true })
    onScroll() // 内容不足一屏时也能兜住

    return () => {
      io.disconnect()
      window.removeEventListener('scroll', onScroll)
      window.removeEventListener('resize', onScroll)
      if (raf) cancelAnimationFrame(raf)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)

  return ref
}

export default useReveal
