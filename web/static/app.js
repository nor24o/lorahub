/* LoRaHub console — core: DOM helpers, API client, router, shared components. */
'use strict';

// ---------- DOM helper ----------
function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  if (attrs) {
    for (const [k, v] of Object.entries(attrs)) {
      if (v === undefined || v === null || v === false) continue;
      if (k === 'class') el.className = v;
      else if (k === 'style' && typeof v === 'object') Object.assign(el.style, v);
      else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
      else if (k === 'html') el.innerHTML = v;
      else if (k === 'value') el.value = v;
      else if (k === 'checked') el.checked = !!v;
      else if (v === true) el.setAttribute(k, '');
      else el.setAttribute(k, v);
    }
  }
  for (const c of kids.flat(Infinity)) {
    if (c === undefined || c === null || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}
const $ = (sel, root = document) => root.querySelector(sel);

// ---------- icons (inline SVG paths) ----------
const ICONS = {
  home: 'M3 10.5 12 3l9 7.5V21h-6v-6H9v6H3z',
  app: 'M4 4h7v7H4zM13 4h7v7h-7zM4 13h7v7H4zM13 13h7v7h-7z',
  gateway: 'M12 13v8M8 21h8M5.6 8.6a9 9 0 0 1 12.8 0M8.5 11.4a5 5 0 0 1 7 0M12 13.5a.5.5 0 1 0 0-1 .5.5 0 0 0 0 1z',
  live: 'M3 12h4l3-8 4 16 3-8h4',
  code: 'M8 7l-5 5 5 5M16 7l5 5-5 5M14 4l-4 16',
  chip: 'M7 7h10v10H7zM9 3v4M15 3v4M9 17v4M15 17v4M3 9h4M3 15h4M17 9h4M17 15h4',
  gear: 'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z',
  device: 'M7 3h10v18H7zM11 18h2',
  plus: 'M12 5v14M5 12h14',
  copy: 'M9 9h11v11H9zM5 15H4V4h11v1',
  eye: 'M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12zM12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6z',
  refresh: 'M20 11a8 8 0 0 0-14.9-3M4 4v4h4M4 13a8 8 0 0 0 14.9 3M20 20v-4h-4',
  pause: 'M8 5v14M16 5v14',
  play: 'M7 5l12 7-12 7z',
  trash: 'M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13',
  download: 'M12 4v11M7 10l5 5 5-5M5 20h14',
  menu: 'M4 6h16M4 12h16M4 18h16',
  moon: 'M21 13A9 9 0 1 1 11 3a7 7 0 0 0 10 10z',
  logout: 'M15 4h4v16h-4M10 8l-4 4 4 4M6 12h11',
  send: 'M4 12l16-8-6 16-3-7z',
  wand: 'M5 19 19 5M15 4v3M18 7h3M4 15h3M7 12v3',
  key: 'M15 9a4 4 0 1 1-3.4 6.1L8 18.7V21H5v-3l5-5A4 4 0 0 1 15 9z',
  link: 'M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1',
  inbox: 'M3 13h5l2 3h4l2-3h5M5 5h14l2 8v6H3v-6z',
};
function icon(name) {
  const s = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  s.setAttribute('viewBox', '0 0 24 24'); s.setAttribute('fill', 'none'); s.setAttribute('stroke', 'currentColor');
  s.setAttribute('stroke-width', '1.8'); s.setAttribute('stroke-linecap', 'round'); s.setAttribute('stroke-linejoin', 'round');
  const p = document.createElementNS('http://www.w3.org/2000/svg', 'path'); p.setAttribute('d', ICONS[name] || ''); s.append(p);
  return s;
}
const LOGO = () => {
  const d = h('span'); d.innerHTML = '<svg viewBox="0 0 32 32"><rect width="32" height="32" rx="7" fill="#1a73e8"/><path d="M9 22a10 10 0 0 1 14 0M12 19a6 6 0 0 1 8 0M6 25a14 14 0 0 1 20 0" stroke="#fff" stroke-width="2.2" fill="none" stroke-linecap="round" opacity=".95"/><circle cx="16" cy="24" r="2" fill="#fff"/></svg>';
  return d.firstChild;
};

// ---------- API ----------
async function api(method, path, body) {
  const opt = { method, headers: {}, credentials: 'same-origin' };
  if (body !== undefined) { opt.headers['Content-Type'] = 'application/json'; opt.body = JSON.stringify(body); }
  const r = await fetch('/api' + path, opt);
  let data = null;
  try { data = await r.json(); } catch (e) { /* empty */ }
  if (r.status === 401 && !path.startsWith('/auth')) { App.showAuth(); throw new Error('authentication required'); }
  if (!r.ok) throw new Error((data && data.error) || r.statusText);
  return data;
}
const GET = (p) => api('GET', p);

// ---------- feedback ----------
function toast(msg, isErr) {
  const t = h('div', { class: 'toast' + (isErr ? ' err' : '') }, msg);
  $('#toasts').append(t);
  setTimeout(() => t.remove(), isErr ? 6000 : 3000);
}
function errToast(e) { toast(e.message || String(e), true); }
function confirmModal(title, text, okLabel = 'Delete', danger = true) {
  return new Promise((resolve) => {
    const close = (v) => { bg.remove(); resolve(v); };
    const bg = h('div', { class: 'modal-bg', onclick: (e) => { if (e.target === bg) close(false); } },
      h('div', { class: 'modal' },
        h('div', { class: 'card-head' }, h('h2', null, title)),
        h('div', { class: 'card-body' }, h('p', null, text),
          h('div', { class: 'form-actions', style: { justifyContent: 'flex-end', marginTop: '14px' } },
            h('button', { class: 'btn', onclick: () => close(false) }, 'Cancel'),
            h('button', { class: 'btn ' + (danger ? 'danger' : 'primary'), onclick: () => close(true) }, okLabel)))));
    document.body.append(bg);
  });
}
async function copyText(t) {
  try { await navigator.clipboard.writeText(t); toast('Copied to clipboard'); }
  catch (e) { const ta = h('textarea', { value: t }); document.body.append(ta); ta.select(); document.execCommand('copy'); ta.remove(); toast('Copied'); }
}

// ---------- formatting ----------
function ago(ts) {
  if (!ts) return 'never';
  const s = Math.round((Date.now() - new Date(ts).getTime()) / 1000);
  if (s < 5) return 'just now';
  if (s < 60) return s + 's ago';
  if (s < 3600) return Math.floor(s / 60) + ' min ago';
  if (s < 86400) return Math.floor(s / 3600) + ' h ago';
  return Math.floor(s / 86400) + ' d ago';
}
function fmtTime(ts) { const d = new Date(ts); return d.toLocaleTimeString([], { hour12: false }); }
function fmtDate(ts) { return ts ? new Date(ts).toLocaleString([], { hour12: false }) : '—'; }
function fmtDur(s) { if (s == null) return '—'; const d = Math.floor(s / 86400), hh = Math.floor(s % 86400 / 3600), m = Math.floor(s % 3600 / 60); return (d ? d + 'd ' : '') + (hh ? hh + 'h ' : '') + m + 'm'; }
function signalBars(snr) {
  const lvl = snr == null ? 0 : snr > 5 ? 4 : snr > 0 ? 3 : snr > -7 ? 2 : snr > -15 ? 1 : 0;
  return h('span', { class: 'signal', title: 'SNR ' + snr + ' dB' }, [4, 7, 10, 14].map((ht, i) => h('i', { class: i < lvl ? 'on' : '', style: { height: ht + 'px' } })));
}
function val(text, opts = {}) {
  const span = h('span', { class: 'val' }, opts.secret ? '•'.repeat(Math.min(32, text.length)) : text);
  const wrap = [span];
  if (opts.secret) {
    let shown = false;
    wrap.push(h('button', { class: 'icon-btn', title: 'Show / hide', onclick: () => { shown = !shown; span.textContent = shown ? text : '•'.repeat(Math.min(32, text.length)); } }, icon('eye')));
  }
  wrap.push(h('button', { class: 'icon-btn', title: 'Copy', onclick: () => copyText(text) }, icon('copy')));
  return wrap;
}

// JSON syntax highlighter (escapes everything).
function jsonView(obj) {
  const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  const str = esc(JSON.stringify(obj, null, 2) || 'null');
  const html = str.replace(/("(\\u[a-zA-Z0-9]{4}|\\[^u]|[^\\"])*"(\s*:)?|\b(true|false)\b|\bnull\b|-?\d+(?:\.\d*)?(?:[eE][+\-]?\d+)?)/g, (m) => {
    let c = 'n';
    if (/^"/.test(m)) c = /:$/.test(m) ? 'k' : 's';
    else if (/true|false/.test(m)) c = 'b';
    else if (/null/.test(m)) c = 'z';
    return '<span class="' + c + '">' + m + '</span>';
  });
  return h('pre', { class: 'json', html });
}

