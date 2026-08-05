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
import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertAction, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'

type TurnstileApi = {
  render: (element: HTMLElement, options: Record<string, unknown>) => string
  remove: (widgetId: string) => void
}

declare global {
  interface Window {
    turnstile?: TurnstileApi
  }
}

const TURNSTILE_SCRIPT_ID = 'cf-turnstile'
const TURNSTILE_SCRIPT_SRC =
  'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit'

let turnstileLoadPromise: Promise<TurnstileApi> | null = null

function loadTurnstile(): Promise<TurnstileApi> {
  if (window.turnstile) return Promise.resolve(window.turnstile)
  if (turnstileLoadPromise) return turnstileLoadPromise

  const loadPromise = new Promise<TurnstileApi>((resolve, reject) => {
    const existingElement = document.querySelector(`#${TURNSTILE_SCRIPT_ID}`)
    if (existingElement && !(existingElement instanceof HTMLScriptElement)) {
      reject(new Error('Turnstile script id is already in use'))
      return
    }

    const script = existingElement ?? document.createElement('script')
    const cleanupListeners = () => {
      script.removeEventListener('load', handleLoad)
      script.removeEventListener('error', handleError)
    }
    const fail = () => {
      cleanupListeners()
      script.remove()
      reject(new Error('Failed to load Turnstile'))
    }
    const handleLoad = () => {
      if (!window.turnstile) {
        fail()
        return
      }
      cleanupListeners()
      resolve(window.turnstile)
    }
    const handleError = () => fail()

    script.addEventListener('load', handleLoad, { once: true })
    script.addEventListener('error', handleError, { once: true })

    if (!existingElement) {
      script.id = TURNSTILE_SCRIPT_ID
      script.src = TURNSTILE_SCRIPT_SRC
      script.async = true
      script.defer = true
      document.head.appendChild(script)
    }
  })

  turnstileLoadPromise = loadPromise
  const clearInFlightLoad = () => {
    if (turnstileLoadPromise === loadPromise) {
      turnstileLoadPromise = null
    }
  }
  void loadPromise.then(clearInFlightLoad, clearInFlightLoad)
  return loadPromise
}

interface TurnstileProps {
  siteKey: string
  onVerify: (token: string) => void
  onExpire?: () => void
  className?: string
}

type FailedAttempt = {
  siteKey: string
  generation: number
}

export function Turnstile(props: TurnstileProps) {
  const { t } = useTranslation()
  const [retryGeneration, setRetryGeneration] = useState(0)
  const [failedAttempt, setFailedAttempt] = useState<FailedAttempt | null>(null)
  const ref = useRef<HTMLDivElement | null>(null)
  const latestOnVerifyRef = useRef(props.onVerify)
  const latestOnExpireRef = useRef(props.onExpire)
  const nextWidgetGenerationRef = useRef(0)
  const activeWidgetGenerationRef = useRef<number | null>(null)

  useLayoutEffect(() => {
    latestOnVerifyRef.current = props.onVerify
    latestOnExpireRef.current = props.onExpire
  }, [props.onExpire, props.onVerify])

  useLayoutEffect(() => {
    nextWidgetGenerationRef.current += 1
    const widgetGeneration = nextWidgetGenerationRef.current
    activeWidgetGenerationRef.current = widgetGeneration

    return () => {
      if (activeWidgetGenerationRef.current === widgetGeneration) {
        activeWidgetGenerationRef.current = null
      }
    }
  }, [props.siteKey])

  useEffect(() => {
    const widgetGeneration = activeWidgetGenerationRef.current
    if (widgetGeneration === null) return

    let disposed = false
    let attemptActive = true
    let widgetApi: TurnstileApi | null = null
    let widgetId: string | null = null
    const attempt = {
      siteKey: props.siteKey,
      generation: retryGeneration,
    }
    const ownsWidgetGeneration = () =>
      !disposed &&
      attemptActive &&
      activeWidgetGenerationRef.current === widgetGeneration
    const markFailed = () => {
      if (!ownsWidgetGeneration()) return
      attemptActive = false
      setFailedAttempt(attempt)
    }

    void loadTurnstile().then((api) => {
      const target = ref.current
      if (!ownsWidgetGeneration() || !target) return

      try {
        const renderedWidgetId = api.render(target, {
          sitekey: props.siteKey,
          callback: (token: string) => {
            if (ownsWidgetGeneration()) latestOnVerifyRef.current(token)
          },
          'error-callback': () => {
            if (ownsWidgetGeneration()) latestOnExpireRef.current?.()
          },
          'expired-callback': () => {
            if (ownsWidgetGeneration()) latestOnExpireRef.current?.()
          },
        })
        if (!renderedWidgetId) {
          markFailed()
          return
        }
        if (!ownsWidgetGeneration()) {
          try {
            api.remove(renderedWidgetId)
          } catch {
            /* empty */
          }
          return
        }
        widgetApi = api
        widgetId = renderedWidgetId
        setFailedAttempt((current) =>
          current?.siteKey === attempt.siteKey &&
          current.generation === attempt.generation
            ? null
            : current
        )
      } catch {
        markFailed()
      }
    }, markFailed)

    return () => {
      disposed = true
      if (!widgetApi || widgetId === null) return
      try {
        widgetApi.remove(widgetId)
      } catch {
        /* empty */
      }
    }
  }, [props.siteKey, retryGeneration])

  const hasLoadError =
    failedAttempt?.siteKey === props.siteKey &&
    failedAttempt.generation === retryGeneration

  return (
    <>
      <div ref={ref} className={props.className} />
      {hasLoadError && (
        <Alert variant='destructive'>
          <AlertDescription>{t('Failed to load')}</AlertDescription>
          <AlertAction>
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={() => setRetryGeneration((current) => current + 1)}
            >
              {t('Retry')}
            </Button>
          </AlertAction>
        </Alert>
      )}
    </>
  )
}
