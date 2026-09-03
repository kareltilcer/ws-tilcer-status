import { useState } from 'react'
import { toast } from 'sonner'
import { STATUS_ORIGIN } from '@/lib/snippets'

type Tab = 'curl' | 'go' | 'js'

function snippets(siteId: string): Record<Tab, string> {
  const url = `${STATUS_ORIGIN}/api/ingest/${siteId}`
  return {
    curl: [
      `curl -sS -X POST ${url} \\`,
      `  -H "X-Ingest-Key: $STATUS_INGEST_KEY" \\`,
      `  -H "Content-Type: application/json" \\`,
      `  -d '{"message":"something broke","level":"error"}'`,
    ].join('\n'),
    go: [
      `// STATUS_INGEST_URL=${url}`,
      `// STATUS_INGEST_KEY=ik_...`,
      `sr, _ := statusreport.NewFromEnv()`,
      `defer sr.Recover()`,
      ``,
      `sr.Report(err, statusreport.WithContext(map[string]any{"route": r.URL.Path}))`,
    ].join('\n'),
    js: [
      `StatusReport.init({`,
      `  url: "${url}",`,
      `  key: "ik_your_public_browser_key",`,
      `  environment: "prod",`,
      `});`,
    ].join('\n'),
  }
}

const tabs: { key: Tab; label: string }[] = [
  { key: 'curl', label: 'curl' },
  { key: 'go', label: 'Go' },
  { key: 'js', label: 'JS' },
]

/** CodeSnippet shows a tabbed, copyable ingest snippet prefilled with a site id. */
export function CodeSnippet({ siteId }: { siteId: string }) {
  const [tab, setTab] = useState<Tab>('curl')
  const code = snippets(siteId)[tab]
  const copy = () => {
    void navigator.clipboard?.writeText(code).then(() => toast.success('Snippet copied'))
  }
  return (
    <div>
      <div style={{ display: 'flex', gap: 4, marginBottom: 10 }}>
        {tabs.map((t) => {
          const active = t.key === tab
          return (
            <button
              key={t.key}
              onClick={() => setTab(t.key)}
              style={{
                height: 28,
                padding: '0 12px',
                borderRadius: 7,
                border: `1px solid ${active ? 'var(--border-strong)' : 'var(--border)'}`,
                background: active ? 'var(--s3)' : 'var(--s2)',
                color: active ? 'var(--text)' : 'var(--muted)',
                fontSize: 12,
                fontWeight: 600,
                cursor: 'pointer',
              }}
            >
              {t.label}
            </button>
          )
        })}
      </div>
      <div style={{ position: 'relative' }}>
        <pre style={{ margin: 0, fontFamily: 'var(--mono)', fontSize: 11.5, lineHeight: 1.65, background: 'var(--s2)', border: '1px solid var(--border)', borderRadius: 8, padding: 13, overflow: 'auto', color: 'var(--text)', whiteSpace: 'pre' }}>{code}</pre>
        <button onClick={copy} title="Copy" style={{ position: 'absolute', top: 8, right: 8, display: 'grid', placeItems: 'center', height: 30, padding: '0 10px', border: '1px solid var(--border)', background: 'var(--s3)', borderRadius: 7, color: 'var(--muted)', cursor: 'pointer', fontSize: 12 }}>Copy</button>
      </div>
    </div>
  )
}
