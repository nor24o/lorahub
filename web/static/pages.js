/* LoRaHub console — pages. */
'use strict';

const KIND_LABEL = { bobcat: 'Bobcat (raw_lora_app)', semtech_udp: 'Semtech UDP', http: 'HTTP', simulated: 'Simulated' };
const MODE_LABEL = { otaa: 'LoRaWAN OTAA', abp: 'LoRaWAN ABP', raw: 'Raw LoRa' };

// ======================= Overview =======================
App.route('/', async () => {
  const [ov, gws, sys] = await Promise.all([GET('/overview'), GET('/gateways'), GET('/system')]);
  const stat = (label, value, hint, href) => h(href ? 'a' : 'div', { class: 'card stat', href, style: { color: 'inherit', textDecoration: 'none' } },
    h('div', { class: 'label' }, label), h('div', { class: 'value' }, value), hint ? h('div', { class: 'hint' }, hint) : null);
  const now = Date.now(), hour = 3600e3;
  const buckets = []; for (let i = 23; i >= 0; i--) buckets.push(Math.floor((now - i * hour) / hour) * hour);
  const series = [
    { label: 'Gateway receptions', color: 'var(--faint)', data: ov.histogram['gs.up'] || {} },
    { label: 'Decoded uplinks', color: 'var(--success)', data: ov.histogram['as.up'] || {} },
    { label: 'Downlinks', color: 'var(--primary)', data: ov.histogram['ns.down.sent'] || {} },
  ];
  return h('div', { class: 'stack' },
    pageHead(null, 'Overview', [h('span', null, sys.hostname), h('span', null, 'up ' + fmtDur(sys.uptime_s)), sys.addresses && sys.addresses.length ? h('span', null, sys.addresses.join(', ')) : null]),
    h('div', { class: 'grid g4' },
      stat('Gateways', `${ov.gateways_online} / ${ov.gateways}`, 'connected / registered', '#/gateways'),
      stat('End devices', ov.devices, ov.devices_active_24h + ' active in 24 h', '#/applications'),
      stat('Uplinks (24 h)', ov.uplinks_24h, ov.gateway_rx_24h + ' gateway receptions', '#/live'),
      stat('Downlinks (24 h)', ov.downlinks_24h, ov.unmatched_24h + ' unmatched packets', '#/live')),
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
        h('li', null, h('a', { href: '#/applications/new' }, 'Create an application')),
        h('li', null, 'Register end devices (OTAA, ABP or raw payload match)'),
        h('li', null, 'Add a payload formatter to get decoded JSON'),
        h('li', null, h('a', { href: '#/gateways/new' }, 'Connect satellite gateways')),
        h('li', null, 'Consume data via MQTT, webhooks or the API')))));
});

// ======================= Applications =======================
App.route('/applications', async () => {
  const apps = await GET('/applications');
  return h('div', { class: 'stack' },
    pageHead([['Applications']], 'Applications', [apps.length + ' applications'], [h('a', { class: 'btn primary', href: '#/applications/new' }, icon('plus'), 'Create application')]),
    h('div', { class: 'card' }, apps.length ? h('div', { class: 'table-wrap' }, h('table', { class: 't' },
      h('thead', null, h('tr', null, ['ID', 'Name', 'Devices', 'Formatter', 'Created'].map((x) => h('th', null, x)))),
      h('tbody', null, apps.map((a) => h('tr', { class: 'click', onclick: () => { location.hash = '#/applications/' + a.application_id; } },
        h('td', null, h('b', null, a.application_id)), h('td', null, a.name || h('span', { class: 'faint' }, '—')),
        h('td', null, a.device_count), h('td', null, h('span', { class: 'badge' }, (a.uplink_formatter || {}).type || 'none')),
        h('td', { class: 'small muted' }, fmtDate(a.created_at))))))) :
      h('div', { class: 'empty' }, icon('app'), 'No applications yet.', h('div', { style: { marginTop: '10px' } }, h('a', { class: 'btn primary', href: '#/applications/new' }, 'Create your first application')))));
});

App.route('/applications/new', async () => {
  const id = inp('', { placeholder: 'my-sensors', class: 'mono' });
  const name = inp('', { placeholder: 'My sensors' });
  const desc = h('textarea', { rows: 3 });
  const tpl = sel('cayenne_lpp', [['none', 'None'], ['cayenne_lpp', 'CayenneLPP'], ['javascript', 'Custom JavaScript'], ['fields', 'Byte fields']]);
  name.addEventListener('input', () => { if (!id.dataset.touched) id.value = name.value.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '').slice(0, 36); });
  id.addEventListener('input', () => { id.dataset.touched = 1; });
  const save = async () => {
    try {
      const f = { type: tpl.value }; if (f.type === 'javascript') f.code = FMT_TEMPLATES.javascript;
      await api('POST', '/applications', { application_id: id.value.trim(), name: name.value, description: desc.value, uplink_formatter: f, downlink_formatter: { type: 'none' } });
      toast('Application created'); location.hash = '#/applications/' + id.value.trim();
    } catch (e) { errToast(e); }
  };
  return h('div', { class: 'stack' }, pageHead([['Applications', '#/applications'], ['Create']], 'Create application'),
    h('div', { class: 'card card-body' }, h('div', { class: 'form' },
      field('Application name', name), field('Application ID', id, 'Lowercase letters, digits and dashes. Used in MQTT topics and the API; cannot be changed later.'),
      field('Description', desc), field('Default uplink payload formatter', tpl),
      h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: save }, 'Create application'), h('a', { class: 'btn', href: '#/applications' }, 'Cancel')))));
});

function appHeader(a, active) {
  const base = '#/applications/' + a.application_id;
  return [pageHead([['Applications', '#/applications'], [a.application_id]], a.name || a.application_id, [h('span', { class: 'mono small' }, 'ID: ' + a.application_id), a.description ? h('span', null, a.description) : null]),
    tabs(base, [['', 'Overview'], ['devices', 'End devices'], ['live', 'Live data'], ['formatters', 'Payload formatters'], ['integrations', 'Integrations'], ['settings', 'General settings']], active)];
}

function deviceTable(appID, devs) {
  if (!devs.length) return h('div', { class: 'empty' }, icon('device'), 'No end devices in this application yet.',
    h('div', { style: { marginTop: '10px' } }, h('a', { class: 'btn primary', href: `#/applications/${appID}/devices/new` }, icon('plus'), 'Register end device')));
  return h('div', { class: 'table-wrap' }, h('table', { class: 't' },
    h('thead', null, h('tr', null, ['ID', 'Name', 'Type', 'DevEUI / match', 'Last seen', 'Signal', 'Uplinks'].map((x) => h('th', null, x)))),
    h('tbody', null, devs.map((d) => h('tr', { class: 'click', onclick: () => { location.hash = `#/applications/${appID}/devices/${d.device_id}`; } },
      h('td', null, h('b', null, d.device_id)), h('td', null, d.name || h('span', { class: 'faint' }, '—')),
      h('td', null, h('span', { class: 'badge ' + (d.mode === 'raw' ? 'purple' : 'info') }, MODE_LABEL[d.mode] || d.mode)),
      h('td', { class: 'mono small' }, d.mode === 'raw' ? (d.raw_match ? `${d.raw_match.type} ${d.raw_match.value}` : '—') : (d.dev_eui || (d.session && d.session.dev_addr) || '—')),
      h('td', { class: 'small nowrap' }, d.last_seen ? h('span', { class: 'row', style: { gap: '6px' } }, h('span', { class: 'dot ' + (Date.now() - new Date(d.last_seen) < 36e5 ? 'on' : '') }), ago(d.last_seen)) : h('span', { class: 'faint' }, 'never')),
      h('td', { class: 'small nowrap' }, d.last_seen ? [signalBars(d.last_snr), ' ', d.last_rssi + ' dBm'] : '—'),
      h('td', null, d.uplink_count || 0))))));
}

