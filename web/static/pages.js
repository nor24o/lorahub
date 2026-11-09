/* LoRaHub console — pages. */
'use strict';

const KIND_LABEL = { bobcat: 'Bobcat (raw_lora_app)', semtech_udp: 'Semtech UDP', http: 'HTTP', simulated: 'Simulated' };

// ======================= Overview =======================
App.route('/', async () => {
  const [ov, gws, sys] = await Promise.all([GET('/overview'), GET('/gateways'), GET('/system')]);
  const stat = (label, value, hint, href) => h(href ? 'a' : 'div', { class: 'card stat', href, style: { color: 'inherit', textDecoration: 'none' } },
    h('div', { class: 'label' }, label), h('div', { class: 'value' }, value), hint ? h('div', { class: 'hint' }, hint) : null);
  const now = Date.now(), hour = 3600e3;
  const buckets = []; for (let i = 23; i >= 0; i--) buckets.push(Math.floor((now - i * hour) / hour) * hour);
  const series = [
    { label: 'Gateway receptions', color: 'var(--faint)', data: ov.histogram['gs.up'] || {} },
    { label: 'Node packets', color: 'var(--success)', data: ov.histogram['as.up'] || {} },
    { label: 'Downlinks', color: 'var(--primary)', data: ov.histogram['ns.down.sent'] || {} },
  ];
  return h('div', { class: 'stack' },
    pageHead(null, 'Overview', [h('span', null, sys.hostname), h('span', null, 'up ' + fmtDur(sys.uptime_s)), sys.addresses && sys.addresses.length ? h('span', null, sys.addresses.join(', ')) : null]),
    h('div', { class: 'grid g4' },
      stat('Gateways', `${ov.gateways_online} / ${ov.gateways}`, 'connected / registered', '#/gateways'),
      stat('Nodes', ov.devices, ov.devices_active_24h + ' active in 24 h', '#/applications'),
      stat('Packets (24 h)', ov.uplinks_24h, ov.gateway_rx_24h + ' gateway receptions', '#/live'),
      stat('Downlinks (24 h)', ov.downlinks_24h, ov.unmatched_24h + ' from unknown NODE_IDs', '#/live')),
    h('div', { class: 'grid g2' },
      h('div', { class: 'card card-body' }, h('h2', null, 'Traffic — last 24 hours'), barChart(series, buckets)),
      h('div', { class: 'card' },
        h('div', { class: 'card-head' }, h('h2', null, 'Gateways'), h('a', { class: 'btn sm', href: '#/gateways' }, 'View all')),
        gws.length ? h('div', { class: 'table-wrap' }, h('table', { class: 't' }, h('tbody', null, gws.slice(0, 6).map((g) =>
          h('tr', { class: 'click', onclick: () => { location.hash = '#/gateways/' + g.gateway_id; } },
            h('td', null, h('div', { class: 'row', style: { gap: '8px' } }, h('span', { class: 'dot ' + (g.online ? 'on' : 'off') }), h('b', null, g.name || g.gateway_id))),
            h('td', { class: 'small muted' }, KIND_LABEL[g.kind] || g.kind),
            h('td', { class: 'small muted right' }, ago(g.last_seen))))))) :
          h('div', { class: 'empty' }, 'No gateways yet. Start raw_lora_app or point a satellite gateway at port 1700.'))),
    liveView({ short: true, title: 'Recent activity', hideGatewayRaw: true }),
    h('div', { class: 'grid g3' },
      h('div', { class: 'card card-body' }, h('h3', null, 'System'), h('dl', { class: 'kv small' },
        h('dt', null, 'Version'), h('dd', null, sys.version + ' · ' + sys.arch),
        h('dt', null, 'CPU temperature'), h('dd', null, sys.cpu_temp_c != null ? sys.cpu_temp_c.toFixed(1) + ' °C' : '—'),
        h('dt', null, 'Load'), h('dd', null, (sys.loadavg || []).join(' ') || '—'),
        h('dt', null, 'Memory'), h('dd', null, sys.memavailable_mb != null ? `${sys.memavailable_mb} MB free of ${sys.memtotal_mb} MB` : '—'))),
      h('div', { class: 'card card-body' }, h('h3', null, 'Listeners'), h('dl', { class: 'kv small' },
        h('dt', null, 'Web console'), h('dd', null, sys.listeners.http),
        h('dt', null, 'Semtech UDP'), h('dd', null, sys.listeners.semtech_udp || 'disabled'),
        h('dt', null, 'raw_lora_app'), h('dd', null, sys.listeners.bobcat_raw || 'disabled'),
        h('dt', null, 'MQTT broker'), h('dd', null, sys.listeners.mqtt ? `${sys.listeners.mqtt} · ${sys.mqtt_clients} clients` : 'disabled'))),
      h('div', { class: 'card card-body' }, h('h3', null, 'Quick start'), h('ol', { class: 'small', style: { margin: 0, paddingLeft: '18px' } },
        h('li', null, h('a', { href: '#/applications/new' }, 'Add a node group'), ' for each NODE_ID (e.g. CAFE)'),
        h('li', null, 'Nodes (SERIAL_IDs) appear automatically when they transmit'),
        h('li', null, 'Pick a decoder for the data bytes'),
        h('li', null, h('a', { href: '#/gateways' }, 'Connect satellite gateways')),
        h('li', null, 'Read the JSON via MQTT, webhooks or the API')))));
});

// ======================= Node groups =======================
// A node group = all nodes whose packets start with the same NODE_ID.
// A node = one SERIAL_ID inside a group.  Packet: NODE_ID | SERIAL_ID | data

App.route('/applications', async () => {
  const apps = await GET('/applications');
  return h('div', { class: 'stack' },
    pageHead([['Node groups']], 'Node groups', ['Packets are sorted into groups by their NODE_ID header'], [h('a', { class: 'btn primary', href: '#/applications/new' }, icon('plus'), 'Add node group')]),
    h('div', { class: 'card' }, apps.length ? h('div', { class: 'table-wrap' }, h('table', { class: 't' },
      h('thead', null, h('tr', null, ['NODE_ID', 'Name', 'Nodes', 'Decoder', 'New nodes', 'Created'].map((x) => h('th', null, x)))),
      h('tbody', null, apps.map((a) => h('tr', { class: 'click', onclick: () => { location.hash = '#/applications/' + a.application_id; } },
        h('td', null, a.node_id ? h('span', { class: 'val' }, a.node_id) : h('span', { class: 'faint' }, '—')),
        h('td', null, h('b', null, a.name || a.application_id)),
        h('td', null, a.device_count), h('td', null, h('span', { class: 'badge' }, (a.uplink_formatter || {}).type || 'none')),
        h('td', null, a.auto_register_nodes ? h('span', { class: 'badge ok' }, 'auto-add') : h('span', { class: 'badge' }, 'manual')),
        h('td', { class: 'small muted' }, fmtDate(a.created_at))))))) :
      h('div', { class: 'empty' }, icon('app'), 'No node groups yet. Create one for each NODE_ID your nodes send (e.g. CAFE).',
        h('div', { style: { marginTop: '10px' } }, h('a', { class: 'btn primary', href: '#/applications/new' }, 'Add node group')))));
});

