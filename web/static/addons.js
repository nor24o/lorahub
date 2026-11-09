/* LoRaHub console: concentrator status, radio, TX, remote gateways and logs. */
'use strict';

function statTile(label, value) { return h('div', { class: 'card stat' }, h('div', { class: 'label' }, label), h('div', { class: 'value', style: { fontSize: '18px' } }, value)); }
function statusTiles(st) {
  return h('div', { class: 'grid g4' }, statTile('Uptime', fmtDur(st.uptime_s)), statTile('Received', st.rx), statTile('Transmitted', st.tx), statTile('CRC errors', st.crc_err),
    statTile('TX errors', st.tx_err), statTile('Queue / dropped', `${st.q_depth} / ${st.q_drop}`), statTile('Chip temperature', st.temp_ok ? st.temp_c + ' °C' : '—'), statTile('Sync word', st.sync),
    statTile('RX state', st.rx_status), statTile('TX state', st.tx_status), statTile('Radio 0', st.rf0_mhz + ' MHz'), statTile('Radio 1', st.rf1_mhz + ' MHz'),
    ...(st.link ? [statTile('Link to hub', (st.link_peer ? 'Reliable' : 'Waiting') + (st.auth ? ', signed' : ', not paired')), statTile('Repeated / lost', `${st.link_retx} / ${st.link_lost}`), statTile('Engine', `${st.engine} · ${st.model}`)] : []));
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
function radioEditor(cfg, save, opts = {}) {
  const st = JSON.parse(JSON.stringify(cfg));
  const holder = h('div');
  let raw = false;
  const shown = new Set(['chan_if', 'chan_radio', 'tx_gain_lut', 'sf_mask']);
  const num = (k) => { shown.add(k); return inp(st[k], { type: 'number', step: 'any', oninput: (e) => { st[k] = e.target.value === '' ? undefined : Number(e.target.value); } }); };
  const txt = (k, cls) => { shown.add(k); return inp(st[k], { class: cls, oninput: (e) => { st[k] = e.target.value; } }); };
  const pick = (k, opts) => { shown.add(k); return sel(st[k], opts, { onchange: (e) => { const n = Number(e.target.value); st[k] = String(n) === e.target.value ? n : e.target.value; } }); };
  const bool = (k, label) => { shown.add(k); const c = checkbox(st[k], label); c.input.onchange = () => { st[k] = c.input.checked; }; return c.el; };
  const label = (k) => k.replace(/_/g, ' ').replace(/^./, (c) => c.toUpperCase());
  const anyField = (obj, k) => {
    const v = obj[k];
    if (typeof v === 'boolean') { const c = checkbox(v, label(k)); c.input.onchange = () => { obj[k] = c.input.checked; }; return c.el; }
    if (typeof v === 'number') return field(label(k), inp(v, { type: 'number', step: 'any', oninput: (e) => { obj[k] = Number(e.target.value); } }));
    if (typeof v === 'string') return field(label(k), h('input', { type: /pass/i.test(k) ? 'password' : 'text', value: v, autocomplete: 'off', oninput: (e) => { obj[k] = e.target.value; } }));
    return field(label(k), h('textarea', { class: 'mono', rows: 3, value: JSON.stringify(v, null, 1), oninput: (e) => { try { obj[k] = JSON.parse(e.target.value); e.target.classList.remove('bad'); } catch (err) { e.target.classList.add('bad'); } } }));
  };
  const otherSettings = () => {
    const keys = Object.keys(st).filter((k) => !shown.has(k)).sort();
    const plain = keys.filter((k) => st[k] === null || typeof st[k] !== 'object' || Array.isArray(st[k]));
    const groups = keys.filter((k) => !plain.includes(k));
    if (!keys.length) return [];
    return [h('h3', null, 'Other settings'), h('div', { class: 'grid g3' }, plain.map((k) => anyField(st, k))),
      ...groups.flatMap((k) => [h('h3', null, label(k)), h('div', { class: 'grid g3' }, Object.keys(st[k]).map((sub) => anyField(st[k], sub)))])];
  };
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
  const saveBtn = (getter) => opts.remote ? h('div', { class: 'form-actions' },
    h('button', { class: 'btn primary', onclick: async () => { try { await save(getter(), true); } catch (e) { errToast(e); } } }, 'Apply to gateway')) : h('div', { class: 'form-actions' },
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
      ...otherSettings(),
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
function loraAirtimeMs(len, sf, bw, cr, preamble, implicit, noCrc) {
  const ts = (2 ** sf) / bw;
  const de = sf >= 11 && bw === 125 ? 1 : 0;
  const n = 8 + Math.max(Math.ceil((8 * len - 4 * sf + 28 + (noCrc ? 0 : 16) - (implicit ? 20 : 0)) / (4 * (sf - 2 * de))) * (cr + 4), 0);
  return (preamble + 4.25) * ts + n * ts;
}
const TX_FREQS = ['863.000', '865.000', '867.100', '867.300', '867.500', '867.700', '867.900', '868.100', '868.300', '868.500', '869.525'];

async function txPage(preset, header) {
  const gws = (await GET('/gateways')).filter((g) => g.kind === 'bobcat');
  if (!gws.length) return h('div', { class: 'stack' }, ...header, h('div', { class: 'empty card' }, 'No raw_lora_app gateway is registered yet.'));
  const log = [];
  const gw = sel(preset || gws[0].gateway_id, gws.map((g) => [g.gateway_id, (g.name || g.gateway_id) + (g.online ? '' : ' (offline)')]));
  const modu = sel('LORA', [['LORA', 'LoRa packet'], ['FSK', 'FSK packet'], ['CW', 'Continuous tone']]);
  const freqList = h('datalist', { id: 'tx-freqs' }, TX_FREQS.map((f) => h('option', { value: f })));
  const freq = inp('868.100', { type: 'number', step: '0.001', list: 'tx-freqs' }), power = inp(14, { type: 'number', min: 0, max: 27 }), rf = sel(0, [[0, 'Chain 0'], [1, 'Chain 1']]);
  const sf = sel(7, [5, 6, 7, 8, 9, 10, 11, 12].map((x) => [x, 'SF' + x])), bw = sel(125, [125, 250, 500].map((x) => [x, x + ' kHz']));
  const cr = sel('4/5', ['4/5', '4/6', '4/7', '4/8']), pre = inp(8, { type: 'number', min: 6, max: 65535 });
  const rate = inp(50000, { type: 'number' }), fdev = inp(25, { type: 'number', min: 1, max: 200 }), foff = inp(0, { type: 'number', min: -128, max: 127 });
  const ipol = checkbox(false, 'Invert IQ'), ncrc = checkbox(false, 'No CRC'), nhdr = checkbox(false, 'Implicit header');
  const source = sel('text', [['text', 'Text'], ['hex', 'Hex bytes'], ['random', 'Random bytes']]);
  const data = inp('PING_FROM_LORAHUB', { class: 'mono' }), randomLen = inp(16, { type: 'number', min: 1, max: 255 });
  const counter = checkbox(false, 'Add a 2-byte counter at the end');
  const count = inp(1, { type: 'number', min: 1, max: 1000 }), interval = inp(5000, { type: 'number', min: 100, step: 100 }), delay = inp(0, { type: 'number', min: 0, max: 60000, step: 100 });
  const readout = h('div', { class: 'chips' });
  const progress = h('div', { class: 'small muted' });
  const counters = h('div');
  const history = h('div');

  const fields = { sf: field('Spreading factor', sf), bw: field('Bandwidth', bw), cr: field('Coding rate', cr), pre: field('Preamble', pre), rate: field('Bit rate (bit/s)', rate), fdev: field('Deviation (kHz)', fdev), foff: field('Tone offset (kHz)', foff), data: field('Payload', data), randomLen: field('Length (bytes)', randomLen) };
  const flags = h('div', { class: 'row' }, ipol.el, ncrc.el, nhdr.el);
  const warn = h('div', { class: 'callout warn small' }, 'A tone keeps transmitting until you press Stop TX.');
  const size = () => {
    if (source.value === 'random') return Number(randomLen.value) + (counter.input.checked ? 2 : 0);
    const n = source.value === 'hex' ? cleanHex(data.value).length / 2 : new TextEncoder().encode(data.value).length;
    return Math.floor(n) + (counter.input.checked ? 2 : 0);
  };
  const sync = () => {
    const m = modu.value, lora = m === 'LORA';
    fields.sf.style.display = fields.bw.style.display = fields.cr.style.display = flags.style.display = lora ? '' : 'none';
    fields.rate.style.display = fields.fdev.style.display = m === 'FSK' ? '' : 'none';
    fields.foff.style.display = warn.style.display = m === 'CW' ? '' : 'none';
    fields.pre.style.display = m === 'CW' ? 'none' : '';
    source.parentElement.style.display = m === 'CW' ? 'none' : '';
    fields.data.style.display = m === 'CW' || source.value === 'random' ? 'none' : '';
    fields.randomLen.style.display = m !== 'CW' && source.value === 'random' ? '' : 'none';
    counter.el.style.display = m === 'CW' ? 'none' : '';
    const chips = [];
    if (m !== 'CW') chips.push(h('span', { class: 'chip' }, h('b', null, 'size '), size() + ' B'));
    if (lora) {
      const ms = loraAirtimeMs(size(), Number(sf.value), Number(bw.value), Number(cr.value.split('/')[1]) - 4, Number(pre.value), nhdr.input.checked, ncrc.input.checked);
      chips.push(h('span', { class: 'chip' }, h('b', null, 'airtime '), ms.toFixed(1) + ' ms'));
      if (Number(count.value) > 1) chips.push(h('span', { class: 'chip' }, h('b', null, 'duty '), ((ms / Number(interval.value)) * 100).toFixed(2) + ' %'));
    }
    if (Number(count.value) > 1) chips.push(h('span', { class: 'chip' }, h('b', null, 'total '), fmtDur(Math.round(((Number(count.value) - 1) * Number(interval.value)) / 1000))));
    readout.replaceChildren(...chips);
  };

  const refresh = async () => {
    const r = await GET(`/gateways/${gw.value}/remote?status=1`);
    counters.replaceChildren(r.status ? h('div', { class: 'grid g4' }, statTile('Transmitted', r.status.tx), statTile('TX errors', r.status.tx_err), statTile('Queue', r.status.q_depth), statTile('TX state', r.status.tx_status)) : h('div', { class: 'callout warn' }, 'No live status from this gateway: ' + (r.status_error || r.error || 'unknown')));
  };
  const paintHistory = () => history.replaceChildren(log.length ? h('div', { class: 'table-wrap' }, h('table', { class: 't compact' }, h('thead', null, h('tr', null, ['Time', 'Gateway', 'Frequency', 'Mode', 'Power', 'Result'].map((x) => h('th', null, x)))),
    h('tbody', null, log.slice(0, 20).map((x) => h('tr', null, h('td', { class: 'small nowrap' }, x.time), h('td', null, x.gw), h('td', { class: 'mono small' }, x.freq), h('td', { class: 'small' }, x.mode), h('td', { class: 'small' }, x.power + ' dBm'), h('td', { class: 'small' }, x.result)))))) : h('div', { class: 'empty' }, 'Nothing sent from this page yet.'));

  const watchJob = async (entry) => {
    const j = await GET('/concentrator/tx/job');
    entry.result = `${j.sent} of ${j.total}` + (j.error ? ': ' + j.error : '');
    progress.textContent = j.running ? `Sending: ${j.sent} of ${j.total}` : `Finished: ${j.sent} of ${j.total} handed to raw_lora_app` + (j.error ? '. ' + j.error : '');
    paintHistory();
    if (j.running && root.isConnected) setTimeout(() => watchJob(entry).catch(errToast), 1000);
  };
  const send = async () => {
    const m = modu.value;
    const body = { gateway: gw.value, modu: m, freq: Number(freq.value), powe: Number(power.value), rfch: Number(rf.value), codr: cr.value, ipol: ipol.input.checked, ncrc: ncrc.input.checked, nhdr: nhdr.input.checked,
      prea: Number(pre.value), count: Number(count.value), interval_ms: Number(interval.value), delay_ms: Number(delay.value), append_counter: m !== 'CW' && counter.input.checked,
      datr: m === 'FSK' ? rate.value : `SF${sf.value}BW${bw.value}`, f_dev: m === 'FSK' ? Number(fdev.value) : 0, f_off: m === 'CW' ? Number(foff.value) : 0 };
    if (m !== 'CW') { if (source.value === 'hex') body.payload_hex = cleanHex(data.value); else if (source.value === 'random') body.random_len = Number(randomLen.value); else body.ascii = data.value; }
    const entry = { time: new Date().toLocaleTimeString(), gw: gw.selectedOptions[0].textContent, freq: Number(freq.value).toFixed(3), mode: m === 'LORA' ? body.datr : m, power: body.powe, result: 'sending' };
    log.unshift(entry);
    try {
      const r = await api('POST', '/concentrator/tx', body);
      if (r.sent) { entry.power = r.sent.powe; entry.result = 'handed to raw_lora_app'; progress.textContent = 'Handed to raw_lora_app. The counters above show the result.'; } else { entry.power = r.power; watchJob(entry).catch(errToast); }
    } catch (e) { entry.result = e.message; errToast(e); }
    paintHistory();
    setTimeout(() => refresh().catch(() => {}), 1500);
  };
  const stop = async () => { try { await api('POST', '/concentrator/tx', { gateway: gw.value, abort: true, rfch: Number(rf.value) }); progress.textContent = 'Stop command sent.'; } catch (e) { errToast(e); } };

  const form = h('div', { class: 'form wide stack' }, warn,
    h('div', { class: 'grid g4' }, field('Gateway', gw), field('Mode', modu), field('Frequency (MHz)', freq), field('Power (dBm)', power)),
    h('div', { class: 'grid g4' }, field('RF chain', rf), fields.sf, fields.bw, fields.cr, fields.pre, fields.rate, fields.fdev, fields.foff), flags,
    h('h3', null, 'Payload'),
    h('div', { class: 'grid g4' }, h('div', { class: 'field' }, h('label', null, 'Content'), source), fields.randomLen), fields.data, counter.el,
    h('h3', null, 'Timing'),
    h('div', { class: 'grid g4' }, field('Packets', count), field('Interval (ms)', interval), field('Start delay (ms)', delay)),
    readout,
    h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: send }, icon('send'), 'Transmit'), h('button', { class: 'btn danger', onclick: stop }, 'Stop TX')), progress, freqList);
  form.addEventListener('input', sync); form.addEventListener('change', sync);
  gw.addEventListener('change', () => refresh().catch(errToast));
  sync(); paintHistory();
  await refresh().catch(() => {});
  const root = h('div', { class: 'stack' }, ...header, counters, h('div', { class: 'card card-body' }, form), h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'Sent from this page')), history));
  pollWhileVisible(root, 5000, () => refresh().catch(() => {}));
  return root;
}
App.route('/tx', () => txPage(null, [pageHead([['TX test']], 'TX test', ['Keep to your region\'s duty-cycle and power limits.'])]));
App.route('/gateways/:id/transmit', async ({ id }) => txPage(id, gwHeader(await GET('/gateways/' + id), 'transmit')));

