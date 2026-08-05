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
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { copyToClipboard } from '@/lib/copy-to-clipboard'

export type OneTimeTokenEntry = {
  id: string | number
  name: string
  key: string
}

type OneTimeTokenDialogProps = {
  open: boolean
  entries: OneTimeTokenEntry[]
  onOpenChange: (open: boolean) => void
}

export function OneTimeTokenDialog({
  open,
  entries,
  onOpenChange,
}: OneTimeTokenDialogProps) {
  const { t } = useTranslation()

  const handleCopyAll = async () => {
    const value = entries
      .map((entry) => `${entry.name}\t${entry.key}`)
      .join('\n')
    if (await copyToClipboard(value)) {
      toast.success(t('Copied'))
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('Save your API key now')}
      description={t(
        'For your security, API keys are shown only once. They cannot be recovered after this dialog is closed.'
      )}
      contentClassName='sm:max-w-xl'
      contentHeight='auto'
      bodyClassName='space-y-4'
      showCloseButton={false}
      footer={
        <>
          {entries.length > 1 ? (
            <Button type='button' variant='outline' onClick={handleCopyAll}>
              {t('Copy all keys')}
            </Button>
          ) : null}
          <Button type='button' onClick={() => onOpenChange(false)}>
            {t('I have saved the key(s)')}
          </Button>
        </>
      }
    >
      <Alert>
        <AlertTitle>{t('Store these keys before continuing')}</AlertTitle>
        <AlertDescription>
          {t(
            'Use a password manager or another secure secret store. Rotating a key is the only way to replace a lost key.'
          )}
        </AlertDescription>
      </Alert>

      <div className='space-y-3'>
        {entries.map((entry, index) => {
          const inputId = `one-time-token-${index}`
          return (
            <div key={entry.id} className='space-y-1.5'>
              <Label htmlFor={inputId}>{entry.name}</Label>
              <div className='flex min-w-0 gap-2'>
                <Input
                  id={inputId}
                  value={entry.key}
                  readOnly
                  className='min-w-0 font-mono text-xs'
                  onFocus={(event) => event.currentTarget.select()}
                />
                <CopyButton
                  value={entry.key}
                  variant='outline'
                  className='size-9'
                  iconClassName='size-4'
                  tooltip={t('Copy API key')}
                  aria-label={t('Copy API key')}
                />
              </div>
            </div>
          )
        })}
      </div>
    </Dialog>
  )
}
