/* LoRaHub console: node registry, Decoder Studio and automation pages. */
'use strict';

function nodeLabel(d, groups) {
  const g = groups[d.application_id];
  const nid = (g && g.node_id) || d.application_id;
  return d.serial ? `${nid} / ${d.serial}` : nid;
}
function nodeHref(d) { return `#/applications/${d.application_id}/devices/${d.device_id}`; }
function valueChips(dec) {
  const skip = new Set(['node_class', 'capability', 'device_type']);
  const entries = Object.entries(dec || {}).filter(([k, v]) => !skip.has(k) && !/^ch\d+_/.test(k) && typeof v !== 'object');
  if (!entries.length) return h('span', { class: 'muted small' }, 'not decoded');
  return h('div', { class: 'chips' }, entries.slice(0, 6).map(([k, v]) => h('span', { class: 'chip' }, h('b', null, k + ' '), String(v))));
}
function pollWhileVisible(root, ms, fn) {
  const t = setInterval(() => { if (!root.isConnected) { clearInterval(t); return; } if (!document.hidden) fn(); }, ms);
}
function modal(title, body, opts = {}) {
  const close = () => bg.remove();
  const bg = h('div', { class: 'modal-bg', onclick: (e) => { if (e.target === bg) close(); } },
    h('div', { class: 'modal' + (opts.wide ? ' wide' : '') }, h('div', { class: 'card-head' }, h('h2', null, title), h('button', { class: 'icon-btn', title: 'Close', onclick: () => close() }, '✕')), h('div', { class: 'card-body' }, body(close))));
  document.body.append(bg);
}

// ======================= Nodes =======================
function adoptModal(d, onDone) {
  modal(d.adopted ? 'Edit node' : 'Adopt node', (close) => {
    const name = inp(d.name), model = inp(d.model, { placeholder: 'e.g. Soil sensor' }), area = inp(d.area, { placeholder: 'e.g. Greenhouse' });
    const cls = sel(d.class || 'A', [['A', 'A: battery, listens only after sending'], ['C', 'C: mains powered, always listening']]);
    const ha = checkbox(d.ha_enabled, 'Expose to Home Assistant', d.last_decoded ? null : 'Available once this node has decoded data.');
    ha.input.disabled = !d.last_decoded && !d.ha_enabled;
    return [field('Name', name), field('Model', model), field('Room / area', area), field('Class', cls, 'Decides when downlinks are sent.'), ha.el,
      h('div', { class: 'form-actions', style: { justifyContent: 'flex-end', marginTop: '14px' } },
        h('button', { class: 'btn', onclick: close }, 'Cancel'),
        h('button', { class: 'btn primary', onclick: async () => {
          try {
            await api('PUT', `/applications/${d.application_id}/devices/${d.device_id}`, Object.assign({}, d,
              { adopted: true, name: name.value, model: model.value, area: area.value, class: cls.value, ha_enabled: ha.input.checked }));
            toast('Node saved'); close(); onDone();
          } catch (e) { errToast(e); }
        } }, d.adopted ? 'Save' : 'Adopt'))];
  });
}

