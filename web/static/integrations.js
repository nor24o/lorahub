/* LoRaHub console: MQTT and Home Assistant. */
'use strict';

const HA_DIAGNOSTICS = [['rssi', 'Signal RSSI'], ['snr', 'Signal SNR'], ['freq', 'Frequency'], ['datr', 'Data rate'], ['gateway_count', 'Gateways that heard it'], ['last_seen', 'Last seen'], ['seq', 'Sequence number']];
const HA_GATEWAY_FIELDS = [['rx', 'Packets received'], ['tx', 'Packets sent'], ['crc_err', 'CRC errors'], ['tx_err', 'TX errors'], ['uptime_s', 'Uptime'], ['temp_c', 'Concentrator temperature']];
const HA_CLASSES = ['', 'temperature', 'humidity', 'pressure', 'voltage', 'current', 'power', 'energy', 'battery', 'illuminance', 'signal_strength', 'moisture', 'distance', 'duration'];

function checkList(options, selected) {
  const picked = new Set(selected || []);
  const el = h('div', { class: 'grid g3' }, options.map(([key, label]) => {
    const c = checkbox(picked.has(key), label);
    c.input.onchange = () => { if (c.input.checked) picked.add(key); else picked.delete(key); };
    return c.el;
  }));
  return { el, values: () => [...picked] };
}

function toggles(obj, items) {
  return h('div', { class: 'stack' }, items.map(([key, label, help]) => {
    const c = checkbox(obj[key], label, help);
    c.input.onchange = () => { obj[key] = c.input.checked; };
    return c.el;
  }));
}

function integrationsHead(active) {
  return [pageHead([['MQTT & Home Assistant']], 'MQTT & Home Assistant', ['What LoRaHub publishes, what it accepts, and what Home Assistant sees']),
    tabs('#/integrations', [['', 'MQTT'], ['commands', 'Commands'], ['homeassistant', 'Home Assistant'], ['exposed', 'Exposed entities'], ['topics', 'Topics']], active)];
}

async function saveSettings(settings, patch) {
  Object.assign(settings, patch);
  await api('PUT', '/settings', settings);
  toast('Saved');
}

const saveRow = (fn, label) => h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: async () => { try { await fn(); } catch (e) { errToast(e); } } }, label || 'Save'));

App.route('/integrations', async () => {
  const [s, sys] = await Promise.all([GET('/settings'), GET('/system')]);
  const out = s.mqtt_publish;
  const prefix = inp(s.mqtt_topic_prefix, { class: 'mono' });
  const qos = sel(out.qos, [[0, '0: at most once'], [1, '1: at least once']]);
  const ext = s.external_mqtt;
  const en = checkbox(ext.enabled, 'Also publish to an external broker');
  const url = inp(ext.url, { class: 'mono', placeholder: 'tcp://192.168.1.10:1883' }), user = inp(ext.username), pass = h('input', { type: 'password', value: ext.password || '', autocomplete: 'new-password' }), cid = inp(ext.client_id, { placeholder: 'lorahub' });
  const conn = sys.listeners.mqtt ? `${location.hostname}${sys.listeners.mqtt.replace(/^[^:]*/, '')}` : null;
  return h('div', { class: 'stack' }, ...integrationsHead(''),
    h('div', { class: 'grid g2' },
      h('div', { class: 'card stat' }, h('div', { class: 'label' }, 'Built-in broker'), h('div', { class: 'value', style: { fontSize: '18px' } }, conn || 'off'), h('div', { class: 'hint' }, sys.mqtt_clients + ' client(s) connected')),
      h('div', { class: 'card stat' }, h('div', { class: 'label' }, 'External broker'), h('div', { class: 'value', style: { fontSize: '18px' } }, ext.enabled ? (sys.external_mqtt_connected ? 'connected' : 'not connected') : 'off'), h('div', { class: 'hint' }, ext.enabled ? ext.url : 'not configured'))),
    h('div', { class: 'card card-body' }, h('div', { class: 'form wide stack' },
      h('h3', null, 'Publishing'),
      h('div', { class: 'grid g2' }, field('Topic prefix', prefix, 'After changing this, restart LoRaHub so commands are heard on the new topics.'), field('QoS', qos)),
      h('h3', null, 'Node data'),
      toggles(out, [['up', 'Full message on …/up', 'Decoded values plus the parts selected below.'], ['state', 'Flat values on …/state', 'One level of key and value, the easiest to use in other systems. Home Assistant needs this, so it stays on while discovery is enabled.'], ['state_retain', 'Retain …/state', 'New subscribers receive the latest values at once.']]),
      h('h3', null, 'Contents of the full message'),
      toggles(out, [['include_gateways', 'Every gateway that heard the packet'], ['include_radio', 'Radio details (frequency, data rate, airtime)'], ['include_raw', 'Raw payload as hex']]),
      h('h3', null, 'Other topics'),
      toggles(out, [['gateway_up', 'Raw packets per gateway', 'One message per gateway and packet. The busiest topic.'], ['gateway_status', 'Gateway status'], ['unmatched', 'Packets from unknown NODE_IDs'], ['down_events', 'Downlink queued, sent and failed']]),
      h('h3', null, 'External broker'), en.el,
      h('div', { class: 'grid g2' }, field('Broker URL', url), field('Client ID', cid)),
      h('div', { class: 'grid g2' }, field('Username', user), field('Password', pass)),
      saveRow(() => saveSettings(s, { mqtt_topic_prefix: prefix.value, mqtt_publish: Object.assign(out, { qos: Number(qos.value) }), external_mqtt: { enabled: en.input.checked, url: url.value, username: user.value, password: pass.value, client_id: cid.value } })))));
});