// ======================= Remote gateways =======================
function remoteForm(g, r, reload) {
  const c = r.config;
  const mhz = (v) => (v ? (v / 1e6).toFixed(3) : '');
  const rf0 = inp(mhz(c.rf0_freq), { type: 'number', step: '0.001' }), rf1 = inp(mhz(c.rf1_freq), { type: 'number', step: '0.001' });
  const sync = sel(c.sync_word, [['private', 'Private (0x12)'], ['public', 'Public (0x34)']]);
  const power = inp(c.tx_power, { type: 'number', min: 0, max: 27 }), txf = inp('', { type: 'number', step: '0.001', placeholder: 'unchanged' });
  const ip = inp(c.target_ip, { class: 'mono' }), port = inp(c.port_up, { type: 'number', min: 1, max: 65535 });
  const apply = async () => {
    const d = {};
    const hz = (input, cur, key) => { if (input.value !== '' && mhz(cur) !== Number(input.value).toFixed(3)) d[key] = Math.round(Number(input.value) * 1e6); };
    hz(rf0, c.rf0_freq, 'rf0_freq'); hz(rf1, c.rf1_freq, 'rf1_freq'); hz(txf, 0, 'tx_default_freq');
    if (sync.value !== c.sync_word) d.sync_word = sync.value;
    if (Number(power.value) !== c.tx_power) d.tx_default_power = Number(power.value);
    if (ip.value.trim() !== c.target_ip) d.target_ip = ip.value.trim();
    if (Number(port.value) !== c.port_up) d.port_up = Number(port.value);
    if (!Object.keys(d).length) { toast('Nothing changed'); return; }
    const leaves = d.target_ip || d.port_up;
    const text = leaves ? 'The gateway will send its packets to the new address. If that address is wrong, it disappears from LoRaHub and has to be fixed on the gateway itself.' : 'The radio restarts to apply this, so reception pauses for a moment.';
    if (!(await confirmModal('Apply to ' + (g.name || g.gateway_id), text, 'Apply', Boolean(leaves)))) return;
    try { await api('PUT', `/gateways/${g.gateway_id}/remote/config`, d); toast('Applied by the gateway'); reload(); } catch (e) { errToast(e); }
  };
  return h('div', { class: 'card card-body' }, h('div', { class: 'form wide stack' },
    h('h3', null, 'Radio'),
    h('div', { class: 'grid g4' }, field('Radio 0 centre (MHz)', rf0), field('Radio 1 centre (MHz)', rf1), field('Sync word', sync), h('div')),
    h('h3', null, 'Transmit defaults'),
    h('div', { class: 'grid g4' }, field('Power (dBm)', power), field('Frequency (MHz)', txf), h('div'), h('div')),
    h('h3', null, 'Where it sends packets'),
    h('div', { class: 'grid g4' }, field('Server IP', ip), field('Uplink port', port), field('Downlink port', inp(c.port_down, { disabled: true }), 'Read only on the gateway.'), h('div')),
    h('div', { class: 'small muted' }, 'These are the settings raw_lora_app accepts remotely. Channels, spreading-factor mask and the TX gain table are not part of its remote command.'),
    h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: apply }, 'Apply to gateway'))));
}

