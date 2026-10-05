import type { MockModule, MockRegistry } from './registry'

// Owners add *.mock.ts files. Discovery requires no common handler edit.
const discovered = import.meta.glob<MockModule>('./features/*.mock.ts', { eager: true, import: 'default' })
export const modules = Object.values(discovered).sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0))
export function registerModules(registry: MockRegistry): void {
  for (const module of modules) registry.register(module)
}