App.route('/applications/:app', async ({ app }) => {
  const [a, devs] = await Promise.all([GET('/applications/' + app), GET(`/applications/${app}/devices`)]);
  const active = devs.filter((d) => d.last_seen && Date.now() - new Date(d.last_seen) < 864e5).length;
  return h('div', { class: 'stack' }, appHeader(a, ''),
    h('div', { class: 'grid g3' },
      h('div', { class: 'card stat' }, h('div', { class: 'label' }, 'End devices'), h('div', { class: 'value' }, devs.length), h('div', { class: 'hint' }, active + ' active in 24 h')),
      h('div', { class: 'card stat' }, h('div', { class: 'label' }, 'Uplink formatter'), h('div', { class: 'value', style: { fontSize: '18px' } }, (a.uplink_formatter || {}).type || 'none')),
      h('div', { class: 'card stat' }, h('div', { class: 'label' }, 'Webhooks'), h('div', { class: 'value' }, (a.webhooks || []).filter((w) => w.enabled).length), h('div', { class: 'hint' }, 'enabled'))),
    h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'End devices'), h('a', { class: 'btn sm primary', href: `#/applications/${app}/devices/new` }, icon('plus'), 'Register end device')), deviceTable(app, devs.slice(0, 10))),
    liveView({ app, short: true, title: 'Live data' }));
});

App.route('/applications/:app/devices', async ({ app }) => {
  const [a, devs] = await Promise.all([GET('/applications/' + app), GET(`/applications/${app}/devices`)]);
  const search = inp('', { placeholder: 'Filter by ID, name, EUI…', style: { maxWidth: '320px' } });
  const holder = h('div', null, deviceTable(app, devs));
  search.addEventListener('input', () => {
    const q = search.value.toLowerCase();
    holder.replaceChildren(deviceTable(app, devs.filter((d) => JSON.stringify([d.device_id, d.name, d.dev_eui, d.session && d.session.dev_addr]).toLowerCase().includes(q))));
  });
  return h('div', { class: 'stack' }, appHeader(a, 'devices'),
    h('div', { class: 'card' }, h('div', { class: 'card-head' }, search, h('a', { class: 'btn primary', href: `#/applications/${app}/devices/new` }, icon('plus'), 'Register end device')), holder));
});

App.route('/applications/:app/live', async ({ app }) => {
  const a = await GET('/applications/' + app);
  return h('div', { class: 'stack' }, appHeader(a, 'live'), liveView({ app }));
});

App.route('/applications/:app/formatters', async ({ app }) => {
  const a = await GET('/applications/' + app);
  let dir = 'up';
  const holder = h('div');
  const seg = h('div', { class: 'seg' });
  const render = () => {
    seg.replaceChildren(h('button', { class: dir === 'up' ? 'on' : '', onclick: () => { dir = 'up'; render(); } }, 'Uplink'), h('button', { class: dir === 'down' ? 'on' : '', onclick: () => { dir = 'down'; render(); } }, 'Downlink'));
    if (dir === 'up') {
      holder.replaceChildren(formatterEditor(a.uplink_formatter, { onSave: async (f) => {
        try { Object.assign(a, await api('PUT', '/applications/' + app, { uplink_formatter: f })); toast('Uplink formatter saved'); } catch (e) { errToast(e); }
      } }));
    } else {
      const st = JSON.parse(JSON.stringify(a.downlink_formatter || { type: 'none' }));
      const typeSel = sel(st.type || 'none', [['none', 'None (send raw bytes)'], ['javascript', 'Custom JavaScript (encodeDownlink)']]);
      const ta = h('textarea', { class: 'code', spellcheck: 'false', value: st.code || FMT_TEMPLATES.javascript });
      const testIn = h('textarea', { class: 'mono', rows: 3, value: '{"relay": true}' });
      const out = h('div');
      holder.replaceChildren(h('div', { class: 'stack' }, field('Formatter type', typeSel), field('Formatter code', ta, 'Implements encodeDownlink(input) → {bytes, fPort}. Used when a downlink is scheduled with decoded_payload (UI, MQTT or API).'),
        h('div', { class: 'form-section stack' }, h('h3', null, 'Test'), field('Decoded payload (JSON)', testIn),
          h('div', null, h('button', { class: 'btn', onclick: async () => {
            try { const r = await api('POST', '/formatters/test', { direction: 'down', formatter: { type: 'javascript', code: ta.value }, decoded_payload: JSON.parse(testIn.value), f_port: 1 });
              out.replaceChildren(r.errors ? h('div', { class: 'callout warn' }, r.errors.join('; ')) : h('div', { class: 'row' }, 'Bytes:', h('span', { class: 'val' }, r.bytes_hex || '(empty)'), 'FPort:', h('b', null, r.f_port)));
            } catch (e) { out.replaceChildren(h('div', { class: 'callout warn' }, e.message)); }
          } }, icon('play'), 'Test encoder')), out),
        h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: async () => {
          try { Object.assign(a, await api('PUT', '/applications/' + app, { downlink_formatter: { type: typeSel.value, code: ta.value } })); toast('Downlink formatter saved'); } catch (e) { errToast(e); }
        } }, 'Save changes'))));
    }
  };
  render();
  return h('div', { class: 'stack' }, appHeader(a, 'formatters'), h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'Payload formatters'), seg), h('div', { class: 'card-body' }, holder)));
});