App.route('/applications/new', async () => {
  const params = new URLSearchParams(location.hash.split('?')[1] || '');
  const s = await GET('/settings');
  const nidLen = s.packet_format.node_id_length;
  const nodeId = inp(params.get('node_id') || '', { class: 'mono', placeholder: 'CAFE', maxlength: nidLen * 2 });
  const name = inp(params.get('node_id') ? 'Node ' + params.get('node_id') : '', { placeholder: 'Greenhouse sensors' });
  const desc = h('textarea', { rows: 2 });
  const tpl = sel('cayenne_lpp', [['none', 'None (raw hex only)'], ['cayenne_lpp', 'CayenneLPP'], ['fields', 'Byte fields (no code)'], ['javascript', 'Custom JavaScript']]);
  const auto = checkbox(true, 'Add new nodes automatically', 'When a packet with this NODE_ID arrives from a SERIAL_ID that is not known yet, the node is created automatically.');
  const hdr = checkbox(true, 'Prepend NODE_ID + SERIAL_ID to downlinks', 'Downlink data is sent as NODE_ID | SERIAL_ID | data so the node can recognise it.');
  const save = async () => {
    try {
      const f = { type: tpl.value }; if (f.type === 'javascript') f.code = FMT_TEMPLATES.javascript;
      const a = await api('POST', '/applications', { node_id: cleanHex(nodeId.value), name: name.value, description: desc.value, uplink_formatter: f, downlink_formatter: { type: 'none' },
        auto_register_nodes: auto.input.checked, downlink_header: hdr.input.checked, downlink_radio: { mode: 'immediate', power: 14, delay_ms: 1000 } });
      toast('Node group created'); location.hash = '#/applications/' + a.application_id;
    } catch (e) { errToast(e); }
  };
  return h('div', { class: 'stack' }, pageHead([['Node groups', '#/applications'], ['Add']], 'Add node group'),
    h('div', { class: 'card card-body' }, h('div', { class: 'form' },
      field('NODE_ID (hex)', nodeId, `The first ${nidLen} byte(s) of every packet from these nodes, e.g. CAFE.`),
      field('Name', name), field('Description', desc), field('Decoder for the data bytes', tpl, 'Can be changed later on the Decoder tab.'),
      auto.el, hdr.el,
      h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: save }, 'Create node group'), h('a', { class: 'btn', href: '#/applications' }, 'Cancel')))));
});

function appHeader(a, active) {
  const base = '#/applications/' + a.application_id;
  return [pageHead([['Node groups', '#/applications'], [a.node_id || a.application_id]], a.name || a.application_id,
    [a.node_id ? h('span', null, 'NODE_ID ', h('span', { class: 'val' }, a.node_id)) : null, a.description ? h('span', null, a.description) : null]),
  tabs(base, [['', 'Overview'], ['devices', 'Nodes'], ['live', 'Live data'], ['formatters', 'Decoder'], ['integrations', 'Integrations'], ['settings', 'Settings']], active)];
}

function deviceTable(appID, devs) {
  if (!devs.length) return h('div', { class: 'empty' }, icon('device'), 'No nodes yet. They appear here automatically when they send a packet (if "add new nodes automatically" is on).',
    h('div', { style: { marginTop: '10px' } }, h('a', { class: 'btn', href: `#/applications/${appID}/devices/new` }, icon('plus'), 'Add node manually')));
  return h('div', { class: 'table-wrap' }, h('table', { class: 't' },
    h('thead', null, h('tr', null, ['SERIAL_ID', 'Name', 'Last seen', 'Signal', 'Packets', 'Last data'].map((x) => h('th', null, x)))),
    h('tbody', null, devs.map((d) => h('tr', { class: 'click', onclick: () => { location.hash = `#/applications/${appID}/devices/${d.device_id}`; } },
      h('td', null, h('span', { class: 'val' }, d.serial || d.device_id)), h('td', null, h('b', null, d.name || d.device_id)),
      h('td', { class: 'small nowrap' }, d.last_seen ? h('span', { class: 'row', style: { gap: '6px' } }, h('span', { class: 'dot ' + (Date.now() - new Date(d.last_seen) < 36e5 ? 'on' : '') }), ago(d.last_seen)) : h('span', { class: 'faint' }, 'never')),
      h('td', { class: 'small nowrap' }, d.last_seen ? [signalBars(d.last_snr), ' ', d.last_rssi + ' dBm'] : '—'),
      h('td', null, d.uplink_count || 0),
      h('td', { class: 'small mono', style: { maxWidth: '360px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' } }, d.last_decoded ? JSON.stringify(d.last_decoded) : '—'))))));
}

App.route('/applications/:app', async ({ app }) => {
  const [a, devs] = await Promise.all([GET('/applications/' + app), GET(`/applications/${app}/devices`)]);
  const active = devs.filter((d) => d.last_seen && Date.now() - new Date(d.last_seen) < 864e5).length;
  return h('div', { class: 'stack' }, appHeader(a, ''),
    h('div', { class: 'grid g3' },
      h('div', { class: 'card stat' }, h('div', { class: 'label' }, 'Nodes'), h('div', { class: 'value' }, devs.length), h('div', { class: 'hint' }, active + ' active in 24 h')),
      h('div', { class: 'card stat' }, h('div', { class: 'label' }, 'Decoder'), h('div', { class: 'value', style: { fontSize: '18px' } }, (a.uplink_formatter || {}).type || 'none')),
      h('div', { class: 'card stat' }, h('div', { class: 'label' }, 'Webhooks'), h('div', { class: 'value' }, (a.webhooks || []).filter((w) => w.enabled).length), h('div', { class: 'hint' }, 'enabled'))),
    h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'Nodes'), h('a', { class: 'btn sm', href: `#/applications/${app}/devices` }, 'View all')), deviceTable(app, devs.slice(0, 10))),
    liveView({ app, short: true, title: 'Live data' }));
});

App.route('/applications/:app/devices', async ({ app }) => {
  const [a, devs] = await Promise.all([GET('/applications/' + app), GET(`/applications/${app}/devices`)]);
  const search = inp('', { placeholder: 'Filter by serial or name…', style: { maxWidth: '320px' } });
  const holder = h('div', null, deviceTable(app, devs));
  search.addEventListener('input', () => {
    const q = search.value.toLowerCase();
    holder.replaceChildren(deviceTable(app, devs.filter((d) => JSON.stringify([d.device_id, d.name, d.serial]).toLowerCase().includes(q))));
  });
  return h('div', { class: 'stack' }, appHeader(a, 'devices'),
    h('div', { class: 'card' }, h('div', { class: 'card-head' }, search, h('a', { class: 'btn', href: `#/applications/${app}/devices/new` }, icon('plus'), 'Add node manually')), holder));
});

App.route('/applications/:app/live', async ({ app }) => {
  const a = await GET('/applications/' + app);
  return h('div', { class: 'stack' }, appHeader(a, 'live'), liveView({ app }));
});

App.route('/applications/:app/formatters', async ({ app }) => {
  const a = await GET('/applications/' + app);
  let sample = '';
  try { const evs = await GET(`/events?app=${app}&names=as.up&limit=1`); if (evs[0]) sample = evs[0].data.data_hex; } catch (e) { /* none */ }
  let dir = 'up';
  const holder = h('div');
  const seg = h('div', { class: 'seg' });
  const render = () => {
    seg.replaceChildren(h('button', { class: dir === 'up' ? 'on' : '', onclick: () => { dir = 'up'; render(); } }, 'Uplink decoder'), h('button', { class: dir === 'down' ? 'on' : '', onclick: () => { dir = 'down'; render(); } }, 'Downlink encoder'));
    if (dir === 'up') {
      holder.replaceChildren(formatterEditor(a.uplink_formatter, { samplePayload: sample, onSave: async (f) => {
        try { Object.assign(a, await api('PUT', '/applications/' + app, { uplink_formatter: f })); toast('Decoder saved'); } catch (e) { errToast(e); }
      } }));
    } else {
      const st = JSON.parse(JSON.stringify(a.downlink_formatter || { type: 'none' }));
      const typeSel = sel(st.type || 'none', [['none', 'None (send hex bytes)'], ['javascript', 'Custom JavaScript (encodeDownlink)']]);
      const ta = h('textarea', { class: 'code', spellcheck: 'false', value: st.code || FMT_TEMPLATES.javascript });
      const testIn = h('textarea', { class: 'mono', rows: 3, value: '{"relay": true}' });
      const out = h('div');
      holder.replaceChildren(h('div', { class: 'stack' }, field('Encoder type', typeSel), field('Encoder code', ta, 'encodeDownlink(input) → {bytes}. Used when you send a downlink as JSON. The NODE_ID + SERIAL header is added afterwards if enabled.'),
        h('div', { class: 'form-section stack' }, h('h3', null, 'Test'), field('Downlink JSON', testIn),
          h('div', null, h('button', { class: 'btn', onclick: async () => {
            try { const r = await api('POST', '/formatters/test', { direction: 'down', formatter: { type: 'javascript', code: ta.value }, decoded_payload: JSON.parse(testIn.value), f_port: 1 });
              out.replaceChildren(r.errors ? h('div', { class: 'callout warn' }, r.errors.join('; ')) : h('div', { class: 'row' }, 'Data bytes:', h('span', { class: 'val' }, r.bytes_hex || '(empty)')));
            } catch (e) { out.replaceChildren(h('div', { class: 'callout warn' }, e.message)); }
          } }, icon('play'), 'Test encoder')), out),
        h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: async () => {
          try { Object.assign(a, await api('PUT', '/applications/' + app, { downlink_formatter: { type: typeSel.value, code: ta.value } })); toast('Encoder saved'); } catch (e) { errToast(e); }
        } }, 'Save changes'))));
    }
  };
  render();
  return h('div', { class: 'stack' }, appHeader(a, 'formatters'), h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'Decoder'), seg), h('div', { class: 'card-body' }, holder)));
});

