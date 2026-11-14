import { Check, ListPlus, Loader2, Plus, Search } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useApi } from '@/api/client'
import {
  useAddToList,
  useCreateList,
  useListMembership,
  useMyLists,
  useRemoveFromList,
} from '@/api/hooks/useLists'
import type { Ids } from '@/api/types'
import type { AddListItemPayload, TraktList } from '@/api/types/lists'
import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  Input,
} from '@/components/ui'
import { listRowID, localRowID } from '@/lib/ids'
import { cn } from '@/lib/utils'

export type MediaKind = 'movie' | 'show'

/**
 * mediaListPayload wraps a media object in the list-items body the API takes.
 *
 * The ids travel back exactly as the API served them — `trakt` (our row id),
 * then tmdb/imdb — because that is what both item writers resolve against.
 */
export function mediaListPayload(
  kind: MediaKind,
  ids: Ids
): AddListItemPayload {
  return kind === 'movie' ? { movies: [{ ids }] } : { shows: [{ ids }] }
}

export interface AddToListDialogProps {
  kind: MediaKind
  ids: Ids | undefined
  /** Shown in the dialog header: "Add The Matrix to…" */
  title?: string
  open: boolean
  onOpenChange: (open: boolean) => void
}

/**
 * AddToListDialog is the "put this title somewhere" surface for a media page:
 * every list of the caller with a checkbox, a filter, and inline creation.
 *
 * The checkbox state is real membership (useListMembership reads the reverse
 * index), so unticking a list removes the title from it rather than merely
 * closing the dialog.
 */
export function AddToListDialog({
  kind,
  ids,
  title,
  open,
  onOpenChange,
}: AddToListDialogProps) {
  const api = useApi()
  const mediaID = localRowID(ids)

  const { data: lists, isLoading } = useMyLists(api)
  const {
    data: memberOf,
    isLoading: memberLoading,
    isError: memberError,
  } = useListMembership(api, kind, mediaID)
  const addToList = useAddToList(api)
  const removeFromList = useRemoveFromList(api)
  const createList = useCreateList(api)

  const [filter, setFilter] = useState('')
  const [newName, setNewName] = useState('')
  const [busyList, setBusyList] = useState<number | null>(null)

  const memberIDs = useMemo(
    () => new Set((memberOf ?? []).map(listRowID)),
    [memberOf]
  )

  const visible = useMemo(() => {
    const rows = lists ?? []
    const needle = filter.trim().toLowerCase()
    if (!needle) return rows
    return rows.filter((list) => list.name.toLowerCase().includes(needle))
  }, [lists, filter])

  if (!ids || mediaID <= 0) return null

  const payload = (list: TraktList) => ({
    id: listRowID(list),
    payload: mediaListPayload(kind, ids),
  })

  const toggle = (list: TraktList) => {
    const id = listRowID(list)
    if (id <= 0) return
    setBusyList(id)
    const done = () => setBusyList(null)
    if (memberIDs.has(id)) {
      removeFromList.mutate(payload(list), { onSettled: done })
    } else {
      addToList.mutate(payload(list), { onSettled: done })
    }
  }

  const createAndAdd = () => {
    // Enter in the input bypasses the button's disabled state, so the pending
    // check has to live here too or a double Enter creates the list twice.
    if (createList.isPending) return
    const name = newName.trim()
    if (!name) return
    createList.mutate(
      { name, privacy: 'private' },
      {
        onSuccess: (created) => {
          setNewName('')
          const id = listRowID(created)
          if (id > 0) {
            addToList.mutate({ id, payload: mediaListPayload(kind, ids) })
          }
        },
      }
    )
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>
            {title ? `Add ${title} to…` : 'Add to list'}
          </DialogTitle>
          <DialogDescription>
            Tick a list to add this title, untick it to remove it.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-3">
          <div className="relative">
            <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-sf-ink-faint" />
            <Input
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder="Filter lists…"
              className="pl-9"
            />
          </div>

          {memberLoading && (
            <p className="text-xs text-sf-ink-faint">
              Checking which lists already have this title…
            </p>
          )}
          {memberError && (
            <p className="text-xs text-red-300">
              Could not read your lists for this title — close and reopen to
              retry.
            </p>
          )}

          {isLoading ? (
            <div className="py-6 text-center text-sm text-sf-ink-muted">
              Loading your lists…
            </div>
          ) : visible.length === 0 ? (
            <div className="py-6 text-center text-sm text-sf-ink-muted">
              {lists && lists.length > 0
                ? 'No list matches that filter.'
                : 'No lists yet — create one below.'}
            </div>
          ) : (
            <div className="max-h-64 space-y-0.5 overflow-y-auto">
              {visible.map((list) => {
                const id = listRowID(list)
                const isMember = memberIDs.has(id)
                const isBusy = busyList === id
                // Until membership resolves every row would render unchecked,
                // including lists that already hold this title.
                const blocked = isBusy || memberLoading || memberError
                return (
                  <button
                    key={id}
                    type="button"
                    onClick={() => toggle(list)}
                    disabled={blocked}
                    aria-pressed={isMember}
                    className={cn(
                      'flex w-full items-center gap-3 rounded-xl px-3 py-2 text-left text-sm transition-colors',
                      isMember
                        ? 'bg-sf-accent/15 font-medium text-sf-ink'
                        : 'text-sf-ink-muted hover:bg-sf-panel-2 hover:text-sf-ink'
                    )}
                  >
                    <span
                      className={cn(
                        'flex size-4 shrink-0 items-center justify-center rounded-sm border transition-colors',
                        isMember
                          ? 'border-sf-accent bg-sf-accent text-white'
                          : 'border-sf-line'
                      )}
                    >
                      {isBusy ? (
                        <Loader2 className="size-3 animate-spin" />
                      ) : isMember ? (
                        <Check className="size-3" />
                      ) : null}
                    </span>
                    <span className="flex-1 truncate">{list.name}</span>
                    <span className="text-xs text-sf-ink-faint tabular-nums">
                      {list.item_count ?? 0}
                    </span>
                  </button>
                )
              })}
            </div>
          )}

          <div className="flex gap-2 border-t border-sf-line pt-3">
            <Input
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              placeholder="New list name…"
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault()
                  createAndAdd()
                }
              }}
            />
            <Button
              size="icon"
              variant="ghost"
              aria-label="Create list and add this title"
              onClick={createAndAdd}
              disabled={!newName.trim() || createList.isPending}
            >
              {createList.isPending ? (
                <Loader2 className="size-4 animate-spin" />
              ) : (
                <Plus className="size-4" />
              )}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}

export interface AddToListButtonProps {
  kind: MediaKind
  ids: Ids | undefined
  title?: string
}

/**
 * AddToListButton is the action-bar entry point: it owns the dialog's open
 * state so a page can drop one control in and get the whole flow.
 */
export function AddToListButton({ kind, ids, title }: AddToListButtonProps) {
  const [open, setOpen] = useState(false)
  if (!ids) return null
  return (
    <>
      <Button size="sm" variant="glass" onClick={() => setOpen(true)}>
        <ListPlus className="size-4" />
        Lists
      </Button>
      <AddToListDialog
        kind={kind}
        ids={ids}
        title={title}
        open={open}
        onOpenChange={setOpen}
      />
    </>
  )
}