App.route('/applications/:app/integrations', async ({ app }) => {
  const [a, settings, sys] = await Promise.all([GET('/applications/' + app), GET('/settings'), GET('/system')]);
  const hooks = JSON.parse(JSON.stringify(a.webhooks || []));
  const list = h('div', { class: 'stack' });
  const EVENTS = [['up', 'Uplink message'], ['join', 'Join accept'], ['downlink_queued', 'Downlink queued'], ['downlink_sent', 'Downlink sent'], ['downlink_failed', 'Downlink failed']];
  const render = () => {
    list.replaceChildren(...hooks.map((w, i) => {
      const hdrs = h('textarea', { class: 'mono', rows: 2, value: Object.entries(w.headers || {}).map(([k, v]) => k + ': ' + v).join('\n'), placeholder: 'Authorization: Bearer xyz',
        oninput: (e) => { w.headers = {}; e.target.value.split('\n').forEach((l) => { const ix = l.indexOf(':'); if (ix > 0) w.headers[l.slice(0, ix).trim()] = l.slice(ix + 1).trim(); }); } });
      const en = checkbox(w.enabled, 'Enabled'); en.input.onchange = () => { w.enabled = en.input.checked; };
      return h('div', { class: 'card card-body stack' },
        h('div', { class: 'row between' }, h('h3', { style: { margin: 0 } }, w.name || 'Webhook ' + (i + 1)), h('button', { class: 'btn sm danger', onclick: () => { hooks.splice(i, 1); render(); } }, icon('trash'), 'Remove')),
        h('div', { class: 'grid g2' }, field('Name', inp(w.name, { oninput: (e) => { w.name = e.target.value; } })),
          field('Payload format', sel(w.format || 'full', [['full', 'Full message (TTN style)'], ['decoded', 'Decoded payload + metadata only']], { onchange: (e) => { w.format = e.target.value; } }))),
        field('Endpoint URL', inp(w.url, { type: 'url', placeholder: 'https://example.com/lora', oninput: (e) => { w.url = e.target.value; } })),
        field('Additional headers', hdrs),
        field('Enabled event types', h('div', { class: 'row' }, EVENTS.map(([k, l]) => { const c = checkbox((w.events || []).includes(k), l);
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
      h('p', { class: 'muted', style: { margin: 0 } }, 'Every decoded message of this application is published on the embedded MQTT broker' + (settings.external_mqtt.enabled ? ' and on the external broker ' + settings.external_mqtt.url : '') + '. Topics follow The Things Stack conventions.'),
      h('dl', { class: 'kv' },
        h('dt', null, 'Broker'), h('dd', null, sys.listeners.mqtt ? val(`mqtt://${host}${sys.listeners.mqtt.replace(/^[^:]*/, '')}`) : h('span', { class: 'badge warn' }, 'embedded broker disabled')),
        h('dt', null, 'Uplinks'), h('dd', null, val(`${p}/${app}/devices/+/up`)),
        h('dt', null, 'Join accepts'), h('dd', null, val(`${p}/${app}/devices/+/join`)),
        h('dt', null, 'Downlink events'), h('dd', null, val(`${p}/${app}/devices/+/down/sent`)),
        h('dt', null, 'Schedule downlink'), h('dd', null, val(`${p}/${app}/devices/{device_id}/down/push`)),
        h('dt', null, 'Downlink body'), h('dd', null, h('code', null, '{"downlinks":[{"f_port":1,"frm_payload":"AQI=","confirmed":false}]}'))),
      h('div', { class: 'small muted' }, 'Home Assistant auto-discovery: ', h('b', null, settings.home_assistant.enabled ? 'enabled' : 'disabled'), ' · ', h('a', { href: '#/settings/integrations' }, 'change'))),
    h('div', { class: 'card card-body stack' },
      h('div', { class: 'row between' }, h('h2', { style: { margin: 0 } }, 'Webhooks'), h('button', { class: 'btn', onclick: () => { hooks.push({ name: '', url: '', events: ['up'], enabled: true, format: 'full', headers: {} }); render(); } }, icon('plus'), 'Add webhook')),
      list,
      h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: async () => {
        try { const r = await api('PUT', '/applications/' + app, { webhooks: hooks }); hooks.splice(0, hooks.length, ...r.webhooks); render(); toast('Webhooks saved'); } catch (e) { errToast(e); }
      } }, 'Save webhooks'))));
});

App.route('/applications/:app/settings', async ({ app }) => {
  const a = await GET('/applications/' + app);
  const name = inp(a.name), desc = h('textarea', { rows: 3, value: a.description || '' });
  return h('div', { class: 'stack' }, appHeader(a, 'settings'),
    h('div', { class: 'card card-body' }, h('div', { class: 'form' },
      field('Application ID', inp(a.application_id, { disabled: true, class: 'mono' })), field('Name', name), field('Description', desc),
      h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: async () => {
        try { await api('PUT', '/applications/' + app, { name: name.value, description: desc.value }); toast('Saved'); } catch (e) { errToast(e); }
      } }, 'Save changes')))),
    h('div', { class: 'card card-body' }, h('h2', null, 'Danger zone'), h('p', { class: 'muted' }, 'Deleting the application also deletes all of its end devices and their sessions.'),
      h('button', { class: 'btn danger', onclick: async () => {
        if (!(await confirmModal('Delete application', `Delete "${app}" and all its end devices? This cannot be undone.`))) return;
        try { await api('DELETE', '/applications/' + app); toast('Application deleted'); location.hash = '#/applications'; } catch (e) { errToast(e); }
      } }, icon('trash'), 'Delete application')));
});

// ======================= End devices =======================
function deviceForm(d, isNew, onSubmit) {
  const st = JSON.parse(JSON.stringify(d));
  st.session = st.session || {};
  st.raw_match = st.raw_match || { type: 'prefix', value: '' };
  st.raw_downlink = st.raw_downlink || { mode: 'immediate', delay_ms: 1000, frequency: 0, data_rate: '', power: 14, invert_polarity: false };
  st.location = st.location || null;
  const holder = h('div', { class: 'form' });
  const bands = ['EU868', 'US915', 'AU915', 'AS923', 'IN865'];
  const render = () => {
    const id = inp(st.device_id, { class: 'mono', disabled: !isNew, oninput: (e) => { st.device_id = e.target.value; }, placeholder: 'node-1' });
    const modeCards = h('div', { class: 'radio-cards' }, [['otaa', 'Over the air activation', 'LoRaWAN join with AppKey (recommended)'], ['abp', 'Activation by personalization', 'LoRaWAN with fixed session keys'], ['raw', 'Raw LoRa device', 'Non-LoRaWAN payload, matched by bytes']].map(([m, t, s]) =>
      h('div', { class: 'radio-card' + (st.mode === m ? ' sel' : ''), onclick: () => { st.mode = m; render(); } }, h('b', null, t), h('span', null, s))));
    const kids = [field('Activation / device type', modeCards),
      h('div', { class: 'grid g2' }, field('End device ID', id, isNew ? 'Lowercase letters, digits and dashes.' : null), field('Name', inp(st.name, { oninput: (e) => { st.name = e.target.value; } }))),
      field('Description', h('textarea', { rows: 2, value: st.description || '', oninput: (e) => { st.description = e.target.value; } }))];
    if (st.mode === 'otaa') {
      const de = hexInput(st.dev_eui, 16), je = hexInput(st.join_eui, 16, 'Fill'), ak = hexInput(st.app_key, 32);
      de.input.oninput = (e) => { st.dev_eui = e.target.value; }; je.input.oninput = (e) => { st.join_eui = e.target.value; }; ak.input.oninput = (e) => { st.app_key = e.target.value; };
      kids.push(h('div', { class: 'form-section stack' }, h('h3', null, 'LoRaWAN OTAA keys'),
        h('div', { class: 'grid g2' }, field('DevEUI', de.el), field('JoinEUI (AppEUI)', je.el, 'All zeros if your device has none.')),
        field('AppKey', ak.el, 'Root key used to verify join requests and derive session keys.')));
    } else if (st.mode === 'abp') {
      const da = hexInput(st.session.dev_addr, 8), nk = hexInput(st.session.nwk_s_key, 32), sk = hexInput(st.session.app_s_key, 32), de = hexInput(st.dev_eui, 16);
      da.input.oninput = (e) => { st.session.dev_addr = e.target.value; }; nk.input.oninput = (e) => { st.session.nwk_s_key = e.target.value; };
      sk.input.oninput = (e) => { st.session.app_s_key = e.target.value; }; de.input.oninput = (e) => { st.dev_eui = e.target.value; };
      kids.push(h('div', { class: 'form-section stack' }, h('h3', null, 'LoRaWAN ABP session'),
        h('div', { class: 'grid g2' }, field('Device address', da.el), field('DevEUI (optional)', de.el)),
        field('Network session key (NwkSKey)', nk.el), field('App session key (AppSKey)', sk.el)));
    } else {
      const m = st.raw_match;
      const strip = checkbox(m.strip_prefix, 'Strip matched prefix before decoding'); strip.input.onchange = () => { m.strip_prefix = strip.input.checked; };
      const dl = st.raw_downlink;
      kids.push(h('div', { class: 'form-section stack' }, h('h3', null, 'Raw payload matching'),
        h('div', { class: 'callout info small' }, 'Raw (non-LoRaWAN) packets are assigned to this device when they match the rule below. The longest matching rule wins.'),
        h('div', { class: 'grid g3' },
          field('Match type', sel(m.type, [['prefix', 'Starts with bytes'], ['bytes', 'Bytes at offset'], ['regex_hex', 'Regex on hex']], { onchange: (e) => { m.type = e.target.value; render(); } })),
          field(m.type === 'regex_hex' ? 'Regular expression' : 'Value (hex)', inp(m.value, { class: 'mono', placeholder: m.type === 'regex_hex' ? '^CAFE.{8}01' : 'CAFE96B8', oninput: (e) => { m.value = e.target.value; } })),
          m.type === 'bytes' ? field('Offset', inp(m.offset || 0, { type: 'number', min: 0, oninput: (e) => { m.offset = Number(e.target.value); } })) : field('Minimum length', inp(m.min_length || '', { type: 'number', min: 0, oninput: (e) => { m.min_length = Number(e.target.value); } }))),
        h('div', { class: 'grid g3' }, field('Exact length (optional)', inp(m.length || '', { type: 'number', min: 0, oninput: (e) => { m.length = Number(e.target.value); } })),
          field('Virtual FPort', inp(st.raw_f_port || 1, { type: 'number', min: 1, max: 255, oninput: (e) => { st.raw_f_port = Number(e.target.value); } }), 'Passed as fPort to the formatter.'),
          h('div', { style: { alignSelf: 'center' } }, strip.el))),
        h('div', { class: 'form-section stack' }, h('h3', null, 'Raw downlinks'),
          h('div', { class: 'grid g3' },
            field('Delivery', sel(dl.mode, [['immediate', 'Immediately (class C style)'], ['after_uplink', 'After next uplink (class A style)']], { onchange: (e) => { dl.mode = e.target.value; } })),
            field('Delay after uplink (ms)', inp(dl.delay_ms, { type: 'number', min: 0, oninput: (e) => { dl.delay_ms = Number(e.target.value); } })),
            field('TX power (dBm)', inp(dl.power, { type: 'number', min: 0, max: 27, oninput: (e) => { dl.power = Number(e.target.value); } }))),
          h('div', { class: 'grid g3' },
            field('Frequency (MHz)', inp(dl.frequency || '', { type: 'number', step: 'any', placeholder: 'same as uplink', oninput: (e) => { dl.frequency = Number(e.target.value); } })),
            field('Data rate', inp(dl.data_rate, { class: 'mono', placeholder: 'SF7BW125 (same as uplink)', oninput: (e) => { dl.data_rate = e.target.value.toUpperCase(); } })),
            field('Coding rate', sel(dl.coding_rate || '4/5', ['4/5', '4/6', '4/7', '4/8'], { onchange: (e) => { dl.coding_rate = e.target.value; } }))),
          (() => { const c = checkbox(dl.invert_polarity, 'Invert IQ polarity', 'Enable if your device listens with inverted IQ (LoRaWAN-style).'); c.input.onchange = () => { dl.invert_polarity = c.input.checked; }; return c.el; })()));
    }
    const lat = inp(st.location ? st.location.latitude : '', { type: 'number', step: 'any', placeholder: '46.77' });
    const lon = inp(st.location ? st.location.longitude : '', { type: 'number', step: 'any', placeholder: '23.59' });
    const updLoc = () => { st.location = lat.value && lon.value ? { latitude: Number(lat.value), longitude: Number(lon.value), altitude: 0, source: 'user' } : null; };
    lat.oninput = updLoc; lon.oninput = updLoc;
    const skip = checkbox(st.skip_fcnt_check, 'Skip frame counter check', 'Accept uplinks with a reset or lower frame counter (insecure, useful for development).');
    skip.input.onchange = () => { st.skip_fcnt_check = skip.input.checked; };
    kids.push(h('div', { class: 'form-section stack' }, h('h3', null, 'Radio & location'),
      h('div', { class: 'grid g3' }, field('Frequency plan', sel(st.frequency_plan || 'EU868', bands, { onchange: (e) => { st.frequency_plan = e.target.value; } })),
        st.mode !== 'raw' ? field('LoRaWAN class', sel(st.class || 'A', [['A', 'Class A'], ['C', 'Class C']], { onchange: (e) => { st.class = e.target.value; } })) : h('div'),
        h('div')),
      h('div', { class: 'grid g2' }, field('Latitude', lat), field('Longitude', lon)),
      st.mode !== 'raw' ? skip.el : null));
    const attrs = h('textarea', { class: 'mono', rows: 3, value: Object.entries(st.attributes || {}).map(([k, v]) => k + '=' + v).join('\n'), placeholder: 'room=kitchen',
      oninput: (e) => { st.attributes = {}; e.target.value.split('\n').forEach((l) => { const i = l.indexOf('='); if (i > 0) st.attributes[l.slice(0, i).trim()] = l.slice(i + 1).trim(); }); } });
    kids.push(field('Attributes', attrs, 'key=value per line. Available to JavaScript formatters as input.attributes.'));
    kids.push(h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: () => {
      const out = JSON.parse(JSON.stringify(st));
      if (out.mode !== 'abp') delete out.session;
      if (out.mode !== 'raw') { delete out.raw_match; delete out.raw_downlink; }
      onSubmit(out);
    } }, isNew ? 'Register end device' : 'Save changes')));
    holder.replaceChildren(...kids);
  };
  render();
  return holder;
}

