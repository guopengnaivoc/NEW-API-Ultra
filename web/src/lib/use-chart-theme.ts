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
import { useCallback, useEffect, useState } from 'react'

import { useTheme } from '@/context/theme-provider'

export type ChartThemeManager = Pick<
  (typeof import('@visactor/vchart'))['ThemeManager'],
  'setCurrentTheme'
>

export type ChartThemeManagerLoader = {
  load: () => Promise<ChartThemeManager>
}

/**
 * Lazy-load VChart's `ThemeManager` and switch its theme to follow the
 * resolved app theme (light / dark). Returns flags consumers can use to
 * defer chart rendering until the theme is ready.
 */
export function createChartThemeManagerLoader(
  loadManager: () => Promise<ChartThemeManager>
): ChartThemeManagerLoader {
  let managerPromise: Promise<ChartThemeManager> | null = null

  return {
    load() {
      if (!managerPromise) {
        const guardedPromise = Promise.resolve()
          .then(loadManager)
          .catch((error: unknown) => {
            if (managerPromise === guardedPromise) {
              managerPromise = null
            }
            throw error
          })
        managerPromise = guardedPromise
      }
      return managerPromise
    },
  }
}

const chartThemeManagerLoader = createChartThemeManagerLoader(() =>
  import('@visactor/vchart').then((module) => module.ThemeManager)
)

export function useChartThemeWithLoader(loader: ChartThemeManagerLoader) {
  const { resolvedTheme } = useTheme()
  const [themeReady, setThemeReady] = useState(false)
  const [themeError, setThemeError] = useState<Error | null>(null)
  const [retryGeneration, setRetryGeneration] = useState(0)
  const retryTheme = useCallback(() => {
    setRetryGeneration((generation) => generation + 1)
  }, [])

  useEffect(() => {
    let cancelled = false
    setThemeReady(false)
    setThemeError(null)

    void loader
      .load()
      .then((ThemeManager) => {
        if (cancelled) return
        ThemeManager.setCurrentTheme(
          resolvedTheme === 'dark' ? 'dark' : 'light'
        )
        if (!cancelled) {
          setThemeReady(true)
        }
      })
      .catch((error: unknown) => {
        if (cancelled) return
        setThemeError(
          error instanceof Error
            ? error
            : new Error('Failed to load the chart theme.')
        )
      })

    return () => {
      cancelled = true
    }
  }, [loader, resolvedTheme, retryGeneration])

  return { resolvedTheme, retryTheme, themeError, themeReady }
}

export function useChartTheme() {
  return useChartThemeWithLoader(chartThemeManagerLoader)
}