// ---------- event typing ----------
const EV = {
  'as.up': ['Forward uplink data', 't-up'],
  'gs.up': ['Receive uplink', 't-gw'],
  'gs.status': ['Gateway status', 't-gw'],
  'gs.gateway.connect': ['Gateway connected', 't-gw'],
  'gs.gateway.registered': ['Gateway registered', 't-join'],
  'gs.down.tx_ack': ['TX rejected', 't-err'],
  'gs.down.manual': ['Manual transmit', 't-down'],
  'gs.down.forwarded': ['Upstream downlink', 't-down'],
  'ns.join.accept': ['Accept join', 't-join'],
  'ns.join.mic_failed': ['Join MIC failed', 't-err'],
  'ns.join.nonce_reused': ['DevNonce reused', 't-warn'],
  'ns.up.unmatched': ['Unknown device', 't-warn'],
  'ns.up.mic_failed': ['MIC failed', 't-err'],
  'ns.up.fcnt_rejected': ['FCnt rejected', 't-err'],
  'ns.up.retransmission': ['Retransmission', 't-warn'],
  'ns.up.mac': ['MAC uplink', 't-gw'],
  'ns.down.sent': ['Send downlink', 't-down'],
  'ns.down.failed': ['Downlink failed', 't-err'],
  'as.down.queued': ['Schedule downlink', 't-down'],
  'as.webhook.failed': ['Webhook failed', 't-err'],
};
function evType(name) { const [label, cls] = EV[name] || [name, 't-gw']; return h('span', { class: 'ev-type ' + cls }, h('i'), label); }

