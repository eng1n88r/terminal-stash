// stash frontend — vanilla ES module, no build step.

const feed = document.getElementById('feed');
const textEl = document.getElementById('text');
const fileInput = document.getElementById('file-input');
const flashEl = document.getElementById('flash');
const dropzone = document.getElementById('dropzone');
const statsEl = document.getElementById('stats');
const sendBtn = document.getElementById('send-btn');
const syncStatus = document.getElementById('sync-status');
const composer = document.querySelector('.composer');

// Keep the editor open while it has content. Otherwise moving focus from the
// textarea to Send collapses the field during pointerdown, shifts the button,
// and causes the browser to discard the click before it reaches pointerup.
function syncComposerState() {
  composer.classList.toggle('has-content', textEl.value.length > 0);
}
textEl.addEventListener('input', syncComposerState);
syncComposerState();

// --- theme cycler (phosphor palettes), persisted -----------------------------
const THEME_KEY = 'hc_theme';
const THEMES = ['green', 'amber', 'cyan', 'ice', 'ultraviolet', 'synthwave', 'matrix', 'mono'];
const themeBtn = document.getElementById('theme-btn');
const themeMenu = document.getElementById('theme-menu');

for (const theme of THEMES) {
  const option = document.createElement('button');
  option.type = 'button';
  option.className = 'theme-option';
  option.dataset.theme = theme;
  option.setAttribute('role', 'menuitemradio');
  option.innerHTML = `<span class="theme-swatch" data-swatch="${theme}"></span><span>${theme}</span>`;
  option.addEventListener('click', () => { applyTheme(theme); closeThemeMenu(); });
  themeMenu.appendChild(option);
}

function applyTheme(t) {
  if (!THEMES.includes(t)) t = 'green';
  if (t === 'green') document.documentElement.removeAttribute('data-theme');
  else document.documentElement.setAttribute('data-theme', t);

  themeBtn.textContent = `theme: ${t}`;
  themeMenu.querySelectorAll('.theme-option').forEach((option) => {
    option.setAttribute('aria-checked', String(option.dataset.theme === t));
  });
  localStorage.setItem(THEME_KEY, t);
}

applyTheme(localStorage.getItem(THEME_KEY) || 'green');
themeBtn.addEventListener('click', () => {
  const open = themeMenu.hidden;
  themeMenu.hidden = !open;
  themeBtn.setAttribute('aria-expanded', String(open));
});
function closeThemeMenu() {
  themeMenu.hidden = true;
  themeBtn.setAttribute('aria-expanded', 'false');
}
document.addEventListener('click', (e) => {
  if (!e.target.closest('.theme-control')) closeThemeMenu();
});
document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') { closeThemeMenu(); themeBtn.focus(); }
});

// --- logout ------------------------------------------------------------------
document.getElementById('logout-btn').addEventListener('click', async () => {
  await fetch('/api/logout', { method: 'POST' });
  window.location.href = '/login';
});

// --- helpers -----------------------------------------------------------------
// flash('msg') shows a transient toast; flash('msg', {label, fn}) adds an
// action button (e.g. undo) and keeps the toast up for the whole undo window.
function flash(msg, action) {
  flashEl.textContent = msg;
  if (action) {
    const b = document.createElement('button');
    b.className = 'act flash-act';
    b.textContent = action.label;
    b.addEventListener('click', () => { hideFlash(); action.fn(); });
    flashEl.append(' ', b);
  }
  flashEl.classList.add('show');
  clearTimeout(flash._t);
  flash._t = setTimeout(hideFlash, action ? UNDO_MS : 1600);
}

function hideFlash() { flashEl.classList.remove('show'); }

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
  el.dataset.size = it.size;

  if (it.kind === 'text') {
    el.innerHTML = `
      <div class="item-meta">
        <span class="tag">text</span>
        <span class="filesize">${fmtSize(it.size)}</span>
        <span class="spacer"></span>
        <span class="item-actions">
          <button class="act act-copy">copy</button>
          <button class="act danger act-del">rm</button>
        </span>
        <time class="time"></time>
      </div>
      <pre class="copyable" title="click to copy"></pre>`;
    el.querySelector('pre').textContent = it.content;
    const copy = async () => {
      try { await navigator.clipboard.writeText(it.content); flash('✓ copied'); }
      catch { flash('✗ clipboard blocked'); }
    };
    el.querySelector('.act-copy').addEventListener('click', copy);
    el.querySelector('pre').addEventListener('click', () => {
      // don't clobber a manual text selection with a full copy
      if (String(window.getSelection())) return;
      copy();
    });
  } else {
    el.innerHTML = `
      <div class="item-meta">
        <span class="tag file">file</span>
        <span class="filesize">${fmtSize(it.size)}</span>
        <span class="mime">${esc(it.mime || '')}</span>
        <span class="spacer"></span>
        <span class="item-actions">
          <a class="act" href="/api/files/${it.id}" download>download</a>
          <button class="act danger act-del">rm</button>
        </span>
        <time class="time"></time>
      </div>
      <div class="filename">${esc(it.filename || it.content)}</div>`;
  }

  const t = el.querySelector('.time');
  t.textContent = ago(it.created_at);
  t.title = new Date(it.created_at * 1000).toLocaleString();

  // Two-step rm: first click arms ("sure?" for 2.5s), second click deletes.
  const del = el.querySelector('.act-del');
  del.addEventListener('click', () => {
    if (!del.classList.contains('confirm')) {
      del.classList.add('confirm');
      del.textContent = 'sure?';
      del._t = setTimeout(() => {
        del.classList.remove('confirm');
        del.textContent = 'rm';
      }, 2500);
      return;
    }
    clearTimeout(del._t);
    deferDelete(it.id, el);
  });
  return el;
}