App.route('/nodes', async () => {
  const body = h('div', { class: 'stack' });
  const render = async () => {
    const [devs, apps] = await Promise.all([GET('/devices'), GET('/applications')]);
    const groups = Object.fromEntries(apps.map((a) => [a.application_id, a]));
    const nodes = devs.filter((d) => d.mode === 'node').sort((a, b) => new Date(b.last_seen || 0) - new Date(a.last_seen || 0));
    const table = (list, adopted) => h('div', { class: 'table-wrap' }, h('table', { class: 't' },
      h('thead', null, h('tr', null, ['Node', 'Name', adopted ? 'Area' : 'Model', 'Class', 'Latest values', 'Decoder', 'Signal', 'Last seen', ''].map((x) => h('th', null, x)))),
      h('tbody', null, list.map((d) => h('tr', null,
        h('td', null, h('a', { href: nodeHref(d) }, h('span', { class: 'val' }, nodeLabel(d, groups)))),
        h('td', null, d.name, d.ha_enabled ? [' ', h('span', { class: 'badge info' }, 'HA')] : null),
        h('td', { class: 'small' }, (adopted ? d.area : d.model) || '—'),
        h('td', null, d.class || '—'),
        h('td', null, valueChips(d.last_decoded)),
        h('td', { class: 'small muted' }, d.last_scheme || ((d.uplink_formatter || {}).type !== 'inherit' ? (d.uplink_formatter || {}).type : '') || '—'),
        h('td', { class: 'small' }, d.last_seen ? [signalBars(d.last_snr), ` ${d.last_rssi} dBm`] : '—'),
        h('td', { class: 'small' }, ago(d.last_seen)),
        h('td', null, h('button', { class: 'btn sm' + (adopted ? '' : ' primary'), onclick: () => adoptModal(d, render) }, adopted ? 'Edit' : 'Adopt')))))));
    const fresh = nodes.filter((d) => !d.adopted), adopted = nodes.filter((d) => d.adopted);
    body.replaceChildren(
      pageHead([['Nodes']], 'Nodes', [h('span', null, `${adopted.length} adopted`), h('span', null, `${fresh.length} waiting`)]),
      h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'New nodes')),
        fresh.length ? table(fresh, false) : h('div', { class: 'empty' }, nodes.length ? 'Every node heard so far has been adopted.' : 'No nodes heard yet. They appear here with their first packet.')),
      h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'Adopted nodes')),
        adopted.length ? table(adopted, true) : h('div', { class: 'empty' }, 'Adopt a node to name it, give it a room and send it to Home Assistant.')));
  };
  await render();
  pollWhileVisible(body, 10000, () => { if (!document.querySelector('.modal-bg')) render().catch(() => {}); });
  return body;
});

// Per-node message history.
App.route('/applications/:app/devices/:dev/messages', async ({ app, dev }) => {
  const [a, d] = await loadDev(app, dev);
  const evs = await GET(`/events?app=${app}&dev=${dev}&names=as.up&limit=50`);
  const keys = [...new Set(evs.flatMap((e) => Object.keys((e.data && e.data.decoded) || {})))]
    .filter((k) => !/^ch\d+_/.test(k) && !['node_class', 'capability', 'device_type'].includes(k));
  return h('div', { class: 'stack' }, devHeader(a, d, 'messages'),
    h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, `Last ${evs.length} messages`)),
      evs.length ? h('div', { class: 'table-wrap' }, h('table', { class: 't' },
        h('thead', null, h('tr', null, ['Time', ...keys, 'RSSI', 'SNR', 'Gateways', 'Payload'].map((x) => h('th', null, x)))),
        h('tbody', null, evs.map((e) => {
          const m = e.data || {}, g = (m.gateways || [])[0] || {};
          return h('tr', null, h('td', { class: 'small' }, fmtDate(e.time)),
            keys.map((k) => { const v = (m.decoded || {})[k]; return h('td', null, v === undefined ? '—' : typeof v === 'object' ? JSON.stringify(v) : String(v)); }),
            h('td', null, g.rssi ?? '—'), h('td', null, g.snr ?? '—'), h('td', null, m.gateway_count ?? '—'),
            h('td', null, h('span', { class: 'val' }, m.payload_hex || '')));
        })))) : h('div', { class: 'empty' }, 'No messages stored for this node.')));
});

// ======================= Decoder Studio =======================
const SCHEME_FIELD_TYPES = FIELD_TYPES;
const MATCH_TYPES = [['prefix', 'Starts with (hex)'], ['size', 'Exact size (bytes)'], ['regex', 'Hex regular expression'], ['condition', 'JavaScript rule'], ['default', 'Catch-all']];
const DEC_TYPES = [['fields', 'Byte fields'], ['ctv', 'Channel / type / value'], ['cayenne_lpp', 'Cayenne LPP'], ['js', 'JavaScript'], ['javascript', 'JavaScript (decodeUplink)'], ['ascii', 'ASCII text'], ...METHOD_TYPES];
const JS_TEMPLATE = 'function decodeUplink(input) {\n  // input.bytes starts at the data offset; input.payload is the whole packet\n  return { data: { value: input.bytes[0] } };\n}\n';
const FIELD_COLORS = ['#1a73e8', '#188038', '#e37400', '#a142f4', '#d93025', '#129eaf', '#b06000', '#c5221f'];