const FILTERS = {
  all: null,
  uplink: (e) => e.name === 'as.up',
  join: (e) => e.name.startsWith('ns.join') || e.name === 'gs.gateway.registered',
  downlink: (e) => e.name.includes('down'),
  gateway: (e) => e.name.startsWith('gs.'),
  problems: (e) => (EV[e.name] || [])[1] === 't-err' || (EV[e.name] || [])[1] === 't-warn',
};

/**
 * Live data view (TTN style): history + websocket stream.
 * opts: { app, dev, gateway, short, hideGatewayRaw, title }
 */
function liveView(opts = {}) {
  const q = new URLSearchParams();
  if (opts.app) q.set('app', opts.app);
  if (opts.dev) q.set('dev', opts.dev);
  if (opts.gateway) q.set('gateway', opts.gateway);
  let paused = false, filter = 'all', ws = null, closed = false, count = 0;
  const events = [];
  const list = h('div', { class: 'live-list' + (opts.short ? ' short' : '') });
  const status = h('span', { class: 'small muted' }, 'connecting…');
  const pauseBtn = h('button', { class: 'btn sm', onclick: () => { paused = !paused; pauseBtn.replaceChildren(icon(paused ? 'play' : 'pause'), paused ? 'Resume' : 'Pause'); } }, icon('pause'), 'Pause');
  const seg = h('div', { class: 'seg' }, Object.keys(FILTERS).map((k) => h('button', { class: k === 'all' ? 'on' : '', onclick: (e) => {
    filter = k; seg.querySelectorAll('button').forEach((b) => b.classList.toggle('on', b === e.target)); rerender();
  } }, k[0].toUpperCase() + k.slice(1))));

  const entity = (e) => {
    if (e.device_id) return h('a', { href: `#/applications/${e.application_id}/devices/${e.device_id}`, onclick: (ev) => ev.stopPropagation() }, e.device_id);
    if (e.gateway_ids && e.gateway_ids.length) return h('a', { href: `#/gateways/${e.gateway_ids[0]}`, onclick: (ev) => ev.stopPropagation() }, e.gateway_ids[0]);
    return h('span', { class: 'faint' }, '—');
  };
  const decodedChips = (e) => {
    const up = e.data && e.data.uplink_message;
    if (!up || !up.decoded_payload) return null;
    return h('div', { class: 'chips' }, Object.entries(up.decoded_payload).slice(0, 12).map(([k, v]) =>
      h('span', { class: 'chip' }, h('b', null, k + ': '), typeof v === 'object' ? JSON.stringify(v) : String(v))));
  };
  const renderEvent = (e, fresh) => {
    let detail = null;
    const row = h('div', { class: 'ev-row' }, h('span', { class: 'ev-time', title: fmtDate(e.time) }, fmtTime(e.time)), evType(e.name), h('span', { class: 'ev-ent' }, entity(e)), h('span', { class: 'ev-sum', title: e.summary }, e.summary));
    const wrap = h('div', { class: 'ev' + (fresh ? ' new' : '') }, row);
    row.onclick = () => {
      if (detail) { detail.remove(); detail = null; return; }
      detail = h('div', { class: 'ev-detail' },
        h('div', { class: 'row' }, h('span', { class: 'badge' }, e.name), h('span', { class: 'small muted' }, fmtDate(e.time)),
          e.gateway_ids && e.gateway_ids.length > 1 ? h('span', { class: 'badge info' }, e.gateway_ids.length + ' gateways') : null,
          h('span', { style: { flex: 1 } }),
          h('button', { class: 'btn sm', onclick: () => copyText(JSON.stringify(e.data, null, 2)) }, icon('copy'), 'Copy JSON')),
        decodedChips(e) ? h('div', { class: 'row' }, decodedChips(e)) : null,
        jsonView(e.data));
      wrap.append(detail);
    };
    return wrap;
  };
  const passes = (e) => !FILTERS[filter] || FILTERS[filter](e);
  function rerender() {
    list.replaceChildren(...events.filter(passes).slice(0, 300).map((e) => renderEvent(e, false)));
    if (!list.children.length) list.append(h('div', { class: 'empty' }, icon('inbox'), 'No events yet. Waiting for traffic…'));
  }
  function add(e) {
    if (opts.hideGatewayRaw && e.name === 'gs.up') return;
    events.unshift(e);
    if (events.length > 1000) events.pop();
    count++;
    if (paused || !passes(e)) return;
    const empty = list.querySelector('.empty');
    if (empty) empty.remove();
    list.prepend(renderEvent(e, true));
    while (list.children.length > 300) list.lastChild.remove();
  }
  function connect() {
    if (closed) return;
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    ws = new WebSocket(`${proto}://${location.host}/api/live?${q}`);
    ws.onopen = () => { status.textContent = 'live'; status.className = 'small badge ok'; };
    ws.onmessage = (m) => { try { add(JSON.parse(m.data)); } catch (e) { /* ignore */ } };
    ws.onclose = () => { status.textContent = 'reconnecting…'; status.className = 'small badge warn'; if (!closed) setTimeout(connect, 2500); };
  }
  const exportBtn = h('button', { class: 'btn sm', onclick: () => {
    const blob = new Blob([JSON.stringify(events, null, 2)], { type: 'application/json' });
    const a = h('a', { href: URL.createObjectURL(blob), download: 'lorahub-events.json' }); a.click();
  } }, icon('download'), 'Export');
  const clearBtn = h('button', { class: 'btn sm', onclick: () => { events.length = 0; rerender(); } }, icon('trash'), 'Clear');

  const hq = new URLSearchParams(q); hq.set('limit', opts.short ? '60' : '200');
  GET('/events?' + hq).then((evs) => {
    for (const e of evs.reverse()) { if (!(opts.hideGatewayRaw && e.name === 'gs.up')) events.unshift(e); }
    rerender();
  }).catch(errToast);
  connect();
  App.onLeave(() => { closed = true; if (ws) ws.close(); });

  return h('div', { class: 'card' },
    h('div', { class: 'live-toolbar' }, opts.title ? h('h2', { style: { margin: 0, marginRight: '8px' } }, opts.title) : null, seg, h('span', { class: 'spacer' }), status, pauseBtn, exportBtn, clearBtn),
    list);
}