App.route('/applications/:app/integrations', async ({ app }) => {
  const [a, settings, sys] = await Promise.all([GET('/applications/' + app), GET('/settings'), GET('/system')]);
  const hooks = JSON.parse(JSON.stringify(a.webhooks || []));
  const list = h('div', { class: 'stack' });
  const EVENTS = [['up', 'Node data'], ['downlink_queued', 'Downlink queued'], ['downlink_sent', 'Downlink sent'], ['downlink_failed', 'Downlink failed']];
  const render = () => {
    list.replaceChildren(...hooks.map((w, i) => {
      const hdrs = h('textarea', { class: 'mono', rows: 2, value: Object.entries(w.headers || {}).map(([k, v]) => k + ': ' + v).join('\n'), placeholder: 'Authorization: Bearer xyz',
        oninput: (e) => { w.headers = {}; e.target.value.split('\n').forEach((l) => { const ix = l.indexOf(':'); if (ix > 0) w.headers[l.slice(0, ix).trim()] = l.slice(ix + 1).trim(); }); } });
      const en = checkbox(w.enabled, 'Enabled'); en.input.onchange = () => { w.enabled = en.input.checked; };
      return h('div', { class: 'card card-body stack' },
        h('div', { class: 'row between' }, h('h3', { style: { margin: 0 } }, w.name || 'Webhook ' + (i + 1)), h('button', { class: 'btn sm danger', onclick: () => { hooks.splice(i, 1); render(); } }, icon('trash'), 'Remove')),
        h('div', { class: 'grid g2' }, field('Name', inp(w.name, { oninput: (e) => { w.name = e.target.value; } })),
          field('Body', sel(w.format || 'full', [['full', 'Full message (decoded + all gateway metadata)'], ['decoded', 'Short (node, serial, decoded, gateways)']], { onchange: (e) => { w.format = e.target.value; } }))),
        field('URL', inp(w.url, { type: 'url', placeholder: 'https://example.com/lora', oninput: (e) => { w.url = e.target.value; } })),
        field('Extra headers', hdrs),
        field('Send on', h('div', { class: 'row' }, EVENTS.map(([k, l]) => { const c = checkbox((w.events || []).includes(k), l);
          c.input.onchange = () => { w.events = (w.events || []).filter((x) => x !== k); if (c.input.checked) w.events.push(k); }; return c.el; }))),
        en.el);
    }));
    if (!hooks.length) list.append(h('div', { class: 'empty card' }, icon('link'), 'No webhooks configured.'));
  };
  render();
  const host = (sys.addresses || [])[0] || location.hostname;
  const p = settings.mqtt_topic_prefix;
  return h('div', { class: 'stack' }, appHeader(a, 'integrations'),
    h('div', { class: 'card card-body stack' }, h('h2', null, 'MQTT'),
      h('p', { class: 'muted', style: { margin: 0 } }, 'Every decoded packet of this group is published as JSON on the built-in MQTT broker' + (settings.external_mqtt.enabled ? ' and on ' + settings.external_mqtt.url : '') + '.'),
      h('dl', { class: 'kv' },
        h('dt', null, 'Broker'), h('dd', null, sys.listeners.mqtt ? val(`mqtt://${host}${sys.listeners.mqtt.replace(/^[^:]*/, '')}`) : h('span', { class: 'badge warn' }, 'built-in broker disabled')),
        h('dt', null, 'Node data'), h('dd', null, val(`${p}/${app}/+/up`)),
        h('dt', null, 'Send downlink'), h('dd', null, val(`${p}/${app}/{node}/down`)),
        h('dt', null, 'Downlink body'), h('dd', null, h('code', null, '{"data_hex":"0102"}  or  {"decoded":{"relay":true}}')),
        h('dt', null, 'Downlink events'), h('dd', null, val(`${p}/${app}/+/down/sent`))),
      h('div', { class: 'small muted' }, 'Home Assistant auto-discovery: ', h('b', null, settings.home_assistant.enabled ? 'on' : 'off'), ' · ', h('a', { href: '#/integrations/homeassistant' }, 'change'))),
    h('div', { class: 'card card-body stack' },
      h('div', { class: 'row between' }, h('h2', { style: { margin: 0 } }, 'Webhooks'), h('button', { class: 'btn', onclick: () => { hooks.push({ name: '', url: '', events: ['up'], enabled: true, format: 'full', headers: {} }); render(); } }, icon('plus'), 'Add webhook')),
      list,
      h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: async () => {
        try { const r = await api('PUT', '/applications/' + app, { webhooks: hooks }); hooks.splice(0, hooks.length, ...r.webhooks); render(); toast('Webhooks saved'); } catch (e) { errToast(e); }
      } }, 'Save webhooks'))));
});

function radioFields(dl) {
  return [h('div', { class: 'grid g3' },
    field('When to send', sel(dl.mode || 'immediate', [['immediate', 'Immediately'], ['after_uplink', 'After the node\'s next packet']], { onchange: (e) => { dl.mode = e.target.value; } })),
    field('Delay after packet (ms)', inp(dl.delay_ms || 1000, { type: 'number', min: 0, oninput: (e) => { dl.delay_ms = Number(e.target.value); } })),
    field('TX power (dBm)', inp(dl.power || 14, { type: 'number', min: 0, max: 27, oninput: (e) => { dl.power = Number(e.target.value); } }))),
  h('div', { class: 'grid g3' },
    field('Frequency (MHz)', inp(dl.frequency || '', { type: 'number', step: 'any', placeholder: 'same as last packet', oninput: (e) => { dl.frequency = Number(e.target.value); } })),
    field('Data rate', inp(dl.data_rate || '', { class: 'mono', placeholder: 'SF7BW125 (same as last packet)', oninput: (e) => { dl.data_rate = e.target.value.toUpperCase(); } })),
    field('Coding rate', sel(dl.coding_rate || '4/5', ['4/5', '4/6', '4/7', '4/8'], { onchange: (e) => { dl.coding_rate = e.target.value; } }))),
  (() => { const c = checkbox(dl.invert_polarity, 'Invert IQ', 'Only if your nodes listen with inverted IQ.'); c.input.onchange = () => { dl.invert_polarity = c.input.checked; }; return c.el; })()];
}

App.route('/applications/:app/settings', async ({ app }) => {
  const a = await GET('/applications/' + app);
  const name = inp(a.name), desc = h('textarea', { rows: 2, value: a.description || '' }), nodeId = inp(a.node_id, { class: 'mono' });
  const auto = checkbox(a.auto_register_nodes, 'Add new nodes automatically');
  const hdr = checkbox(a.downlink_header, 'Prepend NODE_ID + SERIAL_ID to downlinks');
  const dl = Object.assign({ mode: 'immediate', power: 14, delay_ms: 1000 }, a.downlink_radio || {});
  return h('div', { class: 'stack' }, appHeader(a, 'settings'),
    h('div', { class: 'card card-body' }, h('div', { class: 'form' },
      field('NODE_ID (hex)', nodeId), field('Name', name), field('Description', desc), auto.el, hdr.el,
      h('div', { class: 'form-section stack' }, h('h3', null, 'Downlink radio settings'), ...radioFields(dl)),
      h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: async () => {
        try { await api('PUT', '/applications/' + app, { name: name.value, description: desc.value, node_id: cleanHex(nodeId.value), auto_register_nodes: auto.input.checked, downlink_header: hdr.input.checked, downlink_radio: dl }); toast('Saved'); App.dispatch(); } catch (e) { errToast(e); }
      } }, 'Save changes')))),
    h('div', { class: 'card card-body' }, h('h2', null, 'Delete node group'), h('p', { class: 'muted' }, 'Deletes the group and all of its nodes.'),
      h('button', { class: 'btn danger', onclick: async () => {
        if (!(await confirmModal('Delete node group', `Delete "${a.name || app}" and all its nodes? This cannot be undone.`))) return;
        try { await api('DELETE', '/applications/' + app); toast('Deleted'); location.hash = '#/applications'; } catch (e) { errToast(e); }
      } }, icon('trash'), 'Delete node group')));
});