function newScheme(sample) {
  const clean = cleanHex(sample || '');
  return { id: '', name: clean ? 'Node ' + clean.slice(0, 4) : 'New scheme', enabled: true, matchType: clean ? 'prefix' : 'default', matchValue: clean.slice(0, 4).toLowerCase(),
    matchCondition: 'return bytes.length >= 4;', type: 'fields', dataOffset: 6, serial: '', fields: [], code: JS_TEMPLATE, sampleHex: clean };
}

App.route('/decoders', async () => {
  const [doc, settings] = await Promise.all([GET('/schemes'), GET('/settings')]);
  doc.schemes = doc.schemes || [];
  const root = h('div', { class: 'stack' });
  let current = doc.schemes.length ? 0 : -1;
  const wanted = new URLSearchParams(location.hash.split('?')[1] || '').get('scheme');
  if (wanted) { const i = doc.schemes.findIndex((x) => x.id === wanted); if (i >= 0) current = i; }
  let dirty = false;
  let recent = [];
  try {
    const evs = await GET('/events?names=as.up,ns.up.unmatched&limit=40');
    const seen = new Set();
    recent = evs.map((e) => e.data || {}).filter((m) => m.payload_hex && !seen.has(m.payload_hex) && seen.add(m.payload_hex));
  } catch (e) { /* history is optional */ }

  const save = async () => {
    try {
      const out = await api('PUT', '/schemes', doc);
      doc.schemes = out.schemes; dirty = false; toast('Decoders saved and active'); draw();
    } catch (e) { errToast(e); }
  };
  const move = (i, by) => { const j = i + by; if (j < 0 || j >= doc.schemes.length) return; [doc.schemes[i], doc.schemes[j]] = [doc.schemes[j], doc.schemes[i]]; if (current === i) current = j; else if (current === j) current = i; dirty = true; draw(); };
  const addScheme = (s) => { doc.schemes.unshift(s); current = 0; dirty = true; draw(); };

  function listCard() {
    return h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'Schemes'), h('button', { class: 'btn sm', onclick: () => addScheme(newScheme('')) }, icon('plus'), 'New')),
      h('div', { class: 'scheme-list' }, doc.schemes.length ? doc.schemes.map((s, i) => h('div', { class: 'scheme-row' + (i === current ? ' on' : ''), onclick: () => { current = i; draw(); } },
        h('input', { type: 'checkbox', checked: s.enabled !== false, title: 'Enabled', onclick: (e) => { e.stopPropagation(); s.enabled = e.target.checked; dirty = true; saveBar(); } }),
        h('div', { class: 'scheme-name' }, h('div', null, s.name || s.id), h('div', { class: 'small muted' }, `${s.matchType}${s.matchValue ? ' ' + s.matchValue : ''} · ${s.type}`)),
        h('button', { class: 'icon-btn', title: 'Move up', onclick: (e) => { e.stopPropagation(); move(i, -1); } }, '▲'),
        h('button', { class: 'icon-btn', title: 'Move down', onclick: (e) => { e.stopPropagation(); move(i, 1); } }, '▼'))) : h('div', { class: 'empty' }, 'No schemes yet.')));
  }

  function editorCard(s) {
    s.fields = s.fields || [];
    const pf = settings.packet_format;
    const out = h('div');
    const matchNote = h('span');
    const sample = inp(s.sampleHex || '', { placeholder: 'Packet hex, e.g. CAFE96B840EB0102...', class: 'mono' });
    const bytes = () => cleanHex(sample.value).match(/../g) || [];
    const region = (i) => {
      if (s.type === 'fields') return i >= pf.node_id_offset && i < pf.node_id_offset + pf.node_id_length ? 'node' : (i >= pf.serial_offset && i < pf.serial_offset + pf.serial_length && s.serial !== 'none' && (s.dataOffset || 0) >= pf.serial_offset + pf.serial_length) ? 'serial' : '';
      return i < (s.dataOffset || 0) ? 'head' : 'data';
    };
    let timer = null;
    const test = () => {
      clearTimeout(timer);
      timer = setTimeout(async () => {
        const hexv = cleanHex(sample.value);
        if (!hexv) { out.replaceChildren(h('p', { class: 'muted small' }, 'Pick a packet above to see what comes out.')); matchNote.replaceChildren(); return; }
        try {
          const r = await api('POST', '/schemes/test', { payload_hex: hexv, scheme: Object.assign({}, s, { id: s.id || 'draft' }) });
          matchNote.replaceChildren(h('span', { class: 'badge ' + (r.matched ? 'ok' : 'warn') }, r.matched ? 'This packet matches' : 'This packet does not match'));
          if (mapper) mapper.setValues(r.data || {});
          out.replaceChildren(r.data && Object.keys(r.data).length ? valueTiles(r.data) : h('p', { class: 'muted', style: { margin: 0 } }, 'Nothing comes out yet.'),
            ...(r.errors || []).map((x) => h('div', { class: 'small', style: { color: 'var(--danger)' } }, x)), ...(r.warnings || []).map((x) => h('div', { class: 'small muted' }, x)),
            r.data && Object.keys(r.data).length ? h('details', null, h('summary', { class: 'small muted' }, 'JSON'), jsonView(r.data)) : null);
        } catch (e) { out.replaceChildren(h('div', { class: 'small', style: { color: 'var(--danger)' } }, e.message)); }
      }, 200);
    };
    const touch = () => { dirty = true; saveBar(); test(); };
    const bind = (el, key, conv) => { el.addEventListener('input', () => { s[key] = conv ? conv(el.value) : el.value; touch(); }); return el; };
    let mapper = null;
    const methodBox = h('div');

    const syncSample = () => { s.sampleHex = cleanHex(sample.value); dirty = true; saveBar(); if (mapper) mapper.redraw(); test(); };
    sample.addEventListener('input', syncSample);
    const picker = sel('', [['', recent.length ? `Recent packets (${recent.length})` : 'No recent packets'], ...recent.map((m) => [m.payload_hex, `${m.name || m.node_id || ''} · ${m.payload_hex.slice(0, 28)}${m.payload_hex.length > 28 ? '…' : ''}`])]);
    picker.addEventListener('change', () => { if (picker.value) { sample.value = picker.value; syncSample(); } });

    const matchValue = bind(inp(s.matchValue || ''), 'matchValue');
    const cond = bind(h('textarea', { rows: 3, class: 'mono', value: s.matchCondition || '' }), 'matchCondition');
    const code = bind(h('textarea', { rows: 9, class: 'mono', value: s.code || JS_TEMPLATE }), 'code');
    const jsBody = bind(h('textarea', { rows: 9, class: 'mono', value: s.jsCode || 'return { value: bytes[6] };' }), 'jsCode');
    if (!s.jsCode && s.type === 'js') s.jsCode = jsBody.value;
    const matchSel = sel(s.matchType, MATCH_TYPES);
    const mvField = field('Value', matchValue), condField = field('Rule', cond, 'JavaScript returning true. Receives bytes and hex.');
    const fromPacket = h('button', { class: 'btn sm', type: 'button', onclick: () => { const b = bytes(); if (b.length < 2) { toast('Pick a packet first'); return; } s.matchType = 'prefix'; s.matchValue = b.slice(0, 2).join('').toLowerCase(); matchSel.value = 'prefix'; matchValue.value = s.matchValue; syncMatch(); touch(); } }, 'Use this packet\'s first two bytes');
    const syncMatch = () => {
      mvField.style.display = ['prefix', 'size', 'regex'].includes(s.matchType) ? '' : 'none';
      condField.style.display = s.matchType === 'condition' ? '' : 'none';
    };
    matchSel.addEventListener('change', () => { s.matchType = matchSel.value; syncMatch(); touch(); });
    syncMatch();

    const dataOffset = bind(inp(s.dataOffset ?? 0, { type: 'number', min: 0, style: { maxWidth: '110px' } }), 'dataOffset', (v) => parseInt(v, 10) || 0);
    dataOffset.addEventListener('input', () => { if (mapper) mapper.redraw(); });
    const serialSel = sel(s.serial || '', [['', 'Automatic'], ['header', 'Packet has a serial'], ['none', 'No serial: NODE_ID is the node']]);
    serialSel.addEventListener('change', () => { s.serial = serialSel.value; if (mapper) mapper.redraw(); touch(); });

    const tiles = h('div', { class: 'dec-tiles' });
    let choosing = false;
    const drawMethod = () => {
      const currentType = s.type === 'tlv' ? 'ctv' : s.type;
      const tile = ([k, label]) => h('button', { class: 'dec-tile' + (currentType === k ? ' on' : ''), type: 'button', onclick: () => { if (choosing && currentType === k) { choosing = false; drawMethod(); return; } s.type = k; choosing = false; if (k === 'js' && !s.jsCode) s.jsCode = jsBody.value; drawMethod(); touch(); } }, h('b', null, label), h('span', null, DEC_HELP[k] || ''));
      tiles.replaceChildren(...(choosing ? DEC_TYPES.map(tile) : [tile(DEC_TYPES.find(([k]) => k === currentType) || [currentType, currentType]), h('button', { class: 'dec-tile dec-change', type: 'button', onclick: () => { choosing = true; drawMethod(); } }, h('b', null, 'Change method'), h('span', null, DEC_TYPES.length + ' ways to read a packet'))]));
      mapper = null;
      const kids = [];
      if (s.type === 'fields') {
        mapper = byteMapper({ fields: s.fields, bytes, region, onChange: touch });
        kids.push(mapper.el);
      } else {
        mapper = byteMapper({ fields: [], bytes, region, readonly: true, onChange: () => {} });
        kids.push(field('Data starts at byte', dataOffset), mapper.el, h('div', { class: 'chips' }, h('span', { class: 'chip r-head-chip' }, 'header, skipped'), h('span', { class: 'chip c-data' }, 'data')));
        if (s.type === 'javascript') kids.push(field('decodeUplink', code));
        if (s.type === 'js') kids.push(field('Decoder', jsBody, 'Receives bytes (whole packet) and hex. Return an object with your values.'));
        if (isMethod(s.type)) kids.push(decodeMethodEditor(s, touch));
      }
      methodBox.replaceChildren(...kids);
    };
    drawMethod();
    setTimeout(test, 0);

    const step = (n, title, right, ...body) => h('div', { class: 'card card-body dec-step' }, h('div', { class: 'dec-step-head' }, h('i', null, n), h('h2', null, title), h('span', { style: { flex: 1 } }), right), ...body);
    return h('div', { class: 'stack dec-flow' },
      step(1, 'Which packets?', matchNote,
        h('div', { class: 'grid g2' }, field('Name', bind(inp(s.name), 'name')), field('Example packet', picker)),
        sample,
        h('div', { class: 'grid g2', style: { marginTop: '12px' } }, field('Applies to', matchSel), h('div', null, mvField, condField)),
        h('div', { class: 'row' }, fromPacket)),
      step(2, 'How to read it', null, tiles, h('div', { style: { marginTop: '14px' } }, methodBox),
        h('details', { style: { marginTop: '12px' } }, h('summary', { class: 'small muted' }, 'More options'),
          h('div', { class: 'stack', style: { marginTop: '10px' } }, field('Description', bind(inp(s.description || ''), 'description')), s.type === 'fields' ? field('Data starts at byte', dataOffset) : null, field('Node identity', serialSel, 'Automatic treats a scheme for one NODE_ID with data before byte 6 as having no serial.'), preEditor(s, touch)))),
      step(3, 'What comes out', null, out,
        h('div', { class: 'form-actions', style: { marginTop: '12px' } },
          h('button', { class: 'btn', onclick: () => addScheme(Object.assign(JSON.parse(JSON.stringify(s)), { id: '', name: s.name + ' copy' })) }, icon('copy'), 'Duplicate'),
          h('button', { class: 'btn danger', onclick: async () => {
            if (!(await confirmModal('Delete scheme', `Delete "${s.name}"? It stays active until you save.`))) return;
            doc.schemes.splice(current, 1); current = Math.min(current, doc.schemes.length - 1); dirty = true; draw();
          } }, icon('trash'), 'Delete'))));
  }

  const bar = h('div', { class: 'save-bar' });
  function saveBar() {
    bar.replaceChildren(h('span', { class: 'small muted' }, dirty ? 'Unsaved changes' : 'All changes saved'),
      h('button', { class: 'btn primary', disabled: !dirty, onclick: save }, 'Save and activate'));
  }
  function exportDoc() {
    const a = h('a', { href: URL.createObjectURL(new Blob([JSON.stringify(doc, null, 2)], { type: 'application/json' })), download: 'decoders.json' });
    a.click(); URL.revokeObjectURL(a.href);
  }
  function importDoc() {
    const f = h('input', { type: 'file', accept: '.json,application/json', onchange: async () => {
      try {
        const d = JSON.parse(await f.files[0].text());
        if (!Array.isArray(d.schemes)) throw new Error('This file has no "schemes" list.');
        doc.schemes = d.schemes; current = doc.schemes.length ? 0 : -1; dirty = true; toast('Imported. Review, then save.'); draw();
      } catch (e) { errToast(e); }
    } });
    f.click();
  }
  function draw() {
    saveBar();
    root.replaceChildren(
      pageHead([['Decoder Studio']], 'Decoder Studio', ['The first enabled scheme that matches and decodes wins. A decoder set on a node or group takes priority.'], [
        h('button', { class: 'btn', onclick: importDoc }, 'Import'), h('button', { class: 'btn', onclick: exportDoc }, icon('download'), 'Export')]),
      bar,
      h('div', { class: 'studio' }, listCard(), current >= 0 && doc.schemes[current] ? editorCard(doc.schemes[current]) : h('div', { class: 'card empty' }, 'Choose a scheme or create a new one.')));
  }
  draw();
  return root;
});

