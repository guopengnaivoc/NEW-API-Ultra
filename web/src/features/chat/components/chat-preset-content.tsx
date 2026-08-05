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
import { Link } from '@tanstack/react-router'
import { ExternalLink, MessageCircleWarning } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  chatLinkRequiresApiKey,
  resolveChatUrl,
  type ChatPreset,
} from '@/features/chat/lib/chat-links'

type ChatPresetContentProps = {
  preset?: ChatPreset
  serverAddress: string
}

export function ChatPresetContent(props: ChatPresetContentProps) {
  const { t } = useTranslation()

  if (!props.preset) {
    return (
      <div className='flex h-full flex-col items-center justify-center gap-4 p-6 text-center'>
        <MessageCircleWarning
          aria-hidden='true'
          className='text-muted-foreground h-12 w-12'
        />
        <div className='space-y-1'>
          <h2 className='text-lg font-semibold'>
            {t('Chat preset not found')}
          </h2>
          <p className='text-muted-foreground'>
            {t('The requested chat preset does not exist or has been removed.')}
          </p>
        </div>
        <Button variant='outline' render={<Link to='/dashboard' />}>
          {t('Return to dashboard')}
        </Button>
      </div>
    )
  }

  if (props.preset.type !== 'web') {
    return (
      <div className='flex h-full flex-col items-center justify-center gap-4 p-6 text-center'>
        <MessageCircleWarning
          aria-hidden='true'
          className='text-muted-foreground h-12 w-12'
        />
        <div className='space-y-1'>
          <h2 className='text-lg font-semibold'>{t('Use sidebar shortcut')}</h2>
          <p className='text-muted-foreground'>
            {props.preset.name}{' '}
            {t(
              'opens in an external client. Trigger it from the sidebar or API key actions to launch the configured application.'
            )}
          </p>
        </div>
        <Button variant='outline' render={<Link to='/dashboard' />}>
          {t('Return to dashboard')}
        </Button>
      </div>
    )
  }

  if (chatLinkRequiresApiKey(props.preset.url)) {
    return (
      <div className='flex h-full flex-col items-center justify-center gap-4 p-6'>
        <Alert variant='destructive' className='max-w-xl'>
          <AlertTitle>{t('Unable to open chat')}</AlertTitle>
          <AlertDescription>
            {t(
              'This shortcut requires an API key. Stored keys cannot be revealed; configure the client when a key is first created or rotated.'
            )}
          </AlertDescription>
        </Alert>
        <Button variant='outline' render={<Link to='/keys' />}>
          {t('Manage API keys')}
        </Button>
      </div>
    )
  }

  const externalUrl = resolveChatUrl({
    template: props.preset.url,
    serverAddress: props.serverAddress,
  })

  if (!externalUrl) {
    return (
      <div className='flex h-full flex-col items-center justify-center p-6'>
        <Alert variant='destructive' className='max-w-xl'>
          <AlertTitle>{t('Unable to open chat')}</AlertTitle>
          <AlertDescription>
            {t(
              'Unable to generate chat link. Please contact your administrator.'
            )}
          </AlertDescription>
        </Alert>
      </div>
    )
  }

  return (
    <div className='flex h-full flex-col items-center justify-center gap-4 p-6 text-center'>
      <ExternalLink
        aria-hidden='true'
        className='text-muted-foreground h-12 w-12'
      />
      <h2 className='text-lg font-semibold'>{props.preset.name}</h2>
      <Button
        render={
          <a href={externalUrl} target='_blank' rel='noopener noreferrer' />
        }
      >
        {t('Open in new tab')}
      </Button>
    </div>
  )
}
