// Minimal service worker — exists so the PWA is installable and the Web Share
// Target is registered. We intentionally do NOT cache API responses; the app
// is useless offline (printer is on the LAN) and stale job data is misleading.
const SHELL = ['/', '/index.html', '/manifest.json'];

self.addEventListener('install', (e) => {
  self.skipWaiting();
  e.waitUntil(caches.open('catprint-shell-v2').then(c => c.addAll(SHELL).catch(() => {})));
});

self.addEventListener('activate', (e) => {
  e.waitUntil((async () => {
    const keys = await caches.keys();
    await Promise.all(keys.filter(k => k !== 'catprint-shell-v2').map(k => caches.delete(k)));
    await self.clients.claim();
  })());
});

self.addEventListener('fetch', (e) => {
  const url = new URL(e.request.url);
  // Never cache API or share posts — always hit the network.
  if (e.request.method !== 'GET' ||
      url.pathname.startsWith('/print') ||
      url.pathname.startsWith('/jobs') ||
      url.pathname === '/status') {
    return; // default network handling
  }
  // Cache-first for the static shell, falling back to network.
  e.respondWith(
    caches.match(e.request).then(hit => hit || fetch(e.request))
  );
});
