import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { KeyRound, Puzzle, ShieldCheck, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { useApi } from '@/api/client'
import {
  Badge,
  Banner,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  SectionHead,
} from '@/components/ui'

export const Route = createFileRoute('/settings/integrations')({
  component: IntegrationsPage,
})

/**
 * One registered OAuth application, as `GET /oauth/applications` returns it.
 *
 * The page used to render a hardcoded pair of imaginary clients — "Haaku Web"
 * and "Haaku iOS" — because the endpoint it called only accepted POST and
 * answered 405. Whatever this page lists is now what the account actually
 * authorised.
 */
interface OAuthApplication {
  id: string
  name: string
  redirect_uris: string[]
  created_at: string
  scopes: string[]
  active_tokens: number
  last_issued_at?: string
  access_expires_at?: string
}

function IntegrationsPage() {
  const api = useApi()
  const queryClient = useQueryClient()
  const [revokeId, setRevokeId] = useState<string | null>(null)

  const { data: applications, isLoading } = useQuery({
    queryKey: ['oauth', 'applications'],
    queryFn: async (): Promise<OAuthApplication[]> => {
      if (!api) return []
      const res = await api.GET('/oauth/applications' as never)
      const body = res.data as unknown as { items?: OAuthApplication[] }
      return body?.items ?? []
    },
    enabled: !!api,
  })

  const apps = applications ?? []

  const revokeMutation = useMutation({
    mutationFn: async (applicationID: string) => {
      if (!api) throw new Error('Not authenticated')
      // Deleting the application revokes every token issued to it, which is what
      // "revoke access" means for the reader: the app is gone, not just idle.
      await api.DELETE(
        '/oauth/applications/{id}' as never,
        {
          params: { path: { id: applicationID } },
        } as never
      )
    },
    onSuccess: () => {
      toast.success('Access revoked')
      queryClient.invalidateQueries({ queryKey: ['oauth', 'applications'] })
      setRevokeId(null)
    },
    onError: () => toast.error('Failed to revoke access'),
  })

  const app = apps.find((item) => item.id === revokeId)

  return (
    <div className="space-y-6">
      <SectionHead
        title="Integrations"
        eyebrow="Applications you have authorised"
      />

      <Banner type="info" id="integrations-info">
        Revoking access deletes the application and every token it was issued.
        It will need to be authorised again to reconnect.
      </Banner>

      {isLoading ? null : apps.length === 0 ? (
        <Card>
          <CardContent className="py-8 text-center">
            <Puzzle className="mx-auto size-8 text-sf-ink-hint" />
            <p className="mt-2 text-sm text-sf-ink-muted">
              No applications yet. Anything you authorise through the OAuth flow
              will appear here.
            </p>
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-4">
          {apps.map((application, appIndex) => (
            <Card key={`${application.id}:${appIndex}`}>
              <CardHeader>
                <div className="flex items-center justify-between gap-4">
                  <div className="flex items-center gap-3">
                    <div className="flex size-10 items-center justify-center rounded-full bg-sf-panel-2">
                      <KeyRound className="size-5 text-sf-ink-muted" />
                    </div>
                    <div>
                      <CardTitle className="text-base">
                        {application.name}
                      </CardTitle>
                      <CardDescription>
                        {application.active_tokens > 0
                          ? `Connected — ${application.active_tokens} active token${application.active_tokens === 1 ? '' : 's'}`
                          : 'No active token; it will need to reconnect'}
                      </CardDescription>
                    </div>
                  </div>
                  <Badge variant="secondary">
                    Added{' '}
                    {new Date(application.created_at).toLocaleDateString()}
                  </Badge>
                </div>
              </CardHeader>
              <CardContent className="space-y-3">
                <div className="flex flex-wrap gap-1.5">
                  {(application.scopes && application.scopes.length > 0
                    ? application.scopes
                    : ['no scopes granted']
                  ).map((scope, scopeIndex) => (
                    <Badge
                      key={`${application.id}:${scope}:${scopeIndex}`}
                      variant="secondary"
                    >
                      {scope}
                    </Badge>
                  ))}
                </div>
                {application.last_issued_at && (
                  <p className="flex items-center gap-1.5 text-xs text-sf-ink-hint">
                    <ShieldCheck className="size-3.5" />
                    Token issued{' '}
                    {new Date(application.last_issued_at).toLocaleString()}
                  </p>
                )}
              </CardContent>
              <CardFooter>
                <Button
                  variant="danger"
                  size="sm"
                  onClick={() => setRevokeId(application.id)}
                >
                  <Trash2 className="size-3" />
                  Revoke access
                </Button>
              </CardFooter>
            </Card>
          ))}
        </div>
      )}

      <Dialog
        open={!!revokeId}
        onOpenChange={(open) => {
          if (!open) setRevokeId(null)
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Revoke access</DialogTitle>
            <DialogDescription>
              {app
                ? `${app.name} will immediately lose access to your account, and its tokens are deleted.`
                : 'This application will immediately lose access to your account.'}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setRevokeId(null)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              size="sm"
              onClick={() => revokeId && revokeMutation.mutate(revokeId)}
              loading={revokeMutation.isPending}
            >
              <Trash2 className="size-4" />
              Revoke access
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