// ---------- form helpers ----------
function field(label, input, help) {
  return h('div', { class: 'field' }, h('label', null, label), input, help ? h('div', { class: 'help' }, help) : null);
}
function inp(value, attrs = {}) { return h('input', Object.assign({ type: 'text', value: value ?? '' }, attrs)); }
function sel(value, options, attrs = {}) {
  return h('select', attrs, options.map((o) => {
    const [v, l] = Array.isArray(o) ? o : [o, o];
    return h('option', { value: v, selected: String(v) === String(value) }, l);
  }));
}
function checkbox(checked, label, help) {
  const c = h('input', { type: 'checkbox', checked });
  return { el: h('label', { class: 'check' }, c, h('span', null, label, help ? h('div', { class: 'help' }, help) : null)), input: c };
}
function hexInput(value, len, genLabel) {
  const i = inp(value, { class: 'mono', placeholder: len ? '0'.repeat(len) : '', maxlength: len ? len + 8 : undefined, spellcheck: 'false' });
  const gen = h('button', { class: 'btn', type: 'button', title: 'Generate', onclick: async () => {
    const k = await GET('/tools/keys');
    i.value = len === 32 ? k.key : len === 8 ? k.dev_addr : k.eui;
    i.dispatchEvent(new Event('input', { bubbles: true }));
  } }, icon('wand'), genLabel || 'Generate');
  return { el: h('div', { class: 'input-group' }, i, gen), input: i };
}
function cleanHex(s) { return (s || '').replace(/[\s:-]/g, '').replace(/^0x/i, '').toUpperCase(); }

