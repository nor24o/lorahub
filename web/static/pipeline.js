/* LoRaHub console: live picture of where every packet goes. */
'use strict';

const PIPE_MAX_NODES = 8;
const PIPE_MAX_DECODERS = 6;
const PIPE_FLY_MS = 420;

function pipeDecoderKey(m) {
  const d = m.decoder;
  if (d && d.scheme_id) return 'dec:' + d.scheme_id;
  if (d && d.source) return 'dec:custom';
  return m.decoded && Object.keys(m.decoded).length ? 'dec:custom' : 'dec:none';
}

function flatValues(obj) {
  const out = [];
  Object.entries(obj || {}).forEach(([k, v]) => {
    if (v && typeof v === 'object' && !Array.isArray(v)) Object.entries(v).forEach(([k2, v2]) => { if (typeof v2 !== 'object') out.push([k + ' ' + k2, v2]); });
    else if (!Array.isArray(v)) out.push([k, v]);
  });
  return out;
}

function valueTiles(obj, limit) {
  const items = flatValues(obj).filter(([k]) => !/^ch\d+_/.test(k));
  if (!items.length) return h('p', { class: 'muted small', style: { margin: 0 } }, 'No values.');
  return h('div', { class: 'value-tiles' }, items.slice(0, limit || 40).map(([k, v]) => h('div', { class: 'value-tile' }, h('b', null, typeof v === 'number' ? Number(v.toFixed(3)) : String(v)), h('span', null, k.replace(/_/g, ' ')))));
}