// ======================= Nodes =======================
function deviceForm(d, isNew, onSubmit) {
  const st = JSON.parse(JSON.stringify(d));
  st.mode = st.mode || 'node';
  const serial = inp(st.serial, { class: 'mono', disabled: !isNew, placeholder: '96B840EB', oninput: (e) => { st.serial = e.target.value; } });
  const name = inp(st.name, { oninput: (e) => { st.name = e.target.value; } });
  const desc = h('textarea', { rows: 2, value: st.description || '', oninput: (e) => { st.description = e.target.value; } });
  const lat = inp(st.location ? st.location.latitude : '', { type: 'number', step: 'any', placeholder: '46.77' });
  const lon = inp(st.location ? st.location.longitude : '', { type: 'number', step: 'any', placeholder: '23.59' });
  const attrs = h('textarea', { class: 'mono', rows: 3, value: Object.entries(st.attributes || {}).map(([k, v]) => k + '=' + v).join('\n'), placeholder: 'room=kitchen',
    oninput: (e) => { st.attributes = {}; e.target.value.split('\n').forEach((l) => { const i = l.indexOf('='); if (i > 0) st.attributes[l.slice(0, i).trim()] = l.slice(i + 1).trim(); }); } });
  return h('div', { class: 'form' },
    field('SERIAL_ID (hex)', serial, isNew ? 'The serial bytes that follow the NODE_ID in the packet.' : null),
    field('Name', name), field('Description', desc),
    h('div', { class: 'grid g2' }, field('Latitude', lat), field('Longitude', lon)),
    field('Attributes', attrs, 'key=value per line. Available to JavaScript decoders as input.attributes.'),
    h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: () => {
      st.location = lat.value && lon.value ? { latitude: Number(lat.value), longitude: Number(lon.value), altitude: 0, source: 'user' } : null;
      st.serial = cleanHex(st.serial);
      if (isNew) st.device_id = st.serial.toLowerCase();
      onSubmit(st);
    } }, isNew ? 'Add node' : 'Save changes')));
}

App.route('/applications/:app/devices/new', async ({ app }) => {
  const a = await GET('/applications/' + app);
  return h('div', { class: 'stack' }, pageHead([['Node groups', '#/applications'], [a.node_id || app, '#/applications/' + app], ['Add node']], 'Add node'),
    h('div', { class: 'card card-body' }, deviceForm({ mode: 'node', serial: '', name: '' }, true, async (out) => {
      try { const d = await api('POST', `/applications/${app}/devices`, out); toast('Node added'); location.hash = `#/applications/${app}/devices/${d.device_id}`; } catch (e) { errToast(e); }
    })));
});

function devHeader(a, d, active) {
  const base = `#/applications/${a.application_id}/devices/${d.device_id}`;
  const seen = d.last_seen ? h('span', { class: 'row', style: { gap: '6px' } }, h('span', { class: 'dot ' + (Date.now() - new Date(d.last_seen) < 36e5 ? 'on' : '') }), 'Last packet ' + ago(d.last_seen)) : h('span', { class: 'faint' }, 'No packets yet');
  return [pageHead([['Node groups', '#/applications'], [a.node_id || a.application_id, '#/applications/' + a.application_id], [d.serial || d.device_id]],
    d.name || d.device_id, [h('span', null, h('span', { class: 'val' }, (a.node_id || '?') + ' / ' + (d.serial || d.device_id))), seen,
      h('span', null, (d.uplink_count || 0) + ' packets'), d.downlink_queue && d.downlink_queue.length ? h('span', { class: 'badge warn' }, d.downlink_queue.length + ' downlink(s) queued') : null]),
  tabs(base, [['', 'Overview'], ['live', 'Live data'], ['messages', 'Messages'], ['messaging', 'Downlink'], ['formatters', 'Decoder'], ['settings', 'Settings']], active)];
}

async function loadDev(app, dev) { return Promise.all([GET('/applications/' + app), GET(`/applications/${app}/devices/${dev}`)]); }

App.route('/applications/:app/devices/:dev', async ({ app, dev }) => {
  const [a, d] = await loadDev(app, dev);
  let last = null;
  try { const evs = await GET(`/events?app=${app}&dev=${dev}&names=as.up&limit=1`); last = evs[0] && evs[0].data; } catch (e) { /* none */ }
  const gwTable = last && last.gateways ? h('div', { class: 'table-wrap' }, h('table', { class: 't' },
    h('thead', null, h('tr', null, ['Gateway', 'RSSI', 'SNR', 'Channel', 'Source'].map((x) => h('th', null, x)))),
    h('tbody', null, last.gateways.map((g) => h('tr', null, h('td', null, h('a', { href: '#/gateways/' + g.gateway_id }, g.name || g.gateway_id)),
      h('td', null, g.rssi + ' dBm'), h('td', null, [signalBars(g.snr), ' ', g.snr + ' dB']), h('td', null, g.channel), h('td', { class: 'small muted' }, g.source)))))) : null;
  return h('div', { class: 'stack' }, devHeader(a, d, ''),
    h('div', { class: 'grid g2' },
      h('div', { class: 'card card-body' }, h('h2', null, 'Node'), h('dl', { class: 'kv' },
        h('dt', null, 'NODE_ID (group)'), h('dd', null, val(a.node_id || '—')),
        h('dt', null, 'SERIAL_ID'), h('dd', null, val(d.serial || d.device_id)),
        h('dt', null, 'Name'), h('dd', null, d.name || '—'),
        h('dt', null, 'Decoder'), h('dd', null, (d.uplink_formatter || {}).type === 'inherit' || !(d.uplink_formatter || {}).type ? 'group decoder (' + ((a.uplink_formatter || {}).type || 'none') + ')' : d.uplink_formatter.type),
        h('dt', null, 'Added'), h('dd', null, fmtDate(d.created_at)),
        d.location ? [h('dt', null, 'Location'), h('dd', null, h('a', { href: `https://www.openstreetmap.org/?mlat=${d.location.latitude}&mlon=${d.location.longitude}#map=16/${d.location.latitude}/${d.location.longitude}`, target: '_blank', rel: 'noopener' }, `${d.location.latitude}, ${d.location.longitude}`))] : null)),
      h('div', { class: 'card card-body' }, h('h2', null, 'Activity'), h('dl', { class: 'kv' },
        h('dt', null, 'Last packet'), h('dd', null, d.last_seen ? fmtDate(d.last_seen) + ' (' + ago(d.last_seen) + ')' : 'never'),
        h('dt', null, 'Packets'), h('dd', null, d.uplink_count || 0),
        h('dt', null, 'Best gateway'), h('dd', null, d.last_gateway ? h('a', { href: '#/gateways/' + d.last_gateway }, d.last_gateway) : '—'),
        h('dt', null, 'Signal'), h('dd', null, d.last_seen ? [signalBars(d.last_snr), ` RSSI ${d.last_rssi} dBm · SNR ${d.last_snr} dB`] : '—'),
        last ? [h('dt', null, 'Radio'), h('dd', null, `${(last.radio.frequency / 1e6).toFixed(3)} MHz · ${last.radio.data_rate} · ${last.radio.airtime_ms} ms airtime`)] : null,
        last ? [h('dt', null, 'Last data'), h('dd', null, val(last.data_hex || '(empty)'))] : null))),
    h('div', { class: 'grid g2' },
      h('div', { class: 'card card-body' }, h('h2', null, 'Latest decoded data'), d.last_decoded ? jsonView(d.last_decoded) : h('p', { class: 'muted' }, 'Nothing decoded yet. Set a decoder on the Decoder tab.')),
      h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'Received by (last packet)')), gwTable || h('div', { class: 'empty' }, 'No packets yet.'))),
    liveView({ app, dev, short: true, title: 'Live data' }));
});