async function radioPanel(g) {
  const live = h('div', { class: 'stack' });
  const paintLive = (r) => live.replaceChildren(...(r.status ? [statusTiles(r.status), channelBars(r.status)] : [h('div', { class: 'callout warn' }, 'No live status from this gateway: ' + (r.status_error || 'unknown'))]));
  const body = h('div', { class: 'stack' });
  const load = async () => {
    const r = await GET(`/gateways/${g.gateway_id}/remote`);
    if (!r.supported) { live.replaceChildren(); body.replaceChildren(h('div', { class: 'callout' }, r.error)); return; }
    paintLive(r);
    if (r.local) {
      const c = await GET('/concentrator');
      body.replaceChildren(c.config ? radioEditor(c.config, async (cfg, restart) => { await api('PUT', '/concentrator', cfg); if (restart) await api('POST', '/concentrator/service', { cmd: 'restart' }); }) : h('div', { class: 'callout warn' }, 'Cannot read the configuration: ' + (c.config_error || 'unknown error')));
      return;
    }
    const facts = h('dl', { class: 'kv small' }, h('dt', null, 'Address'), h('dd', { class: 'mono' }, r.host), h('dt', null, 'Control port'), h('dd', { class: 'mono' }, 'UDP ' + r.control_port), h('dt', null, 'Status port'), h('dd', { class: 'mono' }, 'TCP ' + r.status_port));
    const full = r.config && r.config.config;
    const applyFull = async (cfg) => {
      const d = {};
      Object.keys(cfg).forEach((k) => { if (JSON.stringify(cfg[k]) !== JSON.stringify(full[k])) d[k] = cfg[k]; });
      if (!Object.keys(d).length) { toast('Nothing changed'); return; }
      const leaves = d.target_ip !== undefined || d.port_up !== undefined;
      const text = leaves ? 'The gateway will send its packets to the new address. If that address is wrong, it disappears from LoRaHub and has to be fixed on the gateway itself.' : 'The radio restarts to apply this, so reception pauses for a moment. If the radio will not start with these settings, the gateway restores the previous ones.';
      if (!(await confirmModal('Apply to ' + (g.name || g.gateway_id), text, 'Apply', Boolean(leaves)))) return;
      await api('PUT', `/gateways/${g.gateway_id}/remote/config`, d);
      toast('Applied by the gateway');
      load().catch(errToast);
    };
    body.replaceChildren(h('div', { class: 'card card-body' }, h('h2', null, 'Connection'), facts),
      full ? radioEditor(full, applyFull, { remote: true }) : r.config ? remoteForm(g, r, () => load().catch(errToast)) : h('div', { class: 'callout warn' }, r.error || 'The gateway did not answer.'));
  };
  await load();
  const root = h('div', { class: 'stack' }, live, body);
  pollWhileVisible(root, 5000, async () => { if (document.querySelector('.modal-bg')) return; try { const r = await GET(`/gateways/${g.gateway_id}/remote?status=1`); if (r.supported) paintLive(r); } catch (e) { /* next round */ } });
  return root;
}

