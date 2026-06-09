// stash frontend — vanilla ES module, no build step.

const feed = document.getElementById('feed');
const textEl = document.getElementById('text');
const fileInput = document.getElementById('file-input');
const flashEl = document.getElementById('flash');
const dropzone = document.getElementById('dropzone');

// --- theme cycler (phosphor palettes), persisted -----------------------------
const THEME_KEY = 'hc_theme';
const THEMES = ['green', 'amber', 'cyan', 'ice', 'ultraviolet', 'synthwave', 'matrix', 'mono'];
const themeBtn = document.getElementById('theme-btn');

function applyTheme(t) {
  if (!THEMES.includes(t)) t = 'green';
  if (t === 'green') document.documentElement.removeAttribute('data-theme');
  else document.documentElement.setAttribute('data-theme', t);
  themeBtn.textContent = t; // bracket framing comes from CSS ::before/::after
  localStorage.setItem(THEME_KEY, t);
}

applyTheme(localStorage.getItem(THEME_KEY) || 'green');
themeBtn.addEventListener('click', () => {
  const cur = localStorage.getItem(THEME_KEY) || 'green';
  applyTheme(THEMES[(THEMES.indexOf(cur) + 1) % THEMES.length]);
});

// --- logout ------------------------------------------------------------------
document.getElementById('logout-btn').addEventListener('click', async () => {
  await fetch('/api/logout', { method: 'POST' });
  window.location.href = '/login';
});

// --- helpers -----------------------------------------------------------------
function flash(msg) {
  flashEl.textContent = msg;
  flashEl.classList.add('show');
  clearTimeout(flash._t);
  flash._t = setTimeout(() => flashEl.classList.remove('show'), 1600);
}

function esc(s) {
  return s.replace(/[&<>"']/g, (c) => (
    { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]
  ));
}

function fmtSize(n) {
  if (n < 1024) return n + ' B';
  const units = ['KB', 'MB', 'GB', 'TB'];
  let i = -1;
  do { n /= 1024; i++; } while (n >= 1024 && i < units.length - 1);
  return n.toFixed(1) + ' ' + units[i];
}

function ago(epoch) {
  const s = Math.max(0, Math.floor(Date.now() / 1000 - epoch));
  if (s < 5) return 'just now';
  if (s < 60) return s + 's ago';
  if (s < 3600) return Math.floor(s / 60) + 'm ago';
  if (s < 86400) return Math.floor(s / 3600) + 'h ago';
  return Math.floor(s / 86400) + 'd ago';
}

// --- rendering ---------------------------------------------------------------
function itemNode(it) {
  const el = document.createElement('div');
  el.className = 'item';
  el.dataset.id = it.id;
  el.dataset.created = it.created_at;

  if (it.kind === 'text') {
    el.innerHTML = `
      <div class="item-meta">
        <span class="tag">text</span>
        <span class="filesize">${fmtSize(it.size)}</span>
        <span class="spacer"></span>
        <span class="time">${ago(it.created_at)}</span>
      </div>
      <pre></pre>
      <div class="item-actions">
        <button class="btn act-copy">copy</button>
        <button class="btn danger act-del">rm</button>
      </div>`;
    el.querySelector('pre').textContent = it.content;
    el.querySelector('.act-copy').addEventListener('click', async () => {
      try { await navigator.clipboard.writeText(it.content); flash('✓ copied'); }
      catch { flash('✗ clipboard blocked'); }
    });
  } else {
    el.innerHTML = `
      <div class="item-meta">
        <span class="tag file">file</span>
        <span class="filesize">${fmtSize(it.size)}</span>
        <span class="mime">${esc(it.mime || '')}</span>
        <span class="spacer"></span>
        <span class="time">${ago(it.created_at)}</span>
      </div>
      <div class="filename">${esc(it.filename || it.content)}</div>
      <div class="item-actions">
        <a class="btn" href="/api/files/${it.id}" download>download</a>
        <button class="btn danger act-del">rm</button>
      </div>`;
  }

  el.querySelector('.act-del').addEventListener('click', async () => {
    const res = await fetch('/api/items/' + it.id, { method: 'DELETE' });
    if (res.ok) { el.remove(); refreshEmpty(); }
    else flash('✗ delete failed');
  });
  return el;
}

