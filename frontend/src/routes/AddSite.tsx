import { useState, type FormEvent } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import * as api from '@/api/endpoints'
import { qk } from '@/api/keys'
import { paths } from '@/app/routes'
import { ApiError } from '@/api/client'
import { KeyModal } from '@/components/KeyModal'
import { Spinner, cardStyle, inputStyle, fieldLabel, primaryButton, ghostButton } from '@/components/ui'
import { curlIngestSnippet } from '@/lib/snippets'
import type { SiteWithKey } from '@/api/types'

const SLUG = /^[a-z0-9][a-z0-9-]{0,62}$/

export function AddSite() {
  const nav = useNavigate()
  const qc = useQueryClient()
  const [id, setId] = useState('')
  const [name, setName] = useState('')
  const [url, setUrl] = useState('')
  const [expected, setExpected] = useState('200')
  const [win, setWin] = useState('24')
  const [created, setCreated] = useState<SiteWithKey | null>(null)
  const [err, setErr] = useState<string | null>(null)

  const idOk = id === '' || SLUG.test(id)

  const m = useMutation({
    mutationFn: () =>
      api.createSite({
        id,
        name,
        monitor_url: url.trim() ? url.trim() : null,
        expected_status: Number(expected) || 200,
        crash_window_hours: Number(win) || 24,
      }),
    onSuccess: (s) => {
      setCreated(s)
      void qc.invalidateQueries({ queryKey: qk.sites() })
    },
    onError: (e) => {
      const ae = e as ApiError
      setErr(ae.status === 409 ? `A site with id "${id}" already exists. Pick another.` : ae.detail || 'Could not create the site.')
    },
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    setErr(null)
    if (!SLUG.test(id) || !name.trim()) {
      setErr('Enter a valid id and a display name.')
      return
    }
    m.mutate()
  }

  return (
    <div>
      <button onClick={() => nav(paths.board)} style={{ display: 'inline-flex', alignItems: 'center', gap: 5, background: 'none', border: 'none', color: 'var(--muted)', fontSize: 13, fontWeight: 600, cursor: 'pointer', padding: 0, marginBottom: 14, fontFamily: 'inherit' }}>‹ Board</button>
      <h1 style={{ margin: '0 0 4px', fontSize: 24, fontWeight: 800, letterSpacing: '-.01em' }}>Add a site</h1>
      <p style={{ margin: '0 0 22px', fontSize: 13.5, color: 'var(--muted)' }}>Pick an id you'll use everywhere — in monitoring, in crash reports, in the snippet. It can't change later.</p>

      <form onSubmit={submit} style={{ maxWidth: 560, ...cardStyle, padding: 22 }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          {err && (
            <div role="alert" style={{ border: '1px solid color-mix(in oklab, var(--danger) 45%, transparent)', background: 'color-mix(in oklab, var(--danger) 12%, transparent)', color: 'var(--danger-text)', borderRadius: 9, padding: '10px 12px', fontSize: 13 }}>{err}</div>
          )}
          <label style={{ display: 'block' }}>
            {fieldLabel('Site id')}
            <input value={id} onChange={(e) => setId(e.target.value)} placeholder="yarnlog" style={{ ...inputStyle, fontFamily: 'var(--mono)', border: `1px solid ${idOk ? 'var(--border)' : 'var(--danger)'}` }} />
            <span style={{ display: 'block', marginTop: 6, fontSize: 12, color: idOk ? 'var(--subtle)' : 'var(--danger-text)', fontFamily: 'var(--mono)' }}>^[a-z0-9][a-z0-9-]{'{0,62}'}$ · immutable after create</span>
          </label>
          <label style={{ display: 'block' }}>
            {fieldLabel('Display name')}
            <input value={name} onChange={(e) => setName(e.target.value)} placeholder="Yarnlog" style={inputStyle} />
          </label>
          <label style={{ display: 'block' }}>
            {fieldLabel('Monitor URL — optional, omit for a crash-only site')}
            <input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://yarnlog.tilcer.cz/readyz" style={{ ...inputStyle, fontFamily: 'var(--mono)' }} />
          </label>
          <div style={{ display: 'flex', gap: 12 }}>
            <label style={{ flex: 1 }}>
              {fieldLabel('Expected status')}
              <input value={expected} onChange={(e) => setExpected(e.target.value)} style={{ ...inputStyle, fontFamily: 'var(--mono)' }} />
            </label>
            <label style={{ flex: 1 }}>
              {fieldLabel('Crash window (h)')}
              <input value={win} onChange={(e) => setWin(e.target.value)} style={{ ...inputStyle, fontFamily: 'var(--mono)' }} />
            </label>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'flex-end', gap: 10, marginTop: 6 }}>
            <button type="button" onClick={() => nav(paths.board)} style={{ ...ghostButton, background: 'transparent', color: 'var(--muted)' }}>Cancel</button>
            <button type="submit" disabled={m.isPending} style={primaryButton}>
              {m.isPending && <Spinner size={15} />}
              Create site
            </button>
          </div>
        </div>
      </form>

      {created && (
        <KeyModal
          title="Site created"
          subtitle={`Ingest key for ${created.name} (${created.id})`}
          keyValue={created.ingest_key}
          snippet={curlIngestSnippet(created.id, created.ingest_key)}
          onClose={() => nav(paths.site(created.id))}
        />
      )}
    </div>
  )
}
