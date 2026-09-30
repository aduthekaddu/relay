// /dev/ui — the kitchen sink. Every UI-kit component with its states and
// variants, rendered in Carbon and Paper side by side (or one theme), and a
// 390 × 844 phone frame that loads any route. Used for visual QA.
import { useEffect, useMemo, useState } from 'preact/hooks'
import { AREAS } from '../../app/areas'
import { usePageChrome } from '../../app/chrome'
import { openPalette } from '../../command/state'
import { AGENTS } from '../../lib/agents'
import {
  AgentMark,
  Badge,
  Button,
  Card,
  Checkbox,
  CodeBlock,
  CompactBar,
  ConfirmButton,
  Dialog,
  DotMeter,
  DotText,
  EmptyState,
  Field,
  GLYPHS,
  Glyph,
  type GlyphName,
  Input,
  Kbd,
  List,
  ListRow,
  Markdown,
  MenuButton,
  Panel,
  PathBar,
  Progress,
  ProgressRing,
  QR,
  RelayMark,
  SEPARATOR,
  Segmented,
  Select,
  Sheet,
  Skeleton,
  Slider,
  Sparkline,
  Spinner,
  Splitter,
  type Status,
  StatusDot,
  Switch,
  Table,
  Tabs,
  Tag,
  TextArea,
  Tooltip,
  toast,
  useContextMenu,
  VirtualList,
} from '../../ui'
import './devui.css'

type Frame = 'both' | 'carbon' | 'paper'

const SAMPLE_MD = `### Plan

1. Extract \`sign()\` into **sign.go**
2. Add \`ReadOnly\` to \`Link\`
3. Table tests for expiry

> Links stay compatible.

\`\`\`go
func (l Link) Valid(t time.Time) bool { return t.Before(l.Expires) }
\`\`\``

const SAMPLE_GO = `package share

// Valid reports whether the link is unexpired at t.
func (l Link) Valid(t time.Time) bool {
\treturn !l.Expires.IsZero() && t.Before(l.Expires)
}`

function useWave(n = 40): number[] {
  const [v, setV] = useState(() => Array.from({ length: n }, (_, i) => 40 + Math.sin(i / 3) * 25))
  useEffect(() => {
    const t = window.setInterval(
      () =>
        setV((a) => [...a.slice(1), Math.max(2, Math.min(98, a[a.length - 1] + (Math.random() - 0.5) * 22))]),
      1000,
    )
    return () => window.clearInterval(t)
  }, [])
  return v
}

function Story({
  title,
  note,
  children,
}: {
  title: string
  note?: string
  children: preact.ComponentChildren
}) {
  return (
    <section class="story">
      <header class="story__head">
        <h3 class="story__title">{title}</h3>
        {note && <p class="story__note">{note}</p>}
      </header>
      <div class="story__body">{children}</div>
    </section>
  )
}

