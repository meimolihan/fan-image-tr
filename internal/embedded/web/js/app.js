/* fan-image-tr · 图片工坊 —— 前端逻辑（无框架依赖） */
'use strict';

const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

const state = {
  opts: null,          // /api/options
  caps: null,          // /api/capabilities
  version: '',
  cwd: '',
  parent: '',
  files: [],           // 当前目录文件
  sel: new Set(),      // 选中的相对路径
  view: 'browser',
  tasks: [],
  timer: null,
  estimateSeq: 0,
  viewerList: [],
  viewerIdx: 0,
  pollBusy: false,
};

// 挂到 window 便于浏览器控制台排查
window.state = state;

const LS = {
  get(k, d) { try { const v = localStorage.getItem('fit.' + k); return v === null ? d : JSON.parse(v); } catch { return d; } },
  set(k, v) { try { localStorage.setItem('fit.' + k, JSON.stringify(v)); } catch {} },
};

/* ==================== 基础工具 ==================== */

async function api(path, opts = {}) {
  const res = await fetch(path, {
    headers: opts.body && !(opts.body instanceof FormData) ? { 'Content-Type': 'application/json' } : undefined,
    ...opts,
  });
  const text = await res.text();
  let data = null;
  if (text) { try { data = JSON.parse(text); } catch { data = { error: text }; } }
  if (!res.ok) throw new Error((data && data.error) || `HTTP ${res.status}`);
  return data;
}

function toast(msg, kind = '', ms = 3600) {
  const el = document.createElement('div');
  el.className = 'toast ' + kind;
  el.innerHTML = '<span class="tx"></span>';
  el.querySelector('.tx').textContent = msg;
  $('#toasts').appendChild(el);
  setTimeout(() => {
    el.style.transition = 'opacity .25s';
    el.style.opacity = '0';
    setTimeout(() => el.remove(), 260);
  }, ms);
}

function fmtSize(b) {
  if (!b || b < 0) return '—';
  if (b < 1024) return b + ' B';
  const u = ['KB', 'MB', 'GB', 'TB'];
  let v = b / 1024, i = 0;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return (v >= 100 ? v.toFixed(0) : v.toFixed(1)) + ' ' + u[i];
}

function fmtDim(w, h) { return w && h ? `${w}×${h}` : '—'; }
function fmtDuration(s) {
  if (!s || s <= 0) return '';
  if (s < 1) return s.toFixed(2) + 's';
  return s.toFixed(1) + 's';
}
function esc(s) { return String(s == null ? '' : s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c])); }
function extOf(name) { const i = String(name).lastIndexOf('.'); return i < 0 ? '' : name.slice(i + 1).toLowerCase(); }

function debounce(fn, ms) {
  let t;
  return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); };
}

/* 通用弹窗：prompt / confirm / 只读展示 */
function dialog({ title, body, okText = '确定', cancelText = '取消', hideCancel = false }) {
  return new Promise(resolve => {
    const modal = $('#dialog');
    $('#dlgTitle').textContent = title;
    const host = $('#dlgBody');
    host.innerHTML = '';
    if (typeof body === 'string') {
      host.innerHTML = `<div class="cmd">${esc(body)}</div>`;
    } else if (body) {
      host.appendChild(body);
    }
    $('#dlgOk').textContent = okText;
    $('#dlgCancel').textContent = cancelText;
    $('#dlgCancel').classList.toggle('hide', !!hideCancel);
    modal.hidden = false;

    const inputs = $$('input, select, textarea', host);
    const first = inputs.find(i => i.type !== 'checkbox');
    if (first) setTimeout(() => { first.focus(); if (first.select) first.select(); }, 30);

    const done = (val) => {
      modal.hidden = true;
      $('#dlgOk').removeEventListener('click', onOk);
      $('#dlgCancel').removeEventListener('click', onCancel);
      document.removeEventListener('keydown', onKey);
      resolve(val);
    };
    const onOk = () => done(inputs.length ? readVals(inputs) : true);
    const onCancel = () => done(null);
    const onKey = (e) => {
      if (e.key === 'Escape') { e.preventDefault(); onCancel(); }
      if (e.key === 'Enter' && e.target.tagName !== 'TEXTAREA') { e.preventDefault(); onOk(); }
    };
    $('#dlgOk').addEventListener('click', onOk);
    $('#dlgCancel').addEventListener('click', onCancel);
    document.addEventListener('keydown', onKey);
  });
}

function readVals(inputs) {
  const out = {};
  for (const i of inputs) {
    if (!i.name) continue;
    if (i.type === 'checkbox') out[i.name] = i.checked;
    else if (i.type === 'number') out[i.name] = i.value === '' ? 0 : Number(i.value);
    else out[i.name] = i.value;
  }
  return out;
}

function inputField(label, name, value) {
  const wrap = document.createElement('div');
  wrap.className = 'field';
  const lb = document.createElement('label');
  lb.setAttribute('for', 'dlg_' + name);
  lb.textContent = label;
  const inp = document.createElement('input');
  inp.id = 'dlg_' + name;
  inp.name = name;
  inp.type = 'text';
  inp.value = value == null ? '' : value;
  inp.setAttribute('autocomplete', 'off');
  wrap.append(lb, inp);
  return wrap;
}

/* ==================== 启动 ==================== */

async function boot() {
  applyTheme(LS.get('theme', 'dark'));
  bindStaticEvents();
  try {
    const [caps, opts, ver] = await Promise.all([
      api('/api/capabilities'), api('/api/options'), api('/api/version'),
    ]);
    state.caps = caps;
    state.opts = opts;
    state.version = ver.version || '';
    buildOptionUI();
    renderHWBadge();
  } catch (e) {
    console.error('初始化失败', e);
    toast('无法读取服务端能力：' + e.message, 'err', 8000);
  }
  await loadDir('');
  await refreshTasks();
  setInterval(() => { if (state.view === 'queue' && $('#autoRefresh').checked) refreshTasks(); }, 1200);
  LS.get('grid', 3);
  applyGrid(LS.get('grid', 3));
}

function applyTheme(t) {
  document.documentElement.dataset.theme = t;
  LS.set('theme', t);
}

