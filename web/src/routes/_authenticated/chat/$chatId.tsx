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
import { createFileRoute, redirect } from '@tanstack/react-router'

import { ChatPresetContent } from '@/features/chat/components/chat-preset-content'
import { useChatPresets } from '@/features/chat/hooks/use-chat-presets'

export const Route = createFileRoute('/_authenticated/chat/$chatId')({
  loader: async ({ params }) => {
    if (!Number.isInteger(Number(params.chatId))) {
      throw redirect({ to: '/dashboard' })
    }
  },
  component: ChatRouteComponent,
})

function ChatRouteComponent() {
  const { chatId } = Route.useParams()
  const { chatPresets, serverAddress } = useChatPresets()
  const index = Number(chatId)
  const preset = Number.isInteger(index) ? chatPresets[index] : undefined

  return <ChatPresetContent preset={preset} serverAddress={serverAddress} />
}
