// Synthetic workspaces fixtures; owned registrations and scenario controls.
import * as db from '../data'
import * as fs from '../fs'
import { body, q, qn } from '../helpers'
import { defineMockModule } from '../registry'
import { emit } from '../sockets'
import { accepted, before, fail, HOUR, noContent, notFound, ok } from '../util'

export default defineMockModule('workspaces', (owner) => {
  const route = owner.http
  function gitStatus(path: string) {
    const w = db.workspaces.find((x) => path.startsWith(x.path)) ?? db.workspaces[0]
    const files =
      w.git && w.git.dirty > 0
        ? [
            { path: 'internal/share/link.go', index: ' ', work: 'M', staged: false, added: 12, removed: 3 },
            {
              path: 'internal/share/link_test.go',
              index: 'A',
              work: ' ',
              staged: true,
              added: 64,
              removed: 0,
            },
            { path: 'internal/share/sign.go', index: '?', work: '?', staged: false, added: 31, removed: 0 },
            { path: 'package.json', index: 'M', work: ' ', staged: true, added: 1, removed: 1 },
            {
              path: 'docs/diagram.png',
              index: ' ',
              work: 'M',
              staged: false,
              added: 0,
              removed: 0,
              binary: true,
            },
          ].slice(0, Math.max(1, Math.min(5, w.git.dirty)))
        : []
    return {
      path,
      root: w.path,
      branch: w.git?.branch ?? 'main',
      upstream: w.git?.remote ? `${w.git.remote}/${w.git.branch}` : undefined,
      ahead: w.git?.ahead ?? 0,
      behind: w.git?.behind ?? 0,
      files,
      last: w.git?.last,
      worktree: false,
      stashes: 1,
      remotes: ['origin'],
      worktrees: [
        { path: w.path, branch: w.git?.branch ?? 'main', head: w.git?.last?.hash ?? '', main: true },
      ],
    }
  }
  const DIFF = `diff --git a/internal/share/link.go b/internal/share/link.go
--- a/internal/share/link.go
+++ b/internal/share/link.go
@@ -8,6 +8,8 @@ import (
 // Link is a signed, expiring pointer to a session.
 type Link struct {
 \tID      string
 \tExpires time.Time
+\t// ReadOnly links can watch but never type.
+\tReadOnly bool
 }
@@ -20,7 +22,7 @@ func (l Link) Valid(t time.Time) bool {
-\treturn t.Before(l.Expires)
+\treturn !l.Expires.IsZero() && t.Before(l.Expires)
 }
`

  route('GET', '/workspaces', () => ok(owner.scenarios.state.empty ? [] : db.workspaces))
  route('POST', '/workspaces/pin', (r) => {
    const b = body<{ path: string; pinned: boolean }>(r)
    const w = db.workspaces.find((x) => x.path === fs.normalize(b.path))
    if (!w) return notFound('Workspace not found')
    w.pinned = b.pinned
    emit('workspace.changed', w)
    return ok(w)
  })
  route('GET', '/workspaces/git/status', (r) => ok(gitStatus(fs.normalize(q(r, 'path')))))
  route('GET', '/workspaces/git/diff', (r) =>
    ok({
      path: fs.normalize(q(r, 'path')),
      file: q(r, 'file') ?? undefined,
      staged: q(r, 'staged') === '1',
      diff: DIFF,
    }),
  )
  route('GET', '/workspaces/git/log', (r) =>
    ok(
      Array.from({ length: Math.min(30, qn(r, 'limit', 30)) }, (_, i) => ({
        hash: `${(0xabcdef12 + i * 7919).toString(16)}00`,
        short: (0xabcdef12 + i * 7919).toString(16).slice(0, 7),
        subject: [
          'Share a read-only session link',
          'Extract signing helpers',
          'Add link expiry tests',
          'Tidy imports',
          'Bump deps',
        ][i % 5],
        author: 'Dev',
        at: before(i * 5 * HOUR + 40 * 60),
      })),
    ),
  )
  for (const action of ['stage', 'unstage', 'discard', 'commit']) {
    route('POST', `/workspaces/git/${action}`, (r) =>
      ok(gitStatus(fs.normalize(body<{ path: string }>(r).path))),
    )
  }
  route('POST', '/workspaces/git/push', () => accepted())
  route('POST', '/workspaces/git/pull', () => accepted())
  route('POST', '/workspaces/git/worktrees', (r) => {
    const b = body<{ path?: string; branch?: string }>(r)
    if (typeof b.branch !== 'string' || !b.branch.trim())
      return fail(400, 'bad_request', 'Invalid branch name', { field: 'branch' })
    const branch = b.branch.trim()
    return ok({
      path: `${fs.normalize(b.path)}-${branch.replace(/\W+/g, '-')}`,
      branch,
      head: 'e4f1a9c2b7',
      main: false,
    })
  })
  route('DELETE', '/workspaces/git/worktrees', () => noContent())
})
