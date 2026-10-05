// Synthetic federated search, never a real provider query.
import type { SearchResult, SearchScope } from '../api/types'
import * as db from './data'
import * as fs from './fs'
import { processes } from './system'

function score(text: string, needle: string): number {
  const i = text.toLowerCase().indexOf(needle)
  if (i < 0) return 0
  return Math.max(0.3, 1 - i / 30 - (text.length - needle.length) / 200)
}

/** Naive federated search across the fixtures (pure over the store). */
export function searchAll(query: string, scopes: SearchScope[] | null, limit: number): SearchResult[] {
  const needle = query.trim().toLowerCase()
  if (!needle) return []
  const want = (s: SearchScope) => !scopes || scopes.includes(s)
  const out: SearchResult[] = []
  const push = (scope: SearchScope, list: SearchResult[]) => {
    if (want(scope))
      out.push(
        ...list
          .filter((x) => x.score > 0)
          .sort((a, b) => b.score - a.score)
          .slice(0, limit),
      )
  }
  push(
    'terminals',
    db.terminals
      .filter((t) => t.meta?.importable !== '1')
      .map((t) => ({
        scope: 'terminals',
        id: t.id,
        title: t.name,
        subtitle: t.preview,
        link: `/terminal/${t.id}`,
        score: score(`${t.name} ${t.command.join(' ')}`, needle),
        at: t.lastOutputAt,
        meta: { activity: t.attention ? 'waiting' : t.activity, ...(t.agent ? { agent: t.agent } : {}) },
      })),
  )
  push(
    'agents',
    db.agentSessions.map((s) => ({
      scope: 'agents',
      id: s.id,
      title: s.title,
      subtitle: `${s.agent} · ${s.cwd.replace(db.HOME, '~')}`,
      link: `/agents/s/${encodeURIComponent(s.id)}`,
      score: score(s.title, needle),
      at: s.updatedAt,
      meta: { agent: s.agent, ...(s.activity ? { activity: s.activity } : {}) },
    })),
  )
  push(
    'workspaces',
    db.workspaces.map((w) => ({
      scope: 'workspaces',
      id: w.path,
      title: w.name,
      subtitle: w.path.replace(db.HOME, '~'),
      link: `/workspace?path=${encodeURIComponent(w.path)}`,
      score: score(w.name, needle),
      meta: { path: w.path },
    })),
  )
  push(
    'files',
    fs.allPaths().map((p) => ({
      scope: 'files',
      id: p,
      title: p.slice(p.lastIndexOf('/') + 1),
      subtitle: p.replace(db.HOME, '~'),
      link: `/files${p.replace(db.HOME, '')}`,
      score: score(p.slice(p.lastIndexOf('/') + 1), needle) * 0.9,
      meta: { path: p },
    })),
  )
  push(
    'previews',
    db.previews
      .filter((p) => !p.hidden)
      .map((p) => ({
        scope: 'previews',
        id: String(p.port),
        title: p.label ?? p.title ?? `:${p.port}`,
        subtitle: `:${p.port} · ${p.process ?? ''}`,
        link: '/previews',
        score: score(`${p.label ?? ''} ${p.title ?? ''} ${p.port} ${p.process ?? ''}`, needle),
      })),
  )
  push(
    'processes',
    processes.map((p) => ({
      scope: 'processes',
      id: String(p.pid),
      title: p.name,
      subtitle: `pid ${p.pid} · ${p.cmd}`,
      link: `/system/processes?pid=${p.pid}`,
      score: score(p.name, needle) * 0.7,
    })),
  )
  push(
    'snippets',
    db.snippets.map((s) => ({
      scope: 'snippets',
      id: s.id,
      title: s.name,
      subtitle: s.body,
      score: score(`${s.name} ${s.body}`, needle) * 0.85,
    })),
  )
  push(
    'notes',
    db.notes.map((n) => ({
      scope: 'notes',
      id: n.id,
      title: n.title,
      subtitle: n.text.split('\n')[0],
      score: score(`${n.title} ${n.text}`, needle) * 0.8,
    })),
  )
  push(
    'scripts',
    db.scripts.map((s) => ({
      scope: 'scripts',
      id: s.id,
      title: s.title,
      subtitle: s.description,
      icon: s.icon,
      score: score(s.title, needle),
    })),
  )
  return out
}
