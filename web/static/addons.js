/* LoRaHub console: concentrator, radio, TX, remote gateways, logs and hardware. */
'use strict';

function statTile(label, value) { return h('div', { class: 'card stat' }, h('div', { class: 'label' }, label), h('div', { class: 'value', style: { fontSize: '18px' } }, value)); }
function statusTiles(st) {
  return h('div', { class: 'grid g4' }, statTile('Uptime', fmtDur(st.uptime_s)), statTile('Received', st.rx), statTile('Transmitted', st.tx), statTile('CRC errors', st.crc_err),
    statTile('TX errors', st.tx_err), statTile('Queue / dropped', `${st.q_depth} / ${st.q_drop}`), statTile('Chip temperature', st.temp_ok ? st.temp_c + ' °C' : '—'), statTile('Sync word', st.sync),
    statTile('RX state', st.rx_status), statTile('TX state', st.tx_status), statTile('Radio 0', st.rf0_mhz + ' MHz'), statTile('Radio 1', st.rf1_mhz + ' MHz'));
}
function channelBars(st) {
  const counts = st.ch_detect || [], peak = Math.max(1, ...counts);
  return h('div', { class: 'card card-body' }, h('h2', null, 'Activity per channel'), h('div', { class: 'ch-bars' }, counts.map((n, i) => h('div', { class: 'ch-bar' },
    h('div', { class: 'ch-track' }, h('div', { class: 'ch-fill', style: { height: Math.round((n / peak) * 100) + '%' } })), h('b', null, n), h('span', { class: 'small muted' }, 'CH' + i)))));
}
function serviceButtons(send, state) {
  const b = (cmd, label, cls) => h('button', { class: 'btn ' + (cls || ''), onclick: async () => {
    if (cmd !== 'start' && !(await confirmModal(label + ' concentrator', 'Packet reception pauses until the service is running again.', label, cmd === 'stop'))) return;
    try { await send(cmd); toast('Service: ' + cmd); } catch (e) { errToast(e); }
  } }, label);
  return [state ? h('span', { class: 'badge ' + (state === 'active' ? 'ok' : 'warn') }, state) : null, b('start', 'Start'), b('restart', 'Restart'), b('stop', 'Stop', 'danger')];
}
function logBox(load) {
  const pre = h('pre', { class: 'log-box' }, 'Loading…');
  const search = inp('', { placeholder: 'Filter lines' });
  let text = '';
  const paint = () => {
    const q = search.value.toLowerCase();
    const atEnd = pre.scrollHeight - pre.scrollTop - pre.clientHeight < 40;
    pre.textContent = q ? text.split('\n').filter((l) => l.toLowerCase().includes(q)).join('\n') : text;
    if (atEnd) pre.scrollTop = pre.scrollHeight;
  };
  const refresh = async () => { try { text = await load(); } catch (e) { text = e.message; } paint(); };
  search.addEventListener('input', paint);
  refresh().then(() => { pre.scrollTop = pre.scrollHeight; });
  return { el: h('div', { class: 'stack' }, search, pre), refresh };
}

// ======================= Realtime status =======================
App.route('/status', async () => {
  const holder = h('div', { class: 'stack' });
  const live = liveView({ title: 'Live packets' });
  let head = null;
  const render = async () => {
    const c = await GET('/concentrator');
    head = pageHead([['Realtime status']], 'Realtime status', [h('span', { class: 'mono small' }, (c.status && c.status.eui) || 'gateway')],
      serviceButtons(async (cmd) => { await api('POST', '/concentrator/service', { cmd }); setTimeout(() => render().catch(() => {}), 2000); }, c.service));
    holder.replaceChildren(head, ...(c.status ? [statusTiles(c.status), channelBars(c.status)] : [h('div', { class: 'callout warn' }, c.status_error || 'raw_lora_app is not answering.')]));
  };
  await render();
  const root = h('div', { class: 'stack' }, holder, live);
  pollWhileVisible(root, 5000, () => { if (!document.querySelector('.modal-bg')) render().catch(() => {}); });
  return root;
});

