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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Loader2, AlertCircle } from 'lucide-react'
import { useState, useEffect, useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { MultiSelect } from '@/components/multi-select'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Textarea } from '@/components/ui/textarea'
import {
  createEntityDialogSessionCoordinator,
  isEntityDialogOwnerCurrent,
  type EntityDialogRequest,
  type EntityDialogLiveOwner,
} from '@/lib/entity-dialog-session'

import { getTagModels, editTagChannels, getGroups } from '../../api'
import { channelsQueryKeys } from '../../lib'
import type { TagOperationParams } from '../../types'
import { useChannels } from '../channels-provider'
import { ModelMappingEditor } from '../model-mapping-editor'

type TagLoadState =
  | { status: 'idle'; request: null }
  | {
      status: 'loading' | 'ready' | 'error'
      request: EntityDialogRequest<string>
    }

type TagBatchEditDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function TagBatchEditDialog({
  open,
  onOpenChange,
}: TagBatchEditDialogProps) {
  const { t } = useTranslation()
  const { currentTag } = useChannels()
  const queryClient = useQueryClient()
  const [isSaving, setIsSaving] = useState(false)
  const [loadState, setLoadState] = useState<TagLoadState>({
    status: 'idle',
    request: null,
  })

  // Form fields
  const [newTag, setNewTag] = useState('')
  const [models, setModels] = useState('')
  const [modelMapping, setModelMapping] = useState('')
  const [groups, setGroups] = useState<string[]>([])
  const coordinatorRef = useRef<ReturnType<
    typeof createEntityDialogSessionCoordinator<string>
  > | null>(null)
  if (coordinatorRef.current === null) {
    coordinatorRef.current = createEntityDialogSessionCoordinator<string>()
  }
  const coordinator = coordinatorRef.current
  const translationRef = useRef(t)
  translationRef.current = t
  const liveOwnerRef = useRef<EntityDialogLiveOwner<string>>({
    open,
    entity: currentTag,
  })
  liveOwnerRef.current = { open, entity: currentTag }

  // Fetch available groups
  const { data: groupsData, isLoading: isLoadingGroups } = useQuery({
    queryKey: ['groups'],
    queryFn: getGroups,
  })

  // Transform groups to multi-select options
  const groupOptions = useMemo(() => {
    if (!groupsData?.data) return []
    const allGroups = new Set([...groupsData.data, ...groups])
    return [...allGroups].map((group) => ({
      value: group,
      label: group,
    }))
  }, [groupsData, groups])

  useEffect(() => {
    if (!open || !currentTag) {
      setLoadState({ status: 'idle', request: null })
      setNewTag('')
      setModels('')
      setModelMapping('')
      setGroups([])
      setIsSaving(false)
      return
    }

    const tag = currentTag
    const session = coordinator.open(tag)
    const request = coordinator.startRequest(session)

    setNewTag('')
    setModels('')
    setModelMapping('')
    setGroups([])
    setIsSaving(false)

    if (request === null) {
      setLoadState({ status: 'idle', request: null })
      return () => coordinator.close(session)
    }

    setLoadState({ status: 'loading', request })
    void getTagModels(tag, {
      signal: request.signal,
      disableDuplicate: true,
    })
      .then((response) => {
        if (
          !isEntityDialogOwnerCurrent(
            liveOwnerRef.current,
            request.session.entity
          ) ||
          !coordinator.isCurrentRequest(request)
        ) {
          return
        }

        if (response.success) {
          setModels(response.data ?? '')
          setNewTag(tag)
          setLoadState({ status: 'ready', request })
          return
        }

        toast.error(
          response.message || translationRef.current('Failed to load tag data')
        )
        setLoadState({ status: 'error', request })
      })
      .catch((error: unknown) => {
        if (
          !isEntityDialogOwnerCurrent(
            liveOwnerRef.current,
            request.session.entity
          ) ||
          !coordinator.isCurrentRequest(request)
        ) {
          return
        }

        toast.error(
          error instanceof Error
            ? error.message
            : translationRef.current('Failed to load tag data')
        )
        setLoadState({ status: 'error', request })
      })

    return () => coordinator.close(session)
    // The load owner is exactly the dialog open state and current tag.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, currentTag])

  const requestOwnsActiveDialog =
    open &&
    currentTag !== null &&
    loadState.request !== null &&
    isEntityDialogOwnerCurrent(
      liveOwnerRef.current,
      loadState.request.session.entity
    ) &&
    coordinator.isCurrentRequest(loadState.request) &&
    loadState.request.session.entity === currentTag
  const isLoading =
    open &&
    currentTag !== null &&
    (!requestOwnsActiveDialog || loadState.status === 'loading')
  const canSave = requestOwnsActiveDialog && loadState.status === 'ready'

  const handleSave = async () => {
    if (
      loadState.status !== 'ready' ||
      loadState.request === null ||
      !requestOwnsActiveDialog
    ) {
      return
    }

    const submissionSession = loadState.request.session
    const owner = submissionSession.entity
    const newTagAtSubmit = newTag
    const modelsAtSubmit = models
    const modelMappingAtSubmit = modelMapping
    const groupsAtSubmit = [...groups]

    // Validate model mapping JSON if provided
    if (modelMappingAtSubmit.trim()) {
      try {
        JSON.parse(modelMappingAtSubmit)
      } catch {
        toast.error(translationRef.current('Model mapping must be valid JSON'))
        return
      }
    }

    setIsSaving(true)
    try {
      const params: Record<string, string | undefined> = {
        tag: owner,
      }

      if (newTagAtSubmit !== owner) {
        params.new_tag = newTagAtSubmit || undefined
      }

      if (modelsAtSubmit.trim()) {
        params.models = modelsAtSubmit
      }

      if (modelMappingAtSubmit.trim()) {
        params.model_mapping = modelMappingAtSubmit
      }

      if (groupsAtSubmit.length > 0) {
        params.groups = groupsAtSubmit.join(',')
      }

      // Check if there are any changes
      if (Object.keys(params).length === 1) {
        toast.warning(translationRef.current('No changes made'))
        return
      }

      const response = await editTagChannels(
        params as unknown as TagOperationParams
      )
      if (
        !isEntityDialogOwnerCurrent(liveOwnerRef.current, owner) ||
        !coordinator.isCurrentSession(submissionSession, owner)
      ) {
        return
      }

      if (response.success) {
        toast.success(translationRef.current('Tag updated successfully'))
        void queryClient.invalidateQueries({
          queryKey: channelsQueryKeys.lists(),
        })
        coordinator.close(submissionSession)
        setLoadState({ status: 'idle', request: null })
        setNewTag('')
        setModels('')
        setModelMapping('')
        setGroups([])
        setIsSaving(false)
        onOpenChange(false)
      } else {
        toast.error(
          response.message || translationRef.current('Failed to update tag')
        )
      }
    } catch (error: unknown) {
      if (
        !isEntityDialogOwnerCurrent(liveOwnerRef.current, owner) ||
        !coordinator.isCurrentSession(submissionSession, owner)
      ) {
        return
      }

      toast.error(
        error instanceof Error
          ? error.message
          : translationRef.current('Failed to update tag')
      )
    } finally {
      if (
        isEntityDialogOwnerCurrent(liveOwnerRef.current, owner) &&
        coordinator.isCurrentSession(submissionSession, owner)
      ) {
        setIsSaving(false)
      }
    }
  }

  const handleClose = (nextOpen: boolean) => {
    if (nextOpen) {
      onOpenChange(true)
      return
    }

    const session = loadState.request?.session
    if (session) coordinator.close(session)
    setLoadState({ status: 'idle', request: null })
    setNewTag('')
    setModels('')
    setModelMapping('')
    setGroups([])
    setIsSaving(false)
    onOpenChange(false)
  }

  if (!currentTag) return null

  return (
    <Dialog
      open={open}
      onOpenChange={handleClose}
      title={t('Batch Edit by Tag')}
      description={
        <>
          {t('Edit all channels with tag:')}
          <strong>{currentTag}</strong>
        </>
      }
      contentClassName='max-w-2xl'
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        canSave ? (
          <>
            <Button
              variant='outline'
              onClick={() => handleClose(false)}
              disabled={isSaving}
            >
              {t('Cancel')}
            </Button>
            <Button onClick={handleSave} disabled={isSaving}>
              {isSaving ? (
                <Loader2 className='mr-2 h-4 w-4 animate-spin' />
              ) : null}
              {isSaving ? t('Saving...') : t('Save Changes')}
            </Button>
          </>
        ) : null
      }
    >
      {isLoading ? (
        <div className='flex items-center justify-center py-12'>
          <Loader2 className='text-muted-foreground h-8 w-8 animate-spin' />
        </div>
      ) : (
        <div className='space-y-4 py-4'>
          <Alert>
            <AlertCircle className='h-4 w-4' />
            <AlertDescription>
              {t(
                'All edits are overwrite operations. Leave fields empty to keep current values unchanged.'
              )}
            </AlertDescription>
          </Alert>

          {/* Tag Name */}
          <div className='space-y-2'>
            <Label htmlFor='new-tag'>{t('Tag Name')}</Label>
            <Input
              id='new-tag'
              placeholder={t('Enter new tag name (leave empty to disband tag)')}
              value={newTag}
              onChange={(e) => setNewTag(e.target.value)}
              disabled={isSaving}
            />
            <p className='text-muted-foreground text-xs'>
              {t('Leave empty to disband the tag')}
            </p>
          </div>

          {/* Models */}
          <div className='space-y-2'>
            <Label htmlFor='models'>{t('Models')}</Label>
            <Textarea
              id='models'
              placeholder={t(
                'Comma-separated model names (leave empty to keep current)'
              )}
              value={models}
              onChange={(e) => setModels(e.target.value)}
              disabled={isSaving}
              rows={3}
            />
            <p className='text-muted-foreground text-xs'>
              {t(
                'Current models for the longest channel in this tag. May not include all models from all channels.'
              )}
            </p>
          </div>

          {/* Model Mapping */}
          <div className='space-y-2'>
            <Label htmlFor='model-mapping'>{t('Model Mapping')}</Label>
            <ModelMappingEditor
              value={modelMapping}
              onChange={setModelMapping}
              disabled={isSaving}
            />
          </div>

          {/* Groups */}
          <div className='space-y-2'>
            <Label htmlFor='groups'>{t('Groups')}</Label>
            {isLoadingGroups ? (
              <Skeleton className='h-10 w-full' />
            ) : (
              <MultiSelect
                options={groupOptions}
                selected={groups}
                onChange={setGroups}
                placeholder={t('Select groups (leave empty to keep current)')}
              />
            )}
            <p className='text-muted-foreground text-xs'>
              {t('User groups that can access channels with this tag')}
            </p>
          </div>
        </div>
      )}
    </Dialog>
  )
}