App.route('/applications/:app/devices/:dev/live', async ({ app, dev }) => {
  const [a, d] = await loadDev(app, dev);
  return h('div', { class: 'stack' }, devHeader(a, d, 'live'), liveView({ app, dev }));
});

App.route('/applications/:app/devices/:dev/messaging', async ({ app, dev }) => {
  const [a, d] = await loadDev(app, dev);
  const gws = await GET('/gateways');
  let mode = 'hex';
  const payload = inp('', { class: 'mono', placeholder: '01 02 AB' });
  const json = h('textarea', { class: 'mono', rows: 4, value: '{\n  "relay": true\n}' });
  const repl = checkbox(false, 'Replace queued downlinks');
  const payloadHolder = h('div');
  const seg = h('div', { class: 'seg' });
  const renderMode = () => {
    seg.replaceChildren(h('button', { class: mode === 'hex' ? 'on' : '', onclick: () => { mode = 'hex'; renderMode(); } }, 'Hex bytes'), h('button', { class: mode === 'json' ? 'on' : '', onclick: () => { mode = 'json'; renderMode(); } }, 'JSON (encoder)'));
    payloadHolder.replaceChildren(mode === 'hex' ? field('Data (hex)', payload) : field('Data as JSON', json, 'Converted to bytes by the group\'s downlink encoder.'));
  };
  renderMode();
  const dl = Object.assign({ mode: 'immediate' }, a.downlink_radio || {});
  const queue = h('div');
  const renderQueue = (q) => {
    queue.replaceChildren(q && q.length ? h('div', { class: 'table-wrap' }, h('table', { class: 't' }, h('thead', null, h('tr', null, ['#', 'Bytes sent on air', 'Queued'].map((x) => h('th', null, x)))),
      h('tbody', null, q.map((x, i) => h('tr', null, h('td', null, i + 1), h('td', { class: 'mono small' }, b64hex(x.frm_payload)), h('td', { class: 'small muted' }, ago(x.created_at))))))) :
      h('div', { class: 'empty' }, 'Nothing queued.'));
  };
  renderQueue(d.downlink_queue);
  const refresh = async () => { const nd = await GET(`/applications/${app}/devices/${dev}`); renderQueue(nd.downlink_queue); };
  const t = setInterval(refresh, 4000); App.onLeave(() => clearInterval(t));
  const send = async () => {
    const body = { f_port: 1, replace: repl.input.checked };
    try {
      if (mode === 'hex') body.frm_payload_hex = cleanHex(payload.value); else body.decoded_payload = JSON.parse(json.value);
      await api('POST', `/applications/${app}/devices/${dev}/down`, body); toast(dl.mode === 'after_uplink' ? 'Queued — sent after the next packet' : 'Downlink sent'); refresh();
    } catch (e) { errToast(e); }
  };
  const simGw = sel(gws[0] && gws[0].gateway_id, gws.map((g) => [g.gateway_id, g.name || g.gateway_id]));
  const simPayload = inp('0167011002687F', { class: 'mono' });
  const simulate = async () => {
    try { await api('POST', '/simulate', { gateway_id: simGw.value, payload_hex: cleanHex(simPayload.value), application_id: app, device_id: dev, rssi: -70, snr: 8 }); toast('Test packet injected — see live data'); } catch (e) { errToast(e); }
  };
  return h('div', { class: 'stack' }, devHeader(a, d, 'messaging'),
    h('div', { class: 'grid g2' },
      h('div', { class: 'card card-body stack' }, h('div', { class: 'row between' }, h('h2', { style: { margin: 0 } }, 'Send downlink'), seg),
        h('div', { class: 'callout info small' }, (dl.mode === 'after_uplink' ? 'Sent right after this node\'s next packet' : 'Sent immediately via the gateway that heard this node best') +
          (a.downlink_header ? `. On air: ${a.node_id} ${d.serial} + your data.` : '. Your data is sent as-is (no header).'), ' ', h('a', { href: `#/applications/${app}/settings` }, 'Change')),
        payloadHolder, repl.el,
        h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: send }, icon('send'), 'Send'))),
      h('div', { class: 'stack' },
        h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'Queue'), h('button', { class: 'btn sm', onclick: async () => { await api('DELETE', `/applications/${app}/devices/${dev}/down`); refresh(); toast('Queue cleared'); } }, icon('trash'), 'Clear')), queue),
        h('div', { class: 'card card-body stack' }, h('h2', { style: { margin: 0 } }, 'Inject test packet'),
          h('p', { class: 'small muted', style: { margin: 0 } }, `Pretends this node sent ${a.node_id} ${d.serial} + the data below (for testing decoders without hardware).`),
          gws.length ? [field('Gateway', simGw), field('Data (hex, after header)', simPayload), h('div', null, h('button', { class: 'btn', onclick: simulate }, icon('play'), 'Inject'))] : h('div', { class: 'muted small' }, 'No gateway registered yet.')))),
    liveView({ app, dev, short: true, title: 'Live data', hideGatewayRaw: true }));
});

function b64hex(b64) {
  try { return Array.from(atob(b64 || ''), (c) => c.charCodeAt(0).toString(16).padStart(2, '0')).join('').toUpperCase() || '(empty)'; } catch (e) { return b64; }
}

App.route('/applications/:app/devices/:dev/formatters', async ({ app, dev }) => {
  const [a, d] = await loadDev(app, dev);
  let sample = '';
  try { const evs = await GET(`/events?app=${app}&dev=${dev}&names=as.up&limit=1`); if (evs[0]) sample = evs[0].data.data_hex; } catch (e) { /* none */ }
  return h('div', { class: 'stack' }, devHeader(a, d, 'formatters'),
    h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'Decoder for this node')),
      h('div', { class: 'card-body' }, formatterEditor(d.uplink_formatter, { allowInherit: true, samplePayload: sample, onSave: async (f) => {
        try { await api('PUT', `/applications/${app}/devices/${dev}`, { uplink_formatter: f }); toast('Decoder saved'); } catch (e) { errToast(e); }
      } }))));
});

App.route('/applications/:app/devices/:dev/settings', async ({ app, dev }) => {
  const [a, d] = await loadDev(app, dev);
  return h('div', { class: 'stack' }, devHeader(a, d, 'settings'),
    h('div', { class: 'card card-body' }, deviceForm(d, false, async (out) => {
      try { await api('PUT', `/applications/${app}/devices/${dev}`, out); toast('Node updated'); App.dispatch(); } catch (e) { errToast(e); }
    })),
    h('div', { class: 'card card-body' }, h('h2', null, 'Delete node'), h('p', { class: 'muted' }, 'If the group adds nodes automatically, it comes back with its next packet.'),
      h('button', { class: 'btn danger', onclick: async () => {
        if (!(await confirmModal('Delete node', `Delete node ${d.serial || dev}?`))) return;
        try { await api('DELETE', `/applications/${app}/devices/${dev}`); toast('Deleted'); location.hash = `#/applications/${app}/devices`; } catch (e) { errToast(e); }
      } }, icon('trash'), 'Delete node')));
});

// ======================= Gateways =======================
App.route('/gateways', async () => {
  const gws = await GET('/gateways');
  return h('div', { class: 'stack' },
    pageHead([['Gateways']], 'Gateways', [`${gws.filter((g) => g.online).length} of ${gws.length} connected`], [h('a', { class: 'btn primary', href: '#/gateways/new' }, icon('plus'), 'Register gateway')]),
    h('div', { class: 'card' }, gws.length ? h('div', { class: 'table-wrap' }, h('table', { class: 't' },
      h('thead', null, h('tr', null, ['Status', 'ID', 'Name', 'EUI', 'Connection', 'Address', 'RX / TX', 'Last seen'].map((x) => h('th', null, x)))),
      h('tbody', null, gws.map((g) => h('tr', { class: 'click', onclick: () => { location.hash = '#/gateways/' + g.gateway_id; } },
        h('td', null, h('span', { class: 'dot ' + (g.online ? 'on' : 'off') })), h('td', null, h('b', null, g.gateway_id)), h('td', null, g.name || '—'),
        h('td', { class: 'mono small' }, g.eui), h('td', null, h('span', { class: 'badge' }, KIND_LABEL[g.kind] || g.kind)), h('td', { class: 'mono small' }, g.last_addr || '—'),
        h('td', { class: 'nowrap' }, `${g.rx_count} / ${g.tx_count}`), h('td', { class: 'small muted nowrap' }, ago(g.last_seen))))))) :
      h('div', { class: 'empty' }, icon('gateway'), 'No gateways yet. Gateways auto-register when they connect (see Settings), or register one manually.')),
    connectionHelp());
});