function refreshEmpty() {
  const has = feed.querySelector('.item');
  let empty = feed.querySelector('.empty');
  if (!has && !empty) {
    empty = document.createElement('div');
    empty.className = 'empty';
    empty.textContent = '— nothing here yet. send some text or drop a file. —';
    feed.appendChild(empty);
  } else if (has && empty) {
    empty.remove();
  }
}

function prepend(it) {
  if (feed.querySelector(`.item[data-id="${it.id}"]`)) return;
  feed.insertBefore(itemNode(it), feed.firstChild);
  refreshEmpty();
}

function removeById(id) {
  const el = feed.querySelector(`.item[data-id="${id}"]`);
  if (el) el.remove();
  refreshEmpty();
}

async function loadItems() {
  const res = await fetch('/api/items');
  if (res.status === 401) { window.location.href = '/login'; return; }
  const items = await res.json();
  feed.innerHTML = '';
  for (const it of items) feed.appendChild(itemNode(it));
  refreshEmpty();
}

// Keep relative timestamps fresh.
setInterval(() => {
  feed.querySelectorAll('.item').forEach((el) => {
    const t = el.querySelector('.time');
    if (t) t.textContent = ago(Number(el.dataset.created));
  });
}, 30000);

// --- actions -----------------------------------------------------------------
async function sendText() {
  const content = textEl.value;
  if (!content.trim()) return;
  const res = await fetch('/api/text', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ content }),
  });
  if (res.ok) { textEl.value = ''; flash('✓ sent'); }
  else flash('✗ send failed');
}

async function uploadFiles(files) {
  if (!files || !files.length) return;
  const fd = new FormData();
  for (const f of files) fd.append('files', f, f.name);
  flash('uploading ' + files.length + ' file(s)…');
  const res = await fetch('/api/files', { method: 'POST', body: fd });
  if (res.ok) flash('✓ uploaded');
  else {
    const msg = await res.text();
    flash('✗ ' + (msg || 'upload failed'));
  }
}

document.getElementById('send-btn').addEventListener('click', sendText);
document.getElementById('file-btn').addEventListener('click', () => fileInput.click());
fileInput.addEventListener('change', () => { uploadFiles(fileInput.files); fileInput.value = ''; });

textEl.addEventListener('keydown', (e) => {
  if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') { e.preventDefault(); sendText(); }
});

// Global paste: pasted files/images upload; otherwise let the textarea handle text.
window.addEventListener('paste', (e) => {
  const items = e.clipboardData && e.clipboardData.files;
  if (items && items.length) {
    e.preventDefault();
    uploadFiles(items);
  }
});

// Drag & drop anywhere on the page.
let dragDepth = 0;
window.addEventListener('dragenter', (e) => {
  if (e.dataTransfer && Array.from(e.dataTransfer.types).includes('Files')) {
    dragDepth++;
    document.body.classList.add('dragging');
    dropzone.classList.add('dragging');
  }
});
window.addEventListener('dragover', (e) => { e.preventDefault(); });
window.addEventListener('dragleave', () => {
  dragDepth = Math.max(0, dragDepth - 1);
  if (dragDepth === 0) { document.body.classList.remove('dragging'); dropzone.classList.remove('dragging'); }
});
window.addEventListener('drop', (e) => {
  e.preventDefault();
  dragDepth = 0;
  document.body.classList.remove('dragging');
  dropzone.classList.remove('dragging');
  if (e.dataTransfer && e.dataTransfer.files.length) uploadFiles(e.dataTransfer.files);
});

// --- live updates via SSE ----------------------------------------------------
function connectEvents() {
  const es = new EventSource('/api/events');
  es.onmessage = (e) => {
    try {
      const ev = JSON.parse(e.data);
      if (ev.type === 'created' && ev.item) prepend(ev.item);
      else if (ev.type === 'deleted') removeById(ev.id);
    } catch (_) {}
  };
  es.onerror = () => { /* EventSource auto-reconnects */ };
}

// --- boot --------------------------------------------------------------------
loadItems();
connectEvents();