App.route('/remote', async () => {
  const gws = (await GET('/gateways')).filter((g) => g.kind === 'bobcat');
  return h('div', { class: 'stack' }, pageHead([['Remote config']], 'Remote config', ['Change the radio of each gateway from here']),
    h('div', { class: 'card' }, gws.length ? h('div', { class: 'table-wrap' }, h('table', { class: 't' }, h('thead', null, h('tr', null, ['Gateway', 'Address', 'Status', 'Last seen'].map((x) => h('th', null, x)))),
      h('tbody', null, gws.map((g) => h('tr', { class: 'click', onclick: () => { location.hash = '#/remote/' + g.gateway_id; } }, h('td', null, g.name || g.gateway_id), h('td', { class: 'mono small' }, g.last_addr || '—'), h('td', null, onlineBadge(g.online, g.last_seen)), h('td', { class: 'small' }, ago(g.last_seen))))))) : h('div', { class: 'empty' }, 'No raw_lora_app gateways yet.')));
});

App.route('/remote/:id', async ({ id }) => {
  const g = await GET('/gateways/' + id);
  return h('div', { class: 'stack' }, pageHead([['Remote config', '#/remote'], [g.name || id]], g.name || id, [h('span', { class: 'mono small' }, g.last_addr || ''), h('a', { href: `#/gateways/${id}/settings` }, 'Gateway settings')]), await radioPanel(g));
});

App.route('/gateways/:id/radio', async ({ id }) => {
  const g = await GET('/gateways/' + id);
  return h('div', { class: 'stack' }, ...gwHeader(g, 'radio'), await radioPanel(g));
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