App.route('/integrations/commands', async () => {
  const s = await GET('/settings');
  const c = s.mqtt_commands;
  const on = inp(c.on_hex, { class: 'mono' }), off = inp(c.off_hex, { class: 'mono' });
  return h('div', { class: 'stack' }, ...integrationsHead('commands'),
    h('div', { class: 'card card-body' }, h('div', { class: 'form wide stack' },
      toggles(c, [['accept_down', 'Accept downlinks on …/down', 'JSON such as {"data_hex":"0102"}.'], ['accept_set', 'Accept simple commands on …/set', 'ON, OFF or a hex payload. Home Assistant switches use this, so turning it off removes them.']]),
      h('h3', null, 'What ON and OFF send'),
      h('div', { class: 'grid g2' }, field('ON payload (hex)', on), field('OFF payload (hex)', off)),
      saveRow(() => saveSettings(s, { mqtt_commands: Object.assign(c, { on_hex: cleanHex(on.value), off_hex: cleanHex(off.value) }) })))));
});

App.route('/integrations/homeassistant', async () => {
  const s = await GET('/settings');
  const ha = s.home_assistant;
  const en = checkbox(ha.enabled, 'Announce entities to Home Assistant', 'Uses MQTT discovery. Point Home Assistant at the broker shown on the MQTT tab.');
  const prefix = inp(ha.discovery_prefix, { class: 'mono' });
  const mode = sel(ha.expose_mode, [['adopted', 'Only nodes I adopted and marked for Home Assistant'], ['all', 'Every node with decoded data']]);
  const exclude = h('textarea', { class: 'mono', rows: 3, placeholder: 'ch*_raw\ndebug_*', value: (ha.exclude_fields || []).join('\n') });
  const expire = inp(ha.expire_after_s || '', { type: 'number', min: 0, placeholder: 'never' });
  const diag = checkList(HA_DIAGNOSTICS, ha.diagnostics);
  const gwFields = checkList(HA_GATEWAY_FIELDS, ha.gateway_fields);
  const action = (label, url, done, danger) => h('button', { class: 'btn' + (danger ? ' danger' : ''), onclick: async () => {
    if (danger && !(await confirmModal(label, 'Every LoRaHub entity disappears from Home Assistant. Nodes and gateways that are still exposed come back with their next packet or status.', 'Remove'))) return;
    try { await api('POST', url); toast(done); } catch (e) { errToast(e); }
  } }, label);
  return h('div', { class: 'stack' }, ...integrationsHead('homeassistant'),
    h('div', { class: 'card card-body' }, h('div', { class: 'form wide stack' }, en.el,
      h('div', { class: 'grid g2' }, field('Discovery prefix', prefix, 'After changing this, restart LoRaHub.'), field('Mark entities unavailable after (s)', expire, 'When a node stays silent this long. Empty keeps the last value forever.')),
      h('h3', null, 'Nodes'),
      field('Which nodes', mode),
      field('Hide decoded fields', exclude, 'One name or pattern per line, * matches anything. Fine-tune single entities on the Exposed entities tab.'),
      toggles(ha, [['switches', 'Offer commands to nodes', 'A send-downlink box on every node, switches for relays and valves, and your own buttons. Needs "Accept simple commands" on the Commands tab.'], ['last_payload', 'Show the last raw payload as a sensor']]),
      h('h3', null, 'Diagnostics shown for each node'), diag.el,
      h('h3', null, 'Gateways'),
      toggles(ha, [['gateways', 'Expose gateways as devices']]), gwFields.el,
      h('h3', null, 'Restarts'),
      toggles(ha, [['republish_on_birth', 'Announce everything again when Home Assistant starts']]),
      saveRow(() => saveSettings(s, { home_assistant: Object.assign(ha, { enabled: en.input.checked, discovery_prefix: prefix.value.trim(), expose_mode: mode.value, expire_after_s: Number(expire.value || 0),
        exclude_fields: exclude.value.split('\n').map((x) => x.trim()).filter(Boolean), diagnostics: diag.values(), gateway_fields: gwFields.values() }) })))),
    h('div', { class: 'card card-body' }, h('h2', null, 'Maintenance'), h('div', { class: 'row' }, action('Clean up and announce again', '/ha/republish', 'Old entities removed, discovery sent'), action('Remove from Home Assistant', '/ha/remove', 'Entities removed', true))));
});