function Kit() {
  const wave = useWave()
  const [seg, setSeg] = useState<'list' | 'grid' | 'tree'>('list')
  const [sw, setSw] = useState(true)
  const [chk, setChk] = useState(true)
  const [sl, setSl] = useState(42)
  const [sel, setSel] = useState('claude')
  const [tab, setTab] = useState<'live' | 'history' | 'usage'>('live')
  const [dialog, setDialog] = useState(false)
  const [sheet, setSheet] = useState(false)
  const rows = useMemo(
    () => Array.from({ length: 2000 }, (_, i) => ({ id: i, name: `session-${String(i).padStart(4, '0')}` })),
    [],
  )
  const ctx = useContextMenu([
    { id: 'open', label: 'Open', icon: 'external-link' },
    { id: 'copy', label: 'Copy path', icon: 'copy', shortcut: ['mod', 'C'] },
    SEPARATOR,
    { id: 'del', label: 'Move to trash', icon: 'trash-2', danger: true },
  ])
  const statuses: Status[] = ['working', 'needs-you', 'idle', 'done', 'exited', 'failed', 'offline']

  return (
    <div class="kit">
      <Story title="Type" note="Mona Sans (wdth + wght) for UI, JetBrains Mono for the instrument layer.">
        <div class="stack">
          <p class="t-display">Your machine, relayed.</p>
          <p class="t-title">Page title · 26/30</p>
          <p class="t-section">Section · 15/20</p>
          <p>Body 14/20 — Plain words. Technical detail goes in a quieter mono layer underneath.</p>
          <p class="t-small">Small 13/18 — meta and secondary lines.</p>
          <p class="t-mono">instrument · 11.5/16 · ~/code/relay-demo · 12:04:31 · pid 48210</p>
          <p class="t-meter tnum">42.7%</p>
        </div>
      </Story>

      <Story title="Colour" note="Signal orange means act now. Area hues identify, they never decorate.">
        <div class="swatches">
          {[
            'bg',
            'bg-sunken',
            'surface-1',
            'surface-2',
            'surface-3',
            'surface-4',
            'text',
            'text-2',
            'text-3',
            'text-4',
            'signal',
            'ok',
            'warn',
            'danger',
            'info',
          ].map((t) => (
            <div class="swatch" key={t}>
              <span class="swatch__chip" style={{ background: `var(--${t})` }} />
              <code>--{t}</code>
            </div>
          ))}
        </div>
        <div class="swatches">
          {AREAS.map((a) => (
            <div class="swatch" key={a.id}>
              <span class="swatch__chip swatch__chip--glyph">
                <Glyph name={a.id} size={20} color={a.hue} />
              </span>
              <code>{a.id}</code>
            </div>
          ))}
        </div>
      </Story>

      <Story title="Glyph" note="7×7 dot bitmaps: idle · active (cascade) · pulse.">
        <div class="glyph-grid">
          {(Object.keys(GLYPHS) as GlyphName[]).map((g) => (
            <div class="glyph-cell" key={g}>
              <Glyph name={g} size={28} />
              <Glyph name={g} size={28} state="active" color="var(--signal-ink)" />
              <Glyph name={g} size={28} state="pulse" color="var(--ok)" />
              <code>{g}</code>
            </div>
          ))}
        </div>
      </Story>

      <Story title="DotText + RelayMark">
        <div class="stack">
          <DotText text="RELAY" pitch={7} reveal />
          <DotText text="NEEDS YOU 3" pitch={4} color="var(--signal)" />
          <DotText text="0123456789 :.-/!?" pitch={3} />
          <div class="row" style={{ '--gap': 'var(--s-4)' }}>
            <RelayMark size={32} />
            <RelayMark size={32} beacon />
          </div>
        </div>
      </Story>

      <Story title="StatusDot" note="Never colour alone: the words are always available.">
        <div class="row wrap" style={{ '--gap': 'var(--s-5)' }}>
          {statuses.map((s) => (
            <StatusDot key={s} status={s} label />
          ))}
        </div>
      </Story>

      <Story title="AgentMark">
        <div class="row wrap" style={{ '--gap': 'var(--s-3)' }}>
          {AGENTS.map((a) => (
            <AgentMark key={a.id} agent={a.id} />
          ))}
        </div>
        <div class="row wrap" style={{ '--gap': 'var(--s-4)' }}>
          <AgentMark agent="claude" size="sm" withName />
          <AgentMark agent="codex" withName />
          <AgentMark agent="gemini" size="lg" withName />
          <AgentMark agent="some-new-agent" withName />
        </div>
      </Story>

      <Story title="Button">
        <div class="row wrap">
          <Button variant="primary">Resume</Button>
          <Button>Secondary</Button>
          <Button variant="ghost">Ghost</Button>
          <Button variant="danger" icon="trash-2">
            Delete
          </Button>
          <Button variant="icon" icon="plus" label="New terminal" />
          <Button loading>Saving</Button>
          <Button disabled>Disabled</Button>
        </div>
        <div class="row wrap">
          <Button size="sm" icon="terminal">
            Small
          </Button>
          <Button size="md" icon="terminal">
            Medium
          </Button>
          <Button size="lg" icon="terminal" iconEnd="arrow-right">
            Large
          </Button>
          <ConfirmButton onConfirm={() => void toast('Killed', { kind: 'danger' })} icon="x">
            Kill session
          </ConfirmButton>
          <MenuButton
            icon="ellipsis"
            variant="icon"
            label="More"
            menuLabel="Session"
            items={[
              { id: 'r', label: 'Rename', icon: 'file' },
              { id: 'p', label: 'Pin', checked: true },
              SEPARATOR,
              { id: 'k', label: 'Kill', danger: true, icon: 'x' },
            ]}
          />
        </div>
      </Story>

      <Story title="Form">
        <div class="form-grid">
          <Field label="Name" hint="Shown in the tab strip.">
            <Input placeholder="dev server" />
          </Field>
          <Field label="Search" hideLabel>
            <Input icon="search" placeholder="Search files…" />
          </Field>
          <Field label="Branch" error="That branch already exists.">
            <Input value="feat/share" mono />
          </Field>
          <Field label="Agent">
            <Select
              value={sel}
              onChange={setSel}
              options={AGENTS.slice(0, 6).map((a) => ({ value: a.id, label: a.name }))}
            />
          </Field>
          <Field label="Prompt">
            <TextArea autoGrow placeholder="Describe the task…" />
          </Field>
          <div class="stack">
            <Switch checked={sw} onChange={setSw} label="Record agent sessions" />
            <Checkbox checked={chk} onChange={setChk} label="Keep me signed in" />
            <Checkbox checked={false} indeterminate onChange={() => {}} label="Some files selected" />
            <Segmented
              label="View"
              value={seg}
              onChange={setSeg}
              options={[
                { value: 'list', label: 'List' },
                { value: 'grid', label: 'Grid' },
                { value: 'tree', label: 'Tree' },
              ]}
            />
            <Slider label="Font size" value={sl} onChange={setSl} min={9} max={60} format={(v) => `${v}px`} />
          </div>
        </div>
      </Story>

      <Story title="Card · Panel · List">
        <div class="cards">
          <Card>
            <strong>Default card</strong>
            <p class="muted">Surfaces step up; hairlines separate.</p>
          </Card>
          <Card tone="signal">
            <strong>Needs you</strong>
            <p class="muted">Signal-toned for “act now”.</p>
          </Card>
          <Card tone="sunken">
            <strong>Sunken well</strong>
            <p class="muted">Terminal and code backgrounds.</p>
          </Card>
          <Card href="/dev/ui" tone="default">
            <strong>Link card</strong>
            <p class="muted">Whole card is the target.</p>
          </Card>
        </div>
        <Panel
          title="Running now"
          meta="3 sessions"
          actions={
            <Button size="sm" variant="ghost" icon="plus">
              New
            </Button>
          }
        >
          <List label="Sessions">
            <ListRow
              leading={<AgentMark agent="claude" />}
              title="Refactor share links"
              subtitle="Allow running go test?"
              meta="2m"
              trailing={<StatusDot status="needs-you" label />}
              href="/dev/ui"
            />
            <ListRow
              leading={<AgentMark agent="codex" />}
              title="Sliding-window rate limiter"
              subtitle="Editing app/limits.py"
              meta="now"
              trailing={<StatusDot status="working" label />}
              onClick={() => {}}
            />
            <div {...ctx.handlers}>
              <ListRow
                leading="terminal"
                title="dev server"
                subtitle="Right-click or long-press for the menu"
                meta="3h"
                trailing={<StatusDot status="idle" label />}
                selected
              />
            </div>
          </List>
        </Panel>
        {ctx.menu}
      </Story>

      <Story title="Table">
        <Table
          caption="Processes"
          rowKey={(r) => String(r.pid)}
          rows={[
            { pid: 48210, name: 'claude', cpu: 12.4, mem: '412 MB' },
            { pid: 48877, name: 'codex', cpu: 8.1, mem: '301 MB' },
            { pid: 47001, name: 'node', cpu: 3.9, mem: '188 MB' },
          ]}
          columns={[
            { key: 'name', header: 'Name', render: (r) => r.name },
            { key: 'pid', header: 'PID', render: (r) => r.pid, numeric: true, hideBelow: 'sm' },
            { key: 'cpu', header: 'CPU', render: (r) => `${r.cpu}%`, numeric: true },
            { key: 'mem', header: 'Memory', render: (r) => r.mem, numeric: true },
          ]}
        />
      </Story>

      <Story title="Badge · Tag · Kbd">
        <div class="row wrap">
          <Badge>neutral</Badge>
          <Badge tone="signal">needs you</Badge>
          <Badge tone="ok">running</Badge>
          <Badge tone="warn">stale</Badge>
          <Badge tone="danger">failed</Badge>
          <Badge tone="info">info</Badge>
          <Badge tone="signal" solid>
            3
          </Badge>
          <Tag>go</Tag>
          <Tag color="var(--area-code)" onRemove={() => {}}>
            typescript
          </Tag>
          <Kbd keys={['mod', 'K']} />
          <Kbd keys={['shift', 'enter']} />
        </div>
      </Story>

      <Story title="Tabs · PathBar">
        <Tabs
          label="Agents"
          value={tab}
          onChange={setTab}
          items={[
            { id: 'live', label: 'Live', count: 3 },
            { id: 'history', label: 'History', count: 128 },
            { id: 'usage', label: 'Usage' },
          ]}
        />
        <PathBar
          value="/home/dev/code/relay-demo/internal/share"
          home="/home/dev"
          hrefFor={() => '/dev/ui'}
        />
      </Story>

      <Story title="Meters" note="DotMeter for CPU/mem, Sparkline for history, Progress for tasks.">
        <div class="meters">
          <DotMeter
            label="CPU"
            value={wave[wave.length - 1] / 100}
            valueText={`${Math.round(wave[wave.length - 1])}%`}
          />
          <DotMeter label="Memory" value={0.78} valueText="25.1 / 32 GB" warn={0.7} />
          <DotMeter label="Disk" value={0.93} valueText="93%" danger={0.9} size="sm" />
          <Sparkline values={wave} width={220} height={44} fill dot label="CPU, last 40 s" />
          <Sparkline
            values={wave.map((v) => 100 - v)}
            width={220}
            height={44}
            color="var(--area-system)"
            label="Network"
          />
          <Progress label="Uploading photo.jpg" value={0.64} />
          <Progress label="Indexing" tone="signal" />
          <div class="row" style={{ '--gap': 'var(--s-4)' }}>
            <ProgressRing value={0.62} label="5-hour quota">
              <span class="tnum t-small">62%</span>
            </ProgressRing>
            <ProgressRing value={0.91} tone="danger" label="Weekly quota" size={56} />
            <Spinner />
            <Spinner size="lg" />
          </div>
        </div>
      </Story>

      <Story title="EmptyState · Skeleton">
        <div class="cards">
          <Card>
            <EmptyState
              glyph="terminal"
              hue="var(--area-terminal)"
              title="No terminals yet"
              body="Start a shell or an agent; it keeps running when you close the tab."
              action={
                <Button variant="primary" icon="plus">
                  New terminal
                </Button>
              }
              size="sm"
            />
          </Card>
          <Card>
            <div class="stack">
              <Skeleton width="60%" height={18} />
              <Skeleton lines={3} />
              <Skeleton width={120} height={32} radius="var(--r-sm)" />
            </div>
          </Card>
        </div>
      </Story>

      <Story
        title="Overlays"
        note="Dialog, Sheet (bottom on phones, side on desktop), Menu, Tooltip, Toast, command center."
      >
        <div class="row wrap">
          <Button onClick={() => setDialog(true)}>Dialog</Button>
          <Button onClick={() => setSheet(true)}>Sheet</Button>
          <Tooltip content="Copies the path">
            <Button icon="copy">Tooltip</Button>
          </Tooltip>
          <Button onClick={() => toast('Saved', { kind: 'success' })}>Toast</Button>
          <Button
            onClick={() =>
              toast('Claude Code needs you', {
                kind: 'attention',
                body: 'Allow running go test?',
                action: { label: 'Open', onClick: () => {} },
              })
            }
          >
            Attention toast
          </Button>
          <Button onClick={() => toast('Couldn’t reach the machine. Retrying…', { kind: 'warning' })}>
            Warning toast
          </Button>
          <Button variant="primary" icon="command" onClick={() => openPalette()}>
            Command center
          </Button>
        </div>
        <Dialog
          open={dialog}
          onClose={() => setDialog(false)}
          title="Discard changes?"
          description="3 files go back to their last commit. This can’t be undone."
          role="alertdialog"
          footer={
            <>
              <Button variant="ghost" onClick={() => setDialog(false)}>
                Cancel
              </Button>
              <Button variant="danger" onClick={() => setDialog(false)}>
                Discard
              </Button>
            </>
          }
        />
        <Sheet
          open={sheet}
          onClose={() => setSheet(false)}
          title="Session info"
          footer={<Button onClick={() => setSheet(false)}>Done</Button>}
        >
          <List label="Info">
            <ListRow title="Command" meta="claude" />
            <ListRow title="Directory" meta="~/code/relay-demo" />
            <ListRow title="Size" meta="120 × 34" />
          </List>
        </Sheet>
      </Story>

      <Story title="CompactBar" note="Immersive screens on phones.">
        <div class="phone-strip">
          <CompactBar
            back="/dev/ui"
            title="claude · share links"
            subtitle="~/code/relay-demo"
            status={<StatusDot status="needs-you" />}
            actions={<Button variant="icon" icon="ellipsis" label="Menu" size="sm" />}
          />
        </div>
      </Story>

      <Story title="CodeBlock · Markdown" note="Lazy: highlight.js and marked + DOMPurify load on first use.">
        <div class="cards">
          <CodeBlock code={SAMPLE_GO} lang="go" filename="internal/share/link.go" lineNumbers />
          <Card>
            <Markdown source={SAMPLE_MD} />
          </Card>
        </div>
      </Story>

      <Story title="QR · Splitter · VirtualList">
        <div class="cards">
          <Card>
            <QR value="https://5173.atlas.example.test/" label="Open the preview on your phone" />
          </Card>
          <div class="split-demo">
            <Splitter
              label="Resize panes"
              first={<div class="pane">Left</div>}
              second={<div class="pane">Right</div>}
            />
          </div>
          <div class="vlist-demo">
            <VirtualList
              label="2,000 sessions"
              items={rows}
              itemHeight={36}
              itemKey={(r) => r.id}
              render={(r) => <div class="vrow t-mono">{r.name}</div>}
            />
          </div>
        </div>
      </Story>
    </div>
  )
}