App.route('/applications/:app/devices/new', async ({ app }) => {
  const a = await GET('/applications/' + app);
  const params = new URLSearchParams(location.hash.split('?')[1] || '');
  const d = { device_id: params.get('id') || '', mode: params.get('mode') || 'otaa', dev_eui: params.get('dev_eui') || '', join_eui: params.get('join_eui') || '', app_key: '', frequency_plan: 'EU868', class: 'A',
    raw_match: params.get('match') ? { type: 'prefix', value: params.get('match') } : undefined };
  return h('div', { class: 'stack' }, pageHead([['Applications', '#/applications'], [app, '#/applications/' + app], ['End devices', `#/applications/${app}/devices`], ['Register']], 'Register end device'),
    h('div', { class: 'card card-body' }, deviceForm(d, true, async (out) => {
      try { await api('POST', `/applications/${app}/devices`, out); toast('End device registered'); location.hash = `#/applications/${app}/devices/${out.device_id}`; } catch (e) { errToast(e); }
    })));
});

function devHeader(a, d, active) {
  const base = `#/applications/${a.application_id}/devices/${d.device_id}`;
  const seen = d.last_seen ? h('span', { class: 'row', style: { gap: '6px' } }, h('span', { class: 'dot ' + (Date.now() - new Date(d.last_seen) < 36e5 ? 'on' : '') }), 'Last activity ' + ago(d.last_seen)) : h('span', { class: 'faint' }, 'No activity yet');
  return [pageHead([['Applications', '#/applications'], [a.application_id, '#/applications/' + a.application_id], ['End devices', `#/applications/${a.application_id}/devices`], [d.device_id]],
    d.name || d.device_id, [h('span', { class: 'mono small' }, 'ID: ' + d.device_id), h('span', { class: 'badge ' + (d.mode === 'raw' ? 'purple' : 'info') }, MODE_LABEL[d.mode]), seen,
      h('span', null, '↑ ' + (d.uplink_count || 0)), d.downlink_queue && d.downlink_queue.length ? h('span', { class: 'badge warn' }, d.downlink_queue.length + ' queued') : null]),
  tabs(base, [['', 'Overview'], ['live', 'Live data'], ['messaging', 'Messaging'], ['formatters', 'Payload formatters'], ['settings', 'Settings']], active)];
}