function renderHWBadge() {
  const c = state.caps;
  const el = $('#hwBadge');
  if (!c) { el.textContent = 'FFmpeg 未知'; return; }
  // 以"逐格式真实自检通过"的 hw_formats 为准，hw_accel[].available 只反映解码能力
  const byAccel = new Map();
  for (const [fmt, hw] of Object.entries(c.hw_formats || {})) {
    if (!byAccel.has(hw.accel)) byAccel.set(hw.accel, []);
    byAccel.get(hw.accel).push(`${fmt}→${hw.encoder}`);
  }
  if (byAccel.size) {
    el.className = 'badge on';
    const parts = Array.from(byAccel.entries()).map(([accel, list]) => {
      const name = ((c.hw_accel || []).find(a => a.id === accel) || {}).name || accel;
      return `${name}(${list.length})`;
    });
    el.textContent = `硬件编码 ${parts.join(' ')}`;
    el.title = '已实测可用的硬件图片编码器：\n' + Array.from(byAccel.entries())
      .map(([accel, list]) => `${accel}: ${list.join(', ')}`).join('\n');
  } else {
    el.className = 'badge';
    el.textContent = '软件编码';
    el.title = '未检测到可用的硬件图片编码器（将全部使用软件编码）';
  }
  const v = $('#verTag');
  if (v) v.textContent = `v${state.version || '?'}`;
}

/* ==================== 选项面板 ==================== */

function buildOptionUI() {
  const o = state.opts;
  if (!o) return;

  fillSelect($('#formatSel'), o.formats.map(f => ({
    v: f.id, t: f.name + (f.available ? '' : `（${f.reason || '不可用'}）`), d: f.available ? '' : f.id,
  })));
  fillSelect($('#chromaSel'), o.chromas.map(c => ({ v: c.id, t: c.name, h: c.label })));
  fillSelect($('#colorModeSel'), o.color_modes.map(c => ({ v: c.id, t: c.name, h: c.label })));
  fillSelect($('#resizeSel'), o.resize_modes.map(r => ({ v: r.id, t: r.label || r.name, h: r.name })));
  fillSelect($('#adaptSel'), o.adapts.map(a => ({ v: a.id, t: a.label || a.name, h: a.name })));
  fillSelect($('#rotateSel'), o.rotations.map(r => ({ v: r.value, t: r.name })));
  fillSelect($('#wmPos'), o.positions.map(p => ({ v: p.id, t: p.name })));
  fillSelect($('#accelSel'), [{ v: 'auto', t: '自动（可用则用硬件）' }].concat(
    o.accels.map(a => ({
      v: a.id,
      t: a.name + (a.available ? `（${a.formats.length} 格式）` : `（${a.reason || '不可用'}）`),
      d: a.available ? '' : a.id,
    }))));

  const saved = LS.get('options', null);
  const base = saved && typeof saved === 'object' ? saved : {};
  $('#formatSel').value = base.format || o.default_format || 'webp';
  $('#qualityRange').value = base.quality || o.default_quality || 80;
  $('#chromaSel').value = base.chroma || '420';
  $('#colorModeSel').value = base.color_mode || 'keep';
  $('#resizeSel').value = base.resize || 'keep';
  $('#sizeInput').value = base.size || 1920;
  $('#customW').value = base.custom_width || 1200;
  $('#customH').value = base.custom_height || 1200;
  $('#adaptSel').value = base.adapt || 'fit';
  $('#accelSel').value = base.accel_sel || 'auto';
  $('#rotateSel').value = base.rotate || 0;
  $('#autoOrient').checked = base.auto_orient !== false;
  $('#flipH').checked = !!base.flip_h;
  $('#flipV').checked = !!base.flip_v;
  $('#stripMeta').checked = base.strip_metadata !== false;
  $('#allowUpscale').checked = !!base.allow_upscale;
  $('#animatedChk').checked = !!base.animated;
  $('#bgSel').value = base.background && base.background.startsWith('#') ? 'custom' : (base.background || 'white');
  $('#bgColor').value = base.background && base.background.startsWith('#') ? base.background : '#ffffff';
  for (const [id, k] of [['#sharpenRange', 'sharpen'], ['#blurRange', 'blur'], ['#brightnessRange', 'brightness'],
    ['#contrastRange', 'contrast'], ['#saturationRange', 'saturation']]) {
    $(id).value = base[k] || 0;
  }
  $('#wmEnabled').checked = !!(base.watermark && base.watermark.enabled);
  $('#wmPath').value = (base.watermark && base.watermark.path) || '';
  $('#wmPos').value = (base.watermark && base.watermark.position) || 'bottom_right';
  $('#wmMargin').value = (base.watermark && base.watermark.margin) || 16;
  $('#wmWidth').value = (base.watermark && base.watermark.width) || 0;
  $('#wmOpacity').value = (base.watermark && base.watermark.opacity) || 80;

  const od = $('#outDirLabel');
  if (od) od.textContent = o.output_dir || '（与浏览根目录相同）';
  $('#uploadBtn').hidden = !o.allow_upload;
  if (o.max_upload) $('#uploadInput').title = `单个文件最大 ${o.max_upload} MB`;

  syncFormatUI();
  syncResizeUI();
  syncWatermarkUI();
  updateAllOutputs();
}

// 按格式记忆用户手动设定的质量值
const qualityPref = {};

function fillSelect(sel, items) {
  sel.innerHTML = '';
  for (const it of items) {
    const opt = document.createElement('option');
    opt.value = it.v;
    opt.textContent = it.t;
    if (it.d) opt.disabled = true;
    if (it.h) opt.title = it.h;
    sel.appendChild(opt);
  }
}

function currentFormat() {
  const o = state.opts;
  if (!o) return null;
  return o.formats.find(f => f.id === $('#formatSel').value) || o.formats[0];
}

function syncFormatUI() {
  const f = currentFormat();
  if (!f) return;
  // 界面统一使用 1~100 质量值，服务端 Clamp 再按格式量纲换算成编码器取值，
  // 因此滑块范围固定 1~100，format.quality 的 min/max 只用于提示文案。
  const q = f.quality || {};
  const qf = $('#qualityField');
  if (!q.kind || q.kind === 'none') {
    qf.hidden = true;
  } else {
    qf.hidden = false;
    const r = $('#qualityRange');
    r.min = 1; r.max = 100; r.step = 1;
    const pref = qualityPref[f.id];
    r.value = pref != null ? pref : (f.default_quality || 80);
    $('#qualityLabelText').textContent = q.label || '质量';
    $('#qualityHint').textContent = (q.hint || '') +
      (q.max > q.min ? `（编码器取值 ${q.min}–${q.max}${q.invert ? '，越小越清晰' : ''}）` : '');
  }
  $('#qualityOut').textContent = $('#qualityRange').value;

  // 色度抽样：PNG/BMP/QOI 原生不适用；仅 yuv 类格式有意义
  const showChroma = ['jpg', 'webp', 'avif', 'heic', 'heif'].includes(f.id);
  $('#chromaField').hidden = !showChroma;
  if (showChroma) {
    const c = (state.opts.chromas || []).find(x => x.id === $('#chromaSel').value);
    $('#chromaHint').textContent = c ? c.label : '';
  }

  // 背景色：仅当输出格式不带 alpha 时有意义
  const needBg = !f.alpha;
  $('#bgField').hidden = !needBg;
  $('#bgColor').hidden = $('#bgSel').value !== 'custom';

  // 动画输出：仅当格式本身支持动画
  const canAnim = !!f.animated;
  const ac = $('#animatedChk');
  ac.disabled = !canAnim;
  ac.closest('.toggle').classList.toggle('muted', !canAnim);
  if (!canAnim) ac.checked = false;

  // 水印：输出无 alpha 时才有意义
  const wmG = $('#wmGroup');
  wmG.hidden = !needBg;
  if (needBg && !$('#wmEnabled').checked) syncWatermarkUI();

  const fmtHint = $('#formatHint');
  const bits = [];
  if (f.note) bits.push(f.note);
  if (!f.available) bits.push('⚠ ' + (f.reason || '本机不可用'));
  if (f.lossless) bits.push('无损');
  if (f.alpha) bits.push('支持透明');
  if (f.animated) bits.push('支持动画');
  fmtHint.textContent = bits.join(' · ');
  fmtHint.className = 'hint' + (f.available ? '' : ' bad');

  persistOptions();
}