// ======================= Radio configuration =======================
function radioEditor(cfg, save) {
  const st = JSON.parse(JSON.stringify(cfg));
  const holder = h('div');
  let raw = false;
  const num = (k) => inp(st[k], { type: 'number', step: 'any', oninput: (e) => { st[k] = e.target.value === '' ? undefined : Number(e.target.value); } });
  const txt = (k, cls) => inp(st[k], { class: cls, oninput: (e) => { st[k] = e.target.value; } });
  const pick = (k, opts) => sel(st[k], opts, { onchange: (e) => { const n = Number(e.target.value); st[k] = String(n) === e.target.value ? n : e.target.value; } });
  const bool = (k, label) => { const c = checkbox(st[k], label); c.input.onchange = () => { st[k] = c.input.checked; }; return c.el; };
  const chips = ['SX1250', 'SX1255', 'SX1257'], bws = [125, 250, 500], crs = ['4/5', '4/6', '4/7', '4/8'], sfs = [5, 6, 7, 8, 9, 10, 11, 12];
  const channels = () => {
    const body = h('tbody');
    const mhz = (i) => (((st.chan_radio[i] ? st.rf1_freq : st.rf0_freq) + st.chan_if[i]) / 1e6).toFixed(3) + ' MHz';
    const draw = () => body.replaceChildren(...(st.chan_if || []).map((iff, i) => {
      const f = h('span', null, mhz(i));
      return h('tr', null, h('td', null, 'CH' + i), h('td', null, sel(st.chan_radio[i], [[0, 'Radio 0'], [1, 'Radio 1']], { onchange: (e) => { st.chan_radio[i] = Number(e.target.value); draw(); } })),
        h('td', null, inp(iff, { type: 'number', step: 1000, oninput: (e) => { st.chan_if[i] = Number(e.target.value); f.textContent = mhz(i); } })), h('td', { class: 'mono' }, f));
    }));
    draw();
    return h('div', { class: 'table-wrap card' }, h('table', { class: 't compact' }, h('thead', null, h('tr', null, ['Channel', 'Radio', 'IF offset (Hz)', 'Frequency'].map((x) => h('th', null, x)))), body));
  };
  const sfMask = () => {
    if (st.sf_mask === undefined) st.sf_mask = 255;
    return h('div', { class: 'chips' }, sfs.map((sf, bit) => { const c = checkbox((st.sf_mask >> bit) & 1, 'SF' + sf); c.input.onchange = () => { st.sf_mask = c.input.checked ? st.sf_mask | (1 << bit) : st.sf_mask & ~(1 << bit); }; return c.el; }));
  };
  const gainTable = () => {
    st.tx_gain_lut = st.tx_gain_lut || [];
    const cols = ['rf_power', 'pa_gain', 'pwr_idx', 'dac_gain', 'mix_gain', 'dig_gain'];
    const body = h('tbody');
    const draw = () => body.replaceChildren(...st.tx_gain_lut.map((row, i) => h('tr', null, cols.map((c) => h('td', null, inp(row[c], { type: 'number', oninput: (e) => { row[c] = Number(e.target.value); } }))),
      h('td', null, h('button', { class: 'icon-btn', title: 'Remove', onclick: () => { st.tx_gain_lut.splice(i, 1); draw(); } }, icon('trash'))))));
    draw();
    return h('div', null, h('div', { class: 'table-wrap card' }, h('table', { class: 't compact' }, h('thead', null, h('tr', null, [...['Power (dBm)', 'PA gain', 'Power index', 'DAC gain', 'Mixer gain', 'Digital gain'], ''].map((x) => h('th', null, x)))), body)),
      h('button', { class: 'btn sm', style: { marginTop: '8px' }, disabled: st.tx_gain_lut.length >= 16, onclick: () => { st.tx_gain_lut.push({ rf_power: 14, pa_gain: 0, pwr_idx: 0, dac_gain: 0, mix_gain: 0, dig_gain: 0 }); draw(); } }, icon('plus'), 'Add row'));
  };
  const saveBtn = (getter) => h('div', { class: 'form-actions' },
    h('button', { class: 'btn', onclick: async () => { try { await save(getter(), false); toast('Saved. Restart to apply.'); } catch (e) { errToast(e); } } }, 'Save'),
    h('button', { class: 'btn primary', onclick: async () => { try { await save(getter(), true); toast('Saved and restarting'); } catch (e) { errToast(e); } } }, 'Save and restart'));
  const draw = () => {
    if (raw) {
      const ta = h('textarea', { class: 'code', spellcheck: 'false', value: JSON.stringify(st, null, 4) });
      holder.replaceChildren(h('div', { class: 'stack' }, ta, saveBtn(() => JSON.parse(ta.value))));
      return;
    }
    holder.replaceChildren(h('div', { class: 'form wide stack' },
      h('h3', null, 'Radio front-ends'),
      h('div', { class: 'grid g4' }, field('Radio 0 centre (Hz)', num('rf0_freq')), field('Radio 0 chip', pick('rf0_type', chips)), field('Radio 0 RSSI offset', num('rf0_rssi_offset')), h('div', null, bool('rf0_tx_enable', 'Radio 0 can transmit'), bool('rf0_single_input', 'Single input mode'))),
      h('div', { class: 'grid g4' }, field('Radio 1 centre (Hz)', num('rf1_freq')), field('Radio 1 chip', pick('rf1_type', chips)), field('Radio 1 RSSI offset', num('rf1_rssi_offset')), h('div', null, bool('rf1_tx_enable', 'Radio 1 can transmit'), bool('rf1_single_input', 'Single input mode'))),
      h('h3', null, 'Reception'),
      h('div', { class: 'grid g2' }, field('Sync word', pick('sync_word', [['private', 'Private (0x12)'], ['public', 'Public (0x34)']])), field('Spreading factors', sfMask())),
      h('h3', null, 'Multi-SF channels'), channels(),
      h('h3', null, 'Standard LoRa channel'), bool('chan_lora_std_enable', 'Enabled'),
      h('div', { class: 'grid g4' }, field('Radio', pick('chan_lora_std_radio', [[0, 'Radio 0'], [1, 'Radio 1']])), field('IF offset (Hz)', num('chan_lora_std_if')), field('Bandwidth (kHz)', pick('chan_lora_std_bw', bws)), field('Spreading factor', pick('chan_lora_std_sf', sfs))),
      h('h3', null, 'FSK channel'), bool('chan_fsk_enable', 'Enabled'),
      h('div', { class: 'grid g4' }, field('Radio', pick('chan_fsk_radio', [[0, 'Radio 0'], [1, 'Radio 1']])), field('IF offset (Hz)', num('chan_fsk_if')), field('Bandwidth (kHz)', pick('chan_fsk_bw', bws)), field('Data rate (bit/s)', num('chan_fsk_datarate'))),
      h('h3', null, 'Transmit defaults'),
      h('div', { class: 'grid g4' }, field('Frequency (Hz)', num('tx_default_freq')), field('Power (dBm)', num('tx_default_power')), field('Spreading factor', pick('tx_default_sf', sfs)), field('Bandwidth (kHz)', pick('tx_default_bandwidth', bws))),
      h('div', { class: 'grid g4' }, field('Coding rate', pick('tx_default_coderate', crs)), field('Preamble', num('tx_default_preamble')), field('RF chain', pick('tx_default_rfch', [[0, '0'], [1, '1']])), h('div', { style: { alignSelf: 'center' } }, bool('tx_default_invert', 'Invert IQ'))),
      field('TX-free timeout (ms)', num('tx_free_timeout_ms')),
      h('h3', null, 'TX gain table'), gainTable(),
      h('h3', null, 'Network'),
      h('div', { class: 'grid g4' }, field('Server IP', txt('target_ip', 'mono')), field('Uplink port', num('port_up')), field('Downlink port', num('port_down')), field('Status port', num('status_port'))),
      field('Heartbeat (s)', num('heartbeat_interval_s')),
      h('h3', null, 'Hardware'),
      h('div', { class: 'grid g4' }, field('SPI device', txt('spi_path', 'mono')), field('Reset GPIO', txt('reset_gpio', 'mono')), field('PA GPIO', txt('pa_gpio', 'mono')), field('Clock source', pick('clksrc', [[0, 'Radio 0'], [1, 'Radio 1']]))),
      h('div', { class: 'grid g4' }, field('RX LED', txt('led_rx', 'mono')), field('TX LED', txt('led_tx', 'mono')), h('div', null, bool('full_duplex', 'Full duplex'), bool('ftime_enable', 'Fine timestamps')), h('div', null, bool('temp_monitor_enable', 'Temperature monitor'), bool('mcu_health_check', 'MCU health check'))),
      saveBtn(() => st)));
  };
  const seg = h('div', { class: 'seg' });
  const drawSeg = () => seg.replaceChildren(h('button', { class: raw ? '' : 'on', onclick: () => { raw = false; drawSeg(); draw(); } }, 'Form'), h('button', { class: raw ? 'on' : '', onclick: () => { raw = true; drawSeg(); draw(); } }, 'JSON'));
  drawSeg(); draw();
  return h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'raw_lora_app configuration'), seg), h('div', { class: 'card-body' }, holder));
}