async function loadDev(app, dev) { return Promise.all([GET('/applications/' + app), GET(`/applications/${app}/devices/${dev}`)]); }

App.route('/applications/:app/devices/:dev', async ({ app, dev }) => {
  const [a, d] = await loadDev(app, dev);
  const info = [h('dt', null, 'Device ID'), h('dd', null, val(d.device_id)), h('dt', null, 'Type'), h('dd', null, MODE_LABEL[d.mode] + (d.mode !== 'raw' ? ' · class ' + d.class : '')), h('dt', null, 'Frequency plan'), h('dd', null, d.frequency_plan)];
  if (d.dev_eui) info.push(h('dt', null, 'DevEUI'), h('dd', null, val(d.dev_eui)));
  if (d.mode === 'otaa') info.push(h('dt', null, 'JoinEUI'), h('dd', null, val(d.join_eui)), h('dt', null, 'AppKey'), h('dd', null, val(d.app_key, { secret: true })));
  if (d.mode === 'raw' && d.raw_match) info.push(h('dt', null, 'Match rule'), h('dd', null, h('span', { class: 'badge' }, d.raw_match.type), val(d.raw_match.value)), h('dt', null, 'Virtual FPort'), h('dd', null, d.raw_f_port || 1));
  info.push(h('dt', null, 'Created'), h('dd', null, fmtDate(d.created_at)));
  const s = d.session;
  const sess = d.mode === 'raw' ? h('p', { class: 'muted' }, 'Raw devices have no LoRaWAN session.') : s ? h('dl', { class: 'kv' },
    h('dt', null, 'Device address'), h('dd', null, val(s.dev_addr)),
    h('dt', null, 'NwkSKey'), h('dd', null, val(s.nwk_s_key, { secret: true })),
    h('dt', null, 'AppSKey'), h('dd', null, val(s.app_s_key, { secret: true })),
    h('dt', null, 'FCnt up / down'), h('dd', null, `${s.has_uplink ? s.f_cnt_up : '—'} / ${s.f_cnt_down}`),
    h('dt', null, 'Session started'), h('dd', null, fmtDate(s.started_at))) : h('div', { class: 'callout warn' }, 'This OTAA device has not joined yet. Power it on near a gateway — the join request will appear in live data.');
  const lastDec = d.last_decoded ? jsonView(d.last_decoded) : h('p', { class: 'muted' }, 'No decoded payload yet.');
  return h('div', { class: 'stack' }, devHeader(a, d, ''),
    h('div', { class: 'grid g2' },
      h('div', { class: 'card card-body' }, h('h2', null, 'General information'), h('dl', { class: 'kv' }, info)),
      h('div', { class: 'stack' },
        h('div', { class: 'card card-body' }, h('h2', null, 'Activity'), h('dl', { class: 'kv' },
          h('dt', null, 'Last seen'), h('dd', null, d.last_seen ? fmtDate(d.last_seen) + ' (' + ago(d.last_seen) + ')' : 'never'),
          h('dt', null, 'Uplinks'), h('dd', null, d.uplink_count || 0),
          h('dt', null, 'Last gateway'), h('dd', null, d.last_gateway ? h('a', { href: '#/gateways/' + d.last_gateway }, d.last_gateway) : '—'),
          h('dt', null, 'Signal'), h('dd', null, d.last_seen ? [signalBars(d.last_snr), ` RSSI ${d.last_rssi} dBm · SNR ${d.last_snr} dB`] : '—'),
          d.location ? [h('dt', null, 'Location'), h('dd', null, h('a', { href: `https://www.openstreetmap.org/?mlat=${d.location.latitude}&mlon=${d.location.longitude}#map=16/${d.location.latitude}/${d.location.longitude}`, target: '_blank', rel: 'noopener' }, `${d.location.latitude}, ${d.location.longitude}`))] : null)),
        h('div', { class: 'card card-body' }, h('h2', null, 'Session'), sess))),
    h('div', { class: 'card card-body' }, h('h2', null, 'Latest decoded payload'), lastDec),
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
  const fport = inp(1, { type: 'number', min: 1, max: 223, style: { width: '100px' } });
  const payload = inp('', { class: 'mono', placeholder: '01 02 AB' });
  const json = h('textarea', { class: 'mono', rows: 4, value: '{\n  "relay": true\n}' });
  const conf = checkbox(false, 'Confirmed downlink');
  const repl = checkbox(false, 'Replace downlink queue');
  const payloadHolder = h('div');
  const seg = h('div', { class: 'seg' });
  const renderMode = () => {
    seg.replaceChildren(h('button', { class: mode === 'hex' ? 'on' : '', onclick: () => { mode = 'hex'; renderMode(); } }, 'Bytes'), h('button', { class: mode === 'json' ? 'on' : '', onclick: () => { mode = 'json'; renderMode(); } }, 'JSON (encodeDownlink)'));
    payloadHolder.replaceChildren(mode === 'hex' ? field('Payload (hex)', payload) : field('Decoded payload', json, 'Encoded by the downlink payload formatter.'));
  };
  renderMode();
  const queue = h('div');
  const renderQueue = (q) => {
    queue.replaceChildren(q && q.length ? h('div', { class: 'table-wrap' }, h('table', { class: 't' }, h('thead', null, h('tr', null, ['#', 'FPort', 'Payload', 'Confirmed', 'Queued'].map((x) => h('th', null, x)))),
      h('tbody', null, q.map((x, i) => h('tr', null, h('td', null, i + 1), h('td', null, x.f_port), h('td', { class: 'mono small' }, b64hex(x.frm_payload)), h('td', null, x.confirmed ? 'yes' : 'no'), h('td', { class: 'small muted' }, ago(x.created_at))))))) :
      h('div', { class: 'empty' }, 'The downlink queue is empty.'));
  };
  renderQueue(d.downlink_queue);
  const refresh = async () => { const nd = await GET(`/applications/${app}/devices/${dev}`); renderQueue(nd.downlink_queue); };
  const t = setInterval(refresh, 4000); App.onLeave(() => clearInterval(t));
  const schedule = async () => {
    const body = { f_port: Number(fport.value), confirmed: conf.input.checked, replace: repl.input.checked };
    try {
      if (mode === 'hex') body.frm_payload_hex = cleanHex(payload.value); else body.decoded_payload = JSON.parse(json.value);
      await api('POST', `/applications/${app}/devices/${dev}/down`, body); toast('Downlink scheduled'); refresh();
    } catch (e) { errToast(e); }
  };
  // Simulate uplink
  const simGw = sel(gws[0] && gws[0].gateway_id, gws.map((g) => [g.gateway_id, g.name || g.gateway_id]));
  const simPayload = inp(d.mode === 'raw' && d.raw_match && d.raw_match.type === 'prefix' ? d.raw_match.value : '0167011002687F', { class: 'mono' });
  const simPort = inp(1, { type: 'number', min: 1, max: 223, style: { width: '90px' } });
  const simulate = async () => {
    try { await api('POST', '/simulate', { gateway_id: simGw.value, payload_hex: cleanHex(simPayload.value), application_id: app, device_id: dev, f_port: Number(simPort.value), rssi: -70, snr: 8 }); toast('Uplink simulated — see live data'); } catch (e) { errToast(e); }
  };
  return h('div', { class: 'stack' }, devHeader(a, d, 'messaging'),
    h('div', { class: 'grid g2' },
      h('div', { class: 'card card-body stack' }, h('div', { class: 'row between' }, h('h2', { style: { margin: 0 } }, 'Schedule downlink'), seg),
        d.mode === 'raw' ? h('div', { class: 'callout info small' }, (d.raw_downlink && d.raw_downlink.mode === 'after_uplink') ? 'Raw device: sent after the next uplink.' : 'Raw device: transmitted immediately via the last gateway that heard it.')
          : h('div', { class: 'callout info small' }, d.class === 'C' ? 'Class C: transmitted immediately on RX2.' : 'Class A: transmitted in the receive window after the next uplink.'),
        d.mode !== 'raw' ? field('FPort', fport) : null, payloadHolder, h('div', { class: 'row' }, d.mode !== 'raw' ? conf.el : null, repl.el),
        h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: schedule }, icon('send'), 'Schedule downlink'))),
      h('div', { class: 'stack' },
        h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'Downlink queue'), h('button', { class: 'btn sm', onclick: async () => { await api('DELETE', `/applications/${app}/devices/${dev}/down`); refresh(); toast('Queue cleared'); } }, icon('trash'), 'Clear')), queue),
        h('div', { class: 'card card-body stack' }, h('h2', { style: { margin: 0 } }, 'Simulate uplink'),
          h('p', { class: 'small muted', style: { margin: 0 } }, d.mode === 'raw' ? 'Injects the raw payload as if received by the gateway.' : 'Encrypts the application payload with the device session and injects it as if received by the gateway.'),
          gws.length ? [field('Gateway', simGw), h('div', { class: 'row' }, h('div', { style: { flex: 1 } }, field('Payload (hex)', simPayload)), d.mode !== 'raw' ? field('FPort', simPort) : null),
            h('div', null, h('button', { class: 'btn', onclick: simulate }, icon('play'), 'Simulate uplink'))] : h('div', { class: 'muted small' }, 'Register a gateway first.')))),
    liveView({ app, dev, short: true, title: 'Live data', hideGatewayRaw: true }));
});