function syncResizeUI() {
  const mode = $('#resizeSel').value;
  const modes = state.opts ? state.opts.resize_modes : [];
  const def = (modes.find(m => m.id === mode) || {}).name || '';
  $('#sizeField').hidden = !['long_edge', 'short_edge', 'width', 'height', 'percent'].includes(mode);
  const labelMap = { long_edge: '长边 (px)', short_edge: '短边 (px)', width: '宽度 (px)', height: '高度 (px)', percent: '百分比 (%)' };
  $('#sizeLabel').textContent = labelMap[mode] || '尺寸';
  if (mode === 'percent') { $('#sizeInput').max = 400; } else { $('#sizeInput').max = 20000; }
  $('#customField').hidden = mode !== 'exact';
  $('#adaptField').hidden = !['long_edge', 'short_edge', 'width', 'height', 'exact'].includes(mode);
  const a = (state.opts ? state.opts.adapts : []).find(x => x.id === $('#adaptSel').value);
  $('#adaptHint').textContent = a ? a.label : def;
  persistOptions();
}

function syncWatermarkUI() {
  const on = $('#wmEnabled').checked;
  $('#wmBody').classList.toggle('hide', !on);
  $('#wmPath').disabled = !on;
  persistOptions();
}

function collectOptions() {
  const wmOn = $('#wmEnabled').checked;
  const bg = $('#bgSel').value;
  return {
    format: $('#formatSel').value,
    quality: +$('#qualityRange').value,
    color_mode: $('#colorModeSel').value,
    chroma: $('#chromaSel').value,
    auto_orient: $('#autoOrient').checked,
    rotate: +$('#rotateSel').value,
    flip_h: $('#flipH').checked,
    flip_v: $('#flipV').checked,
    resize: $('#resizeSel').value,
    size: +$('#sizeInput').value || 0,
    custom_width: +$('#customW').value || 0,
    custom_height: +$('#customH').value || 0,
    adapt: $('#adaptSel').value,
    allow_upscale: $('#allowUpscale').checked,
    sharpen: +$('#sharpenRange').value,
    blur: +$('#blurRange').value,
    brightness: +$('#brightnessRange').value,
    contrast: +$('#contrastRange').value,
    saturation: +$('#saturationRange').value,
    background: bg === 'custom' ? $('#bgColor').value : bg,
    animated: $('#animatedChk').checked,
    strip_metadata: $('#stripMeta').checked,
    watermark: {
      enabled: wmOn,
      path: wmOn ? $('#wmPath').value.trim() : '',
      position: $('#wmPos').value,
      margin: +$('#wmMargin').value || 0,
      width: +$('#wmWidth').value || 0,
      opacity: +$('#wmOpacity').value || 100,
    },
    accel: $('#accelSel').value === 'auto' ? '' : $('#accelSel').value,
  };
}

function persistOptions() {
  LS.set('options', collectOptions());
  LS.set('accel_sel', $('#accelSel').value);
}

function updateAllOutputs() {
  for (const [id, out] of [['#qualityRange', '#qualityOut'], ['#sharpenRange', '#sharpenOut'], ['#blurRange', '#blurOut'],
    ['#brightnessRange', '#brightnessOut'], ['#contrastRange', '#contrastOut'], ['#saturationRange', '#saturationOut'],
    ['#wmOpacity', '#wmOpacityOut']]) {
    const e = $(out);
    if (e) e.textContent = $(id).value;
  }
  const a = state.opts ? state.opts.accels.find(x => x.id === $('#accelSel').value) : null;
  const ah = $('#accelHint');
  if (ah) ah.textContent = a ? (a.available ? `硬件编码器：${(a.encoders || []).join(' ') || '—'}` : (a.reason || '不可用')) : '按格式逐个判断，失败自动回退软件编码';
}

/* ==================== 目录浏览 ==================== */

async function loadDir(path) {
  try {
    const d = await api('/api/media/dir?path=' + encodeURIComponent(path || ''));
    state.cwd = d.path || '';
    state.parent = d.parent == null ? '' : d.parent;
    // 目录也渲染成卡片（fileCard 里的 is_dir 分支），否则子目录无法进入、
    // 新建的目录也会"凭空消失"。目录排在文件前面。
    state.files = (d.dirs || []).concat(d.files || []);
    for (const p of Array.from(state.sel)) {
      if (p !== state.cwd && !p.startsWith(state.cwd ? state.cwd + '/' : '')) state.sel.delete(p);
    }
    renderCrumbs();
    renderGrid();
    renderSelInfo();
  } catch (e) {
    toast('打开目录失败：' + e.message, 'err');
  }
}

function renderCrumbs() {
  const host = $('#crumbs');
  host.innerHTML = '';
  const segs = state.cwd ? state.cwd.split('/') : [];
  const rootBtn = document.createElement('button');
  rootBtn.textContent = '根目录';
  rootBtn.onclick = () => loadDir('');
  host.appendChild(rootBtn);
  let acc = '';
  segs.forEach((s, i) => {
    acc = acc ? acc + '/' + s : s;
    const p = acc;
    const sep = document.createElement('span');
    sep.className = 'sep';
    sep.textContent = '/';
    host.appendChild(sep);
    if (i === segs.length - 1) {
      const cur = document.createElement('span');
      cur.className = 'cur';
      cur.textContent = s;
      host.appendChild(cur);
    } else {
      const b = document.createElement('button');
      b.textContent = s;
      b.onclick = () => loadDir(p);
      host.appendChild(b);
    }
  });
}