// ======================= Automation =======================
const OPERATORS = [['>', 'is above'], ['<', 'is below'], ['>=', 'is at least'], ['<=', 'is at most'], ['==', 'equals'], ['!=', 'differs from'], ['always', 'on every packet']];

function ruleModal(rule, nodes, groups, onDone) {
  const r = Object.assign({ name: '', enabled: true, operator: '>', threshold: 0, field: '', payload_hex: '0101', cooldown_s: 60, pulse_s: 0, pulse_off_hex: '' }, rule);
  const keyOf = (a, d) => (a && d ? `${a}|${d}` : '');
  const opts = nodes.map((d) => [keyOf(d.application_id, d.device_id), `${d.name} (${nodeLabel(d, groups)})`]);
  modal(r.id ? 'Edit rule' : 'New rule', (close) => {
    const name = inp(r.name, { placeholder: 'e.g. Open valve when hot' });
    const src = sel(keyOf(r.source_group, r.source_node), [['', 'Choose…'], ...opts]);
    const fieldEl = h('select');
    const refreshFields = () => {
      const d = nodes.find((x) => keyOf(x.application_id, x.device_id) === src.value);
      const keys = Object.entries((d && d.last_decoded) || {}).filter(([, v]) => typeof v !== 'object').map(([k]) => k);
      if (r.field && !keys.includes(r.field)) keys.push(r.field);
      fieldEl.replaceChildren(...keys.map((k) => h('option', { value: k, selected: k === r.field }, k)));
    };
    src.addEventListener('change', refreshFields); refreshFields();
    const op = sel(r.operator, OPERATORS), thr = inp(r.threshold, { type: 'number', step: 'any' });
    const tgt = sel(keyOf(r.target_group, r.target_node), [['', 'Choose…'], ...opts]);
    const payload = inp(r.payload_hex, { class: 'mono' }), cool = inp(r.cooldown_s, { type: 'number', min: 0 }), pulse = inp(r.pulse_s, { type: 'number', min: 0 }), off = inp(r.pulse_off_hex, { class: 'mono' });
    const simVal = inp('', { type: 'number', step: 'any', placeholder: 'Try a value' }), simOut = h('span', { class: 'small' });
    simVal.addEventListener('input', async () => {
      if (simVal.value === '') { simOut.textContent = ''; return; }
      try { const x = await api('POST', '/rules/simulate', { operator: op.value, threshold: parseFloat(thr.value) || 0, value: parseFloat(simVal.value) }); simOut.textContent = x.matched ? 'Would fire' : 'Would not fire'; } catch (e) { simOut.textContent = e.message; }
    });
    return [field('Name', name), field('When node', src), h('div', { class: 'grid g2' }, field('Field', fieldEl), field('Condition', op)), field('Threshold', thr),
      h('div', { class: 'grid g2' }, field('Check', simVal), h('div', { style: { alignSelf: 'center' } }, simOut)),
      field('Send to node', tgt), field('Downlink payload (hex)', payload), h('div', { class: 'grid g2' }, field('Cooldown (s)', cool), field('Pulse length (s)', pulse, '0 = no pulse')), field('Pulse-off payload (hex)', off),
      h('div', { class: 'form-actions', style: { justifyContent: 'flex-end', marginTop: '14px' } },
        h('button', { class: 'btn', onclick: close }, 'Cancel'),
        h('button', { class: 'btn primary', onclick: async () => {
          const [sg, sn] = src.value.split('|'), [tg, tn] = tgt.value.split('|');
          const out = { name: name.value, enabled: r.enabled, source_group: sg, source_node: sn, field: fieldEl.value, operator: op.value, threshold: parseFloat(thr.value) || 0,
            target_group: tg, target_node: tn, payload_hex: payload.value, cooldown_s: parseInt(cool.value, 10) || 0, pulse_s: parseInt(pulse.value, 10) || 0, pulse_off_hex: off.value };
          try { await api(r.id ? 'PUT' : 'POST', '/rules' + (r.id ? '/' + r.id : ''), out); toast('Rule saved'); close(); onDone(); } catch (e) { errToast(e); }
        } }, 'Save')) ];
  });
}