const CMD_FORMATS = [['u8', '1 byte'], ['i8', '1 byte, signed'], ['u16be', '2 bytes, big endian'], ['u16le', '2 bytes, little endian'], ['i16be', '2 bytes signed, big endian'], ['i16le', '2 bytes signed, little endian'],
  ['u32be', '4 bytes, big endian'], ['u32le', '4 bytes, little endian'], ['i32be', '4 bytes signed, big endian'], ['i32le', '4 bytes signed, little endian']];
const cmdPayload = (c) => (c.payload_hex || '').replace(/[\s:-]/g, '').toUpperCase().replace('{VALUE}', '{value}');
const cmdField = (label, control) => h('label', { class: 'cmd-field' }, h('span', { class: 'small muted' }, label), control);
function cmdOut(c) {
  const out = { name: c.name, kind: c.kind || 'button', payload_hex: cmdPayload(c) };
  if (out.kind === 'number') Object.assign(out, { min: Number(c.min || 0), max: Number(c.max || 0), step: Number(c.step || 1), unit: c.unit || '', encoding: c.encoding || 'u8', scale: Number(c.scale || 1) });
  if (out.kind === 'text') out.encoding = c.encoding || 'hex';
  return out;
}

function openNodeExposure(n, reload) {
  const overrides = JSON.parse(JSON.stringify(n.ha_fields || {}));
  const ov = (k) => (overrides[k] = overrides[k] || {});
  const state = (e) => (e.exposed ? h('span', { class: 'badge ok' }, 'exposed') : h('span', { class: 'muted' }, (n.exposed ? e.reason : 'node is not exposed') || 'hidden'));
  const row = (e) => h('tr', null,
    h('td', null, h('input', { type: 'checkbox', checked: !(overrides[e.key] && overrides[e.key].disabled), onchange: (ev) => { ov(e.key).disabled = !ev.target.checked; } })),
    h('td', null, h('span', { class: 'mono small' }, e.key)), h('td', null, h('span', { class: 'badge' }, e.component)),
    h('td', null, inp((overrides[e.key] || {}).name || '', { placeholder: e.name, oninput: (ev) => { ov(e.key).name = ev.target.value; } })),
    h('td', null, e.component === 'sensor' ? sel((overrides[e.key] || {}).device_class || '', HA_CLASSES.map((c) => [c, c || (e.device_class ? `auto (${e.device_class})` : 'none')]), { onchange: (ev) => { ov(e.key).device_class = ev.target.value; } }) : null),
    h('td', null, e.component === 'sensor' ? inp((overrides[e.key] || {}).unit || '', { placeholder: e.unit || '', style: { width: '80px' }, oninput: (ev) => { ov(e.key).unit = ev.target.value; } }) : null),
    h('td', { class: 'small' }, state(e)));
  modal(n.name || n.node, (close) => {
    const toggle = n.adopted ? h('button', { class: 'btn', onclick: async () => {
      try { await api('PUT', `/applications/${n.group}/devices/${n.node}`, { ha_enabled: !n.ha_enabled }); toast('Saved'); close(); reload(); } catch (e) { errToast(e); }
    } }, n.ha_enabled ? 'Stop exposing this node' : 'Expose this node') : h('a', { class: 'btn', href: '#/nodes', onclick: close }, 'Adopt this node first');
    const save = h('button', { class: 'btn primary', onclick: async () => {
      Object.keys(overrides).forEach((k) => { const o = overrides[k]; if (!o.disabled && !o.name && !o.device_class && !o.unit) delete overrides[k]; });
      try { await api('PUT', `/applications/${n.group}/devices/${n.node}`, { ha_fields: overrides, ha_commands: commands.filter((c) => c.name.trim() || cmdPayload(c)).map(cmdOut), ha_switch: outputBox.input.checked, class: classSel.value }); toast('Saved'); close(); reload(); } catch (e) { errToast(e); }
    } }, 'Save');
    const commands = JSON.parse(JSON.stringify(n.ha_commands || []));
    const outputBox = checkbox(n.ha_switch, 'This node has an on/off output', 'Adds a switch to Home Assistant.');
    const cmdBox = h('div', { class: 'stack' });
    const classSel = sel(n.class || 'A', [['A', 'A: listens only after sending'], ['B', 'B: handled like class A'], ['C', 'C: always listening']], { onchange: () => paintCommands() });
    if (n.class_from_firmware) { classSel.disabled = true; classSel.title = 'Set by the node\'s firmware'; }
    const header = (n.downlink_header ? `${n.node_id} ${n.serial} ` : '') + (n.downlink_control ? '[control] ' : '');
    const setKind = (c, kind) => {
      c.kind = kind;
      if (kind === 'number') Object.assign(c, { min: c.min ?? 0, max: c.max || 100, step: c.step || 1, scale: c.scale || 1, encoding: CMD_FORMATS.some((f) => f[0] === c.encoding) ? c.encoding : 'u8' });
      if (kind === 'text') c.encoding = ['hex', 'ascii'].includes(c.encoding) ? c.encoding : 'hex';
      if (kind !== 'button' && !cmdPayload(c).includes('{value}')) c.payload_hex = cmdPayload(c) + '{value}';
      if (kind === 'button') c.payload_hex = cmdPayload(c).replace('{value}', '');
      paintCommands();
    };
    const cmdRow = (c, i) => {
      const kind = c.kind || 'button';
      const air = h('div', { class: 'small muted mono' });
      const showAir = () => { air.textContent = `On air: ${header}${cmdPayload(c) || '…'}`; };
      showAir();
      const num = (key, label, attrs) => cmdField(label, inp(c[key] ?? '', Object.assign({ type: 'number', step: 'any', oninput: (e) => { c[key] = e.target.value; } }, attrs)));
      const extra = kind === 'number' ? h('div', { class: 'cmd-extra' }, num('min', 'Minimum'), num('max', 'Maximum'), num('step', 'Step'),
        cmdField('Unit', inp(c.unit || '', { placeholder: '%', oninput: (e) => { c.unit = e.target.value; } })),
        cmdField('Value is sent as', sel(c.encoding || 'u8', CMD_FORMATS, { onchange: (e) => { c.encoding = e.target.value; } })),
        num('scale', 'Multiply value by'))
        : kind === 'text' ? h('div', { class: 'cmd-extra' }, cmdField('Value is typed as', sel(c.encoding || 'hex', [['hex', 'Hex'], ['ascii', 'Plain text']], { onchange: (e) => { c.encoding = e.target.value; } }))) : null;
      return h('div', { class: 'cmd-item' },
        h('div', { class: 'cmd-row' },
          inp(c.name, { placeholder: kind === 'number' ? 'Dimmer' : 'Open valve', oninput: (e) => { c.name = e.target.value; } }),
          sel(kind, classSel.value === 'C' || kind !== 'button' ? [['button', 'Button'], ['number', 'Number'], ['text', 'Text']] : [['button', 'Button']], { onchange: (e) => setKind(c, e.target.value) }),
          inp(c.payload_hex, { class: 'mono', placeholder: kind === 'button' ? '0101' : '02{value}', spellcheck: 'false', oninput: (e) => { c.payload_hex = e.target.value; showAir(); } }),
          kind === 'button' ? h('button', { class: 'btn sm', type: 'button', title: 'Send this downlink now', onclick: async () => {
            try { await api('POST', `/applications/${n.group}/devices/${n.node}/down`, { f_port: 1, frm_payload_hex: cmdPayload(c) }); toast('Downlink sent to the queue'); } catch (e) { errToast(e); }
          } }, 'Send') : h('span'),
          h('button', { class: 'icon-btn', type: 'button', title: 'Remove', onclick: () => { commands.splice(i, 1); paintCommands(); } }, icon('trash'))),
        extra, air,
        kind !== 'button' && classSel.value !== 'C' ? h('div', { class: 'small', style: { color: 'var(--warning)' } }, 'Hidden from Home Assistant until this node is class C.') : null);
    };
    const paintCommands = () => cmdBox.replaceChildren(...commands.map(cmdRow));
    paintCommands();
    return h('div', { class: 'stack' },
      h('div', { class: 'row between' }, h('span', { class: 'mono small muted' }, `${n.group} / ${n.node}`), h('span', { class: 'badge ' + (n.exposed ? 'ok' : '') }, n.exposed ? 'exposed' : 'not exposed')),
      n.entities.length ? h('div', { class: 'table-wrap' }, h('table', { class: 't compact' }, h('thead', null, h('tr', null, ['On', 'Field', 'Type', 'Name', 'Class', 'Unit', 'State'].map((x) => h('th', null, x)))), h('tbody', null, n.entities.map(row)))) : h('div', { class: 'empty' }, 'No decoded data yet, so there is nothing to expose.'),
      n.entities.length ? h('div', { class: 'stack' }, h('h3', { style: { margin: 0 } }, 'Commands to this node'), h('p', { class: 'small muted', style: { margin: 0 } }, 'Define them here and LoRaHub shows them in Home Assistant, for this node only. A Button sends a fixed payload. On class C nodes, Number adds a slider and Text adds a value box; put {value} in the payload where the value goes.'),
        cmdField('Node class', classSel), cmdBox,
        h('div', { class: 'row between' }, h('button', { class: 'btn sm', type: 'button', onclick: () => { commands.push({ name: '', kind: 'button', payload_hex: '' }); paintCommands(); } }, icon('plus'), 'Add command'), outputBox.el)) : null,
      h('div', { class: 'form-actions', style: { justifyContent: 'space-between' } }, toggle, n.entities.length ? h('div', { class: 'row' }, h('button', { class: 'btn', onclick: close }, 'Cancel'), save) : h('button', { class: 'btn', onclick: close }, 'Close')));
  }, { wide: true });
}