function visibleFiles() {
  const q = $('#searchInput').value.trim().toLowerCase();
  let list = state.files.slice();
  if (q) list = list.filter(f => f.name.toLowerCase().includes(q));
  if ($('#onlySelected').checked) list = list.filter(f => state.sel.has(f.path));
  const s = $('#sortSel').value;
  list.sort((a, b) => {
    // 目录永远排在文件前面，不受排序方式影响
    if (!!a.is_dir !== !!b.is_dir) return a.is_dir ? -1 : 1;
    if (a.is_dir) return a.name.localeCompare(b.name, 'zh-Hans-CN');
    if (s === 'size_desc') return b.size - a.size;
    if (s === 'size_asc') return a.size - b.size;
    if (s === 'mtime_desc') return (b.mod_time || 0) - (a.mod_time || 0);
    return a.name.localeCompare(b.name, 'zh-Hans-CN');
  });
  return list;
}

function applyGrid(n) {
  const g = $('#grid');
  g.className = 'grid cols-' + n;
  $$('.viewmode .chip').forEach(b => b.classList.toggle('active', +b.dataset.grid === n));
  LS.set('grid', n);
}

function renderGrid() {
  const g = $('#grid');
  const list = visibleFiles();
  g.innerHTML = '';
  $('#empty').classList.toggle('hide', list.length > 0);
  if (!list.length) {
    $('#empty').classList.remove('hide');
    return;
  }
  const frag = document.createDocumentFragment();
  for (const f of list) {
    frag.appendChild(fileCard(f));
  }
  g.appendChild(frag);
  lazyThumbs(g);
}

function fileCard(f) {
  const isDir = !!f.is_dir;
  const sel = state.sel.has(f.path);
  const el = document.createElement('div');
  el.className = 'card' + (isDir ? ' dir' : '') + (sel ? ' sel' : '');
  el.dataset.path = f.path;
  el.title = f.name;

  const thumb = document.createElement('div');
  thumb.className = 'thumb';
  if (isDir) {
    thumb.innerHTML = '<span class="ph">📁</span>';
  } else {
    thumb.innerHTML = `<img loading="lazy" data-src="/api/media/thumb?path=${encodeURIComponent(f.path)}&size=480" alt="">
      <span class="ph" hidden>🖼</span>`;
  }
  el.appendChild(thumb);

  if (!isDir) {
    const tick = document.createElement('span');
    tick.className = 'tick';
    tick.textContent = '✓';
    el.appendChild(tick);

    const acts = document.createElement('div');
    acts.className = 'acts';
    acts.innerHTML = `
      <button data-act="preview" title="预览">⤢</button>
      <button data-act="download" title="下载">↓</button>
      <button data-act="rename" title="重命名">✎</button>
      <button data-act="delete" title="删除">🗑</button>`;
    el.appendChild(acts);
  }

  const info = document.createElement('div');
  info.className = 'info';
  info.innerHTML = `<div class="name"></div><div class="sub">${isDir ? '目录' : fmtSize(f.size)}</div>`;
  info.querySelector('.name').textContent = f.name;
  el.appendChild(info);

  el.addEventListener('click', async (e) => {
    const act = e.target.closest('.acts button');
    if (act) {
      e.stopPropagation();
      await cardAction(act.dataset.act, f);
      return;
    }
    if (e.shiftKey || e.metaKey || e.ctrlKey) { toggleSel(f.path); return; }
    if (isDir) { loadDir(f.path); return; }
    toggleSel(f.path);
  });
  el.addEventListener('dblclick', (e) => {
    if (e.target.closest('.acts')) return;
    if (isDir) loadDir(f.path); else openViewer(f.path);
  });
  el.addEventListener('contextmenu', (e) => {
    e.preventDefault();
    if (!state.sel.has(f.path)) { state.sel.clear(); state.sel.add(f.path); renderGrid(); }
    showContextMenu(e, f);
  });
  return el;
}

function lazyThumbs(scope) {
  const imgs = $$('img[data-src]', scope);
  if (!imgs.length) return;
  const load = (img) => {
    img.src = img.dataset.src;
    img.onload = () => { img.nextElementSibling && (img.nextElementSibling.hidden = true); };
    img.onerror = () => { img.hidden = true; const ph = img.nextElementSibling; if (ph) ph.hidden = false; };
  };
  if (!('IntersectionObserver' in window)) { imgs.forEach(load); return; }
  const io = new IntersectionObserver((ents) => {
    for (const en of ents) {
      if (en.isIntersecting) { load(en.target); io.unobserve(en.target); }
    }
  }, { root: $('.grid-wrap'), rootMargin: '400px' });
  imgs.forEach(i => io.observe(i));
}

function toggleSel(path) {
  // 目录不可参与批量转换：从当前列表里剔除所有目录后再切换
  if (!state.files.some(f => f.path === path && f.is_dir)) {
    if (state.sel.has(path)) state.sel.delete(path); else state.sel.add(path);
  }
  const card = $(`.card[data-path="${CSS.escape(path)}"]`);
  if (card) card.classList.toggle('sel', state.sel.has(path));
  renderSelInfo();
  updateEstimate();
}

function renderSelInfo() {
  const n = state.sel.size;
  $('#selCount').textContent = n;
  const box = $('#selInfo');
  box.hidden = n === 0;
  const body = $('#selInfoBody');
  if (!n) { body.innerHTML = ''; return; }
  const files = state.files.filter(f => state.sel.has(f.path));
  const total = files.reduce((a, f) => a + (f.size || 0), 0);
  const ex = new Set(files.map(f => extOf(f.name)).filter(Boolean));
  const f0 = files[0];
  body.innerHTML = `
    <span>张数</span><b>${n}</b>
    <span>总体积</span><b>${fmtSize(total)}</b>
    <span>格式</span><b>${[...ex].join('、') || '—'}</b>
    ${files.length === 1 && f0 ? `<span>单张大小</span><b>${fmtSize(f0.size)}</b>` : ''}`;
  updateEstimate();
}

async function cardAction(act, f) {
  try {
    if (act === 'preview') openViewer(f.path);
    else if (act === 'download') location.href = '/api/media/download?path=' + encodeURIComponent(f.path);
    else if (act === 'rename') {
      const base = f.name.replace(/\.[^.]+$/, '');
      const v = await dialog({ title: '重命名', body: inputField('新名称', 'name', base), okText: '重命名' });
      if (!v || !v.name) return;
      await api('/api/media/rename', { method: 'POST', body: JSON.stringify({ path: f.path, new_name: v.name }) });
      toast('已重命名为 ' + v.name, 'ok');
      loadDir(state.cwd);
    } else if (act === 'delete') {
      const ok = await dialog({ title: '删除确认', body: `确定要删除 <b>${esc(f.name)}</b> 吗？此操作不可恢复。`, okText: '删除' });
      if (!ok) return;
      await api('/api/media/delete', { method: 'POST', body: JSON.stringify({ path: f.path }) });
      toast('已删除 ' + f.name, 'ok');
      state.sel.delete(f.path);
      loadDir(state.cwd);
    }
  } catch (e) {
    toast('操作失败：' + e.message, 'err');
  }
}

