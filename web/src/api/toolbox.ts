// Mirror of internal/api/toolbox.go. Install responses are TerminalSession;
// toolbox.job frames report the lifecycle of that visible terminal.
export interface ToolboxJob {
  tool: string
  terminalId: string
  state: 'running' | 'done' | 'failed'
  /** Absent while running, or when the terminal disappears before exit. */
  exitCode?: number
}
