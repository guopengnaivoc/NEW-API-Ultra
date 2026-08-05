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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'

import { rotateApiKey } from '../api'
import { ERROR_MESSAGES } from '../constants'
import { useApiKeys } from './api-keys-provider'

export function ApiKeysRotateDialog() {
  const { t } = useTranslation()
  const { open, setOpen, currentRow, triggerRefresh, showOneTimeTokens } =
    useApiKeys()
  const [isRotating, setIsRotating] = useState(false)
  const {
    open: verificationOpen,
    methods: verificationMethods,
    state: verificationState,
    executeVerification,
    withVerification,
    cancel: cancelVerification,
    setCode: setVerificationCode,
    switchMethod: switchVerificationMethod,
  } = useSecureVerification()

  const handleRotate = async () => {
    if (!currentRow) return

    setIsRotating(true)
    try {
      await withVerification(
        async (proofToken) => {
          const response = await rotateApiKey(currentRow.id, proofToken)
          if (!response.success || !response.data?.key) {
            throw new Error(response.message || t('Failed to rotate API key'))
          }
          showOneTimeTokens([
            {
              id: response.data.token?.id || currentRow.id,
              name: response.data.token?.name || currentRow.name,
              key: response.data.key,
            },
          ])
          toast.success(t('API key rotated successfully'))
          setOpen(null)
          triggerRefresh()
          return response.data
        },
        {
          scope: 'token.key.rotate',
          target: String(currentRow.id),
          title: t('Security verification'),
          description: t(
            'Confirm your identity before accessing this sensitive action.'
          ),
        }
      )
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t(ERROR_MESSAGES.UNEXPECTED)
      )
    } finally {
      setIsRotating(false)
    }
  }

  return (
    <>
      <AlertDialog
        open={open === 'rotate'}
        onOpenChange={(nextOpen) => {
          if (!nextOpen && !isRotating) {
            cancelVerification()
            setOpen(null)
          }
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Rotate API key?')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(
                'The current key will stop working immediately. The replacement key will be shown only once.'
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={isRotating}>
              {t('Cancel')}
            </AlertDialogCancel>
            <AlertDialogAction
              onClick={handleRotate}
              disabled={isRotating}
              variant='destructive'
            >
              {isRotating ? t('Rotating...') : t('Rotate key')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <SecureVerificationDialog
        open={verificationOpen}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) {
            cancelVerification()
          }
        }}
        methods={verificationMethods}
        state={verificationState}
        onVerify={async (method, code) => {
          await executeVerification(method, code)
        }}
        onCancel={cancelVerification}
        onCodeChange={setVerificationCode}
        onMethodChange={switchVerificationMethod}
      />
    </>
  )
}