/** Kitchen sink route. ?frame=carbon|paper|both, ?phone=<path> shows a phone preview. */
export default function DevUIRoute() {
  const [frame, setFrame] = useState<Frame>(
    () => (new URLSearchParams(location.search).get('frame') as Frame) || 'both',
  )
  const [phone, setPhone] = useState(() => new URLSearchParams(location.search).get('phone') ?? '')
  usePageChrome({ title: 'UI kit', subtitle: '/dev/ui' }, [])
  const embedded = new URLSearchParams(location.search).has('embed')
  if (embedded) return <Kit />
  return (
    <div class="devui">
      <div class="devui-bar">
        <Segmented
          label="Theme frame"
          value={frame}
          onChange={setFrame}
          options={[
            { value: 'both', label: 'Both' },
            { value: 'carbon', label: 'Carbon' },
            { value: 'paper', label: 'Paper' },
          ]}
        />
        <Select
          value={phone}
          onChange={setPhone}
          options={[
            { value: '', label: 'No phone preview' },
            ...AREAS.map((a) => ({ value: a.path, label: `Phone: ${a.label}` })),
            { value: '/login', label: 'Phone: Sign in' },
          ]}
          size="sm"
        />
      </div>
      <div class={`devui-frames devui-frames--${frame}`}>
        {(frame === 'both' || frame === 'carbon') && (
          <div class="devui-frame" data-theme="carbon">
            <p class="devui-label">Carbon</p>
            <Kit />
          </div>
        )}
        {(frame === 'both' || frame === 'paper') && (
          <div class="devui-frame" data-theme="paper">
            <p class="devui-label">Paper</p>
            <Kit />
          </div>
        )}
      </div>
      {phone && (
        <aside class="devui-phone" aria-label="Phone preview">
          <iframe title="Phone preview" src={phone} width={390} height={844} />
        </aside>
      )}
    </div>
  )
}
