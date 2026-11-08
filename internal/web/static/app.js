'use strict';

// telegram-web-app.js loads synchronously (self-hosted), but guard anyway.
let tg = null;
let initData = '';

function whenTelegramReady(cb) {
  const start = Date.now();
  (function poll() {
    if (window.Telegram && window.Telegram.WebApp) return cb();
    if (Date.now() - start > 1500) return cb();
    setTimeout(poll, 50);
  })();
}

const CAT_RU = { frontend: 'Фронтенд', backend: 'Бэкенд', server: 'Сервер' };
const BK_RU = { parsing: 'Парсинг', matching: 'Мэтчинг', trading: 'Торговля' };
const IMP = [
  { v: 'urgent', ru: 'СРОЧНО', cls: 'imp-urgent' },
  { v: 'normal', ru: 'НОРМ', cls: 'imp-normal' },
  { v: 'low', ru: 'НЕ СРОЧНО', cls: 'imp-low' },
];
const IMP_RU = { urgent: 'СРОЧНО', normal: 'НОРМ', low: 'НЕ СРОЧНО' };
const STATUS_RU = {
  unassigned: 'Не распределена',
  analyst_idle: 'Не начата', analyst_wip: 'В работе', analyst_help: 'Горит очко 🔥',
  impl_idle: 'Не начата', impl_wip: 'В работе', impl_help: 'Горит очко 🔥',
  done: 'Сделано ✅', problem: 'Проблема ❌',
};
const STATUS_CLASS = {
  unassigned: 'st-idle',
  analyst_idle: 'st-idle', analyst_wip: 'st-wip', analyst_help: 'st-panic',
  impl_idle: 'st-idle', impl_wip: 'st-wip', impl_help: 'st-panic',
  done: 'st-done', problem: 'st-fail',
};
const NAMES = ['Участник 01', 'Участник 02'];
const COLUMN_WINDOWS = ['new', 'analyst', 'impl'];

const state = { window: 'new', me: null, pendingTask: null };

function stageOf(status) {
  if (status.startsWith('analyst')) return 'analyst';
  if (status.startsWith('impl')) return 'impl';
  return '';
}
const isHelp = (s) => s === 'analyst_help' || s === 'impl_help';