function b64hex(b64) {
  try { return Array.from(atob(b64 || ''), (c) => c.charCodeAt(0).toString(16).padStart(2, '0')).join('').toUpperCase() || '(empty)'; } catch (e) { return b64; }
}

App.route('/applications/:app/devices/:dev/formatters', async ({ app, dev }) => {
  const [a, d] = await loadDev(app, dev);
  let sample = '';
  try { const evs = await GET(`/events?app=${app}&dev=${dev}&names=as.up&limit=1`); if (evs[0]) sample = evs[0].data.uplink_message.frm_payload_hex; } catch (e) { /* none */ }
  return h('div', { class: 'stack' }, devHeader(a, d, 'formatters'),
    h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'Uplink payload formatter')),
      h('div', { class: 'card-body' }, formatterEditor(d.uplink_formatter, { allowInherit: true, samplePayload: sample, fport: d.raw_f_port || 1, onSave: async (f) => {
        try { await api('PUT', `/applications/${app}/devices/${dev}`, { uplink_formatter: f }); toast('Formatter saved'); } catch (e) { errToast(e); }
      } }))));
});

App.route('/applications/:app/devices/:dev/settings', async ({ app, dev }) => {
  const [a, d] = await loadDev(app, dev);
  return h('div', { class: 'stack' }, devHeader(a, d, 'settings'),
    h('div', { class: 'card card-body' }, deviceForm(d, false, async (out) => {
      try { await api('PUT', `/applications/${app}/devices/${dev}`, out); toast('End device updated'); App.dispatch(); } catch (e) { errToast(e); }
    })),
    d.mode !== 'raw' ? h('div', { class: 'card card-body' }, h('h2', null, 'Reset session'),
      h('p', { class: 'muted' }, d.mode === 'otaa' ? 'Forget the current session and used DevNonces. The device must re-join.' : 'Reset the frame counters to zero.'),
      h('button', { class: 'btn', onclick: async () => {
        if (!(await confirmModal('Reset session', 'Reset the LoRaWAN session of this device?', 'Reset', false))) return;
        try { await api('POST', `/applications/${app}/devices/${dev}/reset`); toast('Session reset'); App.dispatch(); } catch (e) { errToast(e); }
      } }, icon('refresh'), 'Reset session')) : null,
    h('div', { class: 'card card-body' }, h('h2', null, 'Delete end device'),
      h('button', { class: 'btn danger', onclick: async () => {
        if (!(await confirmModal('Delete end device', `Delete "${dev}"? This cannot be undone.`))) return;
        try { await api('DELETE', `/applications/${app}/devices/${dev}`); toast('Deleted'); location.hash = `#/applications/${app}/devices`; } catch (e) { errToast(e); }
      } }, icon('trash'), 'Delete end device')));
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
  tabs('#/gateways/' + g.gateway_id, [['', 'Overview'], ['live', 'Live data'], ['transmit', 'Transmit'], ['settings', 'Settings'], ['connect', 'Connection']], active)];
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

App.route('/gateways/:id/transmit', async ({ id }) => {
  const g = await GET('/gateways/' + id);
  const f = { payload: inp('48656C6C6F', { class: 'mono' }), freq: inp(869.525, { type: 'number', step: 'any' }), dr: inp('SF9BW125', { class: 'mono' }),
    cr: sel('4/5', ['4/5', '4/6', '4/7', '4/8']), pw: inp(14, { type: 'number', min: 0, max: 27 }) };
  const ipol = checkbox(false, 'Invert IQ polarity (LoRaWAN downlink style)');
  return h('div', { class: 'stack' }, gwHeader(g, 'transmit'),
    h('div', { class: 'card card-body' }, h('div', { class: 'form' },
      h('div', { class: 'callout warn small' }, 'Transmits a packet immediately. Respect the duty cycle and the maximum EIRP of your region.'),
      field('Payload (hex)', f.payload),
      h('div', { class: 'grid g4' }, field('Frequency (MHz)', f.freq), field('Data rate', f.dr), field('Coding rate', f.cr), field('Power (dBm)', f.pw)), ipol.el,
      h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: async () => {
        try { await api('POST', `/gateways/${id}/downlink`, { payload_hex: cleanHex(f.payload.value), frequency: Number(f.freq.value), data_rate: f.dr.value.toUpperCase(), coding_rate: f.cr.value, power: Number(f.pw.value), invert_polarity: ipol.input.checked }); toast('Transmitted'); } catch (e) { errToast(e); }
      } }, icon('send'), 'Transmit now')))));
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
      h('div', { class: 'form-section stack' }, dl.el, dis.el),
      h('div', { class: 'form-section stack' }, h('div', { class: 'row between' }, h('h3', { style: { margin: 0 } }, 'Upstream forwarding (Semtech UDP)'), h('button', { class: 'btn sm', onclick: () => { st.forwarders.push({ name: '', address: '', enabled: true, downlink: false }); renderFw(); } }, icon('plus'), 'Add upstream')), fwHolder),
      h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: async () => {
        st.location = lat.value && lon.value ? { latitude: Number(lat.value), longitude: Number(lon.value), altitude: Number(alt.value || 0), source: 'user' } : null;
        st.downlink_enabled = dl.input.checked; st.disabled = dis.input.checked;
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
  const devs = await GET('/devices');
  const unmatched = await GET('/events?names=ns.up.unmatched&limit=100');
  const seen = new Map();
  for (const e of unmatched) {
    const lw = e.data.lorawan;
    const key = lw ? (lw.dev_eui || lw.dev_addr) : (e.data.raw_payload_hex || '').slice(0, 4);
    if (!seen.has(key)) seen.set(key, { e, lw, key, n: 0 });
    seen.get(key).n++;
  }
  const apps = await GET('/applications');
  const firstApp = apps[0] && apps[0].application_id;
  const onboarding = seen.size ? h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'Unknown devices heard recently'), h('span', { class: 'small muted' }, 'register them to get decoded data')),
    h('div', { class: 'table-wrap' }, h('table', { class: 't' }, h('thead', null, h('tr', null, ['Kind', 'Identifier', 'Packets', 'Last heard', 'Sample', ''].map((x) => h('th', null, x)))),
      h('tbody', null, [...seen.values()].slice(0, 8).map(({ e, lw, key, n }) => {
        let href = null;
        if (firstApp) {
          if (lw && lw.dev_eui) href = `#/applications/${firstApp}/devices/new?mode=otaa&dev_eui=${lw.dev_eui}&join_eui=${lw.join_eui}`;
          else if (!lw) href = `#/applications/${firstApp}/devices/new?mode=raw&match=${key}&id=node-${key.toLowerCase()}`;
        }
        return h('tr', null, h('td', null, h('span', { class: 'badge ' + (lw ? 'info' : 'purple') }, lw ? lw.m_type : 'Raw')), h('td', { class: 'mono small' }, lw ? (lw.dev_eui ? 'DevEUI ' + lw.dev_eui : 'DevAddr ' + lw.dev_addr) : 'prefix ' + key),
          h('td', null, n), h('td', { class: 'small muted' }, ago(e.time)), h('td', { class: 'mono small' }, (e.data.raw_payload_hex || '').slice(0, 24)),
          h('td', { class: 'right' }, href ? h('a', { class: 'btn sm', href }, icon('plus'), 'Register') : h('span', { class: 'faint small' }, lw ? 'ABP: register with session keys' : 'create an application first')));
      }))))) : null;
  return h('div', { class: 'stack' }, pageHead([['Live data']], 'Live data', ['All events of all gateways, applications and devices', h('span', null, devs.length + ' devices')]), onboarding, liveView({}));
});