// ---------- formatter editor (TTN "payload formatters") ----------
const FMT_TEMPLATES = {
  javascript: `// TTN-compatible payload formatter.
// input.bytes: array of bytes, input.fPort: port, input.hex: hex string
function decodeUplink(input) {
  var b = input.bytes;
  var data = {};
  if (b.length >= 2) {
    data.temperature = ((b[0] << 8 | b[1]) << 16 >> 16) / 10; // int16 BE / 10
  }
  if (b.length >= 3) data.humidity = b[2] / 2;
  return { data: data, warnings: [], errors: [] };
}

// Optional: encode downlinks from JSON {data, fPort}
function encodeDownlink(input) {
  return { bytes: [input.data.relay ? 1 : 0], fPort: input.fPort || 1 };
}
`,
};
const FIELD_TYPES = ['uint8', 'int8', 'uint16_be', 'int16_be', 'uint16_le', 'int16_le', 'uint24_be', 'int24_be', 'uint32_be', 'int32_be', 'uint32_le', 'int32_le', 'float32_be', 'float32_le', 'bool', 'bit', 'hex', 'ascii'];

/**
 * formatterEditor(fmt, {allowInherit, onSave, samplePayload, fport})
 * Returns element; editing state is internal; onSave(fmt) persists.
 */
