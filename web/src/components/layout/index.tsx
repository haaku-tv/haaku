import type { PropsWithChildren } from 'react'
import * as React from 'react'
import { useRailWheel } from '@/lib/rail-wheel'
import { cn } from '@/lib/utils'

/**
 * Responsive page wrapper.
 * Applies consistent horizontal padding and max-width constraint.
 */
export function Page({
  children,
  className,
}: PropsWithChildren<{ className?: string }>) {
  return (
    <div
      className={cn(
        'mx-auto w-full max-w-[1440px] px-5 sm:px-8 lg:px-10 pb-12 pt-6',
        className
      )}
    >
      {children}
    </div>
  )
}

/**
 * Content section wrapper.
 */
export function Section({
  children,
  className,
  title,
}: PropsWithChildren<{ className?: string; title?: string }>) {
  return (
    <section className={cn('py-5', className)}>
      {title && (
        <h2 className="mb-4 font-display text-xl font-semibold text-sf-ink">
          {title}
        </h2>
      )}
      {children}
    </section>
  )
}

/**
 * Grid of poster cards — responsive auto-fill.
 */
export function PosterGrid({
  children,
  className,
}: PropsWithChildren<{ className?: string }>) {
  return (
    <div
      className={cn(
        'grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6',
        className
      )}
    >
      {children}
    </div>
  )
}

/**
 * Horizontal scrollable row.
 *
 * The wheel handler is what keeps the PAGE scrollable: without it a vertical
 * wheel over a row scrolls the row sideways and the page never moves (see
 * `lib/rail-wheel.ts`).
 */
export function ScrollRow({
  children,
  className,
}: PropsWithChildren<{ className?: string }>) {
  const ref = React.useRef<HTMLDivElement>(null)
  useRailWheel(ref)
  return (
    <div
      ref={ref}
      className={cn(
        'flex gap-3 overflow-x-auto overflow-y-hidden pb-2 scrollbar-hide',
        className
      )}
    >
      {children}
    </div>
  )
}