function showContextMenu(e, f) {
  const n = state.sel.size;
  const items = [
    { t: `已选 ${n} 项`, d: true },
    { t: '预览', fn: () => openViewer(f.path) },
    { t: '下载所选', fn: () => { for (const p of state.sel) location.href = '/api/media/download?path=' + encodeURIComponent(p); } },
    { t: '复制绝对路径', fn: async () => {
        try { await navigator.clipboard.writeText(f.path); toast('已复制路径', 'ok'); }
        catch { toast('剪贴板不可用', 'warn'); }
      } },
    { t: '重命名', fn: () => cardAction('rename', f) },
    { t: '删除所选', d: true, fn: async () => {
        const ok = await dialog({ title: '删除确认', body: `确定删除选中的 ${n} 项吗？不可恢复。`, okText: '删除' });
        if (!ok) return;
        let bad = 0;
        for (const p of state.sel) {
          try { await api('/api/media/delete', { method: 'POST', body: JSON.stringify({ path: p }) }); }
          catch { bad++; }
        }
        state.sel.clear();
        toast(bad ? `${bad} 项删除失败` : '已删除', bad ? 'warn' : 'ok');
        loadDir(state.cwd);
      } },
  ];
  const menu = document.createElement('div');
  menu.className = 'ctx';
  menu.style.cssText = `position:fixed;z-index:80;min-width:180px;background:var(--panel);border:1px solid var(--line);
    border-radius:9px;box-shadow:var(--shadow);padding:5px;left:${Math.min(e.clientX, innerWidth - 195)}px;top:${Math.min(e.clientY, innerHeight - 210)}px`;
  for (const it of items) {
    const b = document.createElement('div');
    b.textContent = it.t;
    b.style.cssText = `padding:7px 10px;border-radius:6px;font-size:12.5px;cursor:${it.d && !it.fn ? 'default' : 'pointer'};
      color:${it.d && !it.fn ? 'var(--text-mute)' : 'var(--text)'}`;
    if (it.fn && !(it.d && !it.fn)) b.onclick = () => { menu.remove(); it.fn(); };
    menu.appendChild(b);
  }
  document.body.appendChild(menu);
  const close = (ev) => { if (!menu.contains(ev.target)) { menu.remove(); document.removeEventListener('mousedown', close); } };
  setTimeout(() => document.addEventListener('mousedown', close), 0);
}

/* ==================== 预估 ==================== */

const doEstimate = debounce(async () => {
  const paths = Array.from(state.sel).filter(p => state.files.some(f => f.path === p));
  if (!paths.length) { $('#estBox').hidden = true; return; }
  const seq = ++state.estimateSeq;
  try {
    const r = await api('/api/estimate', { method: 'POST', body: JSON.stringify({ options: collectOptions(), path: paths[0] }) });
    if (seq !== state.estimateSeq) return;
    $('#estBox').hidden = false;
    $('#estSize').textContent = r.text || fmtSize(r.bytes);
    $('#estDim').textContent = fmtDim(r.width, r.height);
    const f0 = state.files.find(f => f.path === paths[0]);
    const total = paths.reduce((a, p) => { const f = state.files.find(x => x.path === p); return a + (f ? f.size : 0); }, 0);
    $('#estSrc').textContent = total > 0 ? `${fmtSize(total)}（${paths.length} 张）` : (f0 ? fmtSize(f0.size) : '—');
  } catch { $('#estBox').hidden = true; }
}, 260);

function updateEstimate() { doEstimate(); }

/* ==================== 转换 ==================== */

async function startConvert() {
  const paths = Array.from(state.sel);
  if (!paths.length) { toast('请先选择图片', 'warn'); return; }
  const btn = $('#convertBtn');
  btn.disabled = true;
  try {
    const r = await api('/api/tasks', { method: 'POST', body: JSON.stringify({ inputs: paths, options: collectOptions() }) });
    toast(`已加入队列：${r.count} 个任务（批次 ${r.batch}）`, 'ok');
    switchView('queue');
    await refreshTasks();
  } catch (e) {
    toast('提交失败：' + e.message, 'err', 6000);
  } finally {
    btn.disabled = false;
  }
}

async function showCommand() {
  const paths = Array.from(state.sel);
  try {
    const r = await api('/api/preview', {
      method: 'POST',
      body: JSON.stringify({ path: paths[0] || '', options: collectOptions() }),
    });
    await dialog({ title: '将要执行的命令', body: r.command, okText: '复制', cancelText: '关闭' });
    try { await navigator.clipboard.writeText(r.command); toast('已复制到剪贴板', 'ok'); } catch {}
  } catch (e) {
    toast('无法生成命令：' + e.message, 'err');
  }
}

/* ==================== 任务队列 ==================== */

async function refreshTasks() {
  if (state.pollBusy) return;
  state.pollBusy = true;
  try {
    const r = await api('/api/tasks');
    state.tasks = r.tasks || [];
    renderTasks();
  } catch { /* 轮询失败静默 */ } finally {
    state.pollBusy = false;
  }
}

function renderStats(st) {
  const host = $('#statsBar');
  if (!st) {
    const c = { queued: 0, running: 0, completed: 0, failed: 0, cancelled: 0 };
    for (const t of state.tasks) if (t.status in c) c[t.status]++;
    st = c;
  }
  host.innerHTML = ['queued', 'running', 'completed', 'failed', 'cancelled']
    .map(k => `<div class="stat ${k}"><b>${st[k] || 0}</b>${({ queued: '排队', running: '进行中', completed: '成功', failed: '失败', cancelled: '取消' })[k]}</div>`)
    .join('');
  const active = (st.queued || 0) + (st.running || 0);
  const pill = $('#queueCount');
  pill.textContent = active;
  pill.classList.toggle('hide', !active);
}

function renderTasks() {
  const filter = $('#filterSel').value;
  const list = state.tasks.filter(t => filter === 'all' || t.status === filter)
    .sort((a, b) => new Date(b.created_at) - new Date(a.created_at));
  renderStats();
  const host = $('#taskList');
  host.innerHTML = '';
  $('#queueEmpty').classList.toggle('hide', list.length > 0);
  for (const t of list) host.appendChild(taskCard(t));
}

