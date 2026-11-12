'use strict';
// Device settings pages shared by the hub console (#/device) and a satellite's
// own page: status, network (wired, Wi-Fi client, access point), tunnel,
// general (name and time), SD card, lights, system and logs.
//
// HostUI.render(page, view, ctx) draws one page into view. ctx carries:
//   api(method, path, body)  the device API (paths like '/status'); errors have .status
//   url(path)                the same API as a link (downloads)
//   toast(message, isError)  feedback
//   onLeave(fn)              cleanup when the page is left
//   role                     'hub' or 'satellite'
//   hostname                 the board's name
//   setDot(ok)               optional, the "hub answers" light in a top bar
window.HostUI = (() => {
  // ---------- small DOM helpers (private, so they never clash with the console's) ----------
  function h(tag, attrs, ...kids) {
    const el = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v == null || v === false) continue;
      if (k === 'class') el.className = v;
      else if (k === 'style') Object.assign(el.style, v);
      else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
      else if (k === 'value') el.value = v;
      else if (k === 'checked') el.checked = !!v;
      else el.setAttribute(k, v === true ? '' : v);
    }
    for (const c of kids.flat(Infinity)) {
      if (c == null || c === false) continue;
      el.append(c instanceof Node ? c : document.createTextNode(String(c)));
    }
    return el;
  }

  const fmt = {
    bytes(n) {
      if (n == null) return '—';
      const u = ['B', 'KB', 'MB', 'GB', 'TB']; let i = 0;
      while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
      return (i ? n.toFixed(n < 10 ? 1 : 0) : n) + ' ' + u[i];
    },
    dur(s) {
      if (s == null) return '—';
      s = Math.floor(s);
      const d = Math.floor(s / 86400), hh = Math.floor(s % 86400 / 3600), m = Math.floor(s % 3600 / 60);
      if (d) return d + 'd ' + hh + 'h';
      if (hh) return hh + 'h ' + m + 'm';
      if (m) return m + 'm ' + (s % 60) + 's';
      return s + 's';
    },
    ago(iso) {
      if (!iso) return 'never';
      const s = (Date.now() - new Date(iso).getTime()) / 1000;
      return s < 2 ? 'just now' : fmt.dur(s) + ' ago';
    },
    num(n) { return n == null ? '—' : Number(n).toLocaleString(); },
    mhz(hz) { return hz ? (hz / 1e6).toFixed(3) + ' MHz' : '—'; },
    date(iso) { return iso ? new Date(iso).toLocaleString() : '—'; },
  };

  const badge = (text, kind) => h('span', { class: 'badge' + (kind ? ' ' + kind : '') }, text);
  const tile = (label, value, hint) => h('div', { class: 'value-tile', title: hint || null }, h('b', null, value), h('span', null, label));
  const kv = (pairs) => h('dl', { class: 'kv host-kv' }, pairs.filter(Boolean).map(([k, v]) => [h('dt', null, k), h('dd', null, v)]));
  const card = (title, right, ...body) => h('div', { class: 'card' },
    h('div', { class: 'card-head' }, h('h2', null, title), right || null), h('div', { class: 'card-body' }, ...body));
  const field = (label, input, help) => h('div', { class: 'field' }, h('label', null, label), input, help ? h('div', { class: 'help' }, help) : null);
  const check = (label, input, help) => h('label', { class: 'check' }, input, h('span', null, label, help ? h('div', { class: 'help muted small' }, help) : null));
  const bars = (sig) => h('span', { class: 'host-bars', title: sig + '%' }, [20, 40, 60, 80].map((t, i) => h('i', { class: sig > t ? 'on' : '', style: { height: (5 + i * 3) + 'px' } })));
  const keepBox = () => h('input', { type: 'checkbox' });
  const keepHelp = (ctx) => ctx.role === 'hub'
    ? 'After a change the hub pings your router for 60 s. If the router does not answer, the previous setting comes back by itself. Tick this to keep the change anyway.'
    : 'After a change the satellite pings the hub through the tunnel for 60 s. If the hub does not answer, the previous setting comes back by itself. Tick this to keep the change anyway.';

  function every(ctx, ms, f) {
    f();
    const id = setInterval(() => { if (!document.hidden) f(); }, ms);
    ctx.onLeave(() => clearInterval(id));
  }
  function listen(ctx, el, ev, f) { el.addEventListener(ev, f); ctx.onLeave(() => el.removeEventListener(ev, f)); }
  // put replaces el's children; empty slots are skipped (replaceChildren would print "null").
  const put = (el, ...kids) => el.replaceChildren(...kids.flat(Infinity).filter((k) => k != null && k !== false));
  const noAnswer = (e) => h('div', { class: 'callout warn' }, 'No answer from the device: ' + e.message);

  function ifaceLabel(name) {
    if (name.startsWith('end') || name.startsWith('eth')) return 'wired';
    if (name.startsWith('wlan')) return 'Wi-Fi';
    if (name.startsWith('wg')) return 'tunnel';
    if (name.startsWith('br')) return 'bridge';
    if (name.startsWith('tailscale')) return 'Tailscale';
    return '';
  }

  function changeBox(ch) {
    if (!ch) return h('div', { class: 'host-change' });
    const recent = !ch.finished || (Date.now() - new Date(ch.finished).getTime()) < 10 * 60 * 1000;
    if (!recent) return h('div', { class: 'host-change' });
    let text = ch.message || '';
    if (ch.state === 'applying') text = 'Applying…';
    if (ch.state === 'checking') {
      const left = ch.deadline ? Math.max(0, Math.round((new Date(ch.deadline).getTime() - Date.now()) / 1000)) : '';
      text = 'Applied. Checking the connection' + (left !== '' ? ' (' + left + ' s left)' : '') + '; if it is lost, the previous setting comes back.';
    }
    const done = ch.state === 'reverted' || ch.state === 'failed';
    return h('div', { class: 'host-change callout ' + (done ? 'warn' : 'info') }, h('b', null, ch.what), ' ',
      badge(ch.state, ch.state === 'kept' ? 'ok' : done ? 'err' : 'info'), h('div', null, text));
  }

  function lostContact() {
    return h('div', { class: 'host-change callout warn' }, h('b', null, 'The page lost contact with the device.'),
      h('div', null, 'If you changed its address, open the page at the new one. If the connection is not back within 60 s, the previous setting comes back by itself and this page answers again.'));
  }

  // ---------- status ----------
  function status(view, ctx) {
    every(ctx, 5000, async () => {
      let st;
      try { st = await ctx.api('GET', '/status'); } catch (e) { if (e.status !== 401) put(view, noAnswer(e)); return; }
      if (ctx.setDot) ctx.setDot(st.tunnel.hub_reachable);
      put(view, engineCard(st, ctx), h('div', { class: 'grid g2 host-grid' }, tunnelCard(st.tunnel, ctx), boardCard(st.system)),
        ifaceCard(st.interfaces), radioCard(st.radio, ctx));
    });
  }

  function engineCard(st, ctx) {
    const e = st.engine;
    if (!e) return card('Radio engine', badge(st.engine_service || 'down', 'err'), h('div', { class: 'callout warn' }, st.engine_error || 'The radio engine does not answer.'));
    const right = h('div', { class: 'row' }, badge(e.rx_status || '?', e.rx_status === 'LISTENING' ? 'ok' : 'err'),
      badge(e.tx_status || '?', e.tx_status === 'TX_FREE' ? 'ok' : 'warn'),
      ctx.role === 'hub' ? null : badge(e.auth ? 'paired with the hub' : 'not paired', e.auth ? 'ok' : 'warn'));
    return card('Radio engine', right,
      h('div', { class: 'host-tiles' },
        tile('packets received', fmt.num(e.rx)), tile('packets sent', fmt.num(e.tx)), tile('CRC errors', fmt.num(e.crc_err)),
        tile('send errors', fmt.num(e.tx_err)), tile('chip temperature', e.temp_ok ? e.temp_c.toFixed(1) + ' °C' : 'no sensor'),
        tile('engine running', fmt.dur(e.uptime_s)),
        ctx.role === 'hub' ? null : [tile('link acks', fmt.num(e.link_acks), 'uplinks the hub confirmed'),
          tile('link resends', fmt.num(e.link_retx)), tile('link lost', fmt.num(e.link_lost), 'given up after about 12 s')],
        tile('send queue', fmt.num(e.q_depth) + (e.q_drop ? ' (' + e.q_drop + ' dropped)' : '')),
        tile('lights', e.led_mode || 'normal')),
      h('div', { style: { marginTop: '14px' } }, kv([
        ['Gateway', h('span', { class: 'val' }, 'eui-' + String(e.eui || '').toLowerCase())],
        ['Model', e.model || '—'], ['Engine', (e.engine || '—') + (e.link ? ', link protocol ' + e.link : '')],
        ['Radios', (e.rf0_mhz ? e.rf0_mhz + ' MHz' : '—') + ' and ' + (e.rf1_mhz ? e.rf1_mhz + ' MHz' : '—')],
        ctx.role === 'hub' ? null : ['Hub link', e.link_peer ? badge('hub acknowledges', 'ok') : badge('no acknowledgements', 'warn')],
      ])));
  }

  function tunnelCard(t, ctx) {
    if (ctx.role === 'hub') {
      const up = t.peers.filter((p) => p.connected).length;
      return card('Tunnel', h('a', { href: '#/vpn', class: 'small' }, 'WireGuard & Tailscale'),
        kv([['Hub address', t.address || '—'], ['Satellites', up + ' of ' + t.peers.length + ' connected'],
          ...t.peers.map((p) => [p.name || p.allowed_ips.join(', '), (p.connected ? badge('connected', 'ok') : badge('silent', 'warn')),
          ]), ['Router', t.hub_reachable ? 'answers' + (t.hub_ping_ms ? ', ' + t.hub_ping_ms.toFixed(1) + ' ms' : '') : 'does not answer']]));
    }
    const p = t.peers[0] || {};
    return card('Tunnel to the hub', t.hub_reachable ? badge('hub answers' + (t.hub_ping_ms ? ', ' + t.hub_ping_ms.toFixed(1) + ' ms' : ''), 'ok') : badge('hub does not answer', 'err'),
      kv([['This satellite', t.address || '—'], ['Hub', t.hub_ip || '—'],
        ['Last handshake', p.last_handshake ? fmt.ago(p.last_handshake) : 'never'],
        ['Hub reached at', p.endpoint || '—'], ['Traffic', '↓ ' + fmt.bytes(p.rx_bytes) + '  ↑ ' + fmt.bytes(p.tx_bytes)],
        ['Service', t.service || '—']]));
  }

  function boardCard(si) {
    const cpu = si.temps_c['cpu-thermal'] ?? Object.values(si.temps_c)[0];
    const used = si.mem_total ? Math.round((si.mem_total - si.mem_available) / si.mem_total * 100) : null;
    return card('Board', null,
      h('div', { class: 'host-tiles' },
        tile('CPU temperature', cpu != null ? cpu.toFixed(1) + ' °C' : '—'), tile('load (1 min)', si.load[0].toFixed(2) + ' / ' + si.cpus + ' cores'),
        tile('memory used', used != null ? used + '% of ' + fmt.bytes(si.mem_total) : '—'),
        tile('disk free', si.disk_total ? fmt.bytes(si.disk_free) + ' of ' + fmt.bytes(si.disk_total) : '—'), tile('running', fmt.dur(si.uptime_s))),
      h('div', { style: { marginTop: '14px' } }, kv([['Name', si.hostname], ['System', si.os || '—'], ['Kernel', si.kernel || '—'],
        ['Temperatures', Object.entries(si.temps_c).map(([k, v]) => k.replace('-thermal', '') + ' ' + v.toFixed(1) + ' °C').join(', ') || '—']])));
  }

  function ifaceCard(list) {
    return h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'Network interfaces')),
      h('div', { class: 'table-wrap' }, h('table', { class: 't' },
        h('thead', null, h('tr', null, h('th', null, 'Interface'), h('th', null, 'State'), h('th', null, 'Addresses'), h('th', null, 'MAC'))),
        h('tbody', null, list.map((i) => h('tr', null, h('td', null, h('b', null, i.name), ' ', h('span', { class: 'muted small' }, ifaceLabel(i.name))),
          h('td', null, i.up && i.addrs.length ? badge('up', 'ok') : i.up ? badge('up, no address') : badge('down')),
          h('td', null, i.addrs.join(', ') || '—'), h('td', { class: 'mono small' }, i.mac || '—')))))));
  }

  function radioCard(r, ctx) {
    const rows = [['Radio 0', fmt.mhz(r.rf0_freq) + (r.rf0_tx_enable ? ', transmits' : '')], ['Radio 1', fmt.mhz(r.rf1_freq) + (r.rf1_tx_enable ? ', transmits' : '')],
      ['Default downlink', fmt.mhz(r.tx_default_freq) + ', ' + (r.tx_default_power ?? '—') + ' dBm' + (r.tx_default_sf ? ', SF' + r.tx_default_sf : '')],
      ['Sends to', (r.target_ip || '—') + ':' + (r.port_up || '—')], ctx.role === 'hub' ? null : ['Paired', r.paired ? 'yes' : 'not yet']];
    const where = ctx.role === 'hub' ? h('a', { href: '#/radio', class: 'small' }, 'Radio config') : h('span', { class: 'muted small' }, 'Change them on the hub: Gateways, Remote config');
    return card('Radio settings', where, kv(rows));
  }

  // ---------- network ----------
  async function network(view, ctx) {
    let pending = false, lastKey = '', dirty = false, lastAt = 0, scanAPs = null;
    const mark = () => { dirty = true; };
    listen(ctx, view, 'input', mark);
    listen(ctx, view, 'change', mark);
    const refresh = async (force) => {
      let n;
      lastAt = Date.now();
      try { n = await ctx.api('GET', '/network'); } catch (e) {
        if (e.status === 401) return;
        const box = view.querySelector('.host-change');
        if (pending && box) box.replaceWith(lostContact());
        else if (!box) put(view, noAnswer(e));
        return;
      }
      if (ctx.setDot) ctx.setDot(n.hub_reachable);
      const ch = n.change;
      const active = ch && !['kept', 'reverted', 'failed'].includes(ch.state);
      if (pending && !active && ch) { ctx.toast(ch.message || ch.state, ch.state !== 'kept'); force = true; }
      pending = active;
      // Signal strength and client counts change all the time; they do not redraw the forms.
      const key = JSON.stringify([n.ethernet, { ...n.wifi, signal: 0 }, { ...n.ap, clients: 0 }]);
      const box = view.querySelector('.host-change');
      if (!force && box && (key === lastKey || dirty)) { box.replaceWith(changeBox(ch)); return; }
      lastKey = key; dirty = false;
      put(view, changeBox(ch), ethernetCard(n, go, ctx), h('div', { class: 'grid g2 host-grid' }, wifiCard(n, go, ctx), apCard(n, go, ctx)));
    };
    const go = async (path, body, label) => {
      try {
        const c = await ctx.api('POST', path, body);
        if (c && c.state) { pending = true; ctx.toast((label || c.what) + ': applying…'); }
        else ctx.toast(label || 'Saved');
        setTimeout(() => refresh(true), 400);
      } catch (e) { if (e.status !== 401) ctx.toast(e.message, true); }
    };
    // wifiCard keeps the last scan here, so redraws do not lose it.
    go.scan = { get: () => scanAPs, set: (v) => { scanAPs = v; } };
    await refresh(true);
    // Every 2.5 s while a change is being checked, otherwise every 10 s.
    const id = setInterval(() => { if (!document.hidden && (pending || Date.now() - lastAt > 10000)) refresh(false); }, 2500);
    ctx.onLeave(() => clearInterval(id));
  }

  // ipForm edits the address settings of the wired connection or of the
  // connected Wi-Fi network (iface 'ethernet' or 'wifi').
  function ipForm(cfg, iface, go, ctx) {
    const method = h('select', null, h('option', { value: 'auto' }, 'Automatic (DHCP from the router)'), h('option', { value: 'manual' }, 'Fixed address'));
    method.value = cfg.method === 'manual' ? 'manual' : 'auto';
    const addr = h('input', { type: 'text', value: cfg.addresses[0] || (cfg.current[0] || ''), placeholder: '192.168.1.16/24' });
    const gw = h('input', { type: 'text', value: cfg.gateway || '', placeholder: '192.168.1.1' });
    const dns = h('input', { type: 'text', value: cfg.dns.join(' '), placeholder: 'from the router' });
    const keep = keepBox();
    const fixed = h('div', { class: 'grid g2 host-grid' }, field('Address and prefix', addr, 'For example 192.168.1.16/24'), field('Router (gateway)', gw));
    const sync = () => { fixed.style.display = method.value === 'manual' ? '' : 'none'; };
    method.addEventListener('change', sync); sync();
    return h('form', { class: 'form wide', onsubmit: (ev) => { ev.preventDefault(); go('/network/ethernet', { iface, method: method.value, address: addr.value, gateway: gw.value, dns: dns.value, keep: keep.checked }); } },
      ctx.role === 'hub' ? h('div', { class: 'callout' }, 'Satellites find the hub at its current address. If you change it, change it on each satellite too (their Tunnel page).') : null,
      field('Address', method), fixed, field('DNS servers', dns, 'Optional, separated by spaces. Empty uses the ones the router hands out.'),
      check('Keep even if the connection check fails', keep, keepHelp(ctx)),
      h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', type: 'submit' }, 'Apply'),
        h('span', { class: 'muted small', style: { alignSelf: 'center' } }, 'If the address changes, open the page at the new one.')));
  }

  function ethernetCard(n, go, ctx) {
    const e = n.ethernet;
    const state = !e.present ? badge('no wired device', 'err') : e.state === 'connected' || e.bridged ? badge(e.bridged ? 'bridged with the access point' : 'connected', 'ok') : badge(e.state || 'unknown', 'warn');
    return card('Wired network (' + e.device + ')', state,
      kv([['Address now', e.current.join(', ') || 'none'], ['Profile', (e.name || '—') + (e.bridged ? ' (the access point\'s bridge, br0)' : '')]]),
      h('div', { style: { marginTop: '12px' } }, ipForm(e, 'ethernet', go, ctx)));
  }

  function wifiCard(n, go, ctx) {
    const w = n.wifi, ap = n.ap;
    if (!w.present) return card('Wi-Fi', badge('no Wi-Fi device', 'err'), h('div', { class: 'muted' }, 'This board has no ' + w.device + '.'));
    const apBusy = ap.enabled && (!ap.only_wired || ap.cable);
    const list = h('div');
    const paint = () => {
      const aps = go.scan.get();
      if (!aps) return;
      put(list, aps.length ? h('div', { class: 'table-wrap' }, h('table', { class: 't' }, h('tbody', null, aps.map((a) => apRow(a, go, apBusy))))) : h('div', { class: 'muted small' }, 'No networks found.'));
    };
    const scan = async (btn) => {
      btn.disabled = true; btn.textContent = 'Scanning…';
      try { go.scan.set(await ctx.api('GET', '/wifi/scan')); paint(); } catch (e) { ctx.toast(e.message, true); }
      btn.disabled = apBusy || !w.radio; btn.textContent = 'Scan again';
    };
    const scanBtn = h('button', { class: 'btn sm', type: 'button', disabled: apBusy || !w.radio, onclick: (ev) => scan(ev.currentTarget) }, go.scan.get() ? 'Scan again' : 'Scan for networks');
    paint();
    const radioBtn = h('button', { class: 'btn sm' + (w.radio ? ' ghost' : ' primary'), type: 'button', onclick: () => {
      if (w.radio && !confirm('Turn the Wi-Fi radio off?' + (ctx.role === 'hub' && w.uuid ? ' The hub uses Wi-Fi now; without another connection the change is undone after 60 s.' : ''))) return;
      go('/wifi/radio', { on: !w.radio, keep: false }, 'Wi-Fi radio ' + (w.radio ? 'off' : 'on'));
    } }, w.radio ? 'Turn radio off' : 'Turn radio on');
    const state = !w.radio ? badge('radio off') : w.uuid ? badge('connected to ' + (w.ssid || w.name), 'ok') : ap.active ? badge('access point', 'info') : badge('not connected');
    return card('Wi-Fi client (' + w.device + ')', h('div', { class: 'row' }, state, radioBtn),
      apBusy ? h('div', { class: 'callout info', style: { marginBottom: '12px' } }, 'The Wi-Fi is the access point now' + (ap.only_wired ? ', because the cable is connected. Unplug the cable and it becomes a normal Wi-Fi client again.' : '.')) : null,
      w.uuid ? h('div', { class: 'row between', style: { marginBottom: '12px' } },
        h('div', null, h('b', null, w.ssid || w.name), ' ', w.signal ? bars(w.signal) : null, ' ', h('span', { class: 'muted small' }, w.current.join(', '))),
        h('button', { class: 'btn sm danger', type: 'button', onclick: () => go('/wifi/disconnect', { keep: false }, 'Wi-Fi disconnect') }, 'Disconnect')) : null,
      w.uuid ? h('details', { style: { marginBottom: '12px' } },
        h('summary', { class: 'small', style: { cursor: 'pointer' } }, 'Address settings of ' + (w.ssid || w.name) + ': ' + (w.method === 'manual' ? 'fixed ' + (w.addresses[0] || '') : 'DHCP') + (w.dns.length ? ', DNS ' + w.dns.join(' ') : '')),
        h('div', { style: { marginTop: '10px' } }, ipForm(w, 'wifi', go, ctx))) : null,
      w.saved.length ? h('div', { style: { marginBottom: '12px' } }, h('h3', null, 'Saved networks'),
        h('div', { class: 'table-wrap' }, h('table', { class: 't' }, h('tbody', null, w.saved.map((s) => h('tr', null,
          h('td', null, h('b', null, s.name), s.active ? [' ', badge('connected', 'ok')] : null),
          h('td', null, check('connect by itself', h('input', { type: 'checkbox', checked: s.autoconnect, onchange: (ev) => go('/wifi/autoconnect', { uuid: s.uuid, on: ev.target.checked }, ev.target.checked ? 'Will connect by itself' : 'Will not connect by itself') }))),
          h('td', { class: 'right' }, s.active ? null : h('button', { class: 'btn sm ghost', type: 'button', onclick: () => { if (confirm('Forget ' + s.name + '?')) go('/wifi/forget', { uuid: s.uuid }, 'Forgotten'); } }, 'Forget')))))))) : null,
      h('div', { class: 'row between' }, h('h3', { style: { margin: 0 } }, 'Networks nearby'), scanBtn), list);
  }

  function apRow(a, go, busy) {
    const pw = h('input', { type: 'password', placeholder: 'password', autocomplete: 'new-password' });
    const form = h('form', { class: 'input-group', style: { display: 'none', marginTop: '6px' },
      onsubmit: (ev) => { ev.preventDefault(); go('/wifi/connect', { ssid: a.ssid, password: pw.value, keep: false }, 'Wi-Fi ' + a.ssid); } },
    pw, h('button', { class: 'btn sm primary', type: 'submit' }, 'Connect'));
    const open = !a.security || a.security === '--';
    return h('tr', null,
      h('td', null, h('b', null, a.ssid), a.in_use ? [' ', badge('connected', 'ok')] : null, h('div', { class: 'muted small' }, (open ? 'open' : a.security) + (a.channel ? ', channel ' + a.channel : '')), form),
      h('td', { class: 'nowrap' }, bars(a.signal), ' ', h('span', { class: 'small muted' }, a.signal + '%')),
      h('td', { class: 'right' }, a.in_use ? null : h('button', { class: 'btn sm', type: 'button', disabled: busy, onclick: () => {
        if (open) { go('/wifi/connect', { ssid: a.ssid, password: '', keep: false }, 'Wi-Fi ' + a.ssid); return; }
        form.style.display = ''; pw.focus();
      } }, 'Connect')));
  }

  function apCard(n, go, ctx) {
    const ap = n.ap;
    if (!ap.supported) return card('Access point', badge('not supported'), h('div', { class: 'muted' }, 'This Wi-Fi chip cannot be an access point.'));
    const mode = h('select', null,
      h('option', { value: 'bridge' }, 'Bridged to the cable: same network as your router'),
      h('option', { value: 'hotspot', disabled: !ap.hotspot_ok }, 'Own network 10.42.0.x, shared through the cable' + (ap.hotspot_ok ? '' : ' (needs dnsmasq)')));
    mode.value = ap.mode || 'bridge';
    const ssid = h('input', { type: 'text', value: ap.ssid || ((ctx.hostname || 'bobcat') + '-wifi'), maxlength: 32 });
    const pw = h('input', { type: 'password', autocomplete: 'new-password', placeholder: ap.enabled ? 'unchanged' : '8 to 63 characters' });
    const ch = h('select', null, Array.from({ length: 13 }, (_, i) => h('option', { value: i + 1 }, 'Channel ' + (i + 1))));
    ch.value = String(ap.channel || 6);
    const only = h('input', { type: 'checkbox', checked: ap.enabled ? ap.only_wired : true });
    const keep = keepBox();
    const modeHelp = h('div', { class: 'help' });
    const syncHelp = () => {
      modeHelp.textContent = mode.value === 'hotspot'
        ? 'Phones get 10.42.0.x addresses from this board and reach the internet through its cable. Without the cable it is a private network: open this page at http://10.42.0.1 to set the board up in the field.'
        : 'Phones join your home network and get their address from your router. Needs the cable.';
    };
    mode.addEventListener('change', syncHelp); syncHelp();
    const state = !ap.enabled ? badge('off') : ap.active ? badge('on, ' + ap.clients + (ap.clients === 1 ? ' device' : ' devices'), 'ok') : badge('waiting for the cable', 'warn');
    const send = (enabled) => go('/ap', { enabled, mode: mode.value, ssid: ssid.value, password: pw.value, channel: Number(ch.value), only_wired: only.checked, keep: keep.checked });
    return card('Access point', state,
      ctx.role === 'hub' && !ap.cable && !ap.enabled ? h('div', { class: 'callout warn', style: { marginBottom: '12px' } },
        'The hub reaches your network over Wi-Fi now. While the Wi-Fi is an access point it cannot be a client too, so plug in the cable first; otherwise the change is undone after 60 s.') : null,
      h('form', { class: 'form wide', onsubmit: (ev) => { ev.preventDefault(); send(true); } },
        field('Mode', mode, modeHelp),
        h('div', { class: 'grid g2 host-grid' }, field('Network name', ssid), field('Password', pw, 'WPA2')),
        field('Channel', ch, '2.4 GHz'),
        check('Only while the cable is connected', only, 'Without the cable the Wi-Fi goes back to being a normal client.'),
        ap.enabled ? null : check('Keep even if the connection check fails', keep, keepHelp(ctx)),
        h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', type: 'submit' }, ap.enabled ? 'Save' : 'Turn on'),
          ap.enabled ? h('button', { class: 'btn danger', type: 'button', onclick: () => { if (confirm('Turn the access point off?')) send(false); } }, 'Turn off') : null)),
      ap.enabled ? h('div', { class: 'muted small', style: { marginTop: '10px' } }, (ap.address ? 'Address ' + ap.address + '. ' : '') + 'Cable ' + (ap.cable ? 'connected' : 'not connected') + '.') : null);
  }

  // ---------- tunnel (satellite) ----------
  function tunnel(view, ctx) {
    let statusCard = null, drawn = false;
    every(ctx, 5000, async () => {
      let st;
      try { st = await ctx.api('GET', '/status'); } catch (e) { return; }
      const t = st.tunnel, p = t.peers[0] || {};
      if (ctx.setDot) ctx.setDot(t.hub_reachable);
      const next = card('Tunnel', t.hub_reachable ? badge('hub answers' + (t.hub_ping_ms ? ', ' + t.hub_ping_ms.toFixed(1) + ' ms' : ''), 'ok') : badge('hub does not answer', 'err'),
        kv([['Interface', t.interface + (t.up ? '' : ' (down)')], ['This satellite', t.address || '—'], ['Hub', (t.hub_ip || '—') + (p.name ? ' (' + p.name + ')' : '')],
          ['Reached at', p.endpoint || '—'], ['Last handshake', p.last_handshake ? fmt.ago(p.last_handshake) : 'never'],
          ['Traffic', '↓ ' + fmt.bytes(p.rx_bytes) + '  ↑ ' + fmt.bytes(p.tx_bytes)],
          ['Public key', h('span', { class: 'val' }, t.public_key || '—')], ['Away from home', t.roaming ? 'tries ' + t.wan + ' when the home address does not answer' : 'not set']]));
      if (drawn) { statusCard.replaceWith(next); statusCard = next; return; }
      drawn = true; statusCard = next;
      const lan = h('input', { type: 'text', value: t.lan || p.endpoint || '', placeholder: '192.168.1.15:51820' });
      const wan = h('input', { type: 'text', value: t.wan || '', placeholder: 'host:51820 (optional)' });
      const keep = keepBox();
      const form = card('Where the hub is', null, h('form', { class: 'form wide', onsubmit: async (ev) => {
        ev.preventDefault();
        try { const c = await ctx.api('POST', '/tunnel', { lan: lan.value, wan: wan.value, keep: keep.checked }); ctx.toast(c.what + ': applying… the result shows on the Network tab'); } catch (e) { ctx.toast(e.message, true); }
      } },
      h('div', { class: 'grid g2 host-grid' }, field('Hub on the home network', lan, 'The hub\'s address and WireGuard port.'),
        field('Hub from the internet', wan, 'Your router\'s public address or name, with UDP 51820 forwarded to the hub. Used when the home address does not answer.')),
      check('Keep even if the hub does not answer', keep, keepHelp(ctx)),
      h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', type: 'submit' }, 'Save'))));
      put(view, statusCard, form);
    });
  }

  // ---------- general ----------
  async function general(view, ctx) {
    let g, zones;
    try { [g, zones] = await Promise.all([ctx.api('GET', '/general'), ctx.api('GET', '/general/timezones')]); } catch (e) { if (e.status !== 401) put(view, noAnswer(e)); return; }
    const name = h('input', { type: 'text', value: g.hostname, maxlength: 63 });
    const tz = h('select', null, zones.map((z) => h('option', { value: z }, z)));
    tz.value = g.timezone;
    const ntp = h('input', { type: 'checkbox', checked: g.ntp });
    const clock = h('span', null, g.local_time);
    const timer = setInterval(async () => { try { const n = await ctx.api('GET', '/general'); clock.textContent = n.local_time; } catch (e) { /* offline */ } }, 15000);
    ctx.onLeave(() => clearInterval(timer));
    put(view, card('Name and time', g.synced ? badge('clock in step', 'ok') : badge(g.ntp ? 'clock not in step yet' : 'clock set by hand', 'warn'),
      h('form', { class: 'form wide', onsubmit: async (ev) => {
        ev.preventDefault();
        try {
          const n = await ctx.api('POST', '/general', { hostname: name.value.trim(), timezone: tz.value, ntp: ntp.checked });
          clock.textContent = n.local_time; ctx.toast('Saved');
        } catch (e) { ctx.toast(e.message, true); }
      } },
      kv([['Time now', clock]]),
      h('div', { class: 'grid g2 host-grid' }, field('Host name', name, 'Letters, digits and dashes. Shown on your network and in the logs.'), field('Time zone', tz, 'Used for the logs and the lights\' night schedule.')),
      check('Set the time from the internet (NTP)', ntp),
      h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', type: 'submit' }, 'Save')))));
  }

  // ---------- SD card ----------
  function sd(view, ctx) {
    let path = '/', lastKey = '';
    const files = h('div');
    const load = async () => {
      try {
        const list = await ctx.api('GET', '/sd/files?path=' + encodeURIComponent(path));
        const parts = path.split('/').filter(Boolean);
        const crumbs = h('div', { class: 'row small', style: { marginBottom: '8px' } },
          h('a', { href: '#', onclick: (e) => { e.preventDefault(); path = '/'; load(); } }, 'card'),
          parts.map((p, i) => [h('span', { class: 'muted' }, '/'), h('a', { href: '#', onclick: (e) => { e.preventDefault(); path = '/' + parts.slice(0, i + 1).join('/'); load(); } }, p)]));
        put(files, crumbs, list.length ? h('div', { class: 'table-wrap' }, h('table', { class: 't' }, h('tbody', null, list.map((f) => h('tr', null,
          h('td', null, f.dir
            ? h('a', { href: '#', onclick: (e) => { e.preventDefault(); path = (path.replace(/\/$/, '') + '/' + f.name); load(); } }, '📁 ' + f.name + '/')
            : h('a', { href: ctx.url('/sd/download?path=' + encodeURIComponent(path.replace(/\/$/, '') + '/' + f.name)) }, f.name)),
          h('td', { class: 'right small muted' }, f.dir ? '' : fmt.bytes(f.size)),
          h('td', { class: 'small muted nowrap' }, fmt.date(f.modified))))))) : h('div', { class: 'muted small' }, 'Empty.'));
      } catch (e) { put(files, h('div', { class: 'muted small' }, e.message)); }
    };
    const act = async (p, body, done) => {
      try { await ctx.api('POST', p, body); ctx.toast(done); lastKey = ''; refresh(); } catch (e) { ctx.toast(e.message, true); }
    };
    const refresh = async () => {
      let c;
      try { c = await ctx.api('GET', '/sd'); } catch (e) { if (e.status !== 401) put(view, noAnswer(e)); return; }
      const key = JSON.stringify(c);
      if (key === lastKey) return;
      lastKey = key;
      if (!c.present && !c.mounted) {
        put(view, card('SD card', badge('no card'), h('div', { class: 'empty' }, 'No card in the reader. Insert one; this page notices it within a few seconds.')));
        return;
      }
      const head = h('div', { class: 'row' }, badge(c.mounted ? (c.read_only ? 'mounted read-only' : 'mounted for writing') : 'not mounted', c.mounted ? 'ok' : null));
      const partRows = c.parts.map((p) => h('tr', null,
        h('td', null, h('b', null, p.path), p.label ? ' ' + p.label : ''), h('td', null, fmt.bytes(p.size)), h('td', null, p.fstype || 'unknown'),
        h('td', { class: 'right' }, c.mounted ? (p.path === c.from ? badge('at ' + c.mount_point, 'ok') : null) : p.fstype ? h('div', { class: 'row', style: { justifyContent: 'flex-end' } },
          h('button', { class: 'btn sm', onclick: () => act('/sd/mount', { path: p.path, read_only: true }, 'Mounted read-only') }, 'Mount read-only'),
          h('button', { class: 'btn sm', onclick: () => act('/sd/mount', { path: p.path, read_only: false }, 'Mounted for writing') }, 'Mount for writing')) : null)));
      put(view, 
        card('SD card', head,
          kv([['Card', c.device ? c.device + ', ' + fmt.bytes(c.size) : 'removed while mounted'], c.mounted ? ['Space', fmt.bytes(c.free) + ' free of ' + fmt.bytes(c.total)] : null]),
          h('div', { class: 'table-wrap', style: { marginTop: '10px' } }, h('table', { class: 't' }, h('tbody', null, partRows))),
          h('div', { class: 'form-actions', style: { marginTop: '12px' } },
            c.mounted ? h('button', { class: 'btn', onclick: () => act('/sd/unmount', {}, 'Unmounted; the card can be removed') }, 'Unmount') : null,
            c.mounted && c.backup ? h('button', { class: 'btn primary', disabled: c.read_only, title: c.read_only ? 'mount the card for writing first' : null, onclick: async (ev) => {
              ev.currentTarget.disabled = true;
              try { const r = await ctx.api('POST', '/sd/backup', {}); ctx.toast('Backup written: ' + r.file + ' (' + fmt.bytes(r.size) + ')'); lastKey = ''; refresh(); } catch (e) { ctx.toast(e.message, true); }
            } }, 'Back up LoRaHub to the card') : null)),
        c.mounted ? card('Files', null, files) : null);
      if (c.mounted) load();
    };
    every(ctx, 5000, refresh);
  }

  // ---------- lights ----------
  const SWATCH = { green: '#22c55e', red: '#ef4444', blue: '#3b82f6', yellow: '#eab308', cyan: '#06b6d4', magenta: '#d946ef', white: '#f8fafc', off: 'transparent' };
  const swatch = (c) => h('span', { class: 'host-swatch', title: c, style: { background: SWATCH[c] || 'transparent' } });

  async function lights(view, ctx) {
    let l;
    try { l = await ctx.api('GET', '/lights'); } catch (e) { if (e.status !== 401) put(view, noAnswer(e)); return; }
    const describe = {
      normal: 'Heartbeat in the idle colour, a flash for each packet received and sent, and the error signals.',
      steady: 'The idle colour lit all the time, a flash for each packet, and the error signals.',
      quiet: 'Heartbeat in the idle colour and the error signals; no flashes for packets.',
      solid: 'The idle colour lit all the time and the error signals; no flashes for packets.',
      errors: 'Dark while all is well. Fast red: radio error. Yellow blinking: no network.',
      off: 'Always dark.',
    };
    const flashes = (m) => m === 'normal' || m === 'steady';
    const lit = (m) => m !== 'errors' && m !== 'off';
    const modeSel = (val) => { const sel = h('select', null, l.modes.map((m) => h('option', { value: m }, m))); sel.value = val; return sel; };
    const day = modeSel(l.mode);
    const dayHelp = h('div', { class: 'help' });

    const colorRow = (label, key, help) => {
      const sel = h('select', { disabled: !l.colors_supported }, l.color_list.map((c) => h('option', { value: c }, c)));
      sel.value = l.colors[key];
      const sw = h('span', null, swatch(sel.value));
      sel.addEventListener('change', () => put(sw, swatch(sel.value)));
      const tryBtn = h('button', { class: 'btn sm', type: 'button', disabled: !l.colors_supported, onclick: async () => {
        try { await ctx.api('POST', '/lights/preview', { color: sel.value, seconds: 5 }); ctx.toast('Showing ' + sel.value + ' for 5 s'); } catch (e) { ctx.toast(e.message, true); }
      } }, 'Try');
      const row = h('div', { class: 'field' }, h('label', null, label), h('div', { class: 'row' }, sw, h('div', { style: { flex: '1', minWidth: '120px' } }, sel), tryBtn), help ? h('div', { class: 'help' }, help) : null);
      return { row, sel };
    };
    const idle = colorRow('Idle light', 'idle', 'The heartbeat, or the light that stays on in steady and solid.');
    const rx = colorRow('Packet received', 'rx', 'A short flash for each packet the radio hears.');
    const tx = colorRow('Packet sent', 'tx', 'A short flash for each packet the radio sends.');
    const sync = () => {
      dayHelp.textContent = describe[day.value];
      idle.row.style.opacity = lit(day.value) ? '' : '.5';
      rx.row.style.opacity = tx.row.style.opacity = flashes(day.value) ? '' : '.5';
    };
    day.addEventListener('change', sync); sync();

    const nightOn = h('input', { type: 'checkbox', checked: l.night.enabled });
    const night = modeSel(l.night.mode || 'off');
    const from = h('input', { type: 'time', value: l.night.from || '22:00' });
    const to = h('input', { type: 'time', value: l.night.to || '07:00' });
    const now = h('span', { class: 'row', style: { gap: '6px' } });
    const paintNow = (x) => {
      const c = x.engine_colors;
      put(now, badge(x.engine_mode || x.effective, 'info'),
        c && lit(x.engine_mode || x.effective) ? [swatch(c.idle), flashes(x.engine_mode || x.effective) ? [swatch(c.rx), swatch(c.tx)] : null] : null,
        x.engine_mode && x.engine_mode !== x.effective && !x.preview_until ? h('span', { class: 'muted small' }, '(saved: ' + x.effective + ')') : null);
    };
    paintNow(l);

    put(view,
      !l.supported ? h('div', { class: 'callout warn' }, 'The radio engine on this board does not report light modes. Engine 2.1 or later is needed.')
        : !l.colors_supported ? h('div', { class: 'callout warn' }, 'The radio engine on this board shows the modes but not the colours. Engine 2.2 or later is needed for colours, steady and solid.') : null,
      card('Status light', h('div', { class: 'row small' }, 'now ', now),
        h('form', { class: 'form wide', onsubmit: async (ev) => {
          ev.preventDefault();
          try {
            const r = await ctx.api('POST', '/lights', { mode: day.value, colors: { idle: idle.sel.value, rx: rx.sel.value, tx: tx.sel.value },
              night: { enabled: nightOn.checked, mode: night.value, from: from.value, to: to.value } });
            paintNow(r); ctx.toast('Lights: ' + r.effective);
          } catch (e) { ctx.toast(e.message, true); }
        } },
        field('Mode', day, dayHelp),
        h('h3', null, 'Colours'),
        h('div', { class: 'grid g3 host-grid' }, idle.row, rx.row, tx.row),
        h('div', { class: 'help muted small' }, 'Try lights the colour for 5 seconds. The error signals keep their own colours, so pick other ones for the idle light if you want to tell them apart.'),
        h('h3', null, 'Night'),
        check('Use another mode at night', nightOn, 'The colours stay the same.'),
        h('div', { class: 'grid g3 host-grid' }, field('Night mode', night), field('From', from), field('Until', to)),
        h('div', { class: 'help muted small' }, 'Times are in the board\'s time zone (General tab).'),
        h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', type: 'submit' }, 'Save')))));
  }

  // ---------- system and logs ----------
  function system(view, ctx) {
    const units = ctx.role === 'hub'
      ? [['hub', 'LoRaHub'], ['radio', 'Radio engine'], ['tunnel', 'Tunnel'], ['network', 'Network']]
      : [['radio', 'Radio engine'], ['tunnel', 'Tunnel'], ['network', 'Network'], ['page', 'This page']];
    const unit = h('select', null, units.map(([v, l]) => h('option', { value: v }, l)));
    const box = h('pre', { class: 'log-box' }, 'Loading…');
    const load = async () => {
      try { const r = await ctx.api('GET', '/logs?unit=' + unit.value + '&lines=300'); box.textContent = r.text || '(empty)'; box.scrollTop = box.scrollHeight; } catch (e) { box.textContent = e.message; }
    };
    unit.addEventListener('change', load);
    const act = (path, ask, done) => async () => {
      if (!confirm(ask)) return;
      try { await ctx.api('POST', path, {}); ctx.toast(done); } catch (e) { ctx.toast(e.message, true); }
    };
    const who = ctx.role === 'hub' ? 'the hub' : 'this satellite';
    put(view, 
      card('Actions', null, h('div', { class: 'row' },
        h('button', { class: 'btn', onclick: act('/radio/restart', 'Restart the radio engine? Packets during the next few seconds are missed.', 'Radio engine restarted') }, 'Restart the radio engine'),
        h('button', { class: 'btn danger', onclick: act('/reboot', 'Reboot ' + who + '? It is back in about a minute.', 'Rebooting… reload the page in a minute') }, 'Reboot'),
        h('button', { class: 'btn danger', onclick: act('/poweroff', 'Power ' + who + ' off? It stays off until its power is unplugged and plugged in again.', 'Powering off') }, 'Power off'))),
      card('Logs', h('div', { class: 'row' }, unit, h('button', { class: 'btn sm', onclick: load }, 'Refresh')), box));
    load();
    const id = setInterval(() => { if (!document.hidden && box.scrollTop + box.clientHeight >= box.scrollHeight - 20) load(); }, 10000);
    ctx.onLeave(() => clearInterval(id));
  }

  // Help on a satellite: the device topics of help.js.
  function help(view, ctx) {
    if (!window.LHHelp) { put(view, h('div', { class: 'muted' }, 'Help is not loaded.')); return; }
    LHHelp.mount(view, { set: 'device', onLeave: ctx.onLeave, base: '#/help/' });
  }

  const pages = { status, network, tunnel, general, sd, lights, system, help };
  return {
    pages,
    tabs(role) {
      return [['status', 'Status'], ['network', 'Network'], role === 'hub' ? null : ['tunnel', 'Tunnel'], ['general', 'General'],
        ['sd', 'SD card'], ['lights', 'Lights'], ['system', 'System and logs'], role === 'hub' ? null : ['help', 'Help']].filter(Boolean);
    },
    render(page, view, ctx) {
      put(view, h('div', { class: 'muted' }, 'Loading…'));
      (pages[page] || pages.status)(view, ctx);
    },
  };
})();
