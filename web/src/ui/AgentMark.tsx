import { agentMeta } from '../lib/agents'
import { cx } from '../lib/util'
import './signature.css'

export interface AgentMarkProps {
  /** Agent id, e.g. "claude". Unknown ids get a neutral monogram. */
  agent: string
  /** 20 / 28 / 40 px. Default md (28). */
  size?: 'sm' | 'md' | 'lg'
  /** Override the tint (e.g. AgentInfo.color from the server). */
  color?: string
  /** Show the agent name as a visible label to the right. */
  withName?: boolean
  class?: string
}

/** Two-letter monogram on a rounded square tinted with the agent colour. */
export function AgentMark({ agent, size = 'md', color, withName = false, class: className }: AgentMarkProps) {
  const m = agentMeta(agent)
  const mark = (
    <span
      class={cx('agent-mark', `agent-mark--${size}`, !withName && className)}
      style={{ '--agent': color || m.color }}
      {...(withName ? { 'aria-hidden': true } : { role: 'img', 'aria-label': m.name })}
      title={withName ? undefined : m.name}
    >
      {m.mono}
    </span>
  )
  if (!withName) return mark
  return (
    <span class={cx('agent-mark-named', className)}>
      {mark}
      <span class="agent-mark-named__name">{m.name}</span>
    </span>
  )
}