function taskCard(t) {
  const el = document.createElement('div');
  el.className = 'task ' + t.status;
  const total = t.outputs ? t.outputs.length : 0;
  const doneN = (t.outputs || []).filter(o => o.done).length;
  const running = t.status === 'running';
  const indet = running && (t.progress < 0);
  const pct = t.status === 'completed' ? 100 : Math.max(0, Math.min(100, t.progress || 0));

  const stageText = running
    ? (indet ? (t.stage || '处理中') + `（${doneN}/${total}）` : `${Math.round(pct)}% · ${t.stage || '处理中'}`)
    : (t.stage || ({ completed: '已完成', failed: '失败', cancelled: '已取消', queued: '排队中' })[t.status] || t.status);

  el.innerHTML = `
    <div class="task-head">
      <span class="nm"></span>
      <span class="muted">→</span>
      <span class="st ${t.status}">${({ queued: '排队中', running: '进行中', completed: '已完成', failed: '失败', cancelled: '已取消' })[t.status] || t.status}</span>
      <div class="task-acts"></div>
    </div>
    <div class="bar ${indet ? 'indet' : ''}"><i style="width:${pct}%"></i></div>
    <div class="meta">
      <span class="stage"></span>
      <span>${fmtDim(t.source_width, t.source_height)}</span>
      <span>${(t.format || '').toUpperCase()} q${t.quality}</span>
      ${t.variants && t.variants.length > 1 ? `<span>${t.variants.length} 个变体</span>` : ''}
      ${t.duration_sec ? `<span>耗时 ${fmtDuration(t.duration_sec)}</span>` : ''}
      <span>${new Date(t.created_at).toLocaleString('zh-CN', { hour12: false })}</span>
    </div>
    <div class="outs"></div>
    ${t.error ? '<div class="err"></div>' : ''}`;

  el.querySelector('.nm').textContent = t.input_name || t.input;
  el.querySelector('.stage').textContent = stageText;
  if (t.error) el.querySelector('.err').textContent = t.error;

  const outs = el.querySelector('.outs');
  for (const o of t.outputs || []) {
    if (!o.path) continue;
    const c = document.createElement('a');
    c.className = 'out-chip';
    c.href = '/api/media/download?path=' + encodeURIComponent(o.path);
    c.title = o.path;
    c.innerHTML = `<span>${esc(o.variant ? o.variant + ' · ' : '')}${esc(o.name)}</span><span class="sz">${o.done ? fmtSize(o.size) : '…'}</span>`;
    outs.appendChild(c);
  }
  if (!outs.children.length && total) outs.innerHTML = '<span class="muted xs">无产物</span>';

  const acts = el.querySelector('.task-acts');
  const addBtn = (text, fn, cls = 'chip') => {
    const b = document.createElement('button');
    b.className = cls;
    b.textContent = text;
    b.onclick = fn;
    acts.appendChild(b);
  };
  if (running || t.status === 'queued') addBtn('取消', async () => {
    try { await api(`/api/tasks/${t.id}/cancel`, { method: 'POST' }); toast('已请求取消', 'ok'); refreshTasks(); }
    catch (e) { toast('取消失败：' + e.message, 'err'); }
  });
  if (t.status === 'failed' || t.status === 'cancelled') addBtn('重试', async () => {
    try { await api(`/api/tasks/${t.id}/retry`, { method: 'POST' }); toast('已重新排队', 'ok'); refreshTasks(); }
    catch (e) { toast('重试失败：' + e.message, 'err'); }
  });
  if (t.command) addBtn('命令', async () => { await dialog({ title: '实际执行的命令', body: t.command, okText: '复制', cancelText: '关闭' }); });
  if (!running && t.status !== 'queued') addBtn('删除', async () => {
    try { await api('/api/tasks/' + t.id, { method: 'DELETE' }); refreshTasks(); }
    catch (e) { toast('删除失败：' + e.message, 'err'); }
  }, 'chip');
  return el;
}

/* ==================== 预设 ==================== */

async function loadPresets() {
  try {
    const r = await api('/api/presets');
    renderPresets(r.presets || []);
  } catch (e) {
    toast('读取预设失败：' + e.message, 'err');
  }
}

function renderPresets(list) {
  const host = $('#presetGrid');
  host.innerHTML = '';
  for (const p of list) {
    const o = p.options || {};
    const f = state.opts ? state.opts.formats.find(x => x.id === o.format) : null;
    const el = document.createElement('div');
    el.className = 'preset' + (p.built_in ? ' builtin' : '');
    const tags = [];
    if (f) tags.push(`<span class="tag fmt">${esc(f.name)}</span>`);
    if (o.quality) tags.push(`<span class="tag">质量 ${o.quality}</span>`);
    const rm = (state.opts ? state.opts.resize_modes : []).find(x => x.id === o.resize);
    if (rm && o.resize !== 'keep') tags.push(`<span class="tag">${esc(rm.label || rm.name)}${o.size ? ' ' + o.size : ''}</span>`);
    if (o.color_mode && o.color_mode !== 'keep') tags.push(`<span class="tag">${esc(o.color_mode)}</span>`);
    if (o.rotate) tags.push(`<span class="tag">旋转 ${o.rotate}°</span>`);
    if (o.sharpen) tags.push(`<span class="tag">锐化</span>`);
    if (o.blur) tags.push(`<span class="tag">模糊</span>`);
    if (o.watermark && o.watermark.enabled) tags.push(`<span class="tag">水印</span>`);
    if (o.animated) tags.push('<span class="tag">动画</span>');

    el.innerHTML = `
      <div class="ph"><b></b>${p.built_in ? '<span class="tag">内置</span>' : ''}</div>
      <div class="desc"></div>
      <div class="tags">${tags.join('')}</div>
      <div class="pf"></div>`;
    el.querySelector('b').textContent = p.name;
    el.querySelector('.desc').textContent = p.description || '（无描述）';

    const pf = el.querySelector('.pf');
    const apply = document.createElement('button');
    apply.className = 'chip primary grow';
    apply.textContent = '应用';
    apply.onclick = () => { applyPreset(o); toast('已套用预设：' + p.name, 'ok'); };
    pf.appendChild(apply);
    if (!p.built_in) {
      const del = document.createElement('button');
      del.className = 'chip danger';
      del.textContent = '删除';
      del.onclick = async () => {
        const ok = await dialog({ title: '删除预设', body: `确定删除预设 <b>${esc(p.name)}</b>？`, okText: '删除' });
        if (!ok) return;
        try { await api('/api/presets/' + encodeURIComponent(p.name), { method: 'DELETE' }); loadPresets(); }
        catch (e) { toast('删除失败：' + e.message, 'err'); }
      };
      pf.appendChild(del);
    }
    host.appendChild(el);
  }
}