App.route('/rules', async () => {
  const body = h('div', { class: 'stack' });
  const render = async () => {
    const [rules, devs, apps, logs] = await Promise.all([GET('/rules'), GET('/devices'), GET('/applications'), GET('/events?names=rule.fired,rule.failed&limit=30')]);
    const groups = Object.fromEntries(apps.map((a) => [a.application_id, a]));
    const nodes = devs.filter((d) => d.mode === 'node');
    const nameOf = (a, d) => { const n = nodes.find((x) => x.application_id === a && x.device_id === d); return n ? n.name : d; };
    const opText = (o) => (OPERATORS.find((x) => x[0] === o) || [o, o])[1];
    body.replaceChildren(
      pageHead([['Automation']], 'Automation', ['Send a downlink to one node when another reports a value'], [h('button', { class: 'btn primary', onclick: () => ruleModal({}, nodes, groups, render) }, icon('plus'), 'New rule')]),
      h('div', { class: 'card' }, rules.length ? h('div', { class: 'table-wrap' }, h('table', { class: 't' },
        h('thead', null, h('tr', null, ['On', 'Rule', 'When', 'Then', 'Last run', ''].map((x) => h('th', null, x)))),
        h('tbody', null, rules.map((r) => h('tr', null,
          h('td', null, h('input', { type: 'checkbox', checked: r.enabled, onchange: async (e) => { try { await api('PUT', '/rules/' + r.id, Object.assign({}, r, { enabled: e.target.checked })); } catch (x) { errToast(x); e.target.checked = !e.target.checked; } } })),
          h('td', null, r.name || r.id),
          h('td', { class: 'small' }, `${nameOf(r.source_group, r.source_node)}: ${r.operator === 'always' ? 'every packet' : `${r.field} ${opText(r.operator)} ${r.threshold}`}`),
          h('td', { class: 'small' }, `${r.payload_hex} to ${nameOf(r.target_group, r.target_node)}`),
          h('td', { class: 'small' }, r.last_triggered ? `${ago(r.last_triggered)} · ${r.last_status || ''}` : 'never'),
          h('td', null, h('div', { class: 'chips' },
            h('button', { class: 'btn sm', title: 'Send the downlink now', onclick: async () => { try { await api('POST', `/rules/${r.id}/fire`); toast('Downlink queued'); render(); } catch (e) { errToast(e); render(); } } }, 'Test now'),
            h('button', { class: 'btn sm', onclick: () => ruleModal(r, nodes, groups, render) }, 'Edit'),
            h('button', { class: 'icon-btn', title: 'Delete', onclick: async () => { if (await confirmModal('Delete rule', `Delete rule "${r.name || r.id}"?`)) { try { await api('DELETE', '/rules/' + r.id); render(); } catch (e) { errToast(e); } } } }, icon('trash'))))))))) : h('div', { class: 'empty' }, 'No rules yet.')),
      h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'Run log')), logs.length ? h('div', { class: 'table-wrap' }, h('table', { class: 't' },
        h('tbody', null, logs.map((e) => h('tr', null, h('td', { class: 'small' }, fmtDate(e.time)), h('td', null, evType(e.name)), h('td', { class: 'small' }, e.summary)))))) : h('div', { class: 'empty' }, 'Nothing has run yet.')));
  };
  await render();
  return body;
});