App.route('/concentrator', () => { location.replace('#/radio'); return h('div'); });
App.route('/radio', async () => {
  const c = await GET('/concentrator');
  const head = pageHead([['Radio config']], 'Radio config', [h('span', { class: 'mono small' }, c.config_path)]);
  if (!c.config) return h('div', { class: 'stack' }, head, h('div', { class: 'callout warn' }, 'Cannot read the configuration: ' + (c.config_error || 'unknown error')));
  return h('div', { class: 'stack' }, head, radioEditor(c.config, async (cfg, restart) => {
    await api('PUT', '/concentrator', cfg);
    if (restart) await api('POST', '/concentrator/service', { cmd: 'restart' });
  }));
});

// ======================= TX =======================
App.route('/tx', async () => {
  const modu = sel('LORA', [['LORA', 'LoRa packet'], ['CW', 'Continuous tone'], ['FSK', 'FSK packet']]);
  const freq = inp('868.100', { type: 'number', step: '0.001' }), power = inp(14, { type: 'number', min: 0, max: 27 }), rf = sel(0, [[0, 'Chain 0'], [1, 'Chain 1']]);
  const sf = sel('SF7BW125', [7, 8, 9, 10, 11, 12].flatMap((s) => [125, 250, 500].map((b) => `SF${s}BW${b}`)));
  const rate = inp(50000, { type: 'number' }), cr = sel('4/5', ['4/5', '4/6', '4/7', '4/8']), pre = inp(8, { type: 'number', min: 0 });
  const ipol = checkbox(false, 'Invert IQ');
  const asHex = checkbox(false, 'Payload is hex');
  const data = inp('PING_FROM_LORAHUB', { class: 'mono' });
  const out = h('div', { class: 'small muted' });
  const sfField = field('Data rate', sf), rateField = field('FSK rate (bit/s)', rate), crField = field('Coding rate', cr), dataField = field('Payload', data);
  const warn = h('div', { class: 'callout warn small' }, 'A tone keeps transmitting until you press Stop TX.');
  const sync = () => { const m = modu.value; sfField.style.display = m === 'LORA' ? '' : 'none'; crField.style.display = m === 'LORA' ? '' : 'none'; rateField.style.display = m === 'FSK' ? '' : 'none'; dataField.style.display = m === 'CW' ? 'none' : ''; warn.style.display = m === 'CW' ? '' : 'none'; };
  modu.addEventListener('change', sync); sync();
  const counters = h('div');
  const refresh = async () => { const c = await GET('/concentrator'); counters.replaceChildren(c.status ? h('div', { class: 'grid g4' }, statTile('Transmitted', c.status.tx), statTile('TX errors', c.status.tx_err), statTile('Queue', c.status.q_depth), statTile('TX state', c.status.tx_status)) : h('div', { class: 'callout warn' }, 'raw_lora_app is not answering.')); };
  await refresh();
  const send = async () => {
    const body = { modu: modu.value, freq: Number(freq.value), powe: Number(power.value), rfch: Number(rf.value), datr: modu.value === 'FSK' ? rate.value : sf.value, codr: cr.value, ipol: ipol.input.checked, prea: Number(pre.value) };
    if (modu.value !== 'CW') { if (asHex.input.checked) body.payload_hex = cleanHex(data.value); else body.ascii = data.value; }
    try { await api('POST', '/concentrator/tx', body); out.textContent = 'Handed to raw_lora_app. Check the counters below for the result.'; setTimeout(() => refresh().catch(() => {}), 1500); } catch (e) { errToast(e); }
  };
  const root = h('div', { class: 'stack' }, pageHead([['TX test']], 'TX test', ['Sends through the local concentrator. Keep to your region\'s duty cycle and power limits.']), counters,
    h('div', { class: 'card card-body' }, h('div', { class: 'form wide stack' }, warn,
      h('div', { class: 'grid g4' }, field('Mode', modu), field('Frequency (MHz)', freq), field('Power (dBm)', power), field('RF chain', rf)),
      h('div', { class: 'grid g4' }, sfField, rateField, crField, field('Preamble', pre), h('div', { style: { alignSelf: 'center' } }, ipol.el)),
      dataField, asHex.el,
      h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: send }, icon('send'), 'Transmit now'),
        h('button', { class: 'btn danger', onclick: async () => { try { await api('POST', '/concentrator/tx', { abort: true, rfch: Number(rf.value) }); out.textContent = 'Stop command sent.'; } catch (e) { errToast(e); } } }, 'Stop TX')), out)));
  pollWhileVisible(root, 5000, () => refresh().catch(() => {}));
  return root;
});

