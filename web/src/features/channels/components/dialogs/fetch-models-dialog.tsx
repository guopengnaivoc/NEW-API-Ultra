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
import { useQueryClient } from '@tanstack/react-query'
import { Loader2, Search, Info, ChevronDown } from 'lucide-react'
import { useState, useEffect, useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import {
  createEntityDialogSessionCoordinator,
  isEntityDialogOwnerCurrent,
  type EntityDialogLiveOwner,
  type EntityDialogRequest,
  type EntityDialogSession,
} from '@/lib/entity-dialog-session'

import { fetchUpstreamModels, updateChannel } from '../../api'
import {
  channelsQueryKeys,
  categorizeModelsWithRedirect,
  normalizeModelName,
  parseModelsString,
} from '../../lib'
import { useChannels } from '../channels-provider'

function normalizeModelNameList(models: readonly string[]): string[] {
  return [...new Set(models.map((m) => normalizeModelName(m)).filter(Boolean))]
}

function parseFetchedModelList(models: unknown): string[] | null {
  if (models === undefined || models === null) return []
  if (!Array.isArray(models)) return null
  if (!models.every((model: unknown) => typeof model === 'string')) return null
  return models
}

const EMPTY_MODEL_LIST: string[] = []

type CustomModelFetcher = (signal: AbortSignal) => Promise<string[]>
type FetchModelsOwner = number | CustomModelFetcher

type ModelLoadState = {
  status: 'idle' | 'loading' | 'ready' | 'error'
  request: EntityDialogRequest<FetchModelsOwner> | null
  fetchedModels: string[]
  selectedModels: string[]
}

type FetchModelsDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  onModelsSelected?: (models: string[]) => void
  redirectModels?: string[]
  redirectSourceModels?: string[]
  customFetcher?: CustomModelFetcher
  existingModelsOverride?: string[]
  channelName?: string | null
}