// ---- API ----
async function api(path, opts = {}) {
  const headers = Object.assign({ 'X-Telegram-Init-Data': initData }, opts.headers || {});
  if (opts.body) headers['Content-Type'] = 'application/json';
  const res = await fetch(path, Object.assign({}, opts, { headers }));
  if (!res.ok) {
    const t = await res.text().catch(() => '');
    throw new Error('HTTP ' + res.status + ': ' + t);
  }
  return res.status === 204 ? null : res.json();
}
async function uploadFile(taskId, file, origin) {
  const fd = new FormData();
  fd.append('file', file);
  if (origin) fd.append('origin', origin);
  const res = await fetch(`/api/tasks/${taskId}/attach`, {
    method: 'POST', headers: { 'X-Telegram-Init-Data': initData }, body: fd,
  });
  if (!res.ok) throw new Error('upload HTTP ' + res.status);
}
const esc = (s) => (s || '').replace(/[&<>]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' }[c]));

// ---- fullscreen / safe-area ----
function applySafeArea() {
  try {
    const inset = (tg && tg.contentSafeAreaInset) || (tg && tg.safeAreaInset) || {};
    document.documentElement.style.setProperty('--safe-top', ((inset.top || 0)) + 'px');
  } catch (e) {}
}
function supportsFullscreen() {
  return tg && typeof tg.requestFullscreen === 'function' && (!tg.isVersionAtLeast || tg.isVersionAtLeast('8.0'));
}
function isFullscreen() {
  try { return !!(tg && tg.isFullscreen); } catch (e) { return false; }
}
function enterFullscreen() {
  if (!supportsFullscreen()) return;
  try { tg.requestFullscreen(); } catch (e) {}
  try { if (tg.disableVerticalSwipes) tg.disableVerticalSwipes(); } catch (e) {}
}
function exitFullscreen() {
  if (!tg) return;
  // Collapse back to the normal (smaller) Mini App window WITHOUT closing it, so
  // the user can work alongside other windows. Re-enable the swipe-to-minimize
  // gesture too.
  try { if (typeof tg.exitFullscreen === 'function') tg.exitFullscreen(); } catch (e) {}
  try { if (tg.enableVerticalSwipes) tg.enableVerticalSwipes(); } catch (e) {}
}
function toggleFullscreen() {
  if (isFullscreen()) exitFullscreen(); else enterFullscreen();
}
// updateFsButton reflects the current fullscreen state on the header toggle.
function updateFsButton() {
  const btn = document.getElementById('fsBtn');
  if (!btn) return;
  if (!supportsFullscreen()) { btn.style.display = 'none'; return; }
  if (isFullscreen()) { btn.textContent = '🗕'; btn.title = 'Свернуть окно'; }
  else { btn.textContent = '⛶'; btn.title = 'На весь экран'; }
}

function goFullscreen() {
  if (!tg) return;
  try { tg.ready(); } catch (e) {}
  try { tg.expand(); } catch (e) {}
  enterFullscreen();
  ['fullscreenChanged', 'safeAreaChanged', 'contentSafeAreaChanged', 'viewportChanged'].forEach((ev) => {
    try { tg.onEvent(ev, applySafeArea); } catch (e) {}
  });
  try { tg.onEvent('fullscreenChanged', updateFsButton); } catch (e) {}
  applySafeArea();
}

// openExternal opens a URL in the system browser (a normal resizable window),
// using Telegram's openLink inside the Mini App webview, falling back to a tab.
function openExternal(url) {
  try { if (tg && typeof tg.openLink === 'function') { tg.openLink(url); return; } } catch (e) {}
  try { window.open(url, '_blank'); } catch (e) {}
}

// ---- themes ----
const THEMES = [['dark', '🌙 Тёмная'], ['blue', '🌊 Синяя'], ['light', '☀️ Светлая']];
function applyTheme(name) {
  document.documentElement.dataset.theme = name;
  try { localStorage.setItem('tt_theme', name); } catch (e) {}
}
function initTheme() {
  let t = 'dark';
  try { t = localStorage.getItem('tt_theme') || 'dark'; } catch (e) {}
  applyTheme(t);
}
function toggleThemeMenu() {
  const existing = document.getElementById('themeMenu');
  if (existing) { existing.remove(); return; }
  const m = document.createElement('div');
  m.id = 'themeMenu';
  m.className = 'theme-menu';
  const cur = document.documentElement.dataset.theme || 'dark';
  m.innerHTML = THEMES.map(([v, l]) =>
    `<button data-theme="${v}" class="${v === cur ? 'cur' : ''}">${l}</button>`).join('');
  document.body.appendChild(m);
  m.querySelectorAll('button').forEach((b) => b.onclick = () => {
    applyTheme(b.dataset.theme); m.remove();
  });
  // close on outside click
  setTimeout(() => document.addEventListener('click', function close(e) {
    if (!m.contains(e.target) && e.target.id !== 'themeBtn') { m.remove(); document.removeEventListener('click', close); }
  }), 0);
}

// ---- bootstrap ----
async function boot() {
  tg = window.Telegram && window.Telegram.WebApp;
  initTheme();
  goFullscreen();
  initData = (tg && tg.initData) || '';

  // Deep link: ?task=<id> opens that task after the list loads.
  try {
    const p = new URLSearchParams(location.search);
    const tid = p.get('task') || (tg && tg.initDataUnsafe && tg.initDataUnsafe.start_param &&
      /^task(\d+)$/.test(tg.initDataUnsafe.start_param) ? RegExp.$1 : null);
    if (tid) state.pendingTask = Number(tid);
  } catch (e) {}

  try { state.me = await api('/api/me'); } catch (e) {}

  document.querySelectorAll('.tab').forEach((t) =>
    t.addEventListener('click', () => switchTab(t.dataset.window)));
  document.getElementById('addBtn').addEventListener('click', () => {
    if (state.window === 'news') openNewFact(); else openNewBug();
  });
  document.getElementById('fsBtn').addEventListener('click', toggleFullscreen);
  document.getElementById('themeBtn').addEventListener('click', toggleThemeMenu);
  updateFsButton();
  document.getElementById('backdrop').addEventListener('click', closeSheets);

  await loadList();
  if (state.pendingTask) { openDetail(state.pendingTask); state.pendingTask = null; }
  setInterval(() => { if (allSheetsClosed() && !dragInProgress) loadList(); }, 4000);
}

function switchTab(win) {
  state.window = win;
  document.querySelectorAll('.tab').forEach((t) =>
    t.classList.toggle('active', t.dataset.window === win));
  loadList();
}

// ---- list ----
async function loadList() {
  const list = document.getElementById('list');
  if (state.window === 'news') { await loadNews(list); return; }
  let data;
  try { data = await api('/api/tasks?window=' + state.window); }
  catch (e) { list.innerHTML = `<div class="empty">Ошибка загрузки.<br>${esc(e.message)}</div>`; return; }
  const tasks = data.tasks || [];
  cleanupDragArtifacts(); // nuke any leftover ghost clones before re-render
  if (COLUMN_WINDOWS.includes(state.window)) renderColumns(list, tasks);
  else renderFlat(list, tasks);
}

// ---- news feed (Интересные факты) ----
function relTime(iso) {
  try {
    const d = (Date.now() - new Date(iso).getTime()) / 1000;
    if (d < 60) return 'только что';
    if (d < 3600) return Math.floor(d / 60) + ' мин назад';
    if (d < 86400) return Math.floor(d / 3600) + ' ч назад';
    if (d < 7 * 86400) return Math.floor(d / 86400) + ' дн назад';
    return new Date(iso).toLocaleDateString('ru');
  } catch (e) { return ''; }
}

async function loadNews(list) {
  let data;
  try { data = await api('/api/news'); }
  catch (e) { list.innerHTML = `<div class="empty">Ошибка загрузки.<br>${esc(e.message)}</div>`; return; }
  const items = data.news || [];
  list.className = 'list';
  if (!items.length) {
    list.innerHTML = `<div class="empty">💡 Лента пуста.<br>Жми «＋» и поделись идеей или находкой.</div>`;
    return;
  }
  list.innerHTML = items.map(newsCardHTML).join('');
  list.querySelectorAll('.news-del').forEach((b) => b.onclick = async (e) => {
    e.stopPropagation();
    confirmAction('Удалить запись из ленты?', async () => {
      try { await api('/api/news/' + b.dataset.id, { method: 'DELETE' }); loadList(); toast('Удалено 🗑'); }
      catch (err) { toast('Ошибка: ' + err.message); }
    });
  });
}

function newsCardHTML(n) {
  return `<div class="news-item">
    <button class="news-del" data-id="${n.id}" title="Удалить">✕</button>
    <div class="news-text">${esc(n.text)}</div>
    <div class="news-foot">💡 ${esc(n.author_name) || 'кто-то'} · ${relTime(n.created_at)}</div>
  </div>`;
}

function openNewFact() {
  const sheet = document.getElementById('newSheet');
  sheet.innerHTML = `
    <h2>💡 Новый факт</h2>
    <div class="section-label">Идея, находка, что-то интересное</div>
    <textarea id="factText" placeholder="Чем хочешь поделиться с командой..."></textarea>
    <button class="btn btn-primary" id="factCreate">Опубликовать</button>
    <button class="btn btn-ghost" id="factCancel">Отмена</button>`;
  openSheet(sheet);
  document.getElementById('factCancel').onclick = closeSheets;
  document.getElementById('factCreate').onclick = async () => {
    const text = document.getElementById('factText').value.trim();
    if (!text) { toast('Напишите текст'); return; }
    try {
      await api('/api/news', { method: 'POST', body: JSON.stringify({ text }) });
      closeSheets();
      switchTab('news');
      toast('Добавлено в ленту 💡');
    } catch (e) { toast('Ошибка: ' + e.message); }
  };
}

// renderColumns lays tasks out in three importance columns.
function renderColumns(list, tasks) {
  const cols = IMP.map((imp) => {
    const items = tasks.filter((t) => (t.importance || 'normal') === imp.v);
    const cards = items.length
      ? items.map(miniCardHTML).join('')
      : `<div class="col-empty">—</div>`;
    return `<div class="col ${imp.cls}" data-imp="${imp.v}">
      <h3>${imp.ru} <span class="col-count">${items.length}</span></h3>
      <div class="col-body">${cards}</div>
    </div>`;
  }).join('');
  list.className = 'board3';
  list.innerHTML = cols;
  bindCards(list);
}

function renderFlat(list, tasks) {
  list.className = 'list';
  if (!tasks.length) { list.innerHTML = `<div class="empty">Пусто 🎉</div>`; return; }
  list.innerHTML = tasks.map(flatCardHTML).join('');
  bindCards(list);
}

function bindCards(root) {
  root.querySelectorAll('.card').forEach((c) => {
    c.addEventListener('click', () => {
      if (c._dragged) { c._dragged = false; return; } // a drag just ended — not a tap
      openDetail(Number(c.dataset.id));
    });
    if (c.classList.contains('mini')) setupDrag(c);
  });
}

// Drag a mini card between importance columns (touch+mouse). A single global
// drag state + window-level listeners + a hard cleanup make it robust: no stuck
// ghost clones even if a pointerup is missed or the board re-renders mid-drag.
let dragInProgress = false;
let dragState = null;

function cleanupDragArtifacts() {
  document.querySelectorAll('.drag-ghost').forEach((g) => g.remove());
  document.querySelectorAll('.col.drop-target').forEach((c) => c.classList.remove('drop-target'));
  document.querySelectorAll('.card.dragging').forEach((c) => c.classList.remove('dragging'));
}

function onDragMove(e) {
  const d = dragState;
  if (!d) return;
  const dx = e.clientX - d.startX, dy = e.clientY - d.startY;
  if (!d.dragging) {
    if (Math.hypot(dx, dy) < 12) return;
    d.dragging = true; dragInProgress = true;
    d.card.classList.add('dragging');
    const g = d.card.cloneNode(true);
    g.classList.add('drag-ghost');
    g.style.width = d.card.offsetWidth + 'px';
    document.body.appendChild(g);
    d.ghost = g;
  }
  d.ghost.style.left = (e.clientX - d.card.offsetWidth / 2) + 'px';
  d.ghost.style.top = (e.clientY - 18) + 'px';
  const el = document.elementFromPoint(e.clientX, e.clientY);
  const col = el && el.closest ? el.closest('.col') : null;
  if (col !== d.lastCol) {
    if (d.lastCol) d.lastCol.classList.remove('drop-target');
    if (col) col.classList.add('drop-target');
    d.lastCol = col;
  }
}

async function onDragUp() {
  const d = dragState;
  window.removeEventListener('pointermove', onDragMove, true);
  window.removeEventListener('pointerup', onDragUp, true);
  window.removeEventListener('pointercancel', onDragUp, true);
  dragState = null;
  dragInProgress = false;
  if (!d) { cleanupDragArtifacts(); return; }
  const wasDrag = d.dragging;
  const target = d.lastCol && d.lastCol.dataset.imp;
  if (d.card) d.card._dragged = wasDrag; // suppress the click that follows a drag
  cleanupDragArtifacts();
  if (!wasDrag) return; // it was a tap — let the click handler open detail
  if (target && target !== d.imp) {
    try { await api('/api/tasks/' + d.id, { method: 'PATCH', body: JSON.stringify({ importance: target }) }); toast('Важность изменена'); }
    catch (err) { toast('Ошибка: ' + err.message); }
  }
  loadList();
}

function setupDrag(card) {
  card.addEventListener('pointerdown', (e) => {
    if (e.button != null && e.button !== 0) return;
    if (dragState) return;
    dragState = { card, startX: e.clientX, startY: e.clientY, dragging: false, ghost: null, lastCol: null, id: card.dataset.id, imp: card.dataset.imp };
    window.addEventListener('pointermove', onDragMove, true);
    window.addEventListener('pointerup', onDragUp, true);
    window.addEventListener('pointercancel', onDragUp, true);
  });
}

function whoChip(t) {
  const s = t.status;
  if (s === 'unassigned') return `<span class="who who-empty">🙋 Кто возьмёт?</span>`;
  if (stageOf(s) === 'analyst') return `<span class="who who-analyst">🔍 ${esc(t.analyst_name)}</span>`;
  if (stageOf(s) === 'impl') return `<span class="who who-dev">🛠 ${esc(t.dev_name)}</span>`;
  return '';
}
function statusBadge(t) {
  return `<span class="status ${STATUS_CLASS[t.status] || 'st-idle'}">${STATUS_RU[t.status] || t.status}</span>`;
}

// compact card for the 3-column board
function miniCardHTML(t) {
  return `<div class="card mini ${t.category} ${isHelp(t.status) ? 'panic' : ''}" data-id="${t.id}" data-imp="${t.importance || 'normal'}">
    <div class="mini-head"><span class="card-id">#${t.id}</span><span class="badge ${t.category}">${CAT_RU[t.category] || t.category}</span></div>
    <div class="mini-title">${esc(t.title)}</div>
    <div class="mini-foot">${whoChip(t)}${t.status === 'unassigned' ? '' : statusBadge(t)}</div>
  </div>`;
}

// full-width card for flat lists (Сделано / Проблемы)
function flatCardHTML(t) {
  let extra = '';
  if (t.status === 'done') {
    extra = `<div class="card-sub">🔍 ${esc(t.analyst_name) || '—'} · 🛠 ${esc(t.dev_name) || '—'}${t.analyst_lied ? ' · аналитик воздухан 🤥' : ''}</div>`;
  } else if (t.status === 'problem') {
    const who = t.problem_stage === 'impl' ? ('🛠 ' + (esc(t.dev_name) || '—')) : ('🔍 ' + (esc(t.analyst_name) || '—'));
    extra = `<div class="card-sub">Затык: <b>${t.problem_stage === 'impl' ? 'Реализация' : 'Аналитика'}</b> · ${who}</div>
      ${t.problem_desc ? `<div class="card-prob">${esc(t.problem_desc)}</div>` : ''}`;
  }
  return `<div class="card ${t.category}" data-id="${t.id}">
    <div class="card-head"><span class="card-id">#${t.id}</span>${statusBadge(t)}</div>
    <div class="card-title">${esc(t.title)}</div>
    <div class="card-meta"><span class="badge ${t.category}">${CAT_RU[t.category] || t.category}</span>
      <span class="chip">${IMP_RU[t.importance] || ''}</span></div>
    ${extra}
  </div>`;
}

// ---- detail ----
async function openDetail(id) {
  let t;
  try { t = await api('/api/tasks/' + id); } catch (e) { return; }
  const s = t.status;
  const stage = stageOf(s);

  const shots = (t.attachments || []).filter((a) => a.origin !== 'analyst');
  const analystShots = (t.attachments || []).filter((a) => a.origin === 'analyst');
  const comments = (t.comments || []).map((c) =>
    `<div class="comment"><div class="who-line">${c.author_role || 'user'} · ${new Date(c.created_at).toLocaleString('ru')}</div>${esc(c.text)}</div>`).join('');

  let kind = t.backend_kind ? ' / ' + (BK_RU[t.backend_kind] || t.backend_kind) : '';

  // Notes editable while the analyst works (idle/wip/help on analyst stage).
  const notesEditable = (s === 'analyst_idle' || s === 'analyst_wip' || s === 'analyst_help');
  let notesBlock = '';
  if (notesEditable) {
    notesBlock = `<div class="section-label">Решение / новые вводные (аналитик)</div>
      <textarea id="notesInput" placeholder="Причина бага, новые вводные, пути решения...">${esc(t.analyst_notes)}</textarea>
      <button class="btn btn-ghost" id="saveNotes">💾 Сохранить заметку</button>`;
  } else if (t.analyst_notes) {
    notesBlock = `<div class="section-label">📋 Решение от аналитика</div><div class="notes-box">${esc(t.analyst_notes)}</div>`;
  }

  // Done-page solution + verdict (read-only).
  let doneBlock = '';
  if (s === 'done') {
    doneBlock = `<div class="section-label">Вердикт</div>
      <div class="notes-box">Аналитик воздухан: <b>${t.analyst_lied ? 'ДА 🤥' : 'нет'}</b></div>
      ${t.solution_desc ? `<div class="section-label">✅ Реальное решение (реализатор)</div><div class="notes-box">${esc(t.solution_desc)}</div>` : ''}`;
  }
  // Problem-page block (read-only).
  let problemBlock = '';
  if (s === 'problem') {
    problemBlock = `<div class="section-label">⚠️ Затык на этапе: ${t.problem_stage === 'impl' ? 'Реализация' : 'Аналитика'}</div>
      <div class="notes-box">${esc(t.problem_desc) || '—'}</div>`;
  }

  const sheet = document.getElementById('sheet');
  sheet.innerHTML = `
    <h2>#${t.id} — ${esc(t.title)}</h2>
    <div class="card-meta">
      <span class="badge ${t.category}">${CAT_RU[t.category] || t.category}${kind}</span>
      <span class="chip imp-pill ${'imp-' + (t.importance || 'normal')}">${IMP_RU[t.importance] || ''}</span>
      ${statusBadge(t)} ${whoChip(t)}
    </div>
    <div class="section-label">Описание</div>
    <div class="desc">${esc(t.description) || '<span style="color:var(--hint)">—</span>'}</div>
    ${shots.length ? `<div class="section-label">Скриншоты (баг)</div><div class="shots" id="shotsReport"></div>` : ''}
    ${problemBlock}
    ${notesBlock}
    ${analystShots.length ? `<div class="section-label">Скриншоты (аналитик)</div><div class="shots" id="shotsAnalyst"></div>` : ''}
    ${doneBlock}
    <div id="actions"></div>
    <div id="subform"></div>
    <div class="section-label">Добавить скриншот</div>
    <label class="btn btn-ghost file-label">📎 Прикрепить скрин
      <input type="file" id="attachInput" accept="image/*" multiple hidden></label>
    <div class="section-label">Комментарии</div>
    ${comments || '<div style="color:var(--hint);font-size:13px">пока нет</div>'}
    <textarea id="commentInput" placeholder="Добавить комментарий..."></textarea>
    <button class="btn btn-ghost" id="addComment">💬 Комментировать</button>
    <button class="btn btn-ghost" id="closeSheet">Закрыть</button>
    <button class="btn btn-danger" id="deleteBtn">🗑 Удалить задачу</button>`;
  openSheet(sheet);

  loadShots(sheet.querySelector('#shotsReport'), shots);
  loadShots(sheet.querySelector('#shotsAnalyst'), analystShots);

  renderActions(t);
  wireCommon(t);
}

// loadShots fetches attachments as blobs (auth header) into a container.
function loadShots(container, atts) {
  if (!container) return;
  container.innerHTML = atts.map(() => `<img class="shot" alt="загрузка…">`).join('');
  const els = container.querySelectorAll('.shot');
  atts.forEach(async (a, i) => {
    try {
      const res = await fetch('/api/file?id=' + encodeURIComponent(a.tg_file_id), { headers: { 'X-Telegram-Init-Data': initData } });
      if (!res.ok) throw new Error('HTTP ' + res.status);
      els[i].src = URL.createObjectURL(await res.blob());
      els[i].alt = 'скрин';
      els[i].onclick = () => showLightbox(els[i].src);
    } catch (e) { if (els[i]) els[i].alt = '⚠️'; }
  });
}

const reload = () => { closeSheets(); loadList(); };

// renderActions draws the stage-specific action buttons for a task.
function renderActions(t) {
  const box = document.getElementById('actions');
  const id = t.id, s = t.status, stage = stageOf(s);
  let html = '';
  if (s === 'unassigned') {
    html = `<div class="hint-line">Причина ясна — можно сразу в реализацию. Если нужен анализ — в аналитику.</div>
      <div class="section-label">🔍 В аналитику</div>
      <div class="row-actions">${NAMES.map((n) => `<button class="btn btn-primary act-assign" data-mode="assign" data-name="${n}">${n}</button>`).join('')}</div>
      <div class="section-label">🛠 Сразу в реализацию</div>
      <div class="row-actions">${NAMES.map((n) => `<button class="btn btn-ghost act-assign" data-mode="assign-impl" data-name="${n}">${n}</button>`).join('')}</div>`;
  } else if (s === 'analyst_idle' || s === 'impl_idle') {
    html = `<div class="section-label">Действия</div>
      <button class="btn btn-primary" id="startBtn">▶️ Взять в работу</button>
      <button class="btn btn-ghost" id="returnBtn">↩️ Вернуть в распределение</button>`;
  } else if (s === 'analyst_wip' || s === 'analyst_help' || s === 'impl_wip' || s === 'impl_help') {
    html = `<div class="section-label">Действия</div>` + helpButton(stage, isHelp(s)) + finishButtons(stage);
  } else if (s === 'problem') {
    html = `<button class="btn btn-ghost" id="reopenBtn">↩️ Вернуть в Новые</button>`;
  }
  box.innerHTML = html;

  box.querySelectorAll('.act-assign').forEach((b) => b.onclick = async () => {
    const toImpl = b.dataset.mode === 'assign-impl';
    try {
      await api(`/api/tasks/${id}/${b.dataset.mode}`, { method: 'POST', body: JSON.stringify({ name: b.dataset.name }) });
      reload(); toast(`${b.dataset.name} → ${toImpl ? 'Реализация' : 'Аналитика'}`);
    } catch (e) { toast('Ошибка: ' + e.message); }
  });
  const $ = (x) => document.getElementById(x);
  if ($('startBtn')) $('startBtn').onclick = async () => {
    try { await api(`/api/tasks/${id}/start`, { method: 'POST', body: JSON.stringify({ stage }) }); reload(); toast('Взято в работу ▶️'); }
    catch (e) { toast('Ошибка: ' + e.message); }
  };
  if ($('returnBtn')) $('returnBtn').onclick = async () => {
    try { await api(`/api/tasks/${id}/return-to-pool`, { method: 'POST' }); reload(); toast('Вернули в распределение ↩️'); }
    catch (e) { toast('Ошибка: ' + e.message); }
  };
  if ($('helpOnBtn')) $('helpOnBtn').onclick = async () => {
    try { await api(`/api/tasks/${id}/help`, { method: 'POST', body: JSON.stringify({ stage, on: true }) }); reload(); toast('🔥 Очко горит — все оповещены'); }
    catch (e) { toast('Ошибка: ' + e.message); }
  };
  if ($('helpOffBtn')) $('helpOffBtn').onclick = async () => {
    try { await api(`/api/tasks/${id}/help`, { method: 'POST', body: JSON.stringify({ stage, on: false }) }); reload(); toast('Очко потушено 😮‍💨'); }
    catch (e) { toast('Ошибка: ' + e.message); }
  };
  if ($('muzhikBtn')) $('muzhikBtn').onclick = () => (stage === 'analyst' ? analystSuccessForm(t) : implSuccessForm(t));
  if ($('lohBtn')) $('lohBtn').onclick = () => failForm(t);
  if ($('reopenBtn')) $('reopenBtn').onclick = async () => {
    try { await api(`/api/tasks/${id}/reopen`, { method: 'POST' }); reload(); toast('Возвращено в Новые ↩️'); }
    catch (e) { toast('Ошибка: ' + e.message); }
  };
}

function helpButton(stage, on) {
  return on
    ? `<button class="btn btn-ghost" id="helpOffBtn">😮‍💨 Снять «горит очко»</button>`
    : `<button class="btn btn-panic" id="helpOnBtn">🔥 Горит очко</button>`;
}
function finishButtons(stage) {
  return `<div class="row-actions">
    <button class="btn btn-done" id="muzhikBtn">Мужик ✅</button>
    <button class="btn btn-danger" id="lohBtn">Лох ❌</button>
  </div>`;
}

// ---- outcome sub-forms ----
function analystSuccessForm(t) {
  const sf = document.getElementById('subform');
  sf.innerHTML = `<div class="subpanel">
    <div class="section-label">Кто реализует задачу?</div>
    <div class="row-actions">${NAMES.map((n) => `<button class="btn btn-primary pick-impl" data-name="${n}">${n}</button>`).join('')}</div>
    <button class="btn btn-ghost" id="sfCancel">Отмена</button></div>`;
  sf.scrollIntoView({ behavior: 'smooth', block: 'center' });
  sf.querySelectorAll('.pick-impl').forEach((b) => b.onclick = async () => {
    const notes = (document.getElementById('notesInput') || {}).value || t.analyst_notes || '';
    try {
      await api(`/api/tasks/${t.id}/analyst-success`, { method: 'POST', body: JSON.stringify({ impl: b.dataset.name, notes }) });
      reload(); toast(`Мужик ✅ → реализует ${b.dataset.name}`);
    } catch (e) { toast('Ошибка: ' + e.message); }
  });
  document.getElementById('sfCancel').onclick = () => { sf.innerHTML = ''; };
}

function implSuccessForm(t) {
  const sf = document.getElementById('subform');
  sf.innerHTML = `<div class="subpanel">
    <div class="section-label">Аналитик воздухан?</div>
    <div class="row-actions">
      <button class="btn btn-danger seg-lied" data-v="1">Да 🤥</button>
      <button class="btn btn-done seg-lied" data-v="0">Нет</button>
    </div>
    <div class="section-label">Правильное решение</div>
    <textarea id="solInput" placeholder="Как реально решилась задача..."></textarea>
    <button class="btn btn-primary" id="sfDone">Готово → Сделано</button>
    <button class="btn btn-ghost" id="sfCancel">Отмена</button></div>`;
  sf.scrollIntoView({ behavior: 'smooth', block: 'center' });
  let lied = null;
  sf.querySelectorAll('.seg-lied').forEach((b) => b.onclick = () => {
    lied = b.dataset.v === '1';
    sf.querySelectorAll('.seg-lied').forEach((x) => x.classList.toggle('sel', x === b));
  });
  document.getElementById('sfDone').onclick = async () => {
    if (lied === null) { toast('Выберите: воздухан или нет'); return; }
    const solution = document.getElementById('solInput').value;
    try {
      await api(`/api/tasks/${t.id}/impl-success`, { method: 'POST', body: JSON.stringify({ analyst_lied: lied, solution }) });
      reload(); toast('Готово ✅ → Сделано');
    } catch (e) { toast('Ошибка: ' + e.message); }
  };
  document.getElementById('sfCancel').onclick = () => { sf.innerHTML = ''; };
}

function failForm(t) {
  const stage = stageOf(t.status);
  const sf = document.getElementById('subform');
  sf.innerHTML = `<div class="subpanel">
    <div class="section-label">Опишите проблему (почему не вышло)</div>
    <textarea id="probInput" placeholder="В чём затык..."></textarea>
    <button class="btn btn-danger" id="sfFail">Отправить в Проблемы ❌</button>
    <button class="btn btn-ghost" id="sfCancel">Отмена</button></div>`;
  sf.scrollIntoView({ behavior: 'smooth', block: 'center' });
  document.getElementById('sfFail').onclick = async () => {
    const problem = document.getElementById('probInput').value.trim();
    if (!problem) { toast('Опишите проблему'); return; }
    const ep = stage === 'impl' ? 'impl-fail' : 'analyst-fail';
    try {
      await api(`/api/tasks/${t.id}/${ep}`, { method: 'POST', body: JSON.stringify({ problem }) });
      reload(); toast('Лох ❌ → Проблемы');
    } catch (e) { toast('Ошибка: ' + e.message); }
  };
  document.getElementById('sfCancel').onclick = () => { sf.innerHTML = ''; };
}

// ---- common detail wiring (comments, notes, attach, delete) ----
function wireCommon(t) {
  const id = t.id;
  const $ = (x) => document.getElementById(x);
  $('closeSheet').onclick = closeSheets;
  $('deleteBtn').onclick = () => confirmAction(`Удалить задачу #${id}? Необратимо.`, async () => {
    try { await api(`/api/tasks/${id}`, { method: 'DELETE' }); reload(); toast('Удалена 🗑'); }
    catch (e) { toast('Ошибка: ' + e.message); }
  });
  $('addComment').onclick = async () => {
    const text = $('commentInput').value.trim();
    if (!text) return;
    await api(`/api/tasks/${id}/comment`, { method: 'POST', body: JSON.stringify({ text }) });
    openDetail(id);
  };
  if ($('saveNotes')) $('saveNotes').onclick = async () => {
    await api(`/api/tasks/${id}/notes`, { method: 'POST', body: JSON.stringify({ notes: $('notesInput').value }) });
    toast('Заметка сохранена');
  };
  $('attachInput').onchange = async (e) => {
    const files = [...e.target.files];
    if (!files.length) return;
    const origin = stageOf(t.status) === 'analyst' ? 'analyst' : 'report';
    toast('Загрузка...');
    try { for (const f of files) await uploadFile(id, f, origin); toast('Скрин добавлен ✅'); openDetail(id); }
    catch (err) { toast('Ошибка загрузки: ' + err.message); }
  };
}

// ---- new bug ----
function openNewBug() {
  const draft = { category: '', backend_kind: '', importance: 'normal', files: [] };
  const sheet = document.getElementById('newSheet');
  sheet.innerHTML = `
    <h2>Новый баг</h2>
    <div class="section-label">Заголовок</div>
    <input type="text" id="nTitle" placeholder="Кратко суть">
    <div class="section-label">Описание</div>
    <textarea id="nDesc" placeholder="Что происходит, шаги, ожидаемое поведение"></textarea>
    <div class="section-label">Важность</div>
    <div class="seg" id="impSeg">
      <button data-v="urgent">🔴 СРОЧНО</button>
      <button data-v="normal" class="sel">🟡 НОРМ</button>
      <button data-v="low">🟢 НЕ СРОЧНО</button>
    </div>
    <div class="section-label">Категория</div>
    <div class="seg" id="catSeg">
      <button data-v="frontend">Frontend</button>
      <button data-v="backend">Backend</button>
      <button data-v="server">Server</button>
    </div>
    <div class="seg hidden" id="bkSeg">
      <button data-v="parsing">Парсинг</button>
      <button data-v="matching">Мэтчинг</button>
      <button data-v="trading">Торговля</button>
    </div>
    <div class="section-label">Скриншоты</div>
    <label class="btn btn-ghost file-label">📎 Выбрать скрины
      <input type="file" id="nFiles" accept="image/*" multiple hidden></label>
    <div id="nFilesInfo" style="color:var(--hint);font-size:12px;margin-top:4px"></div>
    <button class="btn btn-primary" id="createBtn">Создать задачу</button>
    <button class="btn btn-ghost" id="cancelNew">Отмена</button>`;
  openSheet(sheet);

  document.getElementById('impSeg').querySelectorAll('button').forEach((b) => b.onclick = () => {
    draft.importance = b.dataset.v;
    document.querySelectorAll('#impSeg button').forEach((x) => x.classList.toggle('sel', x === b));
  });
  const bkSeg = document.getElementById('bkSeg');
  document.getElementById('catSeg').querySelectorAll('button').forEach((b) => b.onclick = () => {
    draft.category = b.dataset.v;
    document.querySelectorAll('#catSeg button').forEach((x) => x.classList.toggle('sel', x === b));
    bkSeg.classList.toggle('hidden', draft.category !== 'backend');
    if (draft.category !== 'backend') draft.backend_kind = '';
  });
  bkSeg.querySelectorAll('button').forEach((b) => b.onclick = () => {
    draft.backend_kind = b.dataset.v;
    bkSeg.querySelectorAll('button').forEach((x) => x.classList.toggle('sel', x === b));
  });
  document.getElementById('nFiles').onchange = (e) => {
    draft.files = [...e.target.files];
    document.getElementById('nFilesInfo').textContent = draft.files.length ? `Выбрано файлов: ${draft.files.length}` : '';
  };
  document.getElementById('cancelNew').onclick = closeSheets;
  document.getElementById('createBtn').onclick = async () => {
    const title = document.getElementById('nTitle').value.trim();
    const description = document.getElementById('nDesc').value;
    if (!title) { toast('Введите заголовок'); return; }
    if (!draft.category) { toast('Выберите категорию'); return; }
    if (draft.category === 'backend' && !draft.backend_kind) { toast('Выберите тип бэкенд-ошибки'); return; }
    try {
      const res = await api('/api/tasks', { method: 'POST', body: JSON.stringify({ title, description, category: draft.category, backend_kind: draft.backend_kind, importance: draft.importance }) });
      for (const f of draft.files) { try { await uploadFile(res.id, f, 'report'); } catch (e) {} }
      closeSheets();
      switchTab('new');
      toast('Баг создан ✅');
    } catch (e) { toast('Ошибка: ' + e.message); }
  };
}

// ---- sheet helpers ----
function openSheet(el) { document.getElementById('backdrop').hidden = false; el.hidden = false; }
function closeSheets() {
  document.getElementById('backdrop').hidden = true;
  document.getElementById('sheet').hidden = true;
  document.getElementById('newSheet').hidden = true;
}
function allSheetsClosed() {
  return document.getElementById('sheet').hidden && document.getElementById('newSheet').hidden;
}
let toastTimer = null;
function showLightbox(src) {
  let lb = document.getElementById('lightbox');
  if (!lb) {
    lb = document.createElement('div');
    lb.id = 'lightbox';
    lb.innerHTML = '<span class="lb-close">✕</span><img alt="">';
    lb.addEventListener('click', () => lb.classList.remove('show'));
    document.body.appendChild(lb);
  }
  lb.querySelector('img').src = src;
  lb.classList.add('show');
}
function confirmAction(message, onYes) {
  if (tg && tg.showConfirm) { try { tg.showConfirm(message, (ok) => { if (ok) onYes(); }); return; } catch (e) {} }
  if (window.confirm(message)) onYes();
}
function toast(msg) {
  let el = document.getElementById('toast');
  if (!el) { el = document.createElement('div'); el.id = 'toast'; document.body.appendChild(el); }
  el.textContent = msg;
  el.classList.add('show');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.remove('show'), 2600);
  if (tg && tg.HapticFeedback) { try { tg.HapticFeedback.notificationOccurred('success'); } catch (e) {} }
  console.log(msg);
}

whenTelegramReady(boot);
