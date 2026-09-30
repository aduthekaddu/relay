// The coding agents Relay knows about. Ids match the server's adapter ids
// (AgentInfo.id). Colours are Relay's own tints chosen to be distinct from
// each other and from the signal orange — no third-party logos are used,
// only a two-letter monogram (see ui/AgentMark.tsx).

export interface AgentMeta {
  id: string
  name: string
  /** Two-letter monogram shown in AgentMark. */
  mono: string
  /** Brand tint (works on both themes when mixed by AgentMark). */
  color: string
  vendor: string
}

export const AGENTS: readonly AgentMeta[] = [
  { id: 'claude', name: 'Claude Code', mono: 'CL', color: '#d97757', vendor: 'Anthropic' },
  { id: 'codex', name: 'Codex', mono: 'CX', color: '#10a37f', vendor: 'OpenAI' },
  { id: 'gemini', name: 'Gemini CLI', mono: 'GM', color: '#4f8cff', vendor: 'Google' },
  { id: 'opencode', name: 'OpenCode', mono: 'OC', color: '#e0b84a', vendor: 'SST' },
  { id: 'kiro', name: 'Kiro', mono: 'KR', color: '#8b6cff', vendor: 'AWS' },
  { id: 'cursor', name: 'Cursor Agent', mono: 'CU', color: '#a9a39a', vendor: 'Anysphere' },
  { id: 'grok', name: 'Grok', mono: 'GK', color: '#c7ccd6', vendor: 'xAI' },
  { id: 'pi', name: 'pi', mono: 'PI', color: '#e0609b', vendor: 'pi' },
  { id: 'hermes', name: 'Hermes', mono: 'HM', color: '#c79a62', vendor: 'Nous Research' },
  { id: 'amp', name: 'Amp', mono: 'AM', color: '#f06a5b', vendor: 'Sourcegraph' },
  { id: 'copilot', name: 'Copilot CLI', mono: 'CP', color: '#9d7cf0', vendor: 'GitHub' },
  { id: 'aider', name: 'Aider', mono: 'AI', color: '#3fbf5f', vendor: 'Aider' },
  { id: 'qwen', name: 'Qwen Code', mono: 'QW', color: '#6f6cf5', vendor: 'Alibaba' },
  { id: 'crush', name: 'Crush', mono: 'CR', color: '#ff5fcf', vendor: 'Charm' },
]

const byId = new Map(AGENTS.map((a) => [a.id, a]))

/**
 * Metadata for an agent id. Unknown ids get a neutral mark built from the
 * first two letters so new server adapters render sensibly before the web
 * app learns about them.
 */
export function agentMeta(id: string | undefined): AgentMeta {
  const key = (id || '').toLowerCase()
  const known = byId.get(key)
  if (known) return known
  const letters = key.replace(/[^a-z0-9]/g, '')
  return {
    id: key || 'unknown',
    name: id || 'Agent',
    mono: (letters.slice(0, 2) || '??').toUpperCase(),
    color: '#9a958c',
    vendor: '',
  }
}

/** Display name for an agent id. */
export const agentName = (id: string | undefined): string => agentMeta(id).name