// ======================= Payload lab =======================
App.route('/lab', async () => {
  return h('div', { class: 'stack' }, pageHead([['Payload lab']], 'Payload lab', ['Design and test payload formatters against real bytes before assigning them to an application or device.']),
    h('div', { class: 'card card-body' }, formatterEditor({ type: 'fields', fields: [{ name: 'header', offset: 0, length: 2, type: 'hex' }, { name: 'battery', offset: 8, length: 2, type: 'int16_be', scale: 0.01, unit: 'V' }] },
      { samplePayload: 'CAFE96B840EB0102014A0271FFF500170429036700FC0488000000000000000000050048060004' })),
    h('div', { class: 'callout small' }, 'Tip: copy the formatter you built here into an application\'s or device\'s ', h('b', null, 'Payload formatters'), ' tab.'));
});

// ======================= Concentrator =======================
App.route('/concentrator', async () => {
  const c = await GET('/concentrator');
  const cfg = c.config || null;
  const status = c.status;
  const head = pageHead([['Concentrator']], 'Concentrator (SX1302)', [h('span', { class: 'mono small' }, c.config_path), status ? h('span', { class: 'badge ok' }, 'raw_lora_app running') : h('span', { class: 'badge warn' }, 'raw_lora_app status unavailable')],
    [h('button', { class: 'btn', onclick: async () => {
      if (!(await confirmModal('Restart concentrator', 'Restart the raw_lora_app service? Reception pauses for a few seconds.', 'Restart', false))) return;
      try { await api('POST', '/concentrator/restart'); toast('Concentrator restarted'); setTimeout(() => App.dispatch(), 3000); } catch (e) { errToast(e); }
    } }, icon('refresh'), 'Restart service')]);
  const statusCard = status ? h('div', { class: 'grid g4' },
    [['Uptime', fmtDur(status.uptime_s)], ['RX / TX', `${status.rx} / ${status.tx}`], ['CRC errors', status.crc_err], ['Temperature', status.temp_ok ? status.temp_c + ' °C' : '—'],
      ['TX status', status.tx_status], ['RX status', status.rx_status], ['Sync word', status.sync], ['Queue', `${status.q_depth} (drop ${status.q_drop})`]].map(([l, v]) =>
      h('div', { class: 'card stat' }, h('div', { class: 'label' }, l), h('div', { class: 'value', style: { fontSize: '18px' } }, v)))) :
    h('div', { class: 'callout warn' }, c.status_error || 'Status server not configured.');
  if (!cfg) return h('div', { class: 'stack' }, head, statusCard, h('div', { class: 'callout warn' }, 'Cannot read concentrator config: ' + (c.config_error || 'unknown error')));

  const st = JSON.parse(JSON.stringify(cfg));
  let rawMode = false;
  const holder = h('div');
  const num = (k, attrs = {}) => inp(st[k], Object.assign({ type: 'number', step: 'any', oninput: (e) => { st[k] = Number(e.target.value); } }, attrs));
  const txt = (k, attrs = {}) => inp(st[k], Object.assign({ oninput: (e) => { st[k] = e.target.value; } }, attrs));
  const bool = (k, label, help) => { const c2 = checkbox(st[k], label, help); c2.input.onchange = () => { st[k] = c2.input.checked; }; return c2.el; };
  const chTable = () => {
    const tbody = h('tbody');
    const freqOf = (i) => ((((st.chan_radio || [])[i] || 0) === 0 ? st.rf0_freq : st.rf1_freq) + st.chan_if[i]) / 1e6;
    const render = () => {
      tbody.replaceChildren(...(st.chan_if || []).map((iff, i) => {
        const label = h('span', null, freqOf(i).toFixed(3) + ' MHz');
        return h('tr', null, h('td', null, i),
          h('td', null, sel((st.chan_radio || [])[i] || 0, [[0, 'Radio 0'], [1, 'Radio 1']], { onchange: (e) => { st.chan_radio[i] = Number(e.target.value); render(); } })),
          h('td', null, inp(iff, { type: 'number', step: 1000, style: { width: '120px' }, oninput: (e) => { st.chan_if[i] = Number(e.target.value); label.textContent = freqOf(i).toFixed(3) + ' MHz'; } })),
          h('td', { class: 'mono' }, label));
      }));
    };
    render();
    return h('div', { class: 'table-wrap card' }, h('table', { class: 't' }, h('thead', null, h('tr', null, ['Channel', 'Radio', 'IF offset (Hz)', 'Frequency'].map((x) => h('th', null, x)))), tbody));
  };
  const render = () => {
    if (rawMode) {
      const ta = h('textarea', { class: 'code', spellcheck: 'false', value: JSON.stringify(st, null, 4) });
      holder.replaceChildren(h('div', { class: 'stack' }, ta, h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: async () => {
        try { const parsed = JSON.parse(ta.value); await api('PUT', '/concentrator', parsed); Object.assign(st, parsed); toast('Saved — restart the service to apply'); } catch (e) { errToast(e); }
      } }, 'Save JSON'))));
      return;
    }
    holder.replaceChildren(h('div', { class: 'form wide stack' },
      h('h3', null, 'Radio front-ends'),
      h('div', { class: 'grid g4' }, field('Radio 0 center (Hz)', num('rf0_freq')), field('Radio 1 center (Hz)', num('rf1_freq')), field('RSSI offset 0', num('rf0_rssi_offset')), field('RSSI offset 1', num('rf1_rssi_offset'))),
      h('div', { class: 'grid g4' }, field('Sync word', sel(st.sync_word, [['private', 'Private (0x12) — raw LoRa'], ['public', 'Public (0x34) — LoRaWAN']], { onchange: (e) => { st.sync_word = e.target.value; } }),
        'Use public for LoRaWAN devices, private for most raw LoRa nodes.'), field('Radio 0 type', txt('rf0_type')), field('Radio 1 type', txt('rf1_type')), h('div', { style: { alignSelf: 'center' } }, bool('rf0_tx_enable', 'Radio 0 TX enabled'))),
      h('h3', null, 'Multi-SF channels'), chTable(),
      h('h3', null, 'Transmit defaults'),
      h('div', { class: 'grid g4' }, field('Default power (dBm)', num('tx_default_power')), field('Default frequency (Hz)', num('tx_default_freq')), field('Default SF', num('tx_default_sf')), field('Default bandwidth (kHz)', num('tx_default_bandwidth'))),
      h('h3', null, 'Network'),
      h('div', { class: 'callout info small' }, 'raw_lora_app sends every uplink to target_ip:port_up and always mirrors it to 127.0.0.1. Keep port_up = 5555 and port_down = 5556 so LoRaHub receives packets and can send downlinks.'),
      h('div', { class: 'grid g4' }, field('Target IP', txt('target_ip', { class: 'mono' })), field('Uplink port', num('port_up')), field('Downlink port', num('port_down')), field('Heartbeat (s)', num('heartbeat_interval_s'))),
      h('h3', null, 'Hardware'),
      h('div', { class: 'grid g4' }, field('SPI device', txt('spi_path', { class: 'mono' })), field('Reset GPIO', txt('reset_gpio', { class: 'mono' })), field('Clock source', num('clksrc')), field('Status port', num('status_port'))),
      h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: async () => {
        try { await api('PUT', '/concentrator', st); toast('Saved — restart the service to apply'); } catch (e) { errToast(e); }
      } }, 'Save configuration'))));
  };
  const seg = h('div', { class: 'seg' });
  const renderSeg = () => seg.replaceChildren(h('button', { class: rawMode ? '' : 'on', onclick: () => { rawMode = false; renderSeg(); render(); } }, 'Form'), h('button', { class: rawMode ? 'on' : '', onclick: () => { rawMode = true; renderSeg(); render(); } }, 'JSON'));
  renderSeg(); render();
  return h('div', { class: 'stack' }, head, statusCard, h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'raw_lora_app configuration'), seg), h('div', { class: 'card-body' }, holder)));
});

