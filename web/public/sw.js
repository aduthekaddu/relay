/* Relay service worker.
 *
 * - Precaches the app shell (index, entry JS/CSS, fonts, icons) under a
 *   versioned cache; the build (vite.config.ts → relay-sw) fills in
 *   __RELAY_VERSION__ and __RELAY_SHELL__.
 * - Navigations: network first; offline → cached shell → offline page.
 * - /assets/* (hashed, immutable): cache first.
 * - Never caches /api, app proxies (/apps), previews (/p) or auth.
 * - Web Push: `push` shows a notification; clicking focuses an open Relay
 *   tab (and routes it) or opens a new one.
 *
 * Push payload (JSON): { id?, title, body?, link?, kind?, tag?, agent? }
 */
/* eslint-disable no-restricted-globals */
const VERSION = '__RELAY_VERSION__'
const SHELL = self.__RELAY_SHELL__ || ['/', '/offline.html', '/manifest.webmanifest', '/icons/icon.svg', '/boot.js']
const SHELL_CACHE = `relay-shell-${VERSION}`
const ASSET_CACHE = 'relay-assets-v1'
const ASSET_MAX = 120
const NEVER = [/^\/api\//, /^\/apps\//, /^\/p\//, /^\/_relay\//]

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches
      .open(SHELL_CACHE)
      .then((c) => c.addAll(SHELL.map((u) => new Request(u, { cache: 'reload', credentials: 'same-origin' }))))
      .then(() => self.skipWaiting()),
  )
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    (async () => {
      const keys = await caches.keys()
      await Promise.all(keys.filter((k) => k.startsWith('relay-shell-') && k !== SHELL_CACHE).map((k) => caches.delete(k)))
      if (self.registration.navigationPreload) await self.registration.navigationPreload.enable()
      await self.clients.claim()
    })(),
  )
})

async function trim(cache, max) {
  const keys = await cache.keys()
  for (let i = 0; i < keys.length - max; i++) await cache.delete(keys[i])
}

async function navigation(event) {
  try {
    const preload = await event.preloadResponse
    if (preload) return preload
    const res = await fetch(event.request)
    return res
  } catch {
    const shell = await caches.open(SHELL_CACHE)
    return (await shell.match('/')) || (await shell.match('/offline.html')) || new Response('Offline', { status: 503, headers: { 'Content-Type': 'text/plain' } })
  }
}

async function asset(request) {
  const cache = await caches.open(ASSET_CACHE)
  const hit = (await cache.match(request)) || (await caches.match(request))
  if (hit) return hit
  const res = await fetch(request)
  if (res.ok && res.type === 'basic') {
    await cache.put(request, res.clone())
    void trim(cache, ASSET_MAX)
  }
  return res
}

async function staleWhileRevalidate(event) {
  const cache = await caches.open(SHELL_CACHE)
  const hit = await cache.match(event.request)
  const refresh = fetch(event.request)
    .then((res) => {
      if (res.ok && res.type === 'basic') void cache.put(event.request, res.clone())
      return res
    })
    .catch(() => hit)
  if (hit) {
    event.waitUntil(refresh)
    return hit
  }
  return refresh
}

self.addEventListener('fetch', (event) => {
  const req = event.request
  if (req.method !== 'GET') return
  const url = new URL(req.url)
  if (url.origin !== self.location.origin) return
  if (NEVER.some((re) => re.test(url.pathname))) return
  if (req.mode === 'navigate') {
    event.respondWith(navigation(event))
    return
  }
  if (url.pathname.startsWith('/assets/')) {
    event.respondWith(asset(req))
    return
  }
  if (SHELL.includes(url.pathname)) event.respondWith(staleWhileRevalidate(event))
})

// ---------------------------------------------------------------- push

self.addEventListener('push', (event) => {
  let data = {}
  try {
    data = event.data ? event.data.json() : {}
  } catch {
    data = { title: 'Relay', body: event.data ? event.data.text() : '' }
  }
  const title = data.title || 'Relay'
  const attention = data.kind === 'attention'
  const options = {
    body: data.body || '',
    icon: '/icons/icon-192.png',
    badge: '/icons/badge-96.png',
    tag: data.tag || data.id || undefined,
    renotify: !!(data.tag || data.id),
    requireInteraction: attention,
    timestamp: Date.now(),
    data: { link: data.link || '/', id: data.id },
    actions: attention ? [{ action: 'open', title: 'Open' }, { action: 'dismiss', title: 'Later' }] : [{ action: 'open', title: 'View' }],
  }
  event.waitUntil(self.registration.showNotification(title, options))
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  if (event.action === 'dismiss') return
  const raw = (event.notification.data && event.notification.data.link) || '/'
  // Only ever open same-origin paths.
  // Backslashes are rejected: URL parsing treats a leading "/" + backslash as "//" (another host).
  let target = new URL('/', self.location.origin)
  try {
    const u = new URL(typeof raw === 'string' && raw.startsWith('/') && !/^\/[\\/]|\\/.test(raw) ? raw : '/', self.location.origin)
    if (u.origin === self.location.origin) target = u
  } catch {
    /* keep "/" */
  }
  event.waitUntil(
    (async () => {
      const wins = await self.clients.matchAll({ type: 'window', includeUncontrolled: true })
      const mine = wins.filter((w) => new URL(w.url).origin === self.location.origin)
      const best = mine.find((w) => w.focused) || mine[0]
      if (best) {
        await best.focus()
        best.postMessage({ type: 'relay:navigate', url: target.pathname + target.search + target.hash })
        return
      }
      await self.clients.openWindow(target.href)
    })(),
  )
})