function formatterEditor(fmt, opts = {}) {
  const st = JSON.parse(JSON.stringify(fmt || { type: 'none' }));
  if (!st.type) st.type = opts.allowInherit ? 'inherit' : 'none';
  st.fields = st.fields || [];
  const types = [['none', 'None'], ['cayenne_lpp', 'CayenneLPP'], ['javascript', 'Custom JavaScript'], ['fields', 'Byte fields (no code)']];
  if (opts.allowInherit) types.unshift(['inherit', 'Use application formatter']);
  const body = h('div', { class: 'stack' });
  const typeSel = sel(st.type, types, { onchange: () => { st.type = typeSel.value; if (st.type === 'javascript' && !st.code) st.code = FMT_TEMPLATES.javascript; render(); } });
  const testHex = inp(opts.samplePayload || '', { class: 'mono', placeholder: 'e.g. 0167011002687F' });
  const testPort = inp(opts.fport || 1, { type: 'number', min: 0, max: 255, style: { width: '90px' } });
  const out = h('div');

  function fieldsTable() {
    const tbody = h('tbody');
    st.fields.forEach((f, i) => {
      const upd = (k, conv) => (e) => { f[k] = conv ? conv(e.target.value) : e.target.value; };
      tbody.append(h('tr', null,
        h('td', null, inp(f.name, { oninput: upd('name'), placeholder: 'temperature' })),
        h('td', null, inp(f.offset, { type: 'number', min: 0, oninput: upd('offset', Number), style: { width: '70px' } })),
        h('td', null, inp(f.length || '', { type: 'number', min: 0, oninput: upd('length', Number), style: { width: '70px' }, placeholder: 'auto' })),
        h('td', null, sel(f.type, FIELD_TYPES, { onchange: upd('type') })),
        h('td', null, inp(f.scale || '', { type: 'number', step: 'any', oninput: upd('scale', Number), style: { width: '80px' }, placeholder: '1' })),
        h('td', null, inp(f.add || '', { type: 'number', step: 'any', oninput: upd('add', Number), style: { width: '70px' }, placeholder: '0' })),
        h('td', null, f.type === 'bit' ? inp(f.bit || 0, { type: 'number', min: 0, max: 7, oninput: upd('bit', Number), style: { width: '60px' } }) : h('span', { class: 'faint' }, '—')),
        h('td', null, h('button', { class: 'icon-btn', onclick: () => { st.fields.splice(i, 1); render(); } }, icon('trash')))));
    });
    return h('div', { class: 'stack' },
      h('div', { class: 'table-wrap card' }, h('table', { class: 't' }, h('thead', null, h('tr', null, ['Name', 'Offset', 'Length', 'Type', 'Scale', 'Add', 'Bit', ''].map((x) => h('th', null, x)))), tbody)),
      h('button', { class: 'btn', onclick: () => { st.fields.push({ name: '', offset: 0, type: 'uint8', scale: 1 }); render(); } }, icon('plus'), 'Add field'));
  }

  async function test() {
    out.replaceChildren(h('span', { class: 'muted small' }, 'Running…'));
    try {
      const r = await api('POST', '/formatters/test', { formatter: st, payload_hex: cleanHex(testHex.value), f_port: Number(testPort.value) });
      const parts = [];
      if (r.errors && r.errors.length) parts.push(h('div', { class: 'callout warn' }, h('b', null, 'Errors: '), r.errors.join('; ')));
      if (r.warnings && r.warnings.length) parts.push(h('div', { class: 'callout' }, h('b', null, 'Warnings: '), r.warnings.join('; ')));
      parts.push(jsonView(r.data || {}));
      parts.push(h('div', { class: 'small faint' }, `Executed in ${r.duration_ms.toFixed(2)} ms`));
      out.replaceChildren(...parts);
    } catch (e) { out.replaceChildren(h('div', { class: 'callout warn' }, e.message)); }
  }
  function bytePills() {
    const hx = cleanHex(testHex.value);
    if (!/^[0-9A-F]*$/.test(hx) || hx.length % 2) return h('span', { class: 'small faint' }, 'Enter an even-length hex payload');
    const hl = new Set();
    if (st.type === 'fields') st.fields.forEach((f) => { const l = f.length || (/16/.test(f.type) ? 2 : /24/.test(f.type) ? 3 : /32/.test(f.type) ? 4 : 1); for (let i = 0; i < l; i++) hl.add(Number(f.offset) + i); });
    return h('div', { class: 'bytes' }, (hx.match(/../g) || []).map((b, i) => h('span', { class: hl.has(i) ? 'hl' : '' }, b, h('small', null, i))));
  }
  const pills = h('div');
  testHex.addEventListener('input', () => pills.replaceChildren(bytePills()));

  function render() {
    const kids = [field('Formatter type', typeSel)];
    if (st.type === 'javascript') {
      const ta = h('textarea', { class: 'code', spellcheck: 'false', value: st.code || '', oninput: (e) => { st.code = e.target.value; } });
      ta.addEventListener('keydown', (e) => {
        if (e.key === 'Tab') { e.preventDefault(); const s = ta.selectionStart; ta.setRangeText('  ', s, ta.selectionEnd, 'end'); st.code = ta.value; }
      });
      kids.push(field('Formatter code', ta, 'Implements decodeUplink(input) → {data, warnings, errors}. Compatible with TTN Device Repository codecs. Legacy Decoder(bytes, port) also works.'));
    } else if (st.type === 'cayenne_lpp') {
      kids.push(field('Data offset', inp(st.data_offset || 0, { type: 'number', min: 0, oninput: (e) => { st.data_offset = Number(e.target.value); }, style: { width: '120px' } }),
        'Skip this many bytes before Cayenne LPP data (e.g. 6 for a custom header on raw devices).'));
    } else if (st.type === 'fields') {
      kids.push(field('Fields', fieldsTable(), 'Slice the payload into named values. Scale and add are applied to numeric types (value × scale + add).'));
    } else if (st.type === 'inherit') {
      kids.push(h('div', { class: 'callout info' }, 'This device uses the payload formatter of its application.'));
    }
    if (st.type !== 'none' && st.type !== 'inherit') {
      kids.push(h('div', { class: 'form-section stack' }, h('h3', null, 'Test'),
        h('div', { class: 'row' }, h('div', { style: { flex: 1, minWidth: '220px' } }, field('Byte payload (hex)', testHex)), field('FPort', testPort),
          h('div', { style: { alignSelf: 'flex-end' } }, h('button', { class: 'btn', onclick: test }, icon('play'), 'Test decoder'))),
        pills, out));
    }
    if (opts.onSave) kids.push(h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: () => opts.onSave(JSON.parse(JSON.stringify(st))) }, 'Save changes')));
    body.replaceChildren(...kids);
    pills.replaceChildren(bytePills());
  }
  render();
  body.getState = () => st;
  return body;
}