// ======================= Settings =======================
async function settingsPage(tab) {
  const s = await GET('/settings');
  const base = '#/settings';
  const head = [pageHead([['Settings']], 'Settings'), tabs(base, [['', 'Network server'], ['integrations', 'MQTT & Home Assistant'], ['keys', 'API keys'], ['account', 'Account'], ['system', 'System']], tab)];
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
    return h('div', { class: 'stack' }, head, card(
      h('h3', null, 'Packet processing'),
      h('div', { class: 'grid g2' }, field('De-duplication window (ms)', dedup, 'Receptions of the same packet by several gateways within this window are merged into one uplink with metadata of every gateway.'), field('Default frequency plan', band)),
      auto.el, crc.el, raw.el,
      h('h3', null, 'LoRaWAN'),
      h('div', { class: 'grid g4' }, field('NetID', netid), field('DevAddr prefix', prefix), field('Prefix bits', bits), field('RX1 delay (s)', rx1)),
      h('h3', null, 'Storage'),
      h('div', { class: 'grid g3' }, field('Event retention (hours)', ret), field('Max stored events', rows), field('Gateway offline after (s)', off)),
      h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: () => save({
        dedup_window_ms: Number(dedup.value), default_frequency_plan: band.value, net_id: netid.value, dev_addr_prefix: prefix.value, dev_addr_prefix_bits: Number(bits.value), rx1_delay_s: Number(rx1.value),
        auto_register_gateways: auto.input.checked, drop_crc_errors: crc.input.checked, store_gateway_raw_events: raw.input.checked,
        event_retention_hours: Number(ret.value), event_max_rows: Number(rows.value), gateway_offline_after_s: Number(off.value) }) }, 'Save settings'))));
  }
  if (tab === 'integrations') {
    const prefix = inp(s.mqtt_topic_prefix, { class: 'mono' });
    const ext = s.external_mqtt;
    const en = checkbox(ext.enabled, 'Publish to an external MQTT broker');
    const url = inp(ext.url, { class: 'mono', placeholder: 'tcp://192.168.1.10:1883' }), user = inp(ext.username), pass = h('input', { type: 'password', value: ext.password || '' }), cid = inp(ext.client_id, { placeholder: 'lorahub' });
    const ha = checkbox(s.home_assistant.enabled, 'Home Assistant MQTT discovery', 'Publishes retained discovery configs so every decoded field appears as a sensor in Home Assistant.');
    const haPrefix = inp(s.home_assistant.discovery_prefix, { class: 'mono' });
    return h('div', { class: 'stack' }, head, card(
      field('MQTT topic prefix', prefix, 'Uplinks are published to <prefix>/<application>/devices/<device>/up.'),
      h('div', { class: 'form-section stack' }, en.el, h('div', { class: 'grid g2' }, field('Broker URL', url), field('Client ID', cid)), h('div', { class: 'grid g2' }, field('Username', user), field('Password', pass))),
      h('div', { class: 'form-section stack' }, ha.el, field('Discovery prefix', haPrefix)),
      h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: () => save({ mqtt_topic_prefix: prefix.value,
        external_mqtt: { enabled: en.input.checked, url: url.value, username: user.value, password: pass.value, client_id: cid.value },
        home_assistant: { enabled: ha.input.checked, discovery_prefix: haPrefix.value } }) }, 'Save settings'))));
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
