/** Shared site data: copy that appears on several pages. */

export const REPO = 'https://github.com/aduthekaddu/relay'
export const INSTALL =
  'curl -fsSL https://raw.githubusercontent.com/aduthekaddu/relay/main/scripts/install.sh | bash'

export interface NavItem {
  href: string
  label: string
  area?: string
}

/** Primary navigation (paths are relative to the site base). */
export const NAV: NavItem[] = [
  { href: 'terminal/', label: 'Terminal', area: 'terminal' },
  { href: 'agents/', label: 'Agents', area: 'agents' },
  { href: 'command/', label: 'Command', area: 'system' },
  { href: 'desktop/', label: 'Desktop', area: 'desktop' },
  { href: 'security/', label: 'Security' },
  { href: 'docs/', label: 'Docs' },
]

/** Coding agents Relay knows (monogram, name, brand-ish tint). No logos. */
export const AGENTS: { id: string; mark: string; name: string; tint: string; hooks: string }[] = [
  { id: 'claude', mark: 'CL', name: 'Claude Code', tint: '#d97757', hooks: 'Notification + Stop hooks' },
  { id: 'codex', mark: 'CX', name: 'Codex', tint: '#9aa0a6', hooks: 'notify command' },
  { id: 'gemini', mark: 'GM', name: 'Gemini CLI', tint: '#6aa9ff', hooks: 'hooks' },
  { id: 'opencode', mark: 'OC', name: 'OpenCode', tint: '#f2c14e', hooks: 'plugin events' },
  { id: 'kiro', mark: 'KR', name: 'Kiro', tint: '#b69cff', hooks: 'hooks' },
  { id: 'cursor', mark: 'CU', name: 'Cursor Agent', tint: '#ede9e0', hooks: 'hooks' },
  { id: 'grok', mark: 'GK', name: 'Grok', tint: '#b3aea4', hooks: 'idle heuristic' },
  { id: 'pi', mark: 'PI', name: 'pi', tint: '#5fd89a', hooks: 'idle heuristic' },
  { id: 'hermes', mark: 'HM', name: 'Hermes', tint: '#ff79b0', hooks: 'idle heuristic' },
  { id: 'amp', mark: 'AM', name: 'Amp', tint: '#ff7c4a', hooks: 'idle heuristic' },
  { id: 'copilot', mark: 'CP', name: 'Copilot CLI', tint: '#57d4d1', hooks: 'idle heuristic' },
  { id: 'aider', mark: 'AI', name: 'Aider', tint: '#4fd18b', hooks: 'notify command' },
  { id: 'qwen', mark: 'QW', name: 'Qwen Code', tint: '#8f7cff', hooks: 'hooks' },
  { id: 'crush', mark: 'CR', name: 'Crush', tint: '#ff4d5e', hooks: 'idle heuristic' },
]

/** Rows for the departures board (agent, task, status). */
export const BOARD: {
  agent: string
  task: string
  where: string
  status: 'RUNNING' | 'NEEDS YOU' | 'IDLE' | 'DONE'
}[] = [
  { agent: 'CLAUDE', task: 'refactor-auth', where: 'api', status: 'RUNNING' },
  { agent: 'CODEX', task: 'flaky-test', where: 'web', status: 'NEEDS YOU' },
  { agent: 'GEMINI', task: 'docs-sweep', where: 'site', status: 'RUNNING' },
  { agent: 'OPENCODE', task: 'perf-budget', where: 'web', status: 'IDLE' },
  { agent: 'KIRO', task: 'spec-billing', where: 'api', status: 'DONE' },
  { agent: 'AIDER', task: 'lint-fixes', where: 'cli', status: 'RUNNING' },
]

/** Alternate statuses the board cycles through (index-aligned with BOARD). */
export const BOARD_NEXT: (typeof BOARD)[number]['status'][][] = [
  ['RUNNING', 'NEEDS YOU', 'RUNNING', 'DONE'],
  ['NEEDS YOU', 'RUNNING', 'RUNNING', 'DONE'],
  ['RUNNING', 'RUNNING', 'NEEDS YOU', 'RUNNING'],
  ['IDLE', 'RUNNING', 'RUNNING', 'NEEDS YOU'],
  ['DONE', 'DONE', 'IDLE', 'RUNNING'],
  ['RUNNING', 'DONE', 'IDLE', 'IDLE'],
]

/**
 * Budgets shown in the "Light" section. They are labelled as budgets on
 * the page, not measurements; replace them with numbers measured on the
 * release build once one exists (see docs/dev/SITE.md, section 10).
 */
export const READOUTS: { value: string; unit: string; label: string; note: string }[] = [
  { value: '1', unit: 'binary', label: 'Everything in one file', note: 'Go, web app embedded' },
  { value: '<40', unit: 'MB', label: 'Idle memory', note: 'budget, serve + ptyd' },
  { value: '<150', unit: 'ms', label: 'Cold start', note: 'budget, to first byte' },
  { value: '<100', unit: 'KB', label: 'App shell JS', note: 'budget, gzip' },
]