App.route('/integrations/exposed', async () => {
  const body = h('div');
  const search = inp('', { placeholder: 'Filter nodes' });
  const show = sel('all', [['all', 'All nodes'], ['exposed', 'Exposed'], ['hidden', 'Not exposed']]);
  const warn = h('div');
  let report;
  const paint = () => {
    const q = search.value.toLowerCase();
    const nodes = report.nodes.filter((n) => (!q || `${n.name} ${n.node} ${n.group}`.toLowerCase().includes(q)) && (show.value === 'all' || n.exposed === (show.value === 'exposed')))
      .sort((a, b) => Number(b.exposed) - Number(a.exposed) || (a.name || a.node).localeCompare(b.name || b.node));
    warn.replaceChildren(...(report.enabled ? [] : [h('div', { class: 'callout warn' }, 'Home Assistant discovery is off, so nothing is announced yet. ', h('a', { href: '#/integrations/homeassistant' }, 'Turn it on'))]));
    body.replaceChildren(nodes.length ? h('div', { class: 'card' }, h('div', { class: 'table-wrap' }, h('table', { class: 't' }, h('thead', null, h('tr', null, ['Node', 'Group / ID', 'Status', 'Entities'].map((x) => h('th', null, x)))),
      h('tbody', null, nodes.map((n) => h('tr', { class: 'click', onclick: () => openNodeExposure(n, load) },
        h('td', null, h('b', null, n.name || n.node)), h('td', { class: 'mono small' }, `${n.group} / ${n.node}`),
        h('td', null, h('span', { class: 'badge ' + (n.exposed ? 'ok' : '') }, n.exposed ? 'exposed' : 'not exposed')),
        h('td', { class: 'small nowrap' }, n.has_data ? `${n.entities.filter((e) => e.exposed).length} of ${n.entities.length}` : 'no data yet'))))))) : h('div', { class: 'empty card' }, 'No nodes found.'));
  };
  const load = async () => { report = await GET('/ha/entities'); paint(); };
  await load();
  search.addEventListener('input', paint);
  show.addEventListener('change', paint);
  return h('div', { class: 'stack' }, ...integrationsHead('exposed'), h('div', { class: 'row' }, h('div', { style: { flex: 1, maxWidth: '320px' } }, search), h('div', { style: { minWidth: '160px' } }, show)), warn, body);
});

