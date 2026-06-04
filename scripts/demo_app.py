#!/usr/bin/env python3
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import sys


class DemoHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/sw.js":
            self.send_response(200)
            self.send_header("Content-Type", "application/javascript")
            self.send_header("Service-Worker-Allowed", "/")
            self.end_headers()
            self.wfile.write(SERVICE_WORKER_JS.encode())
            return
        if self.path == "/api/ok":
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps({"ok": True}).encode())
            return
        if self.path.startswith("/api/fail"):
            self.send_response(503)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps({"ok": False}).encode())
            return
        self.send_response(200)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.end_headers()
        self.wfile.write(DEMO_HTML.encode())

    def log_message(self, format, *args):
        pass


DEMO_HTML = """<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>cdp-cli demo app</title>
  <style>
    body { font-family: sans-serif; margin: 32px; }
    main { max-width: 720px; }
    .card { border: 1px solid #ccd; border-radius: 12px; padding: 16px; }
    .agent-form { display: grid; gap: 8px; margin-top: 12px; }
    .checkbox-row { align-items: center; display: flex; gap: 8px; }
    #agent-input { max-width: 320px; padding: 8px; }
    #plan { max-width: 320px; padding: 8px; }
    #drag-target { display: inline-block; margin-top: 12px; padding: 8px 12px; background: #f0f4ff; border: 1px solid #99a; cursor: grab; }
    .covered-wrap { position: relative; display: inline-block; margin-left: 8px; }
    .cover-overlay { position: absolute; inset: 0; z-index: 2; background: rgba(255, 255, 255, 0); }
    .overflow { width: 160px; white-space: nowrap; overflow: hidden; }
    .hidden-fixture { display: none; }
  </style>
</head>
<body>
  <main data-ready="false">
    <h1>CDP CLI Demo Ready</h1>
    <article class="card">
      <h2>Agent-visible post</h2>
      <p>Stable text for snapshot, text extraction, and workflow checks.</p>
      <button id="action">Click target</button>
      <span class="covered-wrap">
        <button id="covered-action">Covered target</button>
        <span id="covered-overlay" class="cover-overlay" aria-hidden="true"></span>
      </span>
      <button id="disabled-action" disabled>Disabled target</button>
      <button id="dismiss" class="hidden-fixture">Dismiss</button>
      <input id="hidden-agent-input" class="hidden-fixture" value="hidden initial">
      <label class="agent-form">
        Agent input
        <input id="agent-input" value="initial" autocomplete="off">
      </label>
      <label class="agent-form">
        Read-only notes
        <textarea id="readonly-notes" readonly>locked</textarea>
      </label>
      <label class="agent-form">
        Plan
        <select id="plan" name="plan">
          <option value="free">Free</option>
          <option value="pro">Pro</option>
        </select>
      </label>
      <select id="hidden-plan" class="hidden-fixture">
        <option value="free">Free</option>
        <option value="pro">Pro</option>
      </select>
      <label class="agent-form checkbox-row" for="subscribe">
        <input id="subscribe" name="subscribe" type="checkbox">
        Subscribe to newsletter
      </label>
      <span class="covered-wrap">
        <input id="covered-checkbox" type="checkbox" aria-label="Covered checkbox">
        <span id="checkbox-cover" class="cover-overlay" aria-hidden="true"></span>
      </span>
      <input id="disabled-checkbox" type="checkbox" disabled aria-label="Disabled checkbox">
      <div id="drag-target" data-testid="drag-target" draggable="true">Drag target</div>
    </article>
    <p class="overflow">This sentence intentionally overflows its box for layout diagnostics.</p>
    <output id="status">booting</output>
  </main>
  <script>
    localStorage.setItem('feature', 'enabled');
    sessionStorage.setItem('nonce', 'demo-session');
    document.cookie = 'demo_session=abc; SameSite=Lax; path=/';
    const cacheReady = 'caches' in window
      ? caches.open('cdp-demo-cache')
          .then(cache => cache.put('/api/cached', new Response(JSON.stringify({cached: true, source: 'demo'}), {
            status: 200,
            headers: {'Content-Type': 'application/json'}
          })))
          .catch(error => console.warn('cache setup failed', error))
      : Promise.resolve();
    const serviceWorkerReady = 'serviceWorker' in navigator
      ? navigator.serviceWorker.register('/sw.js').catch(error => console.warn('service worker setup failed', error))
      : Promise.resolve();
    const indexedDBReady = 'indexedDB' in window
      ? new Promise((resolve, reject) => {
          const request = indexedDB.open('cdp-demo-db', 1);
          request.onupgradeneeded = () => {
            const db = request.result;
            if (!db.objectStoreNames.contains('settings')) {
              db.createObjectStore('settings');
            }
          };
          request.onerror = () => reject(request.error);
          request.onsuccess = () => {
            const db = request.result;
            const tx = db.transaction('settings', 'readwrite');
            tx.objectStore('settings').put({enabled: true, source: 'demo'}, 'feature');
            tx.oncomplete = () => {
              db.close();
              resolve();
            };
            tx.onerror = () => {
              db.close();
              reject(tx.error);
            };
            tx.onabort = () => {
              db.close();
              reject(tx.error);
            };
          };
        }).catch(error => console.warn('indexeddb setup failed', error))
      : Promise.resolve();
    console.log('demo app booted');
    console.error('synthetic demo error');
    fetch('/api/ok').then(() => fetch('/api/fail'));
    Promise.all([cacheReady, serviceWorkerReady, indexedDBReady]).finally(() => {
      setTimeout(() => {
        document.querySelector('main').dataset.ready = 'true';
        document.querySelector('#status').textContent = 'Ready from demo app';
      }, 100);
    });
  </script>
</body>
</html>
"""

SERVICE_WORKER_JS = """
self.addEventListener('install', event => {
  self.skipWaiting();
});
self.addEventListener('activate', event => {
  event.waitUntil(self.clients.claim());
});
self.addEventListener('fetch', event => {});
"""


def main():
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 0
    server = ThreadingHTTPServer(("127.0.0.1", port), DemoHandler)
    print(f"http://127.0.0.1:{server.server_port}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
