import type { MockRequest } from './registry'
export type Req = MockRequest
export const body = <T>(req: Req) => (req.body ?? {}) as T
export const q = (req: Req, k: string) => req.url.searchParams.get(k)
export const qn = (req: Req, k: string, d: number) => {
  const v = Number(q(req, k))
  return Number.isFinite(v) && v > 0 ? v : d
}
export const now = () => new Date().toISOString()
