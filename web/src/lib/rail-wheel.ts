import * as React from 'react'

/**
 * The nearest ancestor that actually scrolls vertically.
 *
 * It is what the page's wheel belongs to. `main` in the app shell is that
 * element: the shell clamps the viewport height and scrolls inside it, so the
 * document itself never overflows and `document.scrollingElement` is not the
 * scroll port.
 */
function verticalScroller(from: HTMLElement | null): HTMLElement | null {
  let el = from?.parentElement ?? null
  while (el && el !== document.body) {
    const style = getComputedStyle(el)
    const scrollsY = style.overflowY === 'auto' || style.overflowY === 'scroll'
    if (scrollsY && el.scrollHeight > el.clientHeight + 1) return el
    el = el.parentElement
  }
  return null
}

/**
 * Keep the vertical wheel for the page when the pointer is over a horizontal rail.
 *
 * A rail takes the wheel: the browser hands the gesture to the nearest scrollable
 * element, and `overflow-x: auto` qualifies even when the gesture is vertical.
 * With the app shell clamped to the viewport, the row scrolls sideways instead of
 * the page moving, so a page made of rows appears frozen — content below the fold
 * is unreachable with a mouse or a trackpad.
 *
 * The rail therefore keeps only the gestures that ask for it (a shift+wheel, or a
 * trackpad swipe whose horizontal delta dominates) and hands everything else to
 * the page's scroll port. Registered natively and not passively, because the
 * default scroll has to be prevented for the hand-off to happen.
 */
export function useRailWheel(ref: React.RefObject<HTMLDivElement | null>) {
  React.useEffect(() => {
    const el = ref.current
    if (!el) return

    const onWheel = (event: WheelEvent) => {
      if (event.shiftKey || Math.abs(event.deltaX) > Math.abs(event.deltaY))
        return
      const scroller = verticalScroller(el)
      if (!scroller) return
      const max = scroller.scrollHeight - scroller.clientHeight
      const next = Math.min(Math.max(scroller.scrollTop + event.deltaY, 0), max)
      if (next === scroller.scrollTop) return
      scroller.scrollTop = next
      event.preventDefault()
    }

    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  }, [ref])
}