App.route('/integrations/topics', async () => {
  const s = await GET('/settings');
  const p = s.mqtt_topic_prefix, out = s.mqtt_publish, c = s.mqtt_commands, ha = s.home_assistant;
  const haOn = ha.enabled;
  const rows = [
    [`${p}/{group}/{node}/up`, 'Out', 'Full message for each packet', out.up],
    [`${p}/{group}/{node}/state`, 'Out', 'Flat latest values' + (out.state_retain ? ', retained' : ''), out.state || haOn],
    [`${p}/{group}/{node}/down/queued`, 'Out', 'Downlink queued', out.down_events],
    [`${p}/{group}/{node}/down/sent`, 'Out', 'Downlink sent', out.down_events],
    [`${p}/{group}/{node}/down/failed`, 'Out', 'Downlink failed', out.down_events],
    [`${p}/unmatched`, 'Out', 'Packet from an unknown NODE_ID', out.unmatched],
    [`${p}/gateways/{gateway}/up`, 'Out', 'Raw packet per gateway', out.gateway_up],
    [`${p}/gateways/{gateway}/status`, 'Out', 'Gateway status, retained', out.gateway_status || (haOn && ha.gateways)],
    [`${p}/{group}/{node}/down`, 'In', 'Send a downlink: {"data_hex":"0102"}', c.accept_down],
    [`${p}/{group}/{node}/set`, 'In', `ON (${c.on_hex}), OFF (${c.off_hex}) or hex`, c.accept_set],
    [`${ha.discovery_prefix}/{component}/{id}/{entity}/config`, 'Out', 'Home Assistant discovery, retained', haOn],
    [`${ha.discovery_prefix}/status`, 'In', 'Home Assistant restart notice', haOn && ha.republish_on_birth],
  ];
  return h('div', { class: 'stack' }, ...integrationsHead('topics'),
    h('div', { class: 'card' }, h('div', { class: 'table-wrap' }, h('table', { class: 't' }, h('thead', null, h('tr', null, ['Topic', 'Direction', 'Content', 'State'].map((x) => h('th', null, x)))),
      h('tbody', null, rows.map(([t, d, x, on]) => h('tr', null, h('td', null, val(t)), h('td', null, h('span', { class: 'badge' }, d)), h('td', { class: 'small' }, x), h('td', null, h('span', { class: 'badge ' + (on ? 'ok' : '') }, on ? 'on' : 'off')))))))));
});