App.route('/pipeline', async () => {
  const [gws, doc, devs, groups, settings, rules, recent] = await Promise.all([GET('/gateways'), GET('/schemes'), GET('/devices'), GET('/applications'), GET('/settings'), GET('/rules'), GET('/events?names=as.up,ns.up.unmatched&limit=200')]);
  const devMap = {}; devs.forEach((d) => { devMap[d.application_id + '/' + d.device_id] = d; });
  const groupMap = {}; groups.forEach((a) => { groupMap[a.application_id] = a; });
  const schemeMap = {}; (doc.schemes || []).forEach((s) => { schemeMap[s.id] = s; });
  const gwName = (id) => { const g = gws.find((x) => x.gateway_id === id); return (g && g.name) || id; };
  const reduced = matchMedia('(prefers-reduced-motion: reduce)').matches;
  const packets = [];
  const counts = {}, edgeCounts = {};
  let selected = null, pinned = null;

  const pub = settings.mqtt_publish, ha = settings.home_assistant;
  const haOn = (d) => ha.enabled && d && (ha.expose_mode === 'all' || (d.adopted && d.ha_enabled));
  const hooksOf = (app) => ((groupMap[app] || {}).webhooks || []).filter((w) => w.enabled && (w.events || []).includes('up'));
  const rulesOf = (app, dev) => rules.filter((r) => r.enabled && r.source_group === app && r.source_node === dev);

  function routeOf(e) {
    const m = e.data || {};
    const gatewayIds = (m.gateways || []).map((g) => 'gw:' + g.gateway_id);
    if (e.name !== 'as.up') return { gws: gatewayIds, dec: 'dec:unknown', node: null, outs: pub.unmatched ? ['out:mqtt', 'out:history'] : ['out:history'] };
    const d = devMap[e.application_id + '/' + e.device_id];
    const outs = ['out:history'];
    if (pub.up || pub.state) outs.push('out:mqtt');
    if (haOn(d)) outs.push('out:ha');
    if (hooksOf(e.application_id).length) outs.push('out:hooks');
    if (rulesOf(e.application_id, e.device_id).length) outs.push('out:rules');
    return { gws: gatewayIds, dec: pipeDecoderKey(m), node: 'node:' + e.application_id + '/' + e.device_id, outs };
  }
  const edgesOf = (rt) => {
    const out = rt.gws.map((g) => [g, 'merge']);
    out.push(['merge', rt.dec]);
    if (rt.node) { out.push([rt.dec, rt.node]); rt.outs.forEach((o) => out.push([rt.node, o])); } else rt.outs.forEach((o) => out.push([rt.dec, o]));
    return out;
  };
  const passes = (e, key) => { const rt = routeOf(e); return !key || key === 'merge' || rt.gws.includes(key) || rt.dec === key || rt.node === key || rt.outs.includes(key); };
  function tally(e) {
    const rt = routeOf(e);
    edgesOf(rt).forEach(([a, b]) => { edgeCounts[a + '>' + b] = (edgeCounts[a + '>' + b] || 0) + 1; });
    [...rt.gws, 'merge', rt.dec, rt.node, ...rt.outs].filter(Boolean).forEach((k) => { counts[k] = (counts[k] || 0) + 1; });
  }
  recent.slice().reverse().forEach((e) => { packets.unshift(e); tally(e); });

  // ---------- drawing ----------
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('class', 'pipe-svg');
  const lanes = h('div', { class: 'pipe-lanes' });
  const wrap = h('div', { class: 'pipe-wrap' }, svg, lanes);
  const boxes = {};
  let paths = {};

  function box(key, tone, title, sub, opts = {}) {
    const n = counts[key] || 0;
    const el = h('div', { class: `pipe-box ${tone}${opts.off ? ' off' : ''}${selected === key ? ' sel' : ''}`, title: 'Click to follow what passes through here', onclick: () => { selected = selected === key ? null : key; pinned = null; render(); } },
      h('div', { class: 'pipe-box-top' }, h('b', null, title), n ? h('span', { class: 'pipe-count' }, n) : null),
      sub ? h('div', { class: 'pipe-sub' }, sub) : null,
      opts.href ? h('a', { class: 'pipe-open', href: opts.href, title: 'Open', onclick: (e) => e.stopPropagation() }, '↗') : null);
    boxes[key] = el;
    return el;
  }
  const lane = (num, title, kids) => h('div', { class: 'pipe-lane' }, h('div', { class: 'pipe-lane-head' }, h('i', null, num), title), h('div', { class: 'pipe-lane-body' }, kids));

  function activeNodes() {
    const seen = new Set(), list = [];
    packets.forEach((e) => { if (e.name === 'as.up') { const k = e.application_id + '/' + e.device_id; if (!seen.has(k)) { seen.add(k); list.push(k); } } });
    devs.forEach((d) => { const k = d.application_id + '/' + d.device_id; if (!seen.has(k) && d.adopted) { seen.add(k); list.push(k); } });
    return list;
  }

  function buildBoxes() {
    Object.keys(boxes).forEach((k) => delete boxes[k]);
    const gwBoxes = gws.map((g) => {
      const st = g.last_status || {};
      return box('gw:' + g.gateway_id, 'blue', g.name || g.gateway_id, [h('span', { class: 'dot ' + (g.online ? 'on' : 'off') }), ' ', st.link >= 2 ? (st.auth ? 'Reliable, signed' : 'Reliable, pairing') : (g.online ? 'Connected' : 'Offline')], { href: '#/gateways/' + g.gateway_id });
    });
    const enabled = (doc.schemes || []).filter((s) => s.enabled !== false);
    const ranked = enabled.slice().sort((a, b) => (counts['dec:' + b.id] || 0) - (counts['dec:' + a.id] || 0));
    const shown = ranked.slice(0, PIPE_MAX_DECODERS);
    const shownIds = new Set(shown.map((s) => s.id));
    const decBoxes = enabled.filter((s) => shownIds.has(s.id)).map((s) => box('dec:' + s.id, 'purple', s.name || s.id, s.type, { href: '#/decoders?scheme=' + encodeURIComponent(s.id) }));
    packets.forEach((e) => { // a scheme that decoded recently but is no longer listed
      const k = e.name === 'as.up' ? pipeDecoderKey(e.data || {}) : null;
      if (k && k.startsWith('dec:') && !boxes[k] && !['dec:custom', 'dec:none'].includes(k)) decBoxes.push(box(k, 'purple', (e.data.decoder || {}).name || k.slice(4), 'recent'));
    });
    decBoxes.push(box('dec:custom', 'purple', 'Group or node decoder', 'set on the group or node'));
    decBoxes.push(box('dec:none', 'grey', 'Not decoded', 'raw bytes only'));
    if (counts['dec:unknown']) decBoxes.push(box('dec:unknown', 'grey', 'Unknown sender', 'no group for this NODE_ID'));

    const keys = activeNodes();
    const nodeBoxes = keys.slice(0, PIPE_MAX_NODES).map((k) => {
      const d = devMap[k];
      const [app, dev] = k.split('/');
      const a = groupMap[app];
      return box('node:' + k, 'green', (d && d.name) || dev, (a && a.node_id) || app, { href: `#/applications/${app}/devices/${dev}` });
    });
    if (keys.length > PIPE_MAX_NODES) nodeBoxes.push(h('a', { class: 'pipe-more', href: '#/nodes' }, `+${keys.length - PIPE_MAX_NODES} more nodes`));
    if (!keys.length) nodeBoxes.push(h('div', { class: 'pipe-more' }, 'No nodes yet'));

    const anyHooks = groups.some((a) => hooksOf(a.application_id).length);
    const outBoxes = [
      box('out:mqtt', 'amber', 'MQTT', pub.up || pub.state ? settings.mqtt_topic_prefix + '/…' : 'off', { off: !(pub.up || pub.state), href: '#/integrations/topics' }),
      box('out:ha', 'amber', 'Home Assistant', ha.enabled ? 'exposed nodes' : 'off', { off: !ha.enabled, href: '#/integrations/exposed' }),
      box('out:hooks', 'amber', 'Webhooks', anyHooks ? 'per group' : 'none set', { off: !anyHooks }),
      box('out:rules', 'amber', 'Automation', rules.filter((r) => r.enabled).length + ' rule(s)', { off: !rules.some((r) => r.enabled), href: '#/rules' }),
      box('out:history', 'amber', 'History', settings.event_retention_hours + ' h kept'),
    ];
    lanes.replaceChildren(
      lane(1, 'Heard by', gwBoxes.length ? gwBoxes : h('div', { class: 'pipe-more' }, 'No gateways yet')),
      lane(2, 'Merged', box('merge', 'grey', 'One packet', `same packet within ${settings.dedup_window_ms} ms`)),
      lane(3, 'Decoded by', decBoxes),
      lane(4, 'Node', nodeBoxes),
      lane(5, 'Sent to', outBoxes));
  }

  function edgeList() {
    const seen = new Set(), list = [];
    const add = (a, b) => { const k = a + '>' + b; if (boxes[a] && boxes[b] && !seen.has(k)) { seen.add(k); list.push([a, b]); } };
    Object.keys(boxes).forEach((k) => { if (k.startsWith('gw:')) add(k, 'merge'); });
    packets.forEach((e) => edgesOf(routeOf(e)).forEach(([a, b]) => add(a, b)));
    return list;
  }

  function activeEdges() {
    if (pinned) return new Set(edgesOf(routeOf(pinned)).map(([a, b]) => a + '>' + b));
    if (selected) { const on = new Set(); packets.filter((e) => passes(e, selected)).forEach((e) => edgesOf(routeOf(e)).forEach(([a, b]) => on.add(a + '>' + b))); return on; }
    return null;
  }

  function drawEdges() {
    const r = wrap.getBoundingClientRect();
    if (!r.width) return;
    svg.setAttribute('viewBox', `0 0 ${r.width} ${r.height}`);
    svg.setAttribute('width', r.width);
    svg.setAttribute('height', r.height);
    const point = (key, right) => { const b = boxes[key].getBoundingClientRect(); return [(right ? b.right : b.left) - r.left, b.top - r.top + b.height / 2]; };
    const on = activeEdges();
    paths = {};
    svg.classList.toggle('dim', Boolean(on));
    svg.replaceChildren(...edgeList().map(([a, b]) => {
      const [x1, y1] = point(a, true), [x2, y2] = point(b, false), mid = (x1 + x2) / 2;
      const p = document.createElementNS('http://www.w3.org/2000/svg', 'path');
      p.setAttribute('d', `M${x1},${y1} C${mid},${y1} ${mid},${y2} ${x2},${y2}`);
      const n = edgeCounts[a + '>' + b] || 0;
      p.setAttribute('stroke-width', on && on.has(a + '>' + b) ? 2.5 : Math.min(1 + Math.log2(n + 1) * 0.6, 4));
      p.setAttribute('class', 'pipe-edge' + (on && on.has(a + '>' + b) ? ' on' : ''));
      paths[a + '>' + b] = p;
      return p;
    }));
  }

  // ---------- animation ----------
  function pulse(key) { const el = boxes[key]; if (!el) return; el.classList.remove('hit'); void el.offsetWidth; el.classList.add('hit'); }
  function flyAlong(p, cls) {
    return new Promise((done) => {
      if (!p || reduced || document.hidden) { done(); return; }
      const c = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
      c.setAttribute('r', 5); c.setAttribute('class', 'pipe-dot ' + (cls || ''));
      svg.append(c);
      const len = p.getTotalLength(), t0 = performance.now();
      const step = (now) => {
        if (!c.isConnected) { done(); return; }
        const k = Math.min(1, (now - t0) / PIPE_FLY_MS), pt = p.getPointAtLength(k * len);
        c.setAttribute('cx', pt.x); c.setAttribute('cy', pt.y);
        if (k < 1) requestAnimationFrame(step); else { c.remove(); done(); }
      };
      requestAnimationFrame(step);
    });
  }
  const fly = async (a, b, cls) => { await flyAlong(paths[a + '>' + b], cls); pulse(b); };
  async function replay(e) {
    const rt = routeOf(e);
    await Promise.all(rt.gws.map((g) => fly(g, 'merge')));
    await fly('merge', rt.dec);
    if (rt.node) { await fly(rt.dec, rt.node); await Promise.all(rt.outs.map((o) => fly(rt.node, o))); } else await Promise.all(rt.outs.map((o) => fly(rt.dec, o)));
  }
  function flyDownlink(e) {
    const a = boxes['node:' + e.application_id + '/' + e.device_id], g = boxes['gw:' + (e.gateway_ids || [])[0]];
    if (!a || !g) return;
    const r = wrap.getBoundingClientRect(), s = a.getBoundingClientRect(), t = g.getBoundingClientRect();
    const x1 = s.left - r.left, y1 = s.top - r.top + s.height / 2, x2 = t.right - r.left, y2 = t.top - r.top + t.height / 2;
    const p = document.createElementNS('http://www.w3.org/2000/svg', 'path');
    p.setAttribute('d', `M${x1},${y1} C${x1 - 80},${y1 + 60} ${x2 + 80},${y2 + 60} ${x2},${y2}`);
    p.setAttribute('class', 'pipe-edge down');
    svg.append(p);
    flyAlong(p, 'down').then(() => { p.remove(); pulse('gw:' + (e.gateway_ids || [])[0]); });
  }

  // ---------- packet list and journey ----------
  const listBox = h('div', { class: 'pipe-list' });
  const journey = h('div');
  const filterNote = h('div');

  function byteStrip(m) {
    const bytes = (m.payload_hex || '').match(/../g) || [];
    const pf = settings.packet_format;
    const dataAt = bytes.length - (m.data_hex || '').length / 2;
    const cls = (i) => (i >= dataAt && m.data_hex !== undefined ? 'r-data' : i >= pf.node_id_offset && i < pf.node_id_offset + pf.node_id_length ? 'r-node' : m.serial && i >= pf.serial_offset && i < pf.serial_offset + pf.serial_length ? 'r-serial' : '');
    return h('div', null, h('div', { class: 'pipe-bytes' }, bytes.map((b, i) => h('span', { class: 'lab-byte ' + cls(i), title: 'Byte ' + i }, b))),
      h('div', { class: 'chips', style: { marginTop: '6px' } }, h('span', { class: 'chip c-node' }, 'NODE_ID'), m.serial ? h('span', { class: 'chip c-serial' }, 'SERIAL') : null, h('span', { class: 'chip c-data' }, 'data')));
  }
  const step = (n, tone, title, ...body) => h('div', { class: 'journey-step ' + tone }, h('div', { class: 'journey-num' }, n), h('div', { class: 'journey-body' }, h('h3', null, title), ...body));

  function ruleLine(r, m) {
    const v = (m.decoded || {})[r.field];
    const ops = { '>': (a, b) => a > b, '<': (a, b) => a < b, '>=': (a, b) => a >= b, '<=': (a, b) => a <= b, '==': (a, b) => a === b, '!=': (a, b) => a !== b, always: () => true };
    const hit = r.operator === 'always' || (typeof v === 'number' && (ops[r.operator] || (() => false))(v, r.threshold));
    return h('div', { class: 'journey-out' }, h('span', { class: 'badge ' + (hit ? 'ok' : '') }, hit ? 'matches' : 'no match'), h('span', null, r.name || r.id), h('span', { class: 'small muted' }, r.operator === 'always' ? 'every packet' : `${r.field} ${r.operator} ${r.threshold} (now ${v === undefined ? '—' : v})`));
  }

  function renderJourney() {
    const e = pinned || packets.find((x) => passes(x, selected));
    if (!e) { journey.replaceChildren(h('div', { class: 'empty' }, 'Waiting for the first packet…')); return; }
    const m = e.data || {};
    const rt = routeOf(e);
    const gwsRows = (m.gateways || []).map((g) => h('tr', null, h('td', null, h('b', null, g.name || gwName(g.gateway_id))), h('td', { class: 'nowrap' }, g.rssi + ' dBm'), h('td', { class: 'nowrap' }, [signalBars(g.snr), ' ', g.snr + ' dB']), h('td', { class: 'small muted' }, 'CH' + g.channel)));
    const radio = m.radio || {};
    const decName = m.decoder && m.decoder.name;
    const dv = routeOf(e).node ? devMap[e.application_id + '/' + e.device_id] : null;
    const prefix = settings.mqtt_topic_prefix;
    const outLines = [];
    if (e.name === 'as.up') {
      if (pub.up) outLines.push(h('div', { class: 'journey-out' }, h('span', { class: 'badge ok' }, 'MQTT'), h('span', { class: 'mono small' }, `${prefix}/${e.application_id}/${e.device_id}/up`)));
      if (pub.state || ha.enabled) outLines.push(h('div', { class: 'journey-out' }, h('span', { class: 'badge ok' }, 'MQTT'), h('span', { class: 'mono small' }, `${prefix}/${e.application_id}/${e.device_id}/state`)));
      outLines.push(h('div', { class: 'journey-out' }, h('span', { class: 'badge ' + (haOn(dv) ? 'ok' : '') }, 'Home Assistant'), h('a', { href: '#/integrations/exposed' }, haOn(dv) ? 'exposed' : 'not exposed')));
      hooksOf(e.application_id).forEach((w) => outLines.push(h('div', { class: 'journey-out' }, h('span', { class: 'badge ok' }, 'Webhook'), h('span', { class: 'small' }, w.name || w.url))));
      rulesOf(e.application_id, e.device_id).forEach((r) => outLines.push(ruleLine(r, m)));
      outLines.push(h('div', { class: 'journey-out' }, h('span', { class: 'badge ok' }, 'History'), h('a', { href: `#/applications/${e.application_id}/devices/${e.device_id}/messages` }, 'stored')));
    } else outLines.push(h('div', { class: 'journey-out' }, h('span', { class: 'badge warn' }, 'Unknown'), h('span', { class: 'small muted' }, `${prefix}/unmatched`)));
    let json = null;
    const jsonBtn = h('button', { class: 'btn sm', onclick: () => { if (json) { json.remove(); json = null; return; } json = jsonView(m); jsonBtn.after(json); } }, 'Show full message');
    journey.replaceChildren(
      step(1, 'blue', `Heard by ${(m.gateways || []).length} gateway${(m.gateways || []).length === 1 ? '' : 's'}`, h('div', { class: 'table-wrap' }, h('table', { class: 't compact' }, h('tbody', null, gwsRows))),
        radio.frequency ? h('div', { class: 'small muted' }, `${(radio.frequency / 1e6).toFixed(3)} MHz · ${radio.data_rate}${radio.airtime_ms ? ' · ' + radio.airtime_ms + ' ms on air' : ''}`) : null),
      step(2, 'grey', 'Merged into one packet', byteStrip(m)),
      step(3, 'purple', rt.dec === 'dec:none' ? 'Not decoded' : rt.dec === 'dec:unknown' ? 'Unknown sender' : 'Decoded by ' + (decName || 'its group or node decoder'),
        m.decoded ? valueTiles(m.decoded) : null, ...(m.decoder_errors || []).map((x) => h('div', { class: 'callout warn small' }, x)), ...(m.decoder_warnings || []).map((x) => h('div', { class: 'small muted' }, x)),
        (m.decoder || {}).scheme_id ? h('a', { class: 'btn sm', href: '#/decoders?scheme=' + encodeURIComponent(m.decoder.scheme_id) }, 'Edit this decoder') : null),
      rt.node ? step(4, 'green', 'Node ' + ((dv && dv.name) || e.device_id), h('a', { href: `#/applications/${e.application_id}/devices/${e.device_id}` }, 'Open node'), h('span', { class: 'small muted' }, ` · group ${m.node_id || e.application_id}`)) : null,
      step(5, 'amber', 'Sent to', ...outLines, h('div', { style: { marginTop: '8px' } }, jsonBtn)));
  }

  function renderList() {
    const list = packets.filter((e) => passes(e, selected)).slice(0, 40);
    filterNote.replaceChildren(...(selected ? [h('div', { class: 'row', style: { padding: '8px 14px' } }, h('span', { class: 'small muted' }, 'Only packets passing through the selected box'), h('button', { class: 'btn sm', onclick: () => { selected = null; render(); } }, 'Show all'))] : []));
    listBox.replaceChildren(...(list.length ? list.map((e) => {
      const m = e.data || {}, d = devMap[e.application_id + '/' + e.device_id];
      return h('div', { class: 'pipe-row' + (pinned === e ? ' on' : ''), onclick: () => { pinned = pinned === e ? null : e; render(); if (pinned) replay(e); } },
        h('span', { class: 'small muted nowrap' }, fmtTime(e.time)), h('b', null, (d && d.name) || e.device_id || 'Unknown'), h('span', { class: 'small muted nowrap' }, (m.gateways || []).length + ' gw'),
        h('div', { class: 'pipe-row-vals' }, flatValues(m.decoded).filter(([k]) => !/^ch\d+_/.test(k)).slice(0, 3).map(([k, v]) => h('span', { class: 'chip' }, h('b', null, k + ' '), String(v)))));
    }) : [h('div', { class: 'empty' }, 'No packets yet.')]));
  }

  function render() { buildBoxes(); renderList(); renderJourney(); requestAnimationFrame(drawEdges); }
  const followBtn = h('button', { class: 'btn sm', onclick: () => { pinned = null; selected = null; render(); } }, 'Follow live');

  // ---------- live ----------
  const statusEl = h('span', { class: 'small muted' }, 'connecting…');
  let ws = null, closed = false;
  function onEvent(e) {
    if (e.name === 'gs.up') { (e.gateway_ids || []).forEach((g) => fly('gw:' + g, 'merge')); return; }
    if (e.name === 'ns.down.sent') { flyDownlink(e); return; }
    if (e.name !== 'as.up' && e.name !== 'ns.up.unmatched') return;
    packets.unshift(e);
    if (packets.length > 400) packets.pop();
    tally(e);
    const k = e.application_id + '/' + e.device_id;
    if (e.name === 'as.up' && !devMap[k]) devMap[k] = { application_id: e.application_id, device_id: e.device_id, name: (e.data || {}).name };
    const needsRebuild = (e.name === 'as.up' && !boxes['node:' + k]) || (routeOf(e).dec && !boxes[routeOf(e).dec]);
    if (needsRebuild) { render(); setTimeout(() => replayNoGateways(e), 60); return; }
    Object.keys(counts).forEach((key) => { const el = boxes[key] && boxes[key].querySelector('.pipe-count'); if (el) el.textContent = counts[key]; });
    renderList();
    if (!pinned) renderJourney();
    replayNoGateways(e);
  }
  async function replayNoGateways(e) {
    const rt = routeOf(e);
    await fly('merge', rt.dec);
    if (rt.node) { await fly(rt.dec, rt.node); await Promise.all(rt.outs.map((o) => fly(rt.node, o))); } else await Promise.all(rt.outs.map((o) => fly(rt.dec, o)));
  }
  function connect() {
    if (closed) return;
    ws = new WebSocket(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/live`);
    ws.onopen = () => { statusEl.textContent = 'live'; statusEl.className = 'small badge ok'; };
    ws.onmessage = (m) => { try { onEvent(JSON.parse(m.data)); } catch (err) { /* ignore */ } };
    ws.onclose = () => { statusEl.textContent = 'reconnecting…'; statusEl.className = 'small badge warn'; if (!closed) setTimeout(connect, 2500); };
  }
  connect();
  const ro = new ResizeObserver(() => drawEdges());
  ro.observe(wrap);
  App.onLeave(() => { closed = true; if (ws) ws.close(); ro.disconnect(); });

  render();
  return h('div', { class: 'stack' },
    pageHead([['Pipeline']], 'Pipeline', ['Where every packet goes, live. Click a box to follow it, or a packet to see its journey.'], [statusEl, followBtn]),
    h('div', { class: 'card' }, h('div', { class: 'pipe-scroll' }, wrap),
      h('div', { class: 'pipe-legend small muted' }, h('span', null, h('i', { class: 'lg up' }), 'packet moving up to the hub'), h('span', null, h('i', { class: 'lg down' }), 'downlink to a gateway'), h('span', null, 'Numbers cover the recent packets on this page.'))),
    h('div', { class: 'pipe-detail' },
      h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'Packets')), filterNote, listBox),
      h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, 'Packet journey')), h('div', { class: 'card-body' }, journey))));
});