export function FetchModelsDialog({
  open,
  onOpenChange,
  onModelsSelected,
  redirectModels = [],
  redirectSourceModels = [],
  customFetcher,
  existingModelsOverride,
  channelName,
}: FetchModelsDialogProps) {
  const { t } = useTranslation()
  const { currentRow } = useChannels()
  const activeChannel = customFetcher ? null : currentRow
  const activeChannelId = activeChannel?.id ?? null
  const activeOwner: FetchModelsOwner | null = customFetcher ?? activeChannelId
  const queryClient = useQueryClient()
  const [isSaving, setIsSaving] = useState(false)
  const [loadState, setLoadState] = useState<ModelLoadState>({
    status: 'idle',
    request: null,
    fetchedModels: [],
    selectedModels: [],
  })
  const [searchKeyword, setSearchKeyword] = useState('')
  const coordinatorRef = useRef<ReturnType<
    typeof createEntityDialogSessionCoordinator<FetchModelsOwner>
  > | null>(null)
  if (coordinatorRef.current === null) {
    coordinatorRef.current =
      createEntityDialogSessionCoordinator<FetchModelsOwner>()
  }
  const coordinator = coordinatorRef.current
  const translationRef = useRef(t)
  translationRef.current = t
  const liveOwnerRef = useRef<EntityDialogLiveOwner<FetchModelsOwner>>({
    open,
    entity: activeOwner,
  })
  liveOwnerRef.current = { open, entity: activeOwner }

  // Parse existing models
  const existingModels = useMemo(
    () =>
      existingModelsOverride ?? parseModelsString(activeChannel?.models || ''),
    [existingModelsOverride, activeChannel?.models]
  )

  const loadModels = async (
    session: EntityDialogSession<FetchModelsOwner>,
    owner: FetchModelsOwner
  ) => {
    if (
      !isEntityDialogOwnerCurrent(liveOwnerRef.current, owner) ||
      !coordinator.isCurrentSession(session, owner)
    ) {
      return
    }

    const request = coordinator.startRequest(session)
    if (request === null) return

    const selectedModelsAtStart = [...existingModels]
    setLoadState({
      status: 'loading',
      request,
      fetchedModels: [],
      selectedModels: [],
    })

    try {
      if (typeof owner === 'function') {
        const result = await owner(request.signal)
        if (
          !isEntityDialogOwnerCurrent(
            liveOwnerRef.current,
            request.session.entity
          ) ||
          !coordinator.isCurrentRequest(request)
        ) {
          return
        }

        const fetchedModels = parseFetchedModelList(result)
        if (fetchedModels === null) {
          throw new Error(translationRef.current('Failed to fetch models'))
        }
        setLoadState({
          status: 'ready',
          request,
          fetchedModels,
          selectedModels: selectedModelsAtStart,
        })
        toast.success(
          translationRef.current('Fetched {{count}} models', {
            count: fetchedModels.length,
          })
        )
        return
      }

      const response = await fetchUpstreamModels(owner, {
        signal: request.signal,
        disableDuplicate: true,
      })
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
        const fetchedModels = parseFetchedModelList(response.data)
        if (fetchedModels === null) {
          throw new Error(translationRef.current('Failed to fetch models'))
        }
        setLoadState({
          status: 'ready',
          request,
          fetchedModels,
          selectedModels: selectedModelsAtStart,
        })
        toast.success(
          translationRef.current('Fetched {{count}} models', {
            count: fetchedModels.length,
          })
        )
        return
      }

      toast.error(
        response.message || translationRef.current('Failed to fetch models')
      )
      setLoadState({
        status: 'error',
        request,
        fetchedModels: [],
        selectedModels: [],
      })
    } catch (error: unknown) {
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
          : translationRef.current('Failed to fetch models')
      )
      setLoadState({
        status: 'error',
        request,
        fetchedModels: [],
        selectedModels: [],
      })
    }
  }

  useEffect(() => {
    if (!open || activeOwner === null) {
      setLoadState({
        status: 'idle',
        request: null,
        fetchedModels: [],
        selectedModels: [],
      })
      setIsSaving(false)
      setSearchKeyword('')
      return
    }

    const session = coordinator.open(activeOwner)
    setIsSaving(false)
    void loadModels(session, activeOwner)

    return () => coordinator.close(session)
    // The request owner is exactly the open state, channel ID, or custom
    // fetcher identity. Existing model arrays are snapshots, not owners.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, activeChannelId, customFetcher])

  const requestOwnsActiveDialog =
    open &&
    activeOwner !== null &&
    loadState.request !== null &&
    isEntityDialogOwnerCurrent(
      liveOwnerRef.current,
      loadState.request.session.entity
    ) &&
    coordinator.isCurrentRequest(loadState.request) &&
    Object.is(loadState.request.session.entity, activeOwner)
  const isFetching = requestOwnsActiveDialog && loadState.status === 'loading'
  const fetchedModels = requestOwnsActiveDialog
    ? loadState.fetchedModels
    : EMPTY_MODEL_LIST
  const selectedModels = requestOwnsActiveDialog
    ? loadState.selectedModels
    : EMPTY_MODEL_LIST

  // Categorize models with redirect models
  const modelCategories = useMemo(
    () => categorizeModelsWithRedirect(existingModels, redirectModels),
    [existingModels, redirectModels]
  )

  const { classificationSet, redirectOnlySet } = modelCategories

  const fetchedModelSet = useMemo(
    () => new Set(normalizeModelNameList(fetchedModels)),
    [fetchedModels]
  )

  // Source keys in model_mapping are aliases, not real upstream IDs, so we
  // must skip them when computing "removed upstream" entries to avoid false
  // positives.
  const redirectSourceKeysSet = useMemo(
    () => new Set(normalizeModelNameList(redirectSourceModels)),
    [redirectSourceModels]
  )

  const removedModels = useMemo(() => {
    const kw = searchKeyword.toLowerCase().trim()
    return normalizeModelNameList(selectedModels).filter((model) => {
      if (fetchedModelSet.has(model)) return false
      if (redirectSourceKeysSet.has(model)) return false
      if (!kw) return true
      return model.toLowerCase().includes(kw)
    })
  }, [fetchedModelSet, redirectSourceKeysSet, searchKeyword, selectedModels])

  const handleFetchModels = () => {
    if (
      activeOwner === null ||
      loadState.request === null ||
      !isEntityDialogOwnerCurrent(
        liveOwnerRef.current,
        loadState.request.session.entity
      ) ||
      !coordinator.isCurrentSession(loadState.request.session, activeOwner)
    ) {
      return
    }

    void loadModels(loadState.request.session, activeOwner)
  }

  const handleSave = async () => {
    if (
      loadState.status !== 'ready' ||
      loadState.request === null ||
      !requestOwnsActiveDialog
    ) {
      return
    }

    const readyRequest = loadState.request
    const submissionSession = readyRequest.session
    const owner = submissionSession.entity
    const models = [...loadState.selectedModels]

    if (typeof owner === 'function') {
      if (
        owner !== customFetcher ||
        !onModelsSelected ||
        !isEntityDialogOwnerCurrent(liveOwnerRef.current, owner) ||
        !coordinator.isCurrentSession(submissionSession, owner)
      ) {
        return
      }

      onModelsSelected(models)
      if (
        !isEntityDialogOwnerCurrent(liveOwnerRef.current, owner) ||
        !coordinator.isCurrentSession(submissionSession, owner)
      ) {
        return
      }
      toast.success(t('Models filled to form'))
      coordinator.close(submissionSession)
      setLoadState({
        status: 'idle',
        request: null,
        fetchedModels: [],
        selectedModels: [],
      })
      setIsSaving(false)
      setSearchKeyword('')
      onOpenChange(false)
      return
    }

    setIsSaving(true)
    try {
      const response = await updateChannel(owner, {
        models: models.join(','),
      })
      if (
        !isEntityDialogOwnerCurrent(liveOwnerRef.current, owner) ||
        !coordinator.isCurrentSession(submissionSession, owner)
      ) {
        return
      }

      if (response.success) {
        toast.success(t('Models updated successfully'))
        void queryClient.invalidateQueries({
          queryKey: channelsQueryKeys.lists(),
        })
        coordinator.close(submissionSession)
        setLoadState({
          status: 'idle',
          request: null,
          fetchedModels: [],
          selectedModels: [],
        })
        setIsSaving(false)
        setSearchKeyword('')
        onOpenChange(false)
      } else {
        toast.error(response.message || t('Failed to update models'))
      }
    } catch (error: unknown) {
      if (
        !isEntityDialogOwnerCurrent(liveOwnerRef.current, owner) ||
        !coordinator.isCurrentSession(submissionSession, owner)
      ) {
        return
      }

      toast.error(
        error instanceof Error ? error.message : t('Failed to update models')
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
    setLoadState({
      status: 'idle',
      request: null,
      fetchedModels: [],
      selectedModels: [],
    })
    setIsSaving(false)
    setSearchKeyword('')
    onOpenChange(false)
  }

  // Categorize models by common prefixes
  const categorizeModels = (models: string[]) => {
    const categories: Record<string, string[]> = {}

    models.forEach((model) => {
      let category = 'Other'

      // Determine category based on model name
      if (
        model.toLowerCase().includes('gpt') ||
        model.toLowerCase().includes('o1') ||
        model.toLowerCase().includes('o3')
      ) {
        category = 'OpenAI'
      } else if (model.toLowerCase().includes('claude')) {
        category = 'Anthropic'
      } else if (model.toLowerCase().includes('gemini')) {
        category = 'Gemini'
      } else if (model.toLowerCase().includes('qwen')) {
        category = 'Qwen'
      } else if (model.toLowerCase().includes('deepseek')) {
        category = 'DeepSeek'
      } else if (model.toLowerCase().includes('glm')) {
        category = 'Zhipu'
      } else if (model.toLowerCase().includes('llama')) {
        category = 'Meta'
      } else if (model.toLowerCase().includes('mistral')) {
        category = 'Mistral'
      }

      if (!categories[category]) {
        categories[category] = []
      }
      categories[category].push(model)
    })

    return categories
  }

  // Filter models by search
  const filteredModels = useMemo(() => {
    if (!searchKeyword) return fetchedModels
    return fetchedModels.filter((model) =>
      model.toLowerCase().includes(searchKeyword.toLowerCase())
    )
  }, [fetchedModels, searchKeyword])

  // Helper to check if a model is considered "existing" (in selected or redirect)
  const isExistingModel = (model: string) =>
    classificationSet.has(normalizeModelName(model))

  // Separate new and existing models
  const newModels = filteredModels.filter((m) => !isExistingModel(m))
  const existingFilteredModels = filteredModels.filter((m) =>
    isExistingModel(m)
  )

  const newModelsByCategory = categorizeModels(newModels)
  const existingModelsByCategory = categorizeModels(existingFilteredModels)

  // 厂商分类按 a-z 排序，Other 放最后，便于查找
  const getSortedCategoryEntries = (
    categories: Record<string, string[]>
  ): [string, string[]][] =>
    Object.entries(categories).sort(([a], [b]) => {
      if (a === 'Other') return 1
      if (b === 'Other') return -1
      return a.localeCompare(b, undefined, { sensitivity: 'base' })
    })

  const toggleModel = (model: string) => {
    setLoadState((current) => {
      if (
        current.status !== 'ready' ||
        current.request === null ||
        activeOwner === null ||
        !isEntityDialogOwnerCurrent(
          liveOwnerRef.current,
          current.request.session.entity
        ) ||
        !coordinator.isCurrentRequest(current.request) ||
        !Object.is(current.request.session.entity, activeOwner)
      ) {
        return current
      }

      const selectedModels = current.selectedModels.includes(model)
        ? current.selectedModels.filter((item) => item !== model)
        : [...current.selectedModels, model]
      return { ...current, selectedModels }
    })
  }

  const toggleCategory = (categoryModels: string[], isChecked: boolean) => {
    setLoadState((current) => {
      if (
        current.status !== 'ready' ||
        current.request === null ||
        activeOwner === null ||
        !isEntityDialogOwnerCurrent(
          liveOwnerRef.current,
          current.request.session.entity
        ) ||
        !coordinator.isCurrentRequest(current.request) ||
        !Object.is(current.request.session.entity, activeOwner)
      ) {
        return current
      }

      if (isChecked) {
        const selectedModels = [...current.selectedModels]
        categoryModels.forEach((model) => {
          if (!selectedModels.includes(model)) {
            selectedModels.push(model)
          }
        })
        return { ...current, selectedModels }
      }

      return {
        ...current,
        selectedModels: current.selectedModels.filter(
          (model) => !categoryModels.includes(model)
        ),
      }
    })
  }

  const isCategorySelected = (categoryModels: string[]) => {
    return categoryModels.every((m) => selectedModels.includes(m))
  }

  const renderModelCategory = (
    categoryName: string,
    categoryModels: string[]
  ) => {
    const allSelected = isCategorySelected(categoryModels)

    return (
      <Collapsible key={categoryName} defaultOpen>
        <CollapsibleTrigger className='hover:bg-muted/50 flex w-full items-center justify-between rounded-lg border p-3'>
          <div className='flex items-center gap-2'>
            <ChevronDown className='h-4 w-4' />
            <span className='font-medium'>
              {categoryName} ({categoryModels.length})
            </span>
          </div>
          <div className='flex items-center gap-2'>
            <span className='text-muted-foreground text-sm'>
              {categoryModels.filter((m) => selectedModels.includes(m)).length}{' '}
              / {categoryModels.length} selected
            </span>
            <Checkbox
              checked={allSelected}
              onCheckedChange={(checked) =>
                toggleCategory(categoryModels, !!checked)
              }
              onClick={(e) => e.stopPropagation()}
            />
          </div>
        </CollapsibleTrigger>
        <CollapsibleContent className='px-4 py-2'>
          <div className='grid grid-cols-2 gap-2'>
            {categoryModels.map((model) => (
              <div key={model} className='flex items-center space-x-2'>
                <Checkbox
                  id={model}
                  checked={selectedModels.includes(model)}
                  onCheckedChange={() => toggleModel(model)}
                />
                <Label
                  htmlFor={model}
                  className='flex cursor-pointer items-center gap-1.5 text-sm font-normal'
                >
                  <span>{model}</span>
                  {redirectOnlySet.has(normalizeModelName(model)) && (
                    <Tooltip>
                      <TooltipTrigger
                        render={<Info className='h-3.5 w-3.5 text-amber-500' />}
                      />
                      <TooltipContent>
                        {t('From model redirect, not yet added to models list')}
                      </TooltipContent>
                    </Tooltip>
                  )}
                </Label>
              </div>
            ))}
          </div>
        </CollapsibleContent>
      </Collapsible>
    )
  }

  const showFooterActions =
    requestOwnsActiveDialog &&
    loadState.status === 'ready' &&
    (fetchedModels.length > 0 || removedModels.length > 0)
  const displayedChannelName = activeChannel?.name || channelName
  const hasNoModels = fetchedModels.length === 0 && removedModels.length === 0
  let defaultTab: 'new' | 'removed' | 'existing' = 'existing'
  if (newModels.length > 0) {
    defaultTab = 'new'
  } else if (removedModels.length > 0) {
    defaultTab = 'removed'
  }

  return (
    <Dialog
      open={open}
      onOpenChange={handleClose}
      title={t('Fetch Models')}
      description={
        displayedChannelName ? (
          <>
            {t('Channel:')} <strong>{displayedChannelName}</strong>
          </>
        ) : (
          t('Fetch available models from upstream')
        )
      }
      contentClassName='max-w-3xl'
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        showFooterActions ? (
          <>
            <Button
              variant='outline'
              onClick={() => handleClose(false)}
              disabled={isSaving}
            >
              {t('Cancel')}
            </Button>
            <Button onClick={handleSave} disabled={isSaving}>
              {isSaving && <Loader2 className='mr-2 h-4 w-4 animate-spin' />}
              {isSaving ? t('Saving...') : t('Save Models')}
            </Button>
          </>
        ) : null
      }
    >
      {activeOwner === null && (
        <div className='text-muted-foreground py-8 text-center'>
          {t('No channel selected')}
        </div>
      )}
      {activeOwner !== null && isFetching && (
        <div className='flex items-center justify-center py-12'>
          <Loader2 className='text-muted-foreground h-8 w-8 animate-spin' />
        </div>
      )}
      {activeOwner !== null && !isFetching && hasNoModels && (
        <div className='text-muted-foreground py-8 text-center'>
          <p>{t('No models fetched yet.')}</p>
          <Button
            className='mt-4'
            onClick={handleFetchModels}
            disabled={isFetching}
          >
            {t('Fetch Models')}
          </Button>
        </div>
      )}
      {activeOwner !== null && !isFetching && !hasNoModels && (
        <div className='space-y-4'>
          {/* Search Bar */}
          <div className='relative'>
            <Search className='text-muted-foreground absolute top-1/2 left-3 h-4 w-4 -translate-y-1/2' />
            <Input
              placeholder={t('Search models...')}
              value={searchKeyword}
              onChange={(e) => setSearchKeyword(e.target.value)}
              className='pl-9'
            />
          </div>

          {/* Tabs for New vs Existing vs Removed */}
          <Tabs
            key={`${activeChannel?.id ?? 'custom'}-${fetchedModels.length}-${removedModels.length}`}
            defaultValue={defaultTab}
          >
            <TabsList
              className={`grid w-full ${removedModels.length > 0 ? 'grid-cols-3' : 'grid-cols-2'}`}
            >
              <TabsTrigger value='new' disabled={newModels.length === 0}>
                {t('New Models ({{count}})', { count: newModels.length })}
              </TabsTrigger>
              <TabsTrigger
                value='existing'
                disabled={existingFilteredModels.length === 0}
              >
                {t('Existing Models ({{count}})', {
                  count: existingFilteredModels.length,
                })}
              </TabsTrigger>
              {removedModels.length > 0 && (
                <TabsTrigger value='removed'>
                  {t('Removed Models ({{count}})', {
                    count: removedModels.length,
                  })}
                </TabsTrigger>
              )}
            </TabsList>

            <TabsContent
              value='new'
              className='max-h-96 space-y-2 overflow-y-auto'
            >
              {getSortedCategoryEntries(newModelsByCategory).map(
                ([category, models]) => renderModelCategory(category, models)
              )}
            </TabsContent>

            <TabsContent
              value='existing'
              className='max-h-96 space-y-2 overflow-y-auto'
            >
              {getSortedCategoryEntries(existingModelsByCategory).map(
                ([category, models]) => renderModelCategory(category, models)
              )}
            </TabsContent>

            {removedModels.length > 0 && (
              <TabsContent
                value='removed'
                className='max-h-96 space-y-2 overflow-y-auto'
              >
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'These models are still in your selection but were not returned by the upstream listing. Entries that are only model_mapping source aliases are omitted. Toggle to adjust before saving.'
                  )}
                </p>
                {renderModelCategory(t('Removed'), removedModels)}
              </TabsContent>
            )}
          </Tabs>

          {/* Selection Summary */}
          <div className='bg-muted/50 rounded-lg border p-3 text-sm'>
            {t('{{n}} model(s) selected', { n: selectedModels.length })}
          </div>
        </div>
      )}
    </Dialog>
  )
}