function applyPreset(o) {
  if (o.format) $('#formatSel').value = o.format;
  if (o.quality) $('#qualityRange').value = o.quality;
  if (o.color_mode) $('#colorModeSel').value = o.color_mode;
  if (o.chroma) $('#chromaSel').value = o.chroma;
  if (o.auto_orient != null) $('#autoOrient').checked = !!o.auto_orient;
  if (o.rotate != null) $('#rotateSel').value = o.rotate;
  $('#flipH').checked = !!o.flip_h;
  $('#flipV').checked = !!o.flip_v;
  if (o.resize) $('#resizeSel').value = o.resize;
  if (o.size) $('#sizeInput').value = o.size;
  if (o.custom_width) $('#customW').value = o.custom_width;
  if (o.custom_height) $('#customH').value = o.custom_height;
  if (o.adapt) $('#adaptSel').value = o.adapt;
  $('#allowUpscale').checked = !!o.allow_upscale;
  for (const [id, k] of [['#sharpenRange', 'sharpen'], ['#blurRange', 'blur'], ['#brightnessRange', 'brightness'],
    ['#contrastRange', 'contrast'], ['#saturationRange', 'saturation']]) {
    $(id).value = o[k] || 0;
  }
  if (o.background) {
    const isHex = String(o.background).startsWith('#');
    $('#bgSel').value = isHex ? 'custom' : o.background;
    if (isHex) $('#bgColor').value = o.background;
  }
  $('#stripMeta').checked = o.strip_metadata !== false;
  $('#animatedChk').checked = !!o.animated;
  const w = o.watermark || {};
  $('#wmEnabled').checked = !!w.enabled;
  $('#wmPath').value = w.path || '';
  $('#wmPos').value = w.position || 'bottom_right';
  $('#wmMargin').value = w.margin || 16;
  $('#wmWidth').value = w.width || 0;
  $('#wmOpacity').value = w.opacity || 80;
  syncFormatUI();
  syncResizeUI();
  syncWatermarkUI();
  updateAllOutputs();
  persistOptions();
  updateEstimate();
  switchView('browser');
}

async function savePreset() {
  const v = await dialog({
    title: '保存为预设',
    body: (() => {
      const w = document.createElement('div');
      w.style.cssText = 'display:flex;flex-direction:column;gap:10px';
      w.appendChild(inputField('预设名称', 'name', ''));
      w.appendChild(inputField('描述（可选）', 'description', ''));
      return w;
    })(),
    okText: '保存',
  });
  if (!v || !v.name) return;
  try {
    await api('/api/presets', {
      method: 'POST',
      body: JSON.stringify({ name: v.name, description: v.description || '', options: collectOptions() }),
    });
    toast('预设已保存：' + v.name, 'ok');
    loadPresets();
  } catch (e) {
    toast('保存失败：' + e.message, 'err');
  }
}

/* ==================== 预览器 ==================== */

function openViewer(path) {
  state.viewerList = Array.from(state.sel).filter(p => p !== path);
  state.viewerList.unshift(path);
  state.viewerIdx = 0;
  $('#viewer').hidden = false;
  showViewerImage();
  loadViewerMeta(path);
}

function showViewerImage() {
  const p = state.viewerList[state.viewerIdx];
  if (!p) return;
  $('#viewerName').textContent = p.split('/').pop();
  const img = $('#viewerImg');
  img.src = '/api/media/raw?path=' + encodeURIComponent(p);
  $('#viewerDownload').href = '/api/media/download?path=' + encodeURIComponent(p);
  $('#viewerMeta').textContent = '加载中…';
}

let metaSeq = 0;
async function loadViewerMeta(path) {
  const seq = ++metaSeq;
  try {
    const i = await api('/api/media/info?path=' + encodeURIComponent(path));
    if (seq !== metaSeq) return;
    $('#viewerMeta').innerHTML = '';
    const bits = [
      fmtDim(i.width, i.height),
      i.format_name, i.pix_fmt + (i.bit_depth ? ' ' + i.bit_depth + 'bit' : ''),
      fmtSize(i.size), i.duration ? '时长 ' + fmtDuration(i.duration) : '',
      i.animated ? '动画 ' + (i.frames || '') + ' 帧' : '',
      i.alpha ? '含透明' : '',
      i.orientation_label || '',
      i.has_metadata ? '含元数据' : '无元数据',
    ].filter(Boolean);
    $('#viewerMeta').textContent = bits.join('  ·  ');
  } catch (e) {
    $('#viewerMeta').textContent = '读取信息失败：' + e.message;
  }
}

function stepViewer(d) {
  if (state.viewerList.length < 2) return;
  state.viewerIdx = (state.viewerIdx + d + state.viewerList.length) % state.viewerList.length;
  showViewerImage();
  loadViewerMeta(state.viewerList[state.viewerIdx]);
}

/* ==================== 上传 / 目录操作 ==================== */

async function doUpload(files) {
  if (!files || !files.length) return;
  const fd = new FormData();
  for (const f of files) fd.append('files', f, f.name);
  toast(`正在上传 ${files.length} 个文件…`);
  try {
    const r = await api('/api/media/upload', { method: 'POST', body: fd });
    if (r.failed && r.failed.length) toast(`${r.failed.length} 个文件上传失败：${r.failed[0]}`, 'warn', 6000);
    else toast(`上传成功 ${r.uploaded} 个文件`, 'ok');
    if (r.files && r.files.length) {
      const dir = state.opts && state.opts.upload_dir ? state.opts.upload_dir : '';
      await loadDir(dir);
      for (const f of r.files) state.sel.add(f.path);
      renderGrid();
      renderSelInfo();
    } else {
      loadDir(state.cwd);
    }
  } catch (e) {
    toast('上传失败：' + e.message, 'err', 6000);
  }
}

async function makeDir() {
  const v = await dialog({ title: '新建目录', body: inputField('目录名称', 'name', ''), okText: '创建' });
  if (!v || !v.name) return;
  try {
    await api('/api/media/mkdir', { method: 'POST', body: JSON.stringify({ path: state.cwd, name: v.name }) });
    toast('已创建 ' + v.name, 'ok');
    loadDir(state.cwd);
  } catch (e) {
    toast('创建失败：' + e.message, 'err');
  }
}

/* ==================== 视图切换 / 快捷键 ==================== */

function switchView(v) {
  state.view = v;
  $$('.tab').forEach(t => t.classList.toggle('active', t.dataset.view === v));
  $$('.view').forEach(s => s.classList.toggle('active', s.id === 'view-' + v));
  if (v === 'queue') refreshTasks();
  if (v === 'presets') loadPresets();
}

