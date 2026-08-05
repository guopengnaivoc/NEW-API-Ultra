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
import { useNavigate } from '@tanstack/react-router'
import { AlertTriangle, Loader2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { logout } from '@/features/auth/api'
import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'
import { clearAuthentication } from '@/lib/api'

import { deleteUserAccount } from '../../api'

// ============================================================================
// Delete Account Dialog Component
// ============================================================================

interface DeleteAccountDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  username: string
}

export function DeleteAccountDialog({
  open,
  onOpenChange,
  username,
}: DeleteAccountDialogProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [loading, setLoading] = useState(false)
  const [confirmation, setConfirmation] = useState('')
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

  const handleDelete = async () => {
    if (confirmation !== username) {
      toast.error(t('Username confirmation does not match'))
      return
    }

    try {
      setLoading(true)
      await withVerification(
        async (proofToken) => {
          const response = await deleteUserAccount(proofToken)
          if (!response.success) {
            throw new Error(response.message || t('Failed to delete account'))
          }

          toast.success(t('Account deleted successfully'))

          // Logout and redirect
          try {
            await logout()
          } catch {
            // Ignore logout errors
          }

          clearAuthentication()
          navigate({ to: '/sign-in' })
          return response
        },
        {
          scope: 'account.delete',
          target: 'self',
          title: t('Security verification'),
          description: t(
            'Confirm your identity before accessing this sensitive action.'
          ),
        }
      )
    } catch (error) {
      toast.error(
        error instanceof Error && error.message
          ? error.message
          : t('Failed to delete account')
      )
    } finally {
      setLoading(false)
    }
  }

  const handleOpenChange = (open: boolean) => {
    if (!loading) {
      onOpenChange(open)
      if (!open) {
        setConfirmation('')
        cancelVerification()
      }
    }
  }

  return (
    <>
      <Dialog
        open={open}
        onOpenChange={handleOpenChange}
        title={
          <>
            <AlertTriangle className='h-5 w-5' />
            {t('Delete Account')}
          </>
        }
        description={t(
          'This action cannot be undone. This will permanently delete your account and remove all your data from our servers.'
        )}
        contentClassName='sm:max-w-md'
        titleClassName='text-destructive flex items-center gap-2'
        contentHeight='auto'
        bodyClassName='space-y-4'
        footer={
          <>
            <Button
              type='button'
              variant='outline'
              onClick={() => handleOpenChange(false)}
              disabled={loading}
            >
              {t('Cancel')}
            </Button>
            <Button
              type='button'
              variant='destructive'
              onClick={handleDelete}
              disabled={loading || confirmation !== username}
            >
              {loading && <Loader2 className='mr-2 h-4 w-4 animate-spin' />}
              {loading ? t('Deleting...') : t('Delete Account')}
            </Button>
          </>
        }
      >
        <div className='my-6 space-y-4'>
          <Alert variant='destructive'>
            <AlertTriangle className='h-4 w-4' />
            <AlertDescription>
              {t('Warning: This action is permanent and irreversible!')}
            </AlertDescription>
          </Alert>

          <div className='space-y-2'>
            <Label htmlFor='confirmation'>
              {t('Type')} <strong>{username}</strong> {t('to confirm')}
            </Label>
            <Input
              id='confirmation'
              type='text'
              value={confirmation}
              onChange={(e) => setConfirmation(e.target.value)}
              disabled={loading}
              placeholder={username}
              autoComplete='off'
            />
          </div>
        </div>
      </Dialog>
      <SecureVerificationDialog
        open={verificationOpen}
        onOpenChange={cancelVerification}
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
