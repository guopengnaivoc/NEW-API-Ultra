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
import { CircleAlert } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

type ChartThemeErrorProps = {
  className?: string
  onRetry: () => void
}

export function ChartThemeError(props: ChartThemeErrorProps) {
  const { t } = useTranslation()

  return (
    <div
      className={cn(
        'flex h-full w-full items-center justify-center p-4',
        props.className
      )}
    >
      <Alert variant='destructive' className='max-w-md'>
        <CircleAlert />
        <AlertTitle>{t('Failed to load')}</AlertTitle>
        <AlertDescription className='flex flex-col items-start gap-3 sm:flex-row sm:items-center sm:justify-between'>
          <span>{t('Please try again later.')}</span>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={props.onRetry}
          >
            {t('Retry')}
          </Button>
        </AlertDescription>
      </Alert>
    </div>
  )
}
