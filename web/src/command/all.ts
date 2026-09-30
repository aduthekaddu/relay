// Eagerly registers every area's commands with the command center.

import { commands as agents } from '../routes/agents/commands'
import { commands as code } from '../routes/code/commands'
import { commands as desktop } from '../routes/desktop/commands'
import { commands as devui } from '../routes/devui/commands'
import { commands as files } from '../routes/files/commands'
import { commands as home } from '../routes/home/commands'
import { commands as login } from '../routes/login/commands'
import { commands as previews } from '../routes/previews/commands'
import { commands as settings } from '../routes/settings/commands'
import { commands as system } from '../routes/system/commands'
import { commands as terminal } from '../routes/terminal/commands'
import { commands as toolbox } from '../routes/toolbox/commands'
import { commands as workspace } from '../routes/workspace/commands'
import { commands as core, registerCoreProviders } from './core'
import { registerCommands } from './registry'

export function registerAll(): void {
  registerCommands([
    ...core,
    ...home,
    ...terminal,
    ...agents,
    ...files,
    ...code,
    ...desktop,
    ...previews,
    ...system,
    ...settings,
    ...workspace,
    ...toolbox,
    ...login,
    ...devui,
  ])
  registerCoreProviders()
}