// ---------- tiny bar chart ----------
function barChart(series, buckets) {
  // series: [{label, color, data: {bucketMs: count}}]; buckets: [ms...]
  const W = 720, H = 170, pad = 24, n = buckets.length;
  const max = Math.max(1, ...buckets.map((b) => Math.max(...series.map((s) => s.data[b] || 0))));
  const bw = (W - pad) / n;
  let svg = `<svg viewBox="0 0 ${W} ${H + 18}" preserveAspectRatio="none" role="img">`;
  for (let i = 0; i <= 4; i++) {
    const y = H - (H - 10) * i / 4;
    svg += `<line x1="${pad}" x2="${W}" y1="${y}" y2="${y}" stroke="var(--border)" stroke-width="1"/>`;
    svg += `<text x="0" y="${y + 4}" font-size="10" fill="var(--faint)">${Math.round(max * i / 4)}</text>`;
  }
  buckets.forEach((b, i) => {
    const sw = (bw - 4) / series.length;
    series.forEach((s, j) => {
      const v = s.data[b] || 0, bh = (H - 10) * v / max;
      svg += `<rect x="${pad + i * bw + 2 + j * sw}" y="${H - bh}" width="${Math.max(1, sw - 1)}" height="${bh}" fill="${s.color}" rx="1"><title>${new Date(b).getHours()}:00 · ${s.label}: ${v}</title></rect>`;
    });
    if (i % 3 === 0) svg += `<text x="${pad + i * bw + bw / 2}" y="${H + 14}" font-size="10" text-anchor="middle" fill="var(--faint)">${String(new Date(b).getHours()).padStart(2, '0')}h</text>`;
  });
  svg += '</svg>';
  return h('div', { class: 'chart' }, h('div', { class: 'legend' }, series.map((s) => h('span', null, h('i', { style: { background: s.color } }), s.label))), h('div', { html: svg }));
}

