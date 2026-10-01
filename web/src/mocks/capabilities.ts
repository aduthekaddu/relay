import type { AppCapability, PreviewCapability } from '../api/capabilities'
import { withLifecycle } from '../api/capabilities'
import type { App, DesktopState, Info, Preview } from '../api/types'

export function setManagedState(
  info: Info,
  apps: App[],
  desktop: DesktopState,
  feature: 'code' | 'desktop',
  state: 'starting' | 'running' | 'failed' | 'stopped',
): AppCapability {
  const capability = withLifecycle(info.capabilities[feature], state)
  info.capabilities[feature] = capability
  info.features[feature] = capability.enabled && capability.available
  const app = apps.find((item) => item.id === feature)
  const legacy =
    capability.state === 'failed'
      ? 'error'
      : capability.state === 'disabled'
        ? 'unavailable'
        : capability.state
  if (app) {
    app.capability = capability
    app.installed = feature === 'desktop' ? capability.enabled && capability.available : capability.available
    app.state = legacy
    app.error = legacy === 'error' ? 'Code could not start or exited unexpectedly.' : undefined
  }
  if (feature === 'desktop') {
    desktop.capability = capability
    desktop.state = legacy === 'error' ? 'stopped' : legacy
    desktop.error = legacy === 'error' ? 'The desktop could not start or exited unexpectedly.' : undefined
    if (app) app.error = desktop.error
  }
  return capability
}

export function setPreviewCapability(info: Info, previews: Preview[], capability: PreviewCapability): void {
  info.capabilities.previews = capability
  info.features.previewsMode = capability.effectiveMode
  info.features.previewsHost = capability.effectiveMode === 'subdomain' ? capability.host : ''
  const origin = new URL(info.publicUrl)
  for (const preview of previews) {
    if (capability.effectiveMode === 'off') preview.url = ''
    else if (capability.effectiveMode === 'path') preview.url = `${origin.origin}/p/${preview.port}/`
    else {
      const port = capability.port ? `:${capability.port}` : ''
      preview.url = `${origin.protocol}//${preview.port}.${capability.host}${port}/`
    }
  }
}
