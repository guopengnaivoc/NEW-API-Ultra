/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useLayoutEffect, useRef, useState } from 'react'

import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'

type LongTextProps = {
  children: React.ReactNode
  className?: string
  contentClassName?: string
}

const viewportInvalidationSubscribers = new Set<() => void>()
let viewportInvalidationFrame: number | null = null

function flushViewportInvalidation() {
  viewportInvalidationFrame = null
  const subscribers = [...viewportInvalidationSubscribers]
  subscribers.forEach((subscriber) => subscriber())
}

function scheduleViewportInvalidation() {
  if (viewportInvalidationFrame !== null) {
    return
  }

  viewportInvalidationFrame = window.requestAnimationFrame(
    flushViewportInvalidation
  )
}

function subscribeViewportInvalidation(subscriber: () => void) {
  viewportInvalidationSubscribers.add(subscriber)
  if (viewportInvalidationSubscribers.size === 1) {
    window.addEventListener('resize', scheduleViewportInvalidation)
  }

  return () => {
    if (!viewportInvalidationSubscribers.delete(subscriber)) {
      return
    }
    if (viewportInvalidationSubscribers.size > 0) {
      return
    }

    window.removeEventListener('resize', scheduleViewportInvalidation)
    if (viewportInvalidationFrame !== null) {
      window.cancelAnimationFrame(viewportInvalidationFrame)
      viewportInvalidationFrame = null
    }
  }
}

export function LongText({
  children,
  className = '',
  contentClassName = '',
}: LongTextProps) {
  const textContainerRef = useRef<HTMLDivElement>(null)
  const [isOverflown, setIsOverflown] = useState(false)
  const [fullText, setFullText] = useState('')

  useLayoutEffect(() => {
    const target = textContainerRef.current
    if (!target) {
      return
    }

    let active = true
    const updateOverflow = () => {
      if (!active) {
        return
      }

      setFullText(target.textContent ?? '')
      setIsOverflown(checkOverflow(target))
    }

    updateOverflow()

    let observer: ResizeObserver | null = null
    if (typeof ResizeObserver !== 'undefined') {
      observer = new ResizeObserver(updateOverflow)
      observer.observe(target)
    }

    let mutationObserver: MutationObserver | null = null
    if (typeof MutationObserver !== 'undefined') {
      mutationObserver = new MutationObserver(updateOverflow)
      mutationObserver.observe(target, {
        childList: true,
        characterData: true,
        subtree: true,
        attributes: true,
        attributeFilter: ['class', 'style'],
      })
    }

    const fontSet = document.fonts
    fontSet?.addEventListener('loadingdone', updateOverflow)
    const unsubscribeViewportInvalidation =
      subscribeViewportInvalidation(updateOverflow)

    return () => {
      active = false
      observer?.disconnect()
      mutationObserver?.disconnect()
      fontSet?.removeEventListener('loadingdone', updateOverflow)
      unsubscribeViewportInvalidation()
    }
  }, [children, className])

  return (
    <div className={cn('relative min-w-0', className)}>
      <div ref={textContainerRef} className='truncate'>
        {children}
      </div>
      {isOverflown && (
        <>
          <div className='absolute inset-0 hidden sm:block'>
            <TooltipProvider delay={0}>
              <Tooltip>
                <TooltipTrigger
                  render={
                    <div
                      aria-label={fullText}
                      className='size-full'
                      data-long-text-overflow-trigger=''
                    />
                  }
                />
                <TooltipContent>
                  <p className={contentClassName}>{fullText}</p>
                </TooltipContent>
              </Tooltip>
            </TooltipProvider>
          </div>
          <div className='absolute inset-0 sm:hidden'>
            <Popover>
              <PopoverTrigger
                nativeButton={false}
                render={
                  <div
                    aria-label={fullText}
                    className='size-full'
                    data-long-text-overflow-trigger=''
                  />
                }
              />
              <PopoverContent className={cn('w-fit', contentClassName)}>
                <p>{fullText}</p>
              </PopoverContent>
            </Popover>
          </div>
        </>
      )}
    </div>
  )
}

const checkOverflow = (textContainer: HTMLDivElement | null) => {
  if (textContainer) {
    return (
      textContainer.offsetHeight < textContainer.scrollHeight ||
      textContainer.offsetWidth < textContainer.scrollWidth
    )
  }
  return false
}