// ======================= Remote gateways =======================
App.route('/remote', async () => {
  const gws = await GET('/gateways');
  return h('div', { class: 'stack' }, pageHead([['Remote config']], 'Remote config', ['Configure and control satellite gateways over SSH']),
    h('div', { class: 'card' }, gws.length ? h('div', { class: 'table-wrap' }, h('table', { class: 't' }, h('thead', null, h('tr', null, ['Gateway', 'Address', 'Status', 'Last seen'].map((x) => h('th', null, x)))),
      h('tbody', null, gws.map((g) => h('tr', { class: 'click', onclick: () => { location.hash = '#/remote/' + g.gateway_id; } }, h('td', null, g.name || g.gateway_id), h('td', { class: 'mono small' }, g.last_addr || '—'), h('td', null, onlineBadge(g.online, g.last_seen)), h('td', { class: 'small' }, ago(g.last_seen))))))) : h('div', { class: 'empty' }, 'No gateways yet.')));
});

App.route('/remote/:id', async ({ id }) => {
  const [g, key] = await Promise.all([GET('/gateways/' + id), GET('/remote/key')]);
  const body = h('div', { class: 'stack' });
  const load = async () => {
    body.replaceChildren(h('div', { class: 'empty' }, 'Connecting to the gateway…'));
    const r = await GET(`/gateways/${id}/remote`);
    const rc = r.remote || {};
    const host = inp(rc.host, { class: 'mono' }), port = inp(rc.port, { type: 'number' }), user = inp(rc.user, { class: 'mono' }), path = inp(rc.config_path, { class: 'mono' });
    const trust = checkbox(false, 'Trust a new host key', rc.host_key ? 'Pinned: ' + rc.host_key : null);
    const conn = h('div', { class: 'card card-body' }, h('h2', null, 'Connection'), h('div', { class: 'form wide stack' },
      h('div', { class: 'grid g4' }, field('Address', host), field('SSH port', port), field('User', user), field('Config file', path)), trust.el,
      h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: async () => {
        try { await api('PUT', `/gateways/${id}/remote`, { host: host.value, port: Number(port.value), user: user.value, config_path: path.value, trust_new_key: trust.input.checked }); toast('Saved'); load(); } catch (e) { errToast(e); }
      } }, 'Save and connect'))));
    const keyCard = h('div', { class: 'card card-body' }, h('h2', null, 'Allow this server to log in'),
      h('p', { class: 'small muted' }, 'Add this line once on the gateway, to /etc/dropbear/authorized_keys on OpenWrt or /root/.ssh/authorized_keys on Debian.'), val(key.public_key));
    if (r.error) { body.replaceChildren(h('div', { class: 'callout warn' }, 'Could not connect: ' + r.error), conn, keyCard); return; }
    const send = async (cmd) => { await api('POST', `/gateways/${id}/remote/service`, { cmd }); setTimeout(() => load().catch(errToast), 2500); };
    const log = logBox(async () => (await GET(`/gateways/${id}/remote/logs`)).log);
    body.replaceChildren(h('div', { class: 'card card-body' }, h('div', { class: 'row between' }, h('h2', { style: { margin: 0 } }, 'Concentrator service'), h('div', { class: 'chips' }, serviceButtons(send, r.running ? 'active' : 'stopped')))),
      ...(r.status ? [statusTiles(r.status), channelBars(r.status)] : []),
      r.config ? radioEditor(r.config, async (cfg, restart) => { await api('PUT', `/gateways/${id}/remote/config`, cfg); if (restart) await send('restart'); }) : h('div', { class: 'callout warn' }, r.config_error || 'No configuration found.'),
      h('div', { class: 'card card-body' }, h('h2', null, 'Log'), log.el), conn);
  };
  await load();
  return h('div', { class: 'stack' }, pageHead([['Remote config', '#/remote'], [g.name || id]], g.name || id, [h('span', { class: 'mono small' }, g.last_addr || '')]), body);
});

