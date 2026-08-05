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
import { Loader2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  SecureVerificationDialog,
  useSecureVerification,
  type VerificationMethod,
} from '@/features/auth/secure-verification'
import { useCountdown } from '@/hooks/use-countdown'

import { sendEmailVerification, bindEmail } from '../../api'

// ============================================================================
// Email Bind Dialog Component
// ============================================================================

interface EmailBindDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  currentEmail?: string
  onSuccess: () => void
}

export function EmailBindDialog({
  open,
  onOpenChange,
  currentEmail,
  onSuccess,
}: EmailBindDialogProps) {
  const { t } = useTranslation()
  const [loading, setLoading] = useState(false)
  const [sendingCode, setSendingCode] = useState(false)
  const [email, setEmail] = useState('')
  const [code, setCode] = useState('')
  const {
    secondsLeft,
    isActive,
    start: startCountdown,
    reset: resetCountdown,
  } = useCountdown({
    initialSeconds: 60,
  })
  const {
    open: verificationOpen,
    setOpen: setVerificationOpen,
    methods: verificationMethods,
    state: verificationState,
    executeVerification,
    cancel: cancelVerification,
    setCode: setVerificationCode,
    switchMethod,
    withVerification,
  } = useSecureVerification({
    onSuccess: () => {
      toast.success(t('Email bound successfully!'))
      onOpenChange(false)
      onSuccess()
      setEmail('')
      setCode('')
      resetCountdown()
    },
  })

  const handleSendCode = async () => {
    if (!email || !email.includes('@')) {
      toast.error(t('Please enter a valid email address'))
      return
    }

    try {
      setSendingCode(true)
      const response = await sendEmailVerification(email)

      if (response.success) {
        toast.success(t('Verification code sent! Please check your email.'))
        startCountdown()
      } else {
        toast.error(response.message || t('Failed to send verification code'))
      }
    } catch {
      toast.error(t('Failed to send verification code'))
    } finally {
      setSendingCode(false)
    }
  }

  const handleBind = async () => {
    if (!email || !code) {
      toast.error(t('Please enter email and verification code'))
      return
    }

    try {
      setLoading(true)
      const normalizedEmail = email.trim().toLowerCase()
      await withVerification(
        async (proofToken?: string) => {
          const response = await bindEmail(normalizedEmail, code, proofToken)
          if (!response.success) {
            throw new Error(response.message || t('Failed to bind email'))
          }
          return response
        },
        {
          scope: 'email.change',
          target: normalizedEmail,
          title: t('Security verification'),
        }
      )
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Failed to bind email')
      )
    } finally {
      setLoading(false)
    }
  }

  const handleVerification = async (
    method: VerificationMethod,
    credential?: string
  ) => {
    try {
      await executeVerification(method, credential)
    } catch {
      // The verification hook displays the actionable error.
    }
  }

  const handleOpenChange = (open: boolean) => {
    if (!loading) {
      onOpenChange(open)
      if (!open) {
        // Reset form when closing
        setEmail('')
        setCode('')
        resetCountdown()
      }
    }
  }

  let sendButtonLabel = t('Send')
  if (isActive) {
    sendButtonLabel = `${secondsLeft}s`
  } else if (sendingCode) {
    sendButtonLabel = t('Sending...')
  }

  return (
    <>
      <Dialog
        open={open}
        onOpenChange={handleOpenChange}
        title={t('Bind Email')}
        description={
          currentEmail
            ? t('Current email: {{email}}. Enter a new email to change.', {
                email: currentEmail,
              })
            : t('Bind an email address to your account.')
        }
        contentClassName='sm:max-w-md'
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
              onClick={handleBind}
              disabled={loading || !email || !code}
            >
              {loading && <Loader2 className='mr-2 h-4 w-4 animate-spin' />}
              {loading ? t('Binding...') : t('Bind Email')}
            </Button>
          </>
        }
      >
        <div className='space-y-4 py-4'>
          <div className='space-y-2'>
            <Label htmlFor='email'>{t('Email Address')}</Label>
            <Input
              id='email'
              type='email'
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder={t('Enter your email')}
              autoComplete='email'
              disabled={loading}
            />
          </div>

          <div className='space-y-2'>
            <Label htmlFor='code'>{t('Verification Code')}</Label>
            <div className='flex gap-2'>
              <Input
                id='code'
                value={code}
                onChange={(e) => setCode(e.target.value)}
                placeholder={t('Enter code')}
                disabled={loading}
                autoComplete='one-time-code'
                autoCapitalize='none'
                autoCorrect='off'
                spellCheck={false}
              />
              <Button
                type='button'
                variant='outline'
                onClick={handleSendCode}
                disabled={sendingCode || isActive || !email}
              >
                {sendButtonLabel}
              </Button>
            </div>
          </div>
        </div>
      </Dialog>
      <SecureVerificationDialog
        open={verificationOpen}
        onOpenChange={(nextOpen) => {
          if (nextOpen) {
            setVerificationOpen(true)
          } else {
            cancelVerification()
          }
        }}
        methods={verificationMethods}
        state={verificationState}
        onVerify={handleVerification}
        onCancel={cancelVerification}
        onCodeChange={setVerificationCode}
        onMethodChange={switchMethod}
      />
    </>
  )
}
