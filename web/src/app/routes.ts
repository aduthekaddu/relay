// Route table. Screens are lazy-loaded per area; the shell stays small.
import type { ComponentType } from 'preact'
import { lazy } from 'preact-iso'
import type { AreaId } from './areas'

export interface RouteDef {
  path: string
  area: AreaId
  /** Full-bleed screens hide page chrome on mobile (terminal, desktop). */
  immersive?: boolean
  public?: boolean
  // biome-ignore lint/suspicious/noExplicitAny: route components take route props
  component: ComponentType<any>
}

const Home = lazy(() => import('../routes/home'))
const Terminal = lazy(() => import('../routes/terminal'))
const Agents = lazy(() => import('../routes/agents'))
const Files = lazy(() => import('../routes/files'))
const Code = lazy(() => import('../routes/code'))
const Desktop = lazy(() => import('../routes/desktop'))
const Previews = lazy(() => import('../routes/previews'))
const System = lazy(() => import('../routes/system'))
const Settings = lazy(() => import('../routes/settings'))
const Login = lazy(() => import('../routes/login'))
const Workspace = lazy(() => import('../routes/workspace'))
const Toolbox = lazy(() => import('../routes/toolbox'))
const DevUI = lazy(() => import('../routes/devui'))

// Each area component reads sub-paths itself via useRoute()/useLocation().
export const ROUTES: RouteDef[] = [
  { path: '/', area: 'home', component: Home },
  { path: '/terminal', area: 'terminal', immersive: true, component: Terminal },
  { path: '/terminal/:id', area: 'terminal', immersive: true, component: Terminal },
  { path: '/agents', area: 'agents', component: Agents },
  { path: '/agents/:tab', area: 'agents', component: Agents },
  { path: '/agents/s/:id', area: 'agents', component: Agents },
  { path: '/files', area: 'files', component: Files },
  { path: '/files/*', area: 'files', component: Files },
  { path: '/code', area: 'code', immersive: true, component: Code },
  { path: '/desktop', area: 'desktop', immersive: true, component: Desktop },
  { path: '/previews', area: 'previews', component: Previews },
  { path: '/system', area: 'system', component: System },
  { path: '/system/:tab', area: 'system', component: System },
  { path: '/settings', area: 'settings', component: Settings },
  { path: '/settings/:section', area: 'settings', component: Settings },
  { path: '/workspace', area: 'home', component: Workspace },
  { path: '/toolbox', area: 'settings', component: Toolbox },
  { path: '/login', area: 'home', public: true, component: Login },
  { path: '/dev/ui', area: 'settings', component: DevUI },
]