// --- delete with undo ----------------------------------------------------------
// rm detaches the item immediately but only sends the DELETE after the undo
// window passes; undo just reattaches the element and cancels the timer.
const UNDO_MS = 4000;
const pendingDeletes = new Map(); // id -> {el, next, timer}

function deferDelete(id, el) {
  const next = el.nextElementSibling;
  el.remove();
  refreshEmpty();
  pendingDeletes.set(id, { el, next, timer: setTimeout(() => commitDelete(id), UNDO_MS) });
  flash('removed', { label: 'undo', fn: () => undoDelete(id) });
}

async function commitDelete(id, unloading = false) {
  const p = pendingDeletes.get(id);
  if (!p) return;
  pendingDeletes.delete(id);
  clearTimeout(p.timer);
  if (unloading) {
    fetch('/api/items/' + id, { method: 'DELETE', keepalive: true });
    return;
  }
  const res = await fetch('/api/items/' + id, { method: 'DELETE' });
  if (!res.ok && res.status !== 404) { reinsert(p); flash('✗ delete failed'); }
}

function undoDelete(id) {
  const p = pendingDeletes.get(id);
  if (!p) return;
  pendingDeletes.delete(id);
  clearTimeout(p.timer);
  reinsert(p);
}

function reinsert({ el, next }) {
  if (next && next.isConnected) {
    next.before(el);
  } else {
    // original neighbor is gone — fall back to newest-first timestamp order
    const created = Number(el.dataset.created);
    const after = [...feed.querySelectorAll('.item')].find((n) => Number(n.dataset.created) <= created);
    if (after) after.before(el);
    else feed.appendChild(el);
  }
  refreshEmpty();
}

// If the tab closes mid-undo-window, still deliver the pending DELETEs.
window.addEventListener('pagehide', () => {
  for (const id of [...pendingDeletes.keys()]) commitDelete(id, true);
});

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
  updateStats();
}

function updateStats() {
  const items = feed.querySelectorAll('.item');
  let total = 0;
  items.forEach((n) => { total += Number(n.dataset.size) || 0; });
  statsEl.textContent = items.length
    ? `${items.length} item${items.length === 1 ? '' : 's'} · ${fmtSize(total)}`
    : '';
}

function prepend(it) {
  if (feed.querySelector(`.item[data-id="${it.id}"]`)) return;
  feed.insertBefore(itemNode(it), feed.firstChild);
  refreshEmpty();
}

function removeById(id) {
  // deleted elsewhere — a local pending delete for it is moot
  const p = pendingDeletes.get(id);
  if (p) { pendingDeletes.delete(id); clearTimeout(p.timer); }
  const el = feed.querySelector(`.item[data-id="${id}"]`);
  if (el) el.remove();
  refreshEmpty();
}

async function loadItems() {
  const res = await fetch('/api/items');
  if (res.status === 401) { window.location.href = '/login'; return; }
  const items = await res.json();
  feed.setAttribute('aria-busy', 'false');
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
  if (!content.trim() || sendBtn.disabled) return;
  sendBtn.disabled = true; // no double-submit on double-click / repeated Ctrl+Enter
  try {
    const res = await fetch('/api/text', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ content }),
    });
    if (res.ok) {
      textEl.value = '';
      syncComposerState();
      flash('✓ sent');
    }
    else flash('✗ send failed');
  } finally {
    sendBtn.disabled = false;
  }
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

sendBtn.addEventListener('click', sendText);
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
  es.onopen = () => {
    syncStatus.classList.add('online');
    syncStatus.querySelector('span:last-child').textContent = 'live';
  };
  es.onmessage = (e) => {
    try {
      const ev = JSON.parse(e.data);
      if (ev.type === 'created' && ev.item) prepend(ev.item);
      else if (ev.type === 'deleted') removeById(ev.id);
    } catch (_) {}
  };
  es.onerror = () => {
    syncStatus.classList.remove('online');
    syncStatus.querySelector('span:last-child').textContent = 'reconnecting';
  };
}

// --- boot --------------------------------------------------------------------
loadItems();
connectEvents();