// ---------- router / shell ----------
const App = {
  routes: [],
  leaveHandlers: [],
  user: null,
  route(pattern, handler) {
    const keys = [];
    const re = new RegExp('^' + pattern.replace(/:(\w+)/g, (_, k) => { keys.push(k); return '([^/]+)'; }) + '/?$');
    this.routes.push({ re, keys, handler });
  },
  onLeave(fn) { this.leaveHandlers.push(fn); },
  async start() {
    let st;
    try { st = await api('GET', '/auth/status'); } catch (e) { document.body.textContent = 'LoRaHub API unreachable: ' + e.message; return; }
    this.version = st.version;
    if (st.setup_required || !st.user) { this.showAuth(st.setup_required); return; }
    this.user = st.user;
    this.shell();
    window.addEventListener('hashchange', () => this.dispatch());
    this.dispatch();
  },
  showAuth(setup) {
    this.leave();
    const u = inp('', { autocomplete: 'username', placeholder: 'admin' });
    const p = h('input', { type: 'password', autocomplete: setup ? 'new-password' : 'current-password' });
    const err = h('div', { class: 'small', style: { color: 'var(--danger)', minHeight: '18px' } });
    const submit = async (e) => {
      e.preventDefault();
      try {
        await api('POST', setup ? '/auth/setup' : '/auth/login', { username: u.value.trim(), password: p.value });
        location.hash = location.hash || '#/'; location.reload();
      } catch (ex) { err.textContent = ex.message; }
    };
    $('#app').replaceChildren(h('div', { class: 'auth-wrap' }, h('div', { class: 'auth' },
      h('div', { class: 'brand' }, LOGO(), 'LoRaHub'),
      h('form', { class: 'card card-body stack', onsubmit: submit },
        h('h2', null, setup ? 'Create the administrator account' : 'Sign in to the console'),
        setup ? h('p', { class: 'muted small', style: { margin: 0 } }, 'Welcome! This is the first start of your gateway. Choose credentials for the web console.') : null,
        field('Username', u), field('Password', p, setup ? 'At least 6 characters.' : null), err,
        h('button', { class: 'btn primary', type: 'submit' }, setup ? 'Create account' : 'Sign in')))));
    u.focus();
  },
  shell() {
    const navItem = (href, ic, label) => h('a', { href, 'data-nav': href }, icon(ic), label);
    this.sidebar = h('nav', { class: 'sidebar' },
      h('div', { class: 'nav' },
        navItem('#/', 'home', 'Overview'),
        h('div', { class: 'nav-section' }, 'Network'),
        navItem('#/applications', 'app', 'Applications'),
        navItem('#/gateways', 'gateway', 'Gateways'),
        navItem('#/live', 'live', 'Live data'),
        h('div', { class: 'nav-section' }, 'Tools'),
        navItem('#/lab', 'code', 'Payload lab'),
        navItem('#/concentrator', 'chip', 'Concentrator'),
        navItem('#/settings', 'gear', 'Settings')));
    this.sidebar.addEventListener('click', (e) => { if (e.target.closest('a')) this.sidebar.classList.remove('open'); });
    this.main = h('main', { class: 'main' });
    const theme = () => {
      const cur = document.documentElement.dataset.theme || (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light');
      const next = cur === 'dark' ? 'light' : 'dark';
      document.documentElement.dataset.theme = next;
      try { localStorage.setItem('lh-theme', next); } catch (e) { /* ignore */ }
    };
    $('#app').replaceChildren(
      h('header', { class: 'topbar' },
        h('button', { class: 'tb-btn menu-toggle', onclick: () => this.sidebar.classList.toggle('open') }, icon('menu')),
        h('a', { class: 'brand', href: '#/' }, LOGO(), 'LoRaHub', h('small', null, 'console ' + (this.version || ''))),
        h('span', { class: 'spacer' }),
        h('button', { class: 'tb-btn', title: 'Toggle theme', onclick: theme }, icon('moon')),
        h('span', { class: 'tb-btn tb-hide', style: { cursor: 'default' } }, this.user),
        h('button', { class: 'tb-btn', title: 'Sign out', onclick: async () => { await api('POST', '/auth/logout'); location.reload(); } }, icon('logout'), h('span', { class: 'tb-hide' }, 'Sign out'))),
      h('div', { class: 'layout' }, this.sidebar, this.main));
  },
  leave() { for (const f of this.leaveHandlers) { try { f(); } catch (e) { /* ignore */ } } this.leaveHandlers = []; },
  async dispatch() {
    this.leave();
    const path = ((location.hash || '#/').slice(1) || '/').split('?')[0];
    document.querySelectorAll('[data-nav]').forEach((a) => {
      const href = a.getAttribute('data-nav').slice(1);
      a.classList.toggle('active', href === '/' ? path === '/' : path.startsWith(href));
    });
    for (const r of this.routes) {
      const m = path.match(r.re);
      if (!m) continue;
      const params = {};
      r.keys.forEach((k, i) => { params[k] = decodeURIComponent(m[i + 1]); });
      this.main.replaceChildren(h('div', { class: 'muted' }, 'Loading…'));
      try {
        const view = await r.handler(params);
        if (view) { this.main.replaceChildren(view); window.scrollTo(0, 0); }
      } catch (e) {
        if (e.message !== 'authentication required') this.main.replaceChildren(h('div', { class: 'card card-body' }, h('h2', null, 'Something went wrong'), h('p', { class: 'muted' }, e.message)));
      }
      return;
    }
    this.main.replaceChildren(h('div', { class: 'card card-body' }, h('h2', null, 'Page not found'), h('a', { href: '#/' }, 'Back to overview')));
  },
};

// Page header helper
function pageHead(crumbs, title, sub, actions) {
  return h('div', null,
    crumbs && crumbs.length ? h('div', { class: 'crumbs' }, crumbs.map(([l, href], i) => [i ? h('span', null, '›') : null, href ? h('a', { href }, l) : h('span', null, l)])) : null,
    h('div', { class: 'page-head' }, h('div', null, h('h1', null, title), sub ? h('div', { class: 'sub' }, sub) : null), actions ? h('div', { class: 'actions' }, actions) : null));
}
function tabs(base, items, active) {
  return h('div', { class: 'tabs' }, items.map(([slug, label]) => h('a', { href: base + (slug ? '/' + slug : ''), class: slug === active ? 'active' : '' }, label)));
}
function onlineBadge(on, last) {
  return h('span', { class: 'row', style: { gap: '6px' } }, h('span', { class: 'dot ' + (on ? 'on' : 'off') }), on ? 'Connected' : (last ? 'Disconnected · ' + ago(last) : 'Never seen'));
}