function connectionHelp(g) {
  const host = location.hostname;
  const eui = g ? g.eui : '<GATEWAY_EUI>';
  const udpCfg = `"gateway_conf": {\n  "gateway_ID": "${eui}",\n  "server_address": "${host}",\n  "serv_port_up": 1700,\n  "serv_port_down": 1700\n}`;
  const rawCfg = `"target_ip": "${host}",\n"port_up": 5555,\n"port_down": 5556`;
  const httpCurl = `curl -X POST http://${location.host}/api/v1/gateways/${eui}/uplink \\\n  -H "Authorization: Bearer ${g && g.api_key ? g.api_key : '<GATEWAY_API_KEY>'}" \\\n  -H "Content-Type: application/json" \\\n  -d '{"rxpk":[{"tmst":1,"freq":868.1,"stat":1,"modu":"LORA","datr":"SF7BW125","codr":"4/5","rssi":-60,"lsnr":9,"size":3,"data":"AQID"}]}'`;
  const block = (title, text, code) => h('div', { class: 'card card-body stack' }, h('div', { class: 'row between' }, h('h3', { style: { margin: 0 } }, title), h('button', { class: 'btn sm', onclick: () => copyText(code) }, icon('copy'), 'Copy')), h('p', { class: 'small muted', style: { margin: 0 } }, text), h('pre', { class: 'json' }, code));
  return h('div', { class: 'stack' }, h('h2', { style: { marginTop: '8px' } }, 'Connecting satellite gateways'),
    h('div', { class: 'grid g3' },
      block('Semtech UDP packet forwarder', 'Any standard gateway (Dragino LPS8, RAK, Kerlink, MikroTik, sx1302_hal lora_pkt_fwd…). Edit global_conf.json / local_conf.json:', udpCfg),
      block('Another Bobcat (raw_lora_app)', 'Point the remote Bobcat\'s /etc/lora/config.json at this hub. The EUI is learned from its heartbeat:', rawCfg),
      block('HTTP push', 'For custom satellites (ESP32, LTE modems…). Register a gateway with connection type HTTP to get an API key:', httpCurl)));
}

App.route('/gateways/new', async () => {
  const bands = await GET('/bands');
  const st = { gateway_id: '', eui: '', name: '', kind: 'semtech_udp', frequency_plan: 'EU868', downlink_enabled: true };
  const id = inp('', { class: 'mono', placeholder: 'my-gateway', oninput: (e) => { st.gateway_id = e.target.value; } });
  const eui = hexInput('', 16); eui.input.oninput = (e) => { st.eui = e.target.value; if (!id.dataset.touched) { st.gateway_id = id.value = 'eui-' + cleanHex(e.target.value).toLowerCase(); } };
  id.addEventListener('input', () => { id.dataset.touched = 1; });
  return h('div', { class: 'stack' }, pageHead([['Gateways', '#/gateways'], ['Register']], 'Register gateway'),
    h('div', { class: 'card card-body' }, h('div', { class: 'form' },
      field('Gateway EUI', eui.el, '64-bit EUI the gateway reports (packet forwarder gateway_ID / SX1302 chip ID).'),
      field('Gateway ID', id), field('Name', inp('', { oninput: (e) => { st.name = e.target.value; } })),
      h('div', { class: 'grid g2' },
        field('Connection type', sel(st.kind, [['semtech_udp', 'Semtech UDP packet forwarder'], ['bobcat', 'Bobcat raw_lora_app'], ['http', 'HTTP push (API key)']], { onchange: (e) => { st.kind = e.target.value; } })),
        field('Frequency plan', sel(st.frequency_plan, bands, { onchange: (e) => { st.frequency_plan = e.target.value; } }))),
      h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: async () => {
        try { await api('POST', '/gateways', st); toast('Gateway registered'); location.hash = '#/gateways/' + st.gateway_id; } catch (e) { errToast(e); }
      } }, 'Register gateway'), h('a', { class: 'btn', href: '#/gateways' }, 'Cancel')))));
});

function gwHeader(g, active) {
  return [pageHead([['Gateways', '#/gateways'], [g.gateway_id]], g.name || g.gateway_id,
    [h('span', { class: 'mono small' }, 'EUI ' + g.eui), onlineBadge(g.online, g.last_seen), h('span', { class: 'badge' }, KIND_LABEL[g.kind] || g.kind), h('span', null, `↑ ${g.rx_count}  ↓ ${g.tx_count}`)]),
  tabs('#/gateways/' + g.gateway_id, [['', 'Overview'], ['live', 'Live data'], ['radio', 'Radio'], ['transmit', 'Transmit'], ['settings', 'Settings'], ['connect', 'Connection']], active)];
}

App.route('/gateways/:id', async ({ id }) => {
  const g = await GET('/gateways/' + id);
  const st = g.last_status || {};
  const statusRows = Object.entries(st).filter(([k]) => k !== 'updated_at').map(([k, v]) => [h('dt', null, k), h('dd', { class: 'mono small' }, typeof v === 'object' ? JSON.stringify(v) : String(v))]);
  return h('div', { class: 'stack' }, gwHeader(g, ''),
    h('div', { class: 'grid g2' },
      h('div', { class: 'card card-body' }, h('h2', null, 'General information'), h('dl', { class: 'kv' },
        h('dt', null, 'Gateway ID'), h('dd', null, val(g.gateway_id)), h('dt', null, 'Gateway EUI'), h('dd', null, val(g.eui)),
        h('dt', null, 'Connection'), h('dd', null, KIND_LABEL[g.kind] || g.kind), h('dt', null, 'Address'), h('dd', { class: 'mono small' }, g.last_addr || '—'),
        h('dt', null, 'Frequency plan'), h('dd', null, g.frequency_plan), h('dt', null, 'Downlinks'), h('dd', null, g.downlink_enabled ? 'enabled' : 'disabled'),
        h('dt', null, 'Last seen'), h('dd', null, fmtDate(g.last_seen)), h('dt', null, 'Received / sent'), h('dd', null, `${g.rx_count} / ${g.tx_count}`),
        h('dt', null, 'Location'), h('dd', null, g.location ? h('a', { href: `https://www.openstreetmap.org/?mlat=${g.location.latitude}&mlon=${g.location.longitude}#map=15/${g.location.latitude}/${g.location.longitude}`, target: '_blank', rel: 'noopener' }, `${g.location.latitude}, ${g.location.longitude}`) : h('a', { href: `#/gateways/${id}/settings` }, 'set location')),
        g.forwarder_status && Object.keys(g.forwarder_status).length ? [h('dt', null, 'Upstream'), h('dd', null, Object.entries(g.forwarder_status).map(([a, ok]) => h('span', { class: 'badge ' + (ok ? 'ok' : 'warn') }, a)))] : null)),
      h('div', { class: 'card card-body' }, h('h2', null, 'Last status report'), statusRows.length ? h('dl', { class: 'kv small' }, statusRows) : h('p', { class: 'muted' }, 'No status received yet.'))),
    liveView({ gateway: id, short: true, title: 'Live data' }));
});

App.route('/gateways/:id/live', async ({ id }) => {
  const g = await GET('/gateways/' + id);
  return h('div', { class: 'stack' }, gwHeader(g, 'live'), liveView({ gateway: id }));
});