function bindStaticEvents() {
  $$('.tab').forEach(t => t.onclick = () => switchView(t.dataset.view));
  $('#themeBtn').onclick = () => applyTheme(document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark');
  $('#helpBtn').onclick = showHelp;

  $('#upBtn').onclick = () => state.parent != null ? loadDir(state.parent) : loadDir('');
  $('#mkDirBtn').onclick = makeDir;
  $('#refreshBtn').onclick = () => loadDir(state.cwd);
  $('#uploadBtn').onclick = () => $('#uploadInput').click();
  $('#uploadInput').onchange = (e) => { doUpload(e.target.files); e.target.value = ''; };
  $('#selAllBtn').onclick = () => { visibleFiles().forEach(f => { if (f.type !== 'dir') state.sel.add(f.path); }); renderGrid(); renderSelInfo(); };
  $('#selNoneBtn').onclick = () => { state.sel.clear(); renderGrid(); renderSelInfo(); };
  $('#selInvBtn').onclick = () => { visibleFiles().forEach(f => { if (f.type !== 'dir' && state.sel.has(f.path)) state.sel.delete(f.path); else if (f.type !== 'dir') state.sel.add(f.path); }); renderGrid(); renderSelInfo(); };
  $('#searchInput').oninput = debounce(renderGrid, 180);
  $('#onlySelected').onchange = renderGrid;
  $('#sortSel').onchange = renderGrid;
  $$('.viewmode .chip').forEach(b => b.onclick = () => applyGrid(+b.dataset.grid));

  $('#formatSel').onchange = syncFormatUI;
  $('#chromaSel').onchange = () => { updateChromaHint(); persistOptions(); };
  $('#colorModeSel').onchange = persistOptions;
  $('#bgSel').onchange = () => { $('#bgColor').hidden = $('#bgSel').value !== 'custom'; syncFormatUI(); };
  $('#bgColor').oninput = syncFormatUI;
  $('#resizeSel').onchange = syncResizeUI;
  $('#adaptSel').onchange = syncResizeUI;
  $('#sizeInput').oninput = debounce(() => { persistOptions(); updateEstimate(); }, 250);
  $('#customW').oninput = $('#customH').oninput = debounce(() => { persistOptions(); updateEstimate(); }, 250);
  $('#accelSel').onchange = () => { updateAllOutputs(); persistOptions(); };
  $('#rotateSel').onchange = persistOptions;
  for (const id of ['#autoOrient', '#flipH', '#flipV', '#stripMeta', '#allowUpscale', '#animatedChk']) {
    $(id).onchange = () => { syncFormatUI(); persistOptions(); };
  }
  for (const [id, out] of [['#qualityRange', '#qualityOut'], ['#sharpenRange', '#sharpenOut'], ['#blurRange', '#blurOut'],
    ['#brightnessRange', '#brightnessOut'], ['#contrastRange', '#contrastOut'], ['#saturationRange', '#saturationOut'],
    ['#wmOpacity', '#wmOpacityOut']]) {
    $(id).oninput = () => {
      if (id === '#qualityRange') { const f = currentFormat(); if (f) qualityPref[f.id] = +$(id).value; }
      updateAllOutputs(); persistOptions(); updateEstimate();
    };
  }
  for (const id of ['#wmEnabled']) $(id).onchange = syncWatermarkUI;
  for (const id of ['#wmPath', '#wmPos', '#wmMargin', '#wmWidth']) $(id).oninput = debounce(persistOptions, 300);

  $('#convertBtn').onclick = startConvert;
  $('#previewBtn').onclick = showCommand;
  $('#presetPickBtn').onclick = async () => { switchView('presets'); };
  $('#savePresetBtn').onclick = savePreset;
  $('#filterSel').onchange = renderTasks;
  $('#clearBtn').onclick = async () => {
    const ok = await dialog({ title: '清理任务记录', body: '将删除所有已结束的任务记录（不影响磁盘上的图片）。', okText: '清理' });
    if (!ok) return;
    try { await api('/api/tasks', { method: 'DELETE' }); toast('已清理', 'ok'); refreshTasks(); }
    catch (e) { toast('清理失败：' + e.message, 'err'); }
  };

  $('#viewerClose').onclick = () => { $('#viewer').hidden = true; $('#viewerImg').src = ''; };
  $('#viewerPrev').onclick = () => stepViewer(-1);
  $('#viewerNext').onclick = () => stepViewer(1);
  $('#viewer').onclick = (e) => { if (e.target === $('#viewer')) $('#viewerClose').click(); };

  document.addEventListener('keydown', (e) => {
    const typing = /^(INPUT|TEXTAREA|SELECT)$/.test(document.activeElement.tagName);
    if (!$('#viewer').hidden) {
      if (e.key === 'Escape') $('#viewerClose').click();
      if (e.key === 'ArrowLeft') stepViewer(-1);
      if (e.key === 'ArrowRight') stepViewer(1);
      return;
    }
    if (typing) return;
    if (e.key === '/') { e.preventDefault(); $('#searchInput').focus(); }
    else if (e.key === 'Enter' && state.sel.size && state.view === 'browser') startConvert();
    else if (e.key === 'a' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); $('#selAllBtn').click(); }
    else if (e.key === 'Escape') { state.sel.clear(); renderGrid(); renderSelInfo(); }
    else if (e.key === 'Backspace' && state.view === 'browser') { e.preventDefault(); $('#upBtn').click(); }
  });

  window.addEventListener('beforeunload', (e) => {
    if (state.sel.size && state.view === 'browser') { /* 允许直接关闭 */ }
  });
  window.addEventListener('resize', debounce(() => { if (state.view === 'browser') renderGrid(); }, 200));
}

function updateChromaHint() {
  const c = (state.opts ? state.opts.chromas : []).find(x => x.id === $('#chromaSel').value);
  $('#chromaHint').textContent = c ? c.label : '';
}

async function showHelp() {
  await dialog({
    title: '使用说明',
    hideCancel: true,
    okText: '知道了',
    body: `<div style="font-size:12.5px;line-height:1.8">
      <b>选择图片</b>：单击卡片选中，Shift/⌘ 单击可多选，右键打开更多操作，双击直接预览。<br>
      <b>批量转换</b>：选中后点右下角「开始转换」，或在浏览器视图按 <code>Enter</code>。<br>
      <b>参数</b>：右侧面板所有设置会记住；「查看命令」可看到实际执行的 ffmpeg 命令。<br>
      <b>快捷键</b>：<code>/</code> 搜索，<code>⌘/Ctrl+A</code> 全选，<code>Esc</code> 清空选择，<code>Backspace</code> 上一级，<code>←/→</code> 预览时切换。<br>
      <b>预设</b>：在「预设」页保存当前参数，之后一键套用；内置预设不可修改。<br>
      <b>硬件加速</b>：顶部徽标显示本机自检通过的编码器，格式不支持时自动回退软件编码。
    </div>`,
  });
}

boot();
