'use strict';
// FSK bridge: FSK packets heard by the gateways go to another program over UDP,
// untouched, and datagrams from it go on the air as FSK (internal/integration/fskbridge.go).

App.route('/fsk', async () => {
  const [d, gws] = await Promise.all([GET('/fsk'), GET('/gateways')]);
  const b = Object.assign({}, d.settings);
  const enabled = checkbox(b.enabled, 'Bridge on', 'FSK packets then skip node processing, Home Assistant and rules.');
  const sendTo = inp(b.send_to, { class: 'mono', placeholder: '192.168.1.20:1731' });
  const listen = inp(b.listen, { class: 'mono', placeholder: ':1730' });
  const format = sel(b.format, [['json', 'JSON: data and metadata'], ['raw', 'Raw: the bytes only']]);
  const gateway = sel(b.gateway || '', [['', 'The one that heard the last FSK packet best'], ...gws.map((g) => [g.gateway_id, g.name || g.gateway_id])]);
  const numIn = (v, attrs) => inp(v, Object.assign({ type: 'number', step: 'any' }, attrs));
  const freq = numIn(b.frequency, { min: 150, max: 1000 });
  const rate = numIn(b.bit_rate, { min: 500, max: 250000, step: 1 });
  const dev = numIn(b.deviation, { min: 1, max: 200, step: 1 });
  const power = numIn(b.power, { min: -10, max: 30, step: 1 });
  const pre = numIn(b.preamble, { min: 3, max: 64, step: 1 });

  const status = h('div', { class: 'small' });
  const showStats = (st) => {
    if (!st) { status.replaceChildren(); return; }
    const bits = [];
    if (st.listening) bits.push(h('span', { class: 'badge ok' }, 'Listening on ' + st.listening));
    else if (st.listen_error) bits.push(h('span', { class: 'badge err' }, 'Cannot listen: ' + st.listen_error));
    else bits.push(h('span', { class: 'badge' }, 'Off'));
    bits.push(h('span', { class: 'muted' }, ` · ${st.forwarded} packet(s) sent to your program · ${st.received} datagram(s) received, ${st.sent} put on the air`));
    const bad = (text) => h('div', { style: { color: 'var(--danger)' } }, text);
    if (st.forward_error) bits.push(bad('Sending to your program: ' + st.forward_error));
    if (st.send_error) bits.push(bad('Last datagram not sent: ' + st.send_error));
    status.replaceChildren(...bits);
  };
  showStats(d.stats);
  const t = setInterval(async () => { if (!document.hidden) { try { showStats((await GET('/fsk')).stats); } catch (e) { /* keep */ } } }, 5000);
  App.onLeave(() => clearInterval(t));

  const save = async () => {
    const fsk = { enabled: enabled.input.checked, send_to: sendTo.value.trim(), listen: listen.value.trim(), format: format.value, gateway: gateway.value,
      frequency: Number(freq.value), bit_rate: Number(rate.value), deviation: Number(dev.value), power: Number(power.value), preamble: Number(pre.value) };
    try {
      await api('PUT', '/settings', { fsk_bridge: fsk });
      toast('FSK bridge saved');
      setTimeout(async () => { try { showStats((await GET('/fsk')).stats); } catch (e) { /* keep */ } }, 400);
    } catch (e) { errToast(e); }
  };

  // Each gateway's FSK channel, as its engine reports it (2.3 and later).
  // The hub's own engine reports from this machine; the others are changed through Remote config.
  const local = (id) => /^127\./.test((gws.find((x) => x.gateway_id === id) || {}).last_addr || '');
  const atLeast = (v, want) => { const a = String(v).split('.').map(Number), b = want.split('.').map(Number); return a[0] > b[0] || (a[0] === b[0] && (a[1] || 0) >= b[1]); };
  const chanRows = d.gateways.map((g) => {
    const f = g.fsk;
    const where = local(g.gateway_id) ? '#/radio' : '#/remote';
    let what;
    if (f) what = h('span', null, h('b', null, (f.freq / 1e6).toFixed(3) + ' MHz'), ` · ${f.datarate} bit/s · sync ${f.sync_word} · CRC ${f.crc.toUpperCase()} · whitening ${f.whitening ? 'on' : 'off'} · ${f.length ? 'fixed ' + f.length + ' bytes' : 'length byte'}`);
    else if (g.engine && atLeast(g.engine, '2.3')) what = h('span', { class: 'muted' }, 'Off');
    else what = h('span', { class: 'muted' }, g.engine ? `Not reported (engine ${g.engine}; 2.3 reports it)` : 'Not reported');
    return h('tr', null, h('td', null, h('span', { class: 'dot ' + (g.online ? 'on' : 'off') }), ' ', h('a', { href: '#/gateways/' + g.gateway_id }, g.name || g.gateway_id)),
      h('td', { class: 'small' }, what), h('td', { class: 'right' }, h('a', { class: 'btn sm', href: where }, 'Change')));
  });

  const testHex = inp('', { class: 'mono', placeholder: '01 02 03 04' });
  const sendTest = async () => {
    try {
      const r = await api('POST', '/fsk/send', { data_hex: cleanHex(testHex.value) });
      toast('Sent as FSK through ' + r.gateway_id);
    } catch (e) { errToast(e); }
  };

  const exOut = '{"data_hex":"A1B2C3", "size":3, "frequency":868.3, "bit_rate":50000,\n "received_at":"…", "gateways":[{"gateway_id":"…", "rssi":-71, "timestamp":…}]}';
  const exIn = '{"data_hex":"0102"}\n{"data_hex":"0102", "gateway":"…", "frequency":868.3, "bit_rate":50000,\n "deviation":25, "power":14, "preamble":5}';

  return h('div', { class: 'stack' },
    pageHead([['FSK bridge']], 'FSK bridge', ['FSK packets to and from your own program over UDP, untouched']),
    h('div', { class: 'callout info small' }, 'Every FSK packet a gateway hears goes once, with the signal of each gateway that heard it, to the address below. ',
      'Every datagram your program sends to the bridge\'s port goes on the air as FSK. The packets are not decoded or stored on any node. ',
      'The gateways\' FSK channel (frequency, bit rate, sync word, CRC) is set in Radio config. ', h('a', { href: '#/help/fsk' }, 'How it works')),
    h('div', { class: 'card card-body stack' }, h('h2', { style: { margin: 0 } }, 'Bridge'), enabled.el,
      h('div', { class: 'grid g3' }, field('Send every packet heard to', sendTo, 'host:port of your program; empty = nowhere'),
        field('Take packets to send on', listen, 'UDP address on this hub, e.g. :1730'), field('Format, both ways', format)),
      h('h3', null, 'Sending'),
      h('div', { class: 'grid g3' }, field('Through', gateway), field('Frequency (MHz)', freq), field('Bit rate (bit/s)', rate)),
      h('div', { class: 'grid g3' }, field('Deviation (kHz)', dev, '±; bit rate + 2 × deviation should stay under 125 kHz'), field('Power (dBm)', power), field('Preamble (bytes)', pre)),
      h('div', { class: 'small muted' }, 'A JSON datagram may give its own gateway, frequency, bit rate, deviation, power and preamble.'),
      status,
      h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', onclick: save }, 'Save'))),
    h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'Gateways\' FSK channel')),
      h('div', { class: 'table-wrap' }, h('table', { class: 't' }, h('tbody', null, chanRows.length ? chanRows : h('tr', null, h('td', null, 'No gateways yet.')))))),
    h('div', { class: 'grid g2' },
      h('div', { class: 'card card-body stack' }, h('h2', { style: { margin: 0 } }, 'Send a test packet'),
        field('Bytes (hex)', testHex, 'Sent with the settings above, at once.'),
        h('div', { class: 'form-actions' }, h('button', { class: 'btn', onclick: sendTest }, icon('send'), 'Send as FSK'))),
      h('div', { class: 'card card-body' }, h('h2', null, 'JSON datagrams'),
        h('div', { class: 'small muted' }, 'To your program, one per packet heard:'), h('pre', { class: 'code small' }, exOut),
        h('div', { class: 'small muted' }, 'From your program, to send (only data_hex is needed):'), h('pre', { class: 'code small' }, exIn))),
    liveView({ title: 'FSK traffic', only: (e) => e.name.startsWith('fsk.') || e.name === 'gs.down.tx_ack' }));
});