App.route('/gateways/:id/settings', async ({ id }) => {
  const [g, bands] = await Promise.all([GET('/gateways/' + id), GET('/bands')]);
  const st = JSON.parse(JSON.stringify(g));
  delete st.online; delete st.forwarder_status;
  st.forwarders = st.forwarders || [];
  const fwHolder = h('div', { class: 'stack' });
  const renderFw = () => {
    fwHolder.replaceChildren(...st.forwarders.map((fw, i) => {
      const en = checkbox(fw.enabled, 'Enabled'); en.input.onchange = () => { fw.enabled = en.input.checked; };
      const dl = checkbox(fw.downlink, 'Accept downlinks from upstream'); dl.input.onchange = () => { fw.downlink = dl.input.checked; };
      return h('div', { class: 'card card-body stack' }, h('div', { class: 'grid g2' }, field('Name', inp(fw.name, { placeholder: 'TTN EU1', oninput: (e) => { fw.name = e.target.value; } })),
        field('Server (host:port)', inp(fw.address, { class: 'mono', placeholder: 'eu1.cloud.thethings.network:1700', oninput: (e) => { fw.address = e.target.value; } }))),
      h('div', { class: 'row between' }, h('div', { class: 'row' }, en.el, dl.el), h('button', { class: 'btn sm danger', onclick: () => { st.forwarders.splice(i, 1); renderFw(); } }, icon('trash'), 'Remove')));
    }));
    if (!st.forwarders.length) fwHolder.append(h('p', { class: 'muted small', style: { margin: 0 } }, 'No upstream forwarders. Add one to also send this gateway\'s raw traffic to TTN, ChirpStack or another LoRaHub.'));
  };
  renderFw();
  const lat = inp(st.location ? st.location.latitude : '', { type: 'number', step: 'any' }), lon = inp(st.location ? st.location.longitude : '', { type: 'number', step: 'any' }), alt = inp(st.location ? st.location.altitude : '', { type: 'number', step: 'any' });
  const dl = checkbox(st.downlink_enabled, 'Downlinks enabled', 'Allow LoRaHub to schedule downlinks through this gateway.');
  const dis = checkbox(st.disabled, 'Disabled', 'Ignore all traffic from this gateway.');
  const numIn = (key, attrs) => inp(st[key] || '', Object.assign({ type: 'number', step: 'any', oninput: (e) => { st[key] = e.target.value === '' ? 0 : Number(e.target.value); } }, attrs));
  const minSnr = inp(st.min_snr ?? '', { type: 'number', step: 'any', placeholder: 'no limit' });
  const attrs = h('textarea', { class: 'mono', rows: 3, placeholder: 'site=roof', value: Object.entries(st.attributes || {}).map(([k, v]) => k + '=' + v).join('\n') });
  return h('div', { class: 'stack' }, gwHeader(g, 'settings'),
    h('div', { class: 'card card-body' }, h('div', { class: 'form' },
      h('div', { class: 'grid g2' }, field('Gateway ID', inp(st.gateway_id, { disabled: true, class: 'mono' })), field('Gateway EUI', inp(st.eui, { class: 'mono', oninput: (e) => { st.eui = e.target.value; } }))),
      field('Name', inp(st.name, { oninput: (e) => { st.name = e.target.value; } })),
      field('Description', h('textarea', { rows: 2, value: st.description || '', oninput: (e) => { st.description = e.target.value; } })),
      h('div', { class: 'grid g3' },
        field('Connection type', sel(st.kind, [['semtech_udp', 'Semtech UDP'], ['bobcat', 'Bobcat raw_lora_app'], ['http', 'HTTP push']], { onchange: (e) => { st.kind = e.target.value; } })),
        field('Frequency plan', sel(st.frequency_plan, bands, { onchange: (e) => { st.frequency_plan = e.target.value; } })),
        field('TX power override (dBm)', inp(st.tx_power_override || '', { type: 'number', min: 0, max: 30, placeholder: 'region default', oninput: (e) => { st.tx_power_override = Number(e.target.value); } }))),
      st.kind === 'http' ? field('Gateway API key', h('div', { class: 'row' }, st.api_key ? val(st.api_key, { secret: true }) : h('span', { class: 'muted' }, 'generated on save'))) : null,
      h('div', { class: 'form-section stack' }, h('h3', null, 'Location'), h('div', { class: 'grid g3' }, field('Latitude', lat), field('Longitude', lon), field('Altitude (m)', alt)),
        h('div', null, h('button', { class: 'btn sm', onclick: () => navigator.geolocation && navigator.geolocation.getCurrentPosition((p) => { lat.value = p.coords.latitude.toFixed(6); lon.value = p.coords.longitude.toFixed(6); alt.value = Math.round(p.coords.altitude || 0); }, (e) => errToast(e)) }, 'Use my current location'))),
      h('div', { class: 'form-section stack' }, h('h3', null, 'Reception'),
        h('div', { class: 'grid g3' }, field('RSSI correction (dB)', numIn('rssi_offset', { placeholder: '0' }), 'Added to every RSSI this gateway reports.'), field('Ignore packets below RSSI (dBm)', numIn('min_rssi', { max: 0, placeholder: 'no limit' })), field('Ignore packets below SNR (dB)', minSnr)), dis.el),
      h('div', { class: 'form-section stack' }, h('h3', null, 'Transmission'), dl.el,
        h('div', { class: 'grid g3' }, field('Antenna gain (dBi)', numIn('antenna_gain_dbi', { min: 0, placeholder: '0' }), 'Subtracted from the requested power, so the radiated power stays at the level you set.'), field('Duty-cycle limit (%)', numIn('duty_cycle_pct', { min: 0, max: 100, placeholder: 'no limit' }), 'Measured over a sliding hour. Downlinks above it are refused.'), h('div'))),
      st.kind === 'bobcat' ? h('div', { class: 'form-section stack' }, h('h3', null, 'raw_lora_app link'),
        h('div', { class: 'grid g3' }, field('Control port (UDP)', numIn('control_port', { min: 1, max: 65535, placeholder: 'detected automatically' }), 'The port_down of this gateway. Used for downlinks and remote settings.'), field('Status port (TCP)', numIn('status_port', { min: 1, max: 65535, placeholder: '5557' })), h('div'))) : null,
      h('div', { class: 'form-section stack' }, h('h3', null, 'Monitoring'),
        h('div', { class: 'grid g3' }, field('Offline after (s)', numIn('offline_after_s', { min: 10, placeholder: 'global setting' })), h('div'), h('div')),
        field('Attributes', attrs, 'key=value per line, for your own notes and integrations.')),
      h('div', { class: 'form-section stack' }, h('div', { class: 'row between' }, h('h3', { style: { margin: 0 } }, 'Upstream forwarding (Semtech UDP)'), h('button', { class: 'btn sm', onclick: () => { st.forwarders.push({ name: '', address: '', enabled: true, downlink: false }); renderFw(); } }, icon('plus'), 'Add upstream')), fwHolder),
      h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: async () => {
        st.location = lat.value && lon.value ? { latitude: Number(lat.value), longitude: Number(lon.value), altitude: Number(alt.value || 0), source: 'user' } : null;
        st.downlink_enabled = dl.input.checked; st.disabled = dis.input.checked;
        st.min_snr = minSnr.value === '' ? null : Number(minSnr.value);
        st.attributes = {}; attrs.value.split('\n').forEach((l) => { const i = l.indexOf('='); if (i > 0) st.attributes[l.slice(0, i).trim()] = l.slice(i + 1).trim(); });
        try { await api('PUT', '/gateways/' + id, st); toast('Gateway saved'); App.dispatch(); } catch (e) { errToast(e); }
      } }, 'Save changes')))),
    h('div', { class: 'card card-body' }, h('h2', null, 'Delete gateway'), h('p', { class: 'muted' }, 'If auto-registration is enabled the gateway re-appears the next time it connects.'),
      h('button', { class: 'btn danger', onclick: async () => {
        if (!(await confirmModal('Delete gateway', `Delete gateway "${id}"?`))) return;
        try { await api('DELETE', '/gateways/' + id); toast('Gateway deleted'); location.hash = '#/gateways'; } catch (e) { errToast(e); }
      } }, icon('trash'), 'Delete gateway')));
});

App.route('/gateways/:id/connect', async ({ id }) => {
  const g = await GET('/gateways/' + id);
  return h('div', { class: 'stack' }, gwHeader(g, 'connect'), connectionHelp(g));
});