// ======================= Logs =======================
function logsPage(source) {
  const log = logBox(async () => (await GET(`/logs?source=${source}&lines=400`)).log);
  const root = h('div', { class: 'stack' }, pageHead([['Logs']], 'Logs'), tabs('#/logs', [['', 'Concentrator'], ['lorahub', 'LoRaHub']], source === 'concentrator' ? '' : source), h('div', { class: 'card card-body' }, log.el));
  pollWhileVisible(root, 4000, log.refresh);
  return root;
}
App.route('/logs', () => logsPage('concentrator'));
App.route('/logs/lorahub', () => logsPage('lorahub'));

// ======================= Hardware =======================
App.route('/hardware', async () => {
  const body = h('div', { class: 'stack' });
  const render = async () => {
    const hw = await GET('/hardware');
    const setGpio = async (g, payload) => { try { await api('POST', '/hardware/gpio', Object.assign({ gpio: g.gpio }, payload)); render(); } catch (e) { errToast(e); } };
    body.replaceChildren(pageHead([['GPIO & hardware']], 'GPIO & hardware', [hw.board || '']),
      h('div', { class: 'grid g2' },
        h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'LEDs')), h('div', { class: 'table-wrap' }, h('table', { class: 't' }, h('tbody', null, hw.leds.map((l) => h('tr', null, h('td', null, l.name), h('td', { class: 'small muted' }, l.trigger), h('td', null, h('span', { class: 'badge ' + (l.brightness ? 'ok' : '') }, l.brightness ? 'on' : 'off')),
          h('td', null, h('button', { class: 'btn sm', onclick: async () => { try { await api('POST', '/hardware/led', { name: l.name, value: l.brightness ? 0 : l.max }); render(); } catch (e) { errToast(e); } } }, 'Toggle')))))))),
        h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'Network')), h('div', { class: 'table-wrap' }, h('table', { class: 't' }, h('thead', null, h('tr', null, ['Interface', 'State', 'RX packets', 'TX packets'].map((x) => h('th', null, x)))), h('tbody', null, hw.net.map((n) => h('tr', null, h('td', null, n.name), h('td', null, h('span', { class: 'badge ' + (n.carrier ? 'ok' : 'warn') }, n.state)), h('td', null, n.rx_packets), h('td', null, n.tx_packets))))))) ),
      h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'GPIO lines in use')), hw.gpio.length ? h('div', { class: 'table-wrap' }, h('table', { class: 't' }, h('thead', null, h('tr', null, ['GPIO', 'Pin', 'Used by', 'Direction', 'Level', ''].map((x) => h('th', null, x)))),
        h('tbody', null, hw.gpio.map((g) => h('tr', null, h('td', { class: 'mono' }, g.gpio), h('td', { class: 'mono' }, g.pin), h('td', null, g.label || '—'), h('td', null, g.direction), h('td', null, h('span', { class: 'badge ' + (g.value ? 'ok' : '') }, g.value ? 'high' : 'low')),
          h('td', null, g.can_set ? h('div', { class: 'chips' }, h('button', { class: 'btn sm', onclick: () => setGpio(g, { action: 'set', value: 1 }) }, 'High'), h('button', { class: 'btn sm', onclick: () => setGpio(g, { action: 'set', value: 0 }) }, 'Low'), h('button', { class: 'btn sm', onclick: () => setGpio(g, { action: 'pulse' }) }, 'Pulse')) : h('span', { class: 'small muted' }, 'protected'))))))) : h('div', { class: 'empty' }, 'GPIO information is not available on this system.')));
  };
  await render();
  return body;
});

App.route('/pinout', async () => {
  const hw = await GET('/hardware');
  const named = hw.gpio.filter((g) => g.label && g.label !== 'sysfs');
  return h('div', { class: 'stack' }, pageHead([['Pinout']], 'Pinout', [hw.board || '', h('span', null, 'Read from this device')]),
    h('div', { class: 'grid g2' },
      h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'Concentrator wiring')), h('div', { class: 'table-wrap' }, h('table', { class: 't' }, h('tbody', null, hw.roles.map((r) => h('tr', null, h('td', null, r.function), h('td', { class: 'mono' }, r.value))))))),
      h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'Pins assigned by the kernel')), h('div', { class: 'table-wrap' }, h('table', { class: 't' }, h('thead', null, h('tr', null, ['Pin', 'GPIO', 'Function'].map((x) => h('th', null, x)))), h('tbody', null, named.map((g) => h('tr', null, h('td', { class: 'mono' }, g.pin), h('td', { class: 'mono' }, g.gpio), h('td', null, g.label)))))))));
});
