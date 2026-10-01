// Mirror of internal/api/capabilities.go. Queries never start services.
export type PreviewMode = 'auto' | 'subdomain' | 'path' | 'off'
export type PreviewDetection =
  | 'off'
  | 'not-required'
  | 'missing-host'
  | 'invalid-host'
  | 'ip-literal'
  | 'explicit'
  | 'localhost'
  | 'pending'
  | 'verified'
  | 'timeout'
  | 'lookup-failed'
  | 'address-mismatch'
  | 'owner-unavailable'
export interface PreviewCapability {
  configuredMode: PreviewMode
  effectiveMode: Exclude<PreviewMode, 'auto'>
  host: string
  port?: string
  detection: PreviewDetection
  checkedAt?: string
}
interface AppPrerequisites {
  available: boolean
  missing: string[]
  implementation?: string
  source?: 'configured' | 'path' | 'local-bin' | 'standalone'
}
export type AppCapability = AppPrerequisites &
  (
    | { enabled: false; state: 'disabled' }
    | { enabled: true; available: false; state: 'unavailable' }
    | { enabled: true; available: true; state: 'stopped' }
    | { enabled: true; state: 'starting' | 'running' | 'failed' }
  )
export interface Capabilities {
  previews: PreviewCapability
  code: AppCapability
  desktop: AppCapability
}
export interface CapabilityChange {
  feature: 'previews' | 'code' | 'desktop'
}
export function withLifecycle(
  capability: AppCapability,
  state: 'starting' | 'running' | 'failed' | 'stopped',
): AppCapability {
  if (!capability.enabled) return { ...capability, state: 'disabled' }
  if (state === 'stopped') {
    if (!capability.available) return { ...capability, available: false, state: 'unavailable' }
    return { ...capability, available: true, state: 'stopped' }
  }
  return { ...capability, state }
}