// ======================= Live data =======================
App.route('/live', async () => {
  const unmatched = await GET('/events?names=ns.up.unmatched&limit=200');
  const seen = new Map();
  for (const e of unmatched) {
    const key = e.data.node_id || (e.data.payload_hex || e.data.raw_payload_hex || '').slice(0, 4);
    if (!seen.has(key)) seen.set(key, { e, key, serials: new Set(), n: 0 });
    const x = seen.get(key); x.n++; if (e.data.serial) x.serials.add(e.data.serial);
  }
  const onboarding = seen.size ? h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'Unknown NODE_IDs heard recently'), h('span', { class: 'small muted' }, 'add a group to start decoding them')),
    h('div', { class: 'table-wrap' }, h('table', { class: 't' }, h('thead', null, h('tr', null, ['NODE_ID', 'Serials', 'Packets', 'Last heard', 'Sample packet', ''].map((x) => h('th', null, x)))),
      h('tbody', null, [...seen.values()].slice(0, 10).map(({ e, key, serials, n }) =>
        h('tr', null, h('td', null, h('span', { class: 'val' }, key)), h('td', { class: 'mono small' }, [...serials].slice(0, 4).join(', ') + (serials.size > 4 ? ' …' : '')),
          h('td', null, n), h('td', { class: 'small muted' }, ago(e.time)), h('td', { class: 'mono small' }, (e.data.payload_hex || e.data.raw_payload_hex || '').slice(0, 28)),
          e.application_id ? h('td', { class: 'right' }, h('a', { class: 'btn sm', href: '#/applications/' + e.application_id + '/settings' }, 'Open group'))
            : h('td', { class: 'right' }, h('a', { class: 'btn sm primary', href: '#/applications/new?node_id=' + key }, icon('plus'), 'Add group')))))))) : null;
  return h('div', { class: 'stack' }, pageHead([['Live data']], 'Live data', ['Everything received by all gateways']), onboarding, liveView({}));
});

// ======================= Concentrator =======================
// ======================= Settings =======================
async function settingsPage(tab) {
  if (tab === 'integrations') { location.replace('#/integrations'); return h('div'); }
  const s = await GET('/settings');
  const base = '#/settings';
  const head = [pageHead([['Settings']], 'Settings'), tabs(base, [['', 'Network server'], ['keys', 'API keys'], ['account', 'Account'], ['system', 'System']], tab)];
  const save = async (patch) => { try { await api('PUT', '/settings', Object.assign({}, s, patch)); toast('Settings saved'); } catch (e) { errToast(e); } };
  const card = (...k) => h('div', { class: 'card card-body' }, h('div', { class: 'form' }, ...k));
  if (tab === '') {
    const dedup = inp(s.dedup_window_ms, { type: 'number', min: 0, max: 2000 });
    const band = sel(s.default_frequency_plan, await GET('/bands'));
    const netid = inp(s.net_id, { class: 'mono' }), prefix = inp(s.dev_addr_prefix, { class: 'mono' }), bits = inp(s.dev_addr_prefix_bits, { type: 'number', min: 0, max: 31 });
    const rx1 = inp(s.rx1_delay_s, { type: 'number', min: 1, max: 15 });
    const auto = checkbox(s.auto_register_gateways, 'Auto-register new gateways', 'Accept traffic from any gateway that connects and register it automatically. Disable to only accept registered gateways.');
    const crc = checkbox(s.drop_crc_errors, 'Drop packets with CRC errors');
    const raw = checkbox(s.store_gateway_raw_events, 'Store gateway receptions in history', 'Keep raw gateway receptions (gs.up) in the event history, not only live.');
    const ret = inp(s.event_retention_hours, { type: 'number', min: 1 }), rows = inp(s.event_max_rows, { type: 'number', min: 1000 }), off = inp(s.gateway_offline_after_s, { type: 'number', min: 10 });
    const pf = s.packet_format;
    const pfIn = (k) => inp(pf[k], { type: 'number', min: 0, max: 64, oninput: (e) => { pf[k] = Number(e.target.value); } });
    const agrp = checkbox(s.auto_create_groups, 'Create node groups automatically', 'Packets with an unknown NODE_ID create a new group instead of showing up as unknown.');
    return h('div', { class: 'stack' }, head, card(
      h('h3', null, 'Packet format: NODE_ID | SERIAL_ID | data'),
      h('div', { class: 'grid g3' }, field('NODE_ID offset', pfIn('node_id_offset')), field('NODE_ID length (bytes)', pfIn('node_id_length')), h('div')),
      h('div', { class: 'grid g3' }, field('SERIAL_ID offset', pfIn('serial_offset')), field('SERIAL_ID length (bytes)', pfIn('serial_length')), field('Data starts at byte', pfIn('data_offset'))),
      agrp.el,
      h('h3', null, 'Packet processing'),
      h('div', { class: 'grid g2' }, field('De-duplication window (ms)', dedup, 'The same packet heard by several gateways within this window becomes one message listing every gateway.'), field('Default frequency plan', band)),
      auto.el, crc.el, raw.el,
      h('h3', null, 'Storage'),
      h('div', { class: 'grid g3' }, field('Event retention (hours)', ret), field('Max stored events', rows), field('Gateway offline after (s)', off)),
      h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: () => save({
        packet_format: pf, auto_create_groups: agrp.input.checked,
        dedup_window_ms: Number(dedup.value), default_frequency_plan: band.value, net_id: netid.value, dev_addr_prefix: prefix.value, dev_addr_prefix_bits: Number(bits.value), rx1_delay_s: Number(rx1.value),
        auto_register_gateways: auto.input.checked, drop_crc_errors: crc.input.checked, store_gateway_raw_events: raw.input.checked,
        event_retention_hours: Number(ret.value), event_max_rows: Number(rows.value), gateway_offline_after_s: Number(off.value) }) }, 'Save settings'))));
  }
  if (tab === 'keys') {
    const keys = await GET('/keys');
    const name = inp('', { placeholder: 'node-red' });
    const created = h('div');
    return h('div', { class: 'stack' }, head,
      h('div', { class: 'card card-body stack' }, h('h2', { style: { margin: 0 } }, 'Create API key'), h('p', { class: 'small muted', style: { margin: 0 } }, 'Use as "Authorization: Bearer <key>" for the REST API and the /api/live websocket (?api_key=…).'),
        h('div', { class: 'row' }, h('div', { style: { flex: 1, maxWidth: '360px' } }, name), h('button', { class: 'btn primary', onclick: async () => {
          try { const k = await api('POST', '/keys', { name: name.value }); created.replaceChildren(h('div', { class: 'callout info stack' }, h('b', null, 'Copy this key now — it will not be shown again.'), h('div', { class: 'row' }, val(k.key)))); } catch (e) { errToast(e); }
        } }, icon('key'), 'Create key')), created),
      h('div', { class: 'card' }, keys.length ? h('table', { class: 't' }, h('thead', null, h('tr', null, ['Name', 'ID', 'Created', ''].map((x) => h('th', null, x)))),
        h('tbody', null, keys.map((k) => h('tr', null, h('td', null, k.name), h('td', { class: 'mono small' }, k.id), h('td', { class: 'small muted' }, fmtDate(k.created_at)),
          h('td', { class: 'right' }, h('button', { class: 'btn sm danger', onclick: async () => { if (await confirmModal('Revoke API key', `Revoke "${k.name}"?`, 'Revoke')) { await api('DELETE', '/keys/' + k.id); App.dispatch(); } } }, 'Revoke')))))) :
        h('div', { class: 'empty' }, 'No API keys.')));
  }
  if (tab === 'account') {
    const cur = h('input', { type: 'password', autocomplete: 'current-password' }), nw = h('input', { type: 'password', autocomplete: 'new-password' });
    return h('div', { class: 'stack' }, head, card(h('h3', null, 'Change password for ' + App.user), field('Current password', cur), field('New password', nw),
      h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: async () => {
        try { await api('POST', '/auth/password', { current: cur.value, new: nw.value }); toast('Password changed'); cur.value = nw.value = ''; } catch (e) { errToast(e); }
      } }, 'Change password'))));
  }
  const sys = await GET('/system');
  return h('div', { class: 'stack' }, head, h('div', { class: 'card card-body' }, jsonView(sys)),
    h('div', { class: 'card card-body' }, h('h2', null, 'Maintenance'), h('div', { class: 'row' },
      h('button', { class: 'btn danger', onclick: async () => { if (await confirmModal('Clear event history', 'Delete all stored events?', 'Clear')) { await api('DELETE', '/events'); toast('Event history cleared'); } } }, icon('trash'), 'Clear event history'))));
}
App.route('/settings', () => settingsPage(''));
App.route('/settings/:tab', ({ tab }) => settingsPage(tab));

App.start();
