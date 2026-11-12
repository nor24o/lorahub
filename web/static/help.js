'use strict';
// Help: every LoRaHub function explained, each with an animated example.
// Shared by the hub console (#/help) and a satellite's own page (Help tab,
// device topics only). LHHelp.mount(view, { set, onLeave, base }).
window.LHHelp = (() => {
  const SVGNS = 'http://www.w3.org/2000/svg';
  const REDUCED = window.matchMedia && matchMedia('(prefers-reduced-motion: reduce)').matches;

  function el(tag, attrs, ...kids) {
    const e = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v == null || v === false) continue;
      if (k === 'class') e.className = v;
      else if (k === 'style') Object.assign(e.style, v);
      else if (k.startsWith('on')) e.addEventListener(k.slice(2), v);
      else if (k === 'value') e.value = v;
      else e.setAttribute(k, v === true ? '' : v);
    }
    for (const c of kids.flat(Infinity)) if (c != null && c !== false) e.append(c instanceof Node ? c : document.createTextNode(String(c)));
    return e;
  }
  function sv(tag, attrs, ...kids) {
    const e = document.createElementNS(SVGNS, tag);
    for (const [k, v] of Object.entries(attrs || {})) if (v != null) e.setAttribute(k, v);
    for (const c of kids) e.append(c instanceof Node ? c : document.createTextNode(String(c)));
    return e;
  }
  // Text with `code` and **bold**.
  function rich(s) {
    const out = [];
    s.split(/(`[^`]+`|\*\*[^*]+\*\*)/).forEach((p) => {
      if (p.startsWith('`')) out.push(el('code', null, p.slice(1, -1)));
      else if (p.startsWith('**')) out.push(el('b', null, p.slice(2, -2)));
      else if (p) out.push(p);
    });
    return out;
  }

  // ---------- player: steps with play, pause, back, next ----------
  function player(steps, show, opts) {
    const caption = el('div', { class: 'hs-caption' });
    const count = el('span', { class: 'hs-count' });
    const play = el('button', { class: 'btn sm', type: 'button', title: 'Play or pause' });
    let i = 0, playing = !REDUCED, timer = null, alive = true;
    const paint = () => { play.textContent = playing ? '❚❚ Pause' : '▶ Play'; };
    const go = (n, animate) => {
      clearTimeout(timer);
      i = (n + steps.length) % steps.length;
      caption.replaceChildren(...rich(steps[i].caption));
      count.textContent = (i + 1) + ' / ' + steps.length;
      show(i, animate && !REDUCED);
      if (playing && alive) timer = setTimeout(() => go(i + 1, true), (steps[i].dur || opts.dur || 2800) + (i === steps.length - 1 ? 2500 : 0));
    };
    play.addEventListener('click', () => { playing = !playing; paint(); go(i, playing); });
    const controls = el('div', { class: 'hs-controls' },
      el('button', { class: 'btn sm ghost', type: 'button', title: 'Start again', onclick: () => go(0, true) }, '⟲'),
      el('button', { class: 'btn sm ghost', type: 'button', title: 'Previous step', onclick: () => { playing = false; paint(); go(i - 1, true); } }, '◀'),
      play,
      el('button', { class: 'btn sm ghost', type: 'button', title: 'Next step', onclick: () => { playing = false; paint(); go(i + 1, true); } }, '▶'),
      count);
    paint();
    return {
      caption, controls,
      start: () => go(0, true),
      pause: () => { clearTimeout(timer); },
      resume: () => { if (playing) go(i, true); },
      stop: () => { alive = false; clearTimeout(timer); },
    };
  }

  function sceneBox(title, body, p, onStop) {
    const box = el('figure', { class: 'hs-scene' }, title ? el('figcaption', { class: 'hs-title' }, title) : null, body, p.caption, p.controls);
    // Only the scenes on screen run.
    let io = null;
    if ('IntersectionObserver' in window) {
      io = new IntersectionObserver((es) => es.forEach((e) => (e.isIntersecting ? p.resume() : p.pause())), { threshold: 0.2 });
      setTimeout(() => io.observe(box), 0);
    }
    box._stop = () => { p.stop(); if (io) io.disconnect(); if (onStop) onStop(); };
    return box;
  }

  // ---------- flow scene: packets travelling between boxes ----------
  function flow(def) {
    const W = def.w || 720, H = def.h || 230;
    const root = sv('svg', { viewBox: `0 0 ${W} ${H}`, class: 'hs-svg', role: 'img', 'aria-label': def.title || 'animation' });
    for (const [a, b] of def.links || []) {
      const A = def.actors[a], B = def.actors[b];
      root.append(sv('line', { x1: A.x, y1: A.y, x2: B.x, y2: B.y, class: 'hs-link' }));
    }
    const boxes = {};
    for (const [id, a] of Object.entries(def.actors)) {
      const w = a.w || 132, hh = a.sub ? 46 : 34;
      const g = sv('g', { class: 'hs-actor hs-k-' + (a.kind || 'box'), transform: `translate(${a.x},${a.y})` },
        sv('rect', { x: -w / 2, y: -hh / 2, width: w, height: hh, rx: 9 }),
        sv('text', { y: a.sub ? -3 : 5, class: 'hs-label' }, a.label));
      if (a.sub) g.append(sv('text', { y: 14, class: 'hs-sub' }, a.sub));
      boxes[id] = g;
      root.append(g);
    }
    const layer = sv('g');
    root.append(layer);
    let run = 0;
    const note = (n) => {
      const A = def.actors[n.at];
      const lines = String(n.text).split('\n');
      const w = Math.max(...lines.map((l) => l.length)) * 6.6 + 18, hh = lines.length * 15 + 10;
      const x = A.x + (n.dx || 0), y = A.y + (n.dy == null ? -48 : n.dy);
      const g = sv('g', { class: 'hs-note hs-n-' + (n.kind || 'info'), transform: `translate(${x},${y})` },
        sv('rect', { x: -w / 2, y: -hh / 2, width: w, height: hh, rx: 7 }));
      lines.forEach((l, j) => g.append(sv('text', { y: -hh / 2 + 17 + j * 15 }, l)));
      return g;
    };
    const packet = (p, to, animate, token) => {
      const A = def.actors[p.from], B = def.actors[to];
      const g = sv('g', { class: 'hs-packet hs-p-' + (p.kind || 'up') }, sv('circle', { r: 7 }));
      const lab = p.label ? sv('text', { y: -13, class: 'hs-plabel' }, p.label) : null;
      if (lab) g.append(lab);
      layer.append(g);
      const t0 = performance.now() + (p.delay || 0), D = p.dur || 1300;
      const place = (k) => {
        const e = k < 0.5 ? 2 * k * k : 1 - (-2 * k + 2) ** 2 / 2;
        g.setAttribute('transform', `translate(${A.x + (B.x - A.x) * e},${A.y + (B.y - A.y) * e})`);
        // The label fades in as the packet leaves and out as it arrives, so it never covers a box.
        if (lab) lab.style.opacity = Math.max(0, Math.min(1, k / 0.12, (1 - k) / 0.18));
      };
      const done = () => {
        if (p.kind === 'bad') g.append(sv('text', { class: 'hs-x', y: 5 }, '✕'));
        if (p.fade) g.style.opacity = 0.35;
      };
      // Without animation the packet waits halfway, label readable.
      if (!animate) { place(0.5); done(); return; }
      g.style.opacity = 0;
      const tick = (now) => {
        if (token !== run) return;
        if (now < t0) { requestAnimationFrame(tick); return; }
        g.style.opacity = 1;
        const k = Math.min(1, (now - t0) / D);
        place(k);
        if (k < 1) requestAnimationFrame(tick); else done();
      };
      requestAnimationFrame(tick);
    };
    const show = (n, animate) => {
      const token = ++run;
      const st = def.steps[n];
      layer.replaceChildren();
      Object.entries(boxes).forEach(([id, g]) => {
        g.classList.toggle('hs-flash', [].concat(st.flash || []).includes(id));
        g.classList.toggle('hs-dim', [].concat(st.dim || []).includes(id));
      });
      [].concat(st.packets || []).forEach((p) => [].concat(p.to).forEach((to) => packet(p, to, animate, token)));
      [].concat(st.notes || []).forEach((nt) => {
        const g = note(nt);
        if (animate && nt.delay) { g.style.opacity = 0; setTimeout(() => { if (token === run) g.style.opacity = 1; }, nt.delay); }
        layer.append(g);
      });
    };
    const p = player(def.steps, show, def);
    const box = sceneBox(def.title, el('div', { class: 'hs-stage' }, root), p);
    setTimeout(p.start, 0);
    return box;
  }

  // ---------- bytes scene: a packet read field by field ----------
  function bytes(def) {
    const hexs = def.hex.replace(/\s+/g, '').match(/../g);
    const row = el('div', { class: 'hs-bytes' });
    const cells = [];
    let at = 0;
    def.groups.forEach((g, gi) => {
      const grp = el('div', { class: 'hs-bgroup hs-g' + (gi % 6) });
      const inner = el('div', { class: 'hs-brow' });
      for (let k = 0; k < g.n; k++) { const c = el('span', { class: 'hs-byte' }, hexs[at++]); cells.push(c); inner.append(c); }
      grp.append(inner, el('div', { class: 'hs-blabel' }, g.label));
      row.append(grp);
      g.el = grp;
    });
    const bits = el('div', { class: 'hs-bits' });
    const show = (n) => {
      const st = def.steps[n];
      const on = st.on || def.groups.map((_, i) => i);
      def.groups.forEach((g, i) => g.el.classList.toggle('hs-off', !on.includes(i)));
      bits.replaceChildren();
      if (st.bits) {
        const v = parseInt(st.bits.hex, 16);
        const cellsB = [];
        for (let b = 7; b >= 0; b--) cellsB.push(el('span', { class: 'hs-bit' + ((v >> b) & 1 ? ' on' : '') }, el('small', null, b), (v >> b) & 1));
        const labels = st.bits.fields.map(([hi, lo, label]) => el('span', { class: 'hs-bitf', style: { gridColumn: `${8 - hi} / ${9 - lo}` } }, label));
        bits.append(el('div', { class: 'hs-bitsrow' }, cellsB), el('div', { class: 'hs-bitsrow hs-bitsf' }, labels),
          el('div', { class: 'hs-bitcap' }, 'byte ' + st.bits.hex));
      }
      if (st.result) bits.append(el('div', { class: 'hs-result' }, ...rich(st.result)));
    };
    const p = player(def.steps, show, def);
    const box = sceneBox(def.title, el('div', { class: 'hs-stage hs-bstage' }, row, bits), p);
    setTimeout(p.start, 0);
    return box;
  }

  // ---------- the status light, simulated like raw_lora_app 2.2 ----------
  const RGB = { off: '#1b1f24', green: '#22c55e', red: '#ef4444', blue: '#3b82f6', yellow: '#eab308', cyan: '#06b6d4', magenta: '#d946ef', white: '#f8fafc' };
  function lightDemo() {
    const led = el('span', { class: 'hs-led' });
    const sel = (opts, v) => { const s = el('select', null, opts.map((o) => el('option', { value: o }, o))); s.value = v; return s; };
    const colours = Object.keys(RGB).filter((c) => c !== 'off').concat('off');
    const mode = sel(['normal', 'steady', 'quiet', 'solid', 'errors', 'off'], 'normal');
    const idle = sel(colours, 'green'), rx = sel(colours, 'red'), tx = sel(colours, 'blue');
    let rxT = 0, txT = 0, err = false, net = false, tick = 0, alive = true, auto = 0;
    const btn = (label, f) => el('button', { class: 'btn sm', type: 'button', onclick: f }, label);
    const errBtn = btn('Radio error', () => { err = !err; errBtn.classList.toggle('primary', err); });
    const netBtn = btn('No network', () => { net = !net; netBtn.classList.toggle('primary', net); });
    const step = () => {
      if (!alive) return;
      const m = mode.value, flashes = m === 'normal' || m === 'steady';
      if (!flashes) { rxT = 0; txT = 0; }
      if (flashes && ++auto % 160 === 0) rxT = 120; // a packet now and then
      let c = 'off';
      if (m === 'off') c = 'off';
      else if (err) c = (tick % 200) < 100 ? 'red' : 'off';
      else if (net) c = (tick % 400) < 200 ? 'yellow' : 'off';
      else if (txT > 0) { c = tx.value; txT -= 20; }
      else if (rxT > 0) { c = rx.value; rxT -= 20; }
      else if (m === 'errors') c = 'off';
      else if (m === 'steady' || m === 'solid') c = idle.value;
      else { const ph = tick % 1200; c = (ph < 80 || (ph >= 220 && ph < 300)) ? idle.value : 'off'; }
      led.style.background = RGB[c];
      led.style.boxShadow = c === 'off' ? 'none' : `0 0 18px 4px ${RGB[c]}99`;
      tick = (tick + 20) % 1200;
    };
    const id = setInterval(step, 20);
    const body = el('div', { class: 'hs-stage hs-ledstage' },
      el('div', { class: 'hs-ledbox' }, led, el('div', { class: 'small muted' }, 'the light on the Bobcat')),
      el('div', { class: 'hs-ledctl' },
        el('label', null, 'Mode ', mode), el('label', null, 'Idle ', idle), el('label', null, 'Received ', rx), el('label', null, 'Sent ', tx),
        el('div', { class: 'row' }, btn('Packet received', () => { rxT = 120; }), btn('Packet sent', () => { txT = 180; }), errBtn, netBtn)));
    const cap = el('div', { class: 'hs-caption' }, 'Try the modes and colours: this is what the engine shows. Error signals keep their own colours.');
    const box = el('figure', { class: 'hs-scene' }, el('figcaption', { class: 'hs-title' }, 'Live example: the status light'), body, cap);
    box._stop = () => { alive = false; clearInterval(id); };
    return box;
  }

  function table(rows, head) {
    return el('div', { class: 'table-wrap' }, el('table', { class: 't compact hs-table' },
      head ? el('thead', null, el('tr', null, head.map((x) => el('th', null, x)))) : null,
      el('tbody', null, rows.map((r) => el('tr', null, r.map((c) => el('td', null, ...rich(String(c)))))))));
  }

  // ---------- actors used again and again ----------
  const A = {
    node: (x, y, sub) => ({ x, y, label: 'Node', sub: sub || 'CAFE 96B840EB', kind: 'node' }),
    gw: (x, y, label, sub) => ({ x, y, label, sub, kind: 'gw' }),
    hub: (x, y, sub) => ({ x, y, label: 'LoRaHub', sub: sub || 'Bobcat 192.168.1.15', kind: 'hub', w: 150 }),
    ha: (x, y) => ({ x, y, label: 'Home Assistant', sub: 'MQTT', kind: 'ha', w: 140 }),
  };

  // ---------- the topics ----------
  const T = [];
  const topic = (id, title, sets, where, text, scenes) => T.push({ id, title, sets, where, text, scenes });

  topic('overview', 'How it fits together', ['hub', 'device'], [['Overview', '#/'], ['Gateways', '#/gateways']], [
    'LoRaHub runs on the main Bobcat. It receives LoRa packets from its own radio and from **satellite gateways** (the Dragino LPS8 and other Bobcats), merges them, decodes the data and sends the result to MQTT and **Home Assistant**. Commands go the other way: from Home Assistant or the console, through the gateway that heard the node best, to the node.',
    'Satellites only run the radio engine (`raw_lora_app`) and a WireGuard tunnel to the hub. Each Bobcat also has its own **device settings** (network, Wi-Fi, lights, SD card): in the console under Device, on a satellite on its own page.',
  ], () => [flow({
    title: 'One packet, three gateways, one message', h: 250,
    actors: { node: A.node(70, 125), lps: A.gw(270, 45, 'Dragino LPS8', 'satellite'), sat: A.gw(270, 125, 'bobcat-sat3', 'satellite'), own: A.gw(270, 205, 'Hub radio', 'on the Bobcat'),
      hub: A.hub(480, 125), ha: A.ha(650, 125) },
    links: [['node', 'lps'], ['node', 'sat'], ['node', 'own'], ['lps', 'hub'], ['sat', 'hub'], ['own', 'hub'], ['hub', 'ha']],
    steps: [
      { caption: 'A node transmits once.', packets: { from: 'node', to: ['lps', 'sat', 'own'], label: 'CAFE 96B840EB 01670110' } },
      { caption: 'Every gateway that heard it passes it to the hub, with its own signal strength.', packets: [{ from: 'lps', to: 'hub' }, { from: 'sat', to: 'hub' }, { from: 'own', to: 'hub' }] },
      { caption: 'The hub merges the copies into **one** message and decodes it.', flash: 'hub', notes: { at: 'hub', text: 'temperature_1 = 27.2 °C\n3 gateways, best -45 dBm', dy: -62 } },
      { caption: 'The result goes to MQTT and Home Assistant.', packets: { from: 'hub', to: 'ha', label: 'lorahub/node-cafe/96b840eb/up' } },
      { caption: 'A command from Home Assistant goes back through the gateway that heard the node best.', packets: [{ from: 'ha', to: 'hub', kind: 'down', label: 'ON' }, { from: 'hub', to: 'sat', kind: 'down', delay: 1300 }, { from: 'sat', to: 'node', kind: 'down', delay: 2600, label: 'CAFE 96B840EB 0101' }], dur: 4400 },
    ] })]);

  topic('uplink', 'From a node to Home Assistant', ['hub'], [['Pipeline', '#/pipeline'], ['Live data', '#/live']], [
    'Each packet is checked against the packet format, matched to a **node group** by its NODE_ID and to a **node** by its SERIAL_ID, merged with the copies other gateways heard (within a short window), decoded, and published: the full message on `lorahub/{group}/{node}/up`, the latest values (retained) on `.../state`.',
    'The **Pipeline** page shows this live for every packet; **Live data** lists the events.',
  ], () => [flow({
    title: 'What happens to an uplink',
    actors: { node: A.node(70, 115), gw: A.gw(230, 115, 'Gateway', 'hears the packet'), hub: A.hub(400, 115, 'merge · decode'), mqtt: { x: 580, y: 60, label: 'MQTT', sub: '.../up and .../state', kind: 'ha' }, ha: A.ha(580, 175) },
    links: [['node', 'gw'], ['gw', 'hub'], ['hub', 'mqtt'], ['hub', 'ha']],
    steps: [
      { caption: 'The node sends `NODE_ID SERIAL_ID data`.', packets: { from: 'node', to: 'gw', label: 'CAFE 96B840EB 0167011002' } },
      { caption: 'The gateway adds RSSI, SNR, frequency and data rate, and passes it on.', packets: { from: 'gw', to: 'hub', label: '-60 dBm · SF7 · 868.1' } },
      { caption: '`CAFE` picks the group, `96B840EB` the node. The decoder reads `0167011002`.', flash: 'hub', notes: { at: 'hub', text: 'group CAFE · node 96B840EB\ntemperature_1 = 27.2', dy: -58 } },
      { caption: 'The full message, with every gateway, goes to `.../up`; the flat values to `.../state`.', packets: [{ from: 'hub', to: 'mqtt', label: 'up + state' }, { from: 'hub', to: 'ha', label: 'state', delay: 300 }] },
      { caption: 'An exposed node shows up in Home Assistant with one sensor per decoded field.', flash: 'ha', notes: { at: 'ha', text: 'Temperature 1: 27.2 °C', dy: 45 } },
    ] })]);

  topic('packet', 'The packet format', ['hub'], [['Settings, Packet format', '#/settings']], [
    'Nodes send `NODE_ID (2 bytes) | SERIAL_ID (4 bytes) | data`. NODE_ID is the **node group**, SERIAL_ID the **node** inside it. Offsets and lengths can be changed under Settings, Packet format.',
    'A NODE_ID never seen before creates a group, a new SERIAL_ID a node, unless that is turned off. Keep decoded field names stable: each one becomes a sensor in Home Assistant.',
  ], () => [bytes({
    title: 'Reading a packet',
    hex: 'CAFE 96B840EB 01 67 0110 02',
    groups: [{ n: 2, label: 'NODE_ID' }, { n: 4, label: 'SERIAL_ID' }, { n: 1, label: 'channel' }, { n: 1, label: 'type' }, { n: 2, label: 'value' }, { n: 1, label: 'next…' }],
    steps: [
      { caption: 'The whole packet as the gateway received it.' },
      { caption: '`CAFE`: the NODE_ID picks the node group.', on: [0], result: 'group **node-cafe**' },
      { caption: '`96B840EB`: the SERIAL_ID picks the node in that group.', on: [1], result: 'node **96b840eb**' },
      { caption: 'Everything after it is data for the decoder.', on: [2, 3, 4, 5], result: 'data `0167011002`' },
      { caption: 'With Cayenne LPP: channel 1, type 67 (temperature), value 0x0110 = 272 tenths.', on: [2, 3, 4], result: '`temperature_1` = **27.2 °C**' },
    ] })]);

  topic('nodes', 'Node groups, nodes and adoption', ['hub'], [['Nodes', '#/nodes'], ['Node groups', '#/applications']], [
    'New groups and nodes are created automatically and appear on the **Nodes** page. **Adopt** a node to say it is yours: you can then name it, set its class, and **expose** it to Home Assistant. Nodes you do not adopt are still decoded and published on MQTT, but stay out of Home Assistant.',
    'A group holds the decoder and downlink settings shared by its nodes (header, control byte, profile byte, radio); a node can override them on its own Downlink tab.',
  ], () => [flow({
    title: 'An unknown node turns up',
    actors: { node: { x: 80, y: 110, label: 'New node', sub: 'BEEF 11223344', kind: 'node' }, hub: A.hub(300, 110), list: { x: 520, y: 60, label: 'Nodes page', sub: 'waiting for adoption', kind: 'box', w: 160 }, ha: A.ha(520, 170) },
    links: [['node', 'hub'], ['hub', 'list'], ['hub', 'ha']],
    steps: [
      { caption: 'A node with an unknown NODE_ID `BEEF` transmits.', packets: { from: 'node', to: 'hub', label: 'BEEF 11223344 …' } },
      { caption: 'LoRaHub creates group `BEEF` and node `11223344`, decodes and publishes it on MQTT.', flash: 'hub', notes: { at: 'hub', text: 'group node-beef created', dy: -48 } },
      { caption: 'It waits on the Nodes page. Not adopted means not in Home Assistant.', packets: { from: 'hub', to: 'list' }, dim: 'ha' },
      { caption: 'Adopt it and expose it: now Home Assistant gets its sensors and controls.', flash: 'list', packets: { from: 'hub', to: 'ha', label: 'discovery' } },
    ] })]);

  topic('decoders', 'Decoders', ['hub'], [['Decoder Studio', '#/decoders'], ['Decoder lab', '#/lab']], [
    'A decoder turns data bytes into named values. Choose one per node or per group: **Cayenne LPP**, a **JavaScript** `decodeUplink(input)` (input.bytes is the data after the header), or **fields** (fixed positions, types, scale). Without one, the **decoder schemes** are tried in order and the first that fits wins.',
    'In Decoder Studio and Decoder lab, drag across the bytes of a real packet to make a field (the byte map), and see the result at once.',
  ], () => [bytes({
    title: 'A decoder with fixed fields',
    hex: '0E74 01F4 5A 01',
    groups: [{ n: 2, label: 'temperature' }, { n: 2, label: 'pressure' }, { n: 1, label: 'humidity' }, { n: 1, label: 'relay' }],
    steps: [
      { caption: 'The data after NODE_ID and SERIAL_ID.' },
      { caption: 'Bytes 0-1, signed, ÷ 100.', on: [0], result: '0x0E74 = 3700 → `temperature` = **37.00**' },
      { caption: 'Bytes 2-3, unsigned.', on: [1], result: '0x01F4 → `pressure` = **500**' },
      { caption: 'Byte 4, unsigned, in %.', on: [2], result: '0x5A → `humidity` = **90 %**' },
      { caption: 'Byte 5 as on/off.', on: [3], result: '`relay` = **true** (also the state of a "Relay" switch, if the node has one)' },
    ] })]);

  topic('profile', 'The profile byte', ['hub'], [['Node groups', '#/applications']], [
    'With the group option **Nodes send a profile after SERIAL_ID**, the firmware itself says how the node listens. The profile byte (and an optional 4-byte radio block) is removed before decoding. The firmware then wins over the group, but settings saved on the node in the console win over the firmware.',
  ], () => [bytes({
    title: 'Profile byte and radio block',
    hex: 'CAFE 96B840EB 90 84ADD2 90 0167011006',
    groups: [{ n: 2, label: 'NODE_ID' }, { n: 4, label: 'SERIAL_ID' }, { n: 1, label: 'profile' }, { n: 3, label: 'frequency' }, { n: 1, label: 'radio' }, { n: 5, label: 'data' }],
    steps: [
      { caption: 'A packet from a node with the profile byte.' },
      { caption: 'The profile byte `90`, bit by bit.', on: [2], bits: { hex: '90', fields: [[7, 6, 'class'], [5, 5, 'IQ'], [4, 4, 'radio'], [3, 3, 'desc'], [2, 0, 'free']] }, result: 'class **C** (10), normal IQ, radio block follows, not a description' },
      { caption: 'Frequency in 100 Hz: 0x84ADD2 = 8 695 250.', on: [3], result: 'downlinks on **869.525 MHz**' },
      { caption: 'Radio byte `90`: SF in the upper half, then bandwidth and coding rate.', on: [4], bits: { hex: '90', fields: [[7, 4, 'SF = 9'], [3, 2, 'BW 125'], [1, 0, 'CR 4/5']] }, result: '**SF9 BW125**, coding rate 4/5' },
      { caption: 'Only the data goes to the decoder.', on: [5], result: '`data_hex` = 0167011006' },
    ] })]);

  topic('downlink', 'Sending to a node: class A and class C', ['hub'], [['Node, Downlink tab', '#/nodes']], [
    '**Class A** nodes sleep and only listen right after they transmit, so a command waits in the queue until the node\'s next uplink and goes out in its receive window: the uplink\'s time plus the delay (1 s by default), through the gateway that heard it best. **Class C** nodes always listen, so the command goes out at once, through the gateway that heard the node best last time.',
    'Class B is handled like class A. A gateway whose engine has not reported for 45 s is avoided for class C.',
  ], () => [flow({
    title: 'Class A: the command waits for the node',
    actors: { ha: A.ha(80, 115), hub: A.hub(280, 115, 'queue'), gw: A.gw(470, 115, 'Gateway', 'best signal'), node: { x: 640, y: 115, label: 'Node', sub: 'class A, sleeping', kind: 'node' } },
    links: [['ha', 'hub'], ['hub', 'gw'], ['gw', 'node']],
    steps: [
      { caption: 'Home Assistant sends ON. The node is asleep.', packets: { from: 'ha', to: 'hub', kind: 'down', label: 'ON' }, dim: 'node' },
      { caption: 'The hub puts `0101` in the node\'s queue.', flash: 'hub', notes: { at: 'hub', text: 'queued: 0101', dy: -45 }, dim: 'node' },
      { caption: 'Later the node wakes up and transmits.', packets: [{ from: 'node', to: 'gw', label: 'uplink' }, { from: 'gw', to: 'hub', delay: 1300 }], dur: 3200 },
      { caption: '1 s after its uplink the node listens. The hub sends the command, timed for that window.', packets: [{ from: 'hub', to: 'gw', kind: 'down' }, { from: 'gw', to: 'node', kind: 'down', delay: 1300, label: 'CAFE 96B840EB 0101' }], dur: 3400 },
    ] }), flow({
    title: 'Class C: the command goes out at once',
    actors: { ha: A.ha(80, 115), hub: A.hub(280, 115), gw: A.gw(470, 115, 'Gateway', 'heard it best'), node: { x: 640, y: 115, label: 'Node', sub: 'class C, listening', kind: 'node' } },
    links: [['ha', 'hub'], ['hub', 'gw'], ['gw', 'node']],
    steps: [
      { caption: 'Home Assistant sends OFF.', packets: { from: 'ha', to: 'hub', kind: 'down', label: 'OFF' } },
      { caption: 'The hub sends `0100` straight away through the gateway that heard the node best.', packets: [{ from: 'hub', to: 'gw', kind: 'down' }, { from: 'gw', to: 'node', kind: 'down', delay: 1300, label: 'CAFE 96B840EB 0100' }], dur: 3200 },
    ] })]);

  topic('control', 'Header and control byte', ['hub'], [['Node groups', '#/applications']], [
    'With the group\'s **header** on, LoRaHub puts `NODE_ID SERIAL_ID` in front of every downlink, so a node can tell its own packets apart. With the **control byte** (which always brings the header) it also says who the packet is for, whether to confirm, and a sequence number, so a repeated packet can be ignored.',
  ], () => [bytes({
    title: 'A downlink with the control byte',
    hex: 'CAFE 96B840EB 85 0101',
    groups: [{ n: 2, label: 'NODE_ID' }, { n: 4, label: 'SERIAL_ID' }, { n: 1, label: 'control' }, { n: 2, label: 'data' }],
    steps: [
      { caption: 'What the node receives.' },
      { caption: 'The header: this node, in group CAFE.', on: [0, 1] },
      { caption: 'The control byte `85`.', on: [2], bits: { hex: '85', fields: [[7, 7, 'hub'], [6, 5, 'to'], [4, 4, 'ack'], [3, 0, 'sequence']] }, result: 'from the hub · for this node only (00) · no confirmation · sequence **5**' },
      { caption: 'To the whole group the serial is `FFFFFFFF` and bits 6-5 are 01 (`A0`); to everyone NODE_ID `FFFF` and 10 (`C0`).', on: [2], bits: { hex: 'A0', fields: [[7, 7, 'hub'], [6, 5, 'to'], [4, 4, 'ack'], [3, 0, 'sequence']] }, result: 'broadcast to the group, sequence 0' },
      { caption: 'Then the command itself.', on: [3], result: 'data `0101`' },
    ] })]);

  topic('broadcast', 'Broadcasts', ['hub'], [['Node group, Broadcast tab', '#/applications']], [
    'A broadcast goes to every node of a group (or everyone) at once: through each chosen gateway in turn, with one sequence number. Class C nodes hear it right away. **Class A** nodes get a copy in their queue, delivered in their next receive window until it expires (60 minutes by default).',
  ], () => [flow({
    title: 'One broadcast, class C now, class A later', h: 250,
    actors: { hub: A.hub(90, 125), gw1: A.gw(290, 70, 'Gateway 1'), gw2: A.gw(290, 180, 'Gateway 2'), c1: { x: 520, y: 45, label: 'Node', sub: 'class C', kind: 'node' }, c2: { x: 520, y: 125, label: 'Node', sub: 'class C', kind: 'node' }, a1: { x: 520, y: 205, label: 'Node', sub: 'class A, asleep', kind: 'node' } },
    links: [['hub', 'gw1'], ['hub', 'gw2'], ['gw1', 'c1'], ['gw1', 'c2'], ['gw2', 'c2'], ['gw2', 'a1']],
    steps: [
      { caption: 'The hub sends `CAFE FFFFFFFF A0 FF` through gateway 1…', packets: [{ from: 'hub', to: 'gw1', kind: 'down' }, { from: 'gw1', to: ['c1', 'c2'], kind: 'down', delay: 1300, label: 'A0 FF' }], dim: 'a1', dur: 3200 },
      { caption: '…then through gateway 2, with the same sequence number. The class A node sleeps through it.', packets: [{ from: 'hub', to: 'gw2', kind: 'down' }, { from: 'gw2', to: ['c2', 'a1'], kind: 'down', delay: 1300, label: 'A0 FF', fade: true }], dim: 'a1', dur: 3200 },
      { caption: 'The class A node wakes and transmits…', packets: [{ from: 'a1', to: 'gw2', label: 'uplink' }, { from: 'gw2', to: 'hub', delay: 1300 }], dur: 3200 },
      { caption: '…and gets its copy in the receive window.', packets: [{ from: 'hub', to: 'gw2', kind: 'down' }, { from: 'gw2', to: 'a1', kind: 'down', delay: 1300, label: 'A0 FF' }], flash: 'a1', dur: 3200 },
    ] })]);

  topic('echo', 'Own downlinks heard back', ['hub'], [['Live data', '#/live']], [
    'A downlink sent with normal IQ sounds like a packet to the other gateways, and to the sending gateway\'s other radio. LoRaHub remembers every transmission for 20 s and ignores the same bytes coming back, so they never turn into fake uplinks in Home Assistant. Nodes that listen with **inverted IQ** avoid it altogether.',
  ], () => [flow({
    title: 'A downlink heard by another gateway',
    actors: { hub: A.hub(100, 115), gw2: A.gw(330, 60, 'Gateway 2', 'transmits'), gw1: A.gw(330, 175, 'Gateway 1', 'also hears it'), node: A.node(580, 115) },
    links: [['hub', 'gw2'], ['hub', 'gw1'], ['gw2', 'node'], ['gw2', 'gw1']],
    steps: [
      { caption: 'The hub sends `0101` through gateway 2.', packets: [{ from: 'hub', to: 'gw2', kind: 'down' }, { from: 'gw2', to: 'node', kind: 'down', delay: 1300, label: 'CAFE 96B840EB 0101' }], dur: 3200 },
      { caption: 'Gateway 1 hears the same transmission and reports it as a packet.', packets: [{ from: 'gw2', to: 'gw1', kind: 'down', label: 'same bytes' }, { from: 'gw1', to: 'hub', kind: 'bad', delay: 1300 }], dur: 3200 },
      { caption: 'The hub recognises its own bytes from a moment ago and drops them: "own downlink heard back, ignored".', flash: 'hub', notes: { at: 'hub', text: 'ignored: own downlink', kind: 'bad', dy: -48 } },
    ] })]);

  topic('describe', 'Nodes that describe their commands', ['hub'], [['Node page', '#/nodes'], ['docs/node-protocol.md', null]], [
    'A node can announce which commands it accepts: after power-on, or when asked (the downlink `FF`: node page, **Ask the node to describe itself**, or `DESCRIBE` on `/set`). LoRaHub stores the list on the node and turns it into controls, in Home Assistant too: **button**, **switch**, **number** (slider), **choice** (list) and **raw** (hex box). A command you set up by hand with the same name wins.',
    'A description is an uplink with profile bit 3 set, then `D1`, a part byte and the commands. Long lists can be split into up to 15 parts. The exact layout and a C example are in `docs/node-protocol.md`.',
  ], () => [flow({
    title: 'Asking a node and getting controls', h: 240,
    actors: { you: { x: 80, y: 60, label: 'Console', sub: 'Ask the node…', kind: 'box' }, hub: A.hub(300, 120), node: { x: 560, y: 120, label: 'Node', sub: 'class C', kind: 'node' }, ha: A.ha(80, 185) },
    links: [['you', 'hub'], ['hub', 'node'], ['hub', 'ha']],
    steps: [
      { caption: 'You press **Ask the node to describe itself**.', packets: { from: 'you', to: 'hub', kind: 'ctl', label: 'describe' } },
      { caption: 'The hub sends the reserved command `FF`.', packets: { from: 'hub', to: 'node', kind: 'down', label: 'CAFE 96B840EB 83 FF' } },
      { caption: 'The node answers with its list of commands.', packets: { from: 'node', to: 'hub', kind: 'up', label: '88 D1 11 01 11 05 Relay …' } },
      { caption: 'LoRaHub reads it: a Relay switch, a Blink button, an Interval slider.', flash: 'hub', notes: { at: 'hub', text: 'Relay (switch)\nBlink (button)\nInterval (number 10-3600 s)', dy: -64 } },
      { caption: 'Home Assistant gets the three controls.', packets: { from: 'hub', to: 'ha', label: 'discovery' }, notes: { at: 'ha', text: '⏻ Relay   ◉ Blink   ━●━ Interval', dy: 42, delay: 1300 } },
      { caption: 'Switching the relay on sends the code the node announced: `01` and the value `01`.', packets: [{ from: 'ha', to: 'hub', kind: 'down', label: 'Relay ON' }, { from: 'hub', to: 'node', kind: 'down', delay: 1300, label: 'CAFE 96B840EB 84 0101' }], dur: 3400 },
    ] }), bytes({
    title: 'The description, byte by byte',
    hex: '88 D1 11 01 11 05 52656C6179 03 23 000A 0E10 00 08 496E74657276616C 01 73',
    groups: [{ n: 1, label: 'profile' }, { n: 1, label: 'format' }, { n: 1, label: 'part' }, { n: 2, label: 'code · kind' }, { n: 6, label: 'name' }, { n: 2, label: 'code · kind' }, { n: 5, label: 'min · max · scale' }, { n: 11, label: 'name · unit' }],
    steps: [
      { caption: 'After NODE_ID and SERIAL_ID (the example in docs/node-protocol.md, without Blink).' },
      { caption: 'Profile `88`: class C, bit 3 = this is a description.', on: [0], bits: { hex: '88', fields: [[7, 6, 'class'], [5, 5, 'IQ'], [4, 4, 'radio'], [3, 3, 'desc'], [2, 0, 'free']] } },
      { caption: '`D1` format 1, `11` part 1 of 1.', on: [1, 2] },
      { caption: 'Code `01`, kind `11`: type 1 switch, format 1 u8.', on: [3], bits: { hex: '11', fields: [[7, 4, 'type 1 switch'], [3, 0, 'format 1 u8']] } },
      { caption: 'Its name: length 5, then "Relay".', on: [4], result: 'switch **Relay**: on `01 01`, off `01 00`' },
      { caption: 'Code `03`, kind `23`: type 2 number, format 3 u16.', on: [5], bits: { hex: '23', fields: [[7, 4, 'type 2 number'], [3, 0, 'format 3 u16']] } },
      { caption: 'Minimum 10, maximum 3600, scale 10^0; then "Interval" and the unit "s".', on: [6, 7], result: 'slider **Interval** 10 to 3600 s: 300 s = `03 012C`' },
    ] })]);

  topic('ha', 'Home Assistant', ['hub'], [['MQTT & Home Assistant', '#/integrations'], ['Exposed nodes', '#/integrations/exposed']], [
    'LoRaHub announces nodes to Home Assistant through MQTT discovery, on Home Assistant\'s broker (192.168.1.13) and on its own. Only **adopted** nodes you **expose** appear. Each decoded field becomes a sensor; you can turn single entities off, rename them, or change their class and unit. Values are published (retained) before the announcement, so nothing starts as "unknown".',
    'Every exposed node also gets **Last payload** and a **Send downlink** box. Commands you define (button, number, text) or the node announces (also switch and choice) become controls; switches, numbers, choices and text are for class C nodes. Old entities are removed automatically; **Clean up and announce again** does it by hand.',
  ], () => [flow({
    title: 'Exposing a node and pressing a button',
    actors: { hub: A.hub(110, 115), mqtt: { x: 340, y: 115, label: 'MQTT broker', sub: '192.168.1.13', kind: 'ha', w: 140 }, ha: A.ha(570, 115) },
    links: [['hub', 'mqtt'], ['mqtt', 'ha']],
    steps: [
      { caption: 'You expose the node. LoRaHub first publishes its last values (retained)…', packets: [{ from: 'hub', to: 'mqtt', label: '.../state' }, { from: 'mqtt', to: 'ha', delay: 1300 }], dur: 3000 },
      { caption: '…then one discovery message per entity.', packets: [{ from: 'hub', to: 'mqtt', kind: 'ctl', label: 'homeassistant/…/config' }, { from: 'mqtt', to: 'ha', kind: 'ctl', delay: 1300 }], notes: { at: 'ha', text: 'Temperature 1 · RSSI · Open valve', dy: 45, delay: 2600 }, dur: 3200 },
      { caption: 'You press the "Open valve" button in Home Assistant.', packets: [{ from: 'ha', to: 'mqtt', kind: 'down', label: '.../set 0A0B' }, { from: 'mqtt', to: 'hub', kind: 'down', delay: 1300 }], dur: 3000 },
      { caption: 'LoRaHub sends it to the node like any other downlink (class A waits, class C goes now).', flash: 'hub', notes: { at: 'hub', text: 'downlink 0A0B', kind: 'down', dy: -45 } },
    ] })]);

  topic('mqtt', 'MQTT topics', ['hub'], [['MQTT & Home Assistant', '#/integrations']], [
    'LoRaHub has its own MQTT broker (port 1883) and also connects to Home Assistant\'s. `{group}` and `{node}` are the IDs shown in the console, such as `node-cafe` and `96b840eb`.',
  ], () => [table([
    ['`lorahub/{group}/{node}/up`', 'every decoded packet, with all gateways and the radio'],
    ['`lorahub/{group}/{node}/state`', 'latest values, flat, retained (read by Home Assistant)'],
    ['`lorahub/{group}/{node}/set`', 'send: `ON`, `OFF`, `DESCRIBE` or hex such as `0101`'],
    ['`lorahub/{group}/{node}/down`', 'send: JSON `{"data_hex":"0101"}`'],
    ['`lorahub/{group}/{node}/cmd/{key}`', 'send a value to a number, text, switch or choice'],
    ['`lorahub/{group}/{node}/description`', 'the commands the node announced, retained'],
    ['`lorahub/gateways/{id}/status`', 'gateway status reports'],
    ['`lorahub/unmatched`', 'packets that matched no node (when turned on)'],
  ], ['Topic', 'What it carries']), flow({
    title: 'Sending a command over MQTT',
    actors: { you: { x: 90, y: 110, label: 'Any MQTT client', sub: 'or Home Assistant', kind: 'box', w: 150 }, hub: A.hub(340, 110), node: A.node(590, 110) },
    links: [['you', 'hub'], ['hub', 'node']],
    steps: [
      { caption: 'Publish `ON` to `lorahub/node-cafe/96b840eb/set`.', packets: { from: 'you', to: 'hub', kind: 'down', label: 'ON' } },
      { caption: '`ON` becomes `0101` (Settings: MQTT commands) and goes to the node.', packets: { from: 'hub', to: 'node', kind: 'down', label: 'CAFE 96B840EB 0101' } },
      { caption: 'The node\'s next packet arrives on `.../up`.', packets: [{ from: 'node', to: 'hub', label: 'uplink' }, { from: 'hub', to: 'you', delay: 1300, label: '.../up' }], dur: 3200 },
    ] })]);

  topic('rules', 'Automation rules', ['hub'], [['Automation', '#/rules']], [
    'A rule watches one field of one node (or every packet with **always**) and, when it crosses a threshold (`>`, `<`, `>=`, `<=`, `==`, `!=`), sends a payload to another node. A **cooldown** keeps it from firing again too soon; a **pulse** sends a second payload after a number of seconds, for example to switch something off again.',
  ], () => [flow({
    title: 'Too hot: start the fan for 5 minutes',
    actors: { sensor: { x: 80, y: 110, label: 'Sensor', sub: 'temperature', kind: 'node' }, hub: A.hub(320, 110, 'rule: temperature > 30'), fan: { x: 570, y: 110, label: 'Fan relay', sub: 'class C', kind: 'node' } },
    links: [['sensor', 'hub'], ['hub', 'fan']],
    steps: [
      { caption: 'The sensor reports 31.4 °C.', packets: { from: 'sensor', to: 'hub', label: 'temperature 31.4' } },
      { caption: '31.4 > 30: the rule fires and sends its payload to the fan.', flash: 'hub', packets: { from: 'hub', to: 'fan', kind: 'down', label: '0101 (on)' } },
      { caption: 'Packets in the cooldown do not fire it again.', packets: { from: 'sensor', to: 'hub', label: 'temperature 31.6' }, notes: { at: 'hub', text: 'cooldown', dy: -45 } },
      { caption: 'After the pulse time the second payload switches the fan off.', packets: { from: 'hub', to: 'fan', kind: 'down', label: '0100 (off)' } },
    ] })]);

  topic('gateways', 'Gateways and satellites', ['hub', 'device'], [['Gateways', '#/gateways'], ['Remote config', '#/remote'], ['Realtime status', '#/status']], [
    'Each gateway runs `raw_lora_app` and talks to LoRaHub over **link protocol 2**: every packet carries a sequence number, the hub acknowledges it, and the gateway sends it again for about 12 s until it is acknowledged, so a lost datagram is not a lost reading. Commands from the hub (radio settings, downlinks) are **signed** with the link key; a new gateway is paired automatically.',
    'On a satellite its own page shows the link: acknowledged, resent, lost. Its radio settings are changed on the hub: Gateways, Remote config.',
  ], () => [flow({
    title: 'A packet whose acknowledgement gets lost',
    actors: { gw: A.gw(110, 115, 'Satellite', 'raw_lora_app'), hub: A.hub(520, 115) },
    links: [['gw', 'hub']],
    steps: [
      { caption: 'The satellite sends a packet with sequence number 41.', packets: { from: 'gw', to: 'hub', label: 'seq 41' } },
      { caption: 'The hub takes it and acknowledges it, but the acknowledgement is lost.', packets: { from: 'hub', to: 'gw', kind: 'bad', label: 'ack 41' } },
      { caption: 'No acknowledgement: the satellite sends it again.', packets: { from: 'gw', to: 'hub', label: 'seq 41 again' } },
      { caption: 'The hub already has 41: it drops the copy and acknowledges again.', packets: { from: 'hub', to: 'gw', kind: 'ctl', label: 'ack 41' }, notes: { at: 'hub', text: 'duplicate, dropped', dy: -45 } },
      { caption: 'A command from the hub carries a signature the satellite checks.', packets: { from: 'hub', to: 'gw', kind: 'ctl', label: 'L2:nonce:signature:{…}' } },
    ] })]);

  topic('tunnel', 'The tunnel: WireGuard and Tailscale', ['hub', 'device'], [['WireGuard & Tailscale', '#/vpn']], [
    'Satellites reach the hub through **WireGuard** (10.77.0.0/24, UDP 51820): the hub is 10.77.0.1, the LPS8 10.77.0.2, bobcat-sat3 10.77.0.3. When the tunnel has no recent handshake, a satellite alternates between the hub\'s home address and its internet address, so it also works from another network (UDP 51820 must be forwarded to the hub on the router).',
    '**Tailscale** runs on the hub and shares 10.77.0.0/24, so your phone or PC can open any satellite\'s page from anywhere.',
  ], () => [flow({
    title: 'A satellite on another network', h: 240,
    actors: { sat: A.gw(90, 70, 'Satellite', '10.77.0.3'), router: { x: 340, y: 70, label: 'Your router', sub: 'UDP 51820 → hub', kind: 'router', w: 150 }, hub: A.hub(590, 70, '10.77.0.1'), phone: { x: 340, y: 185, label: 'Phone', sub: 'Tailscale', kind: 'box' } },
    links: [['sat', 'router'], ['router', 'hub'], ['phone', 'hub']],
    steps: [
      { caption: 'The satellite tries the hub\'s home address: no answer, it is somewhere else.', packets: { from: 'sat', to: 'router', kind: 'bad', label: '192.168.1.15' } },
      { caption: 'It switches to the hub\'s internet address; the router forwards UDP 51820 to the hub.', packets: [{ from: 'sat', to: 'router', kind: 'ctl', label: 'internet address' }, { from: 'router', to: 'hub', kind: 'ctl', delay: 1300 }], dur: 3200 },
      { caption: 'The tunnel is up: packets flow as at home.', packets: [{ from: 'sat', to: 'router', label: 'uplink' }, { from: 'router', to: 'hub', delay: 1300 }], flash: 'hub', dur: 3200 },
      { caption: 'From your phone, Tailscale reaches the hub and through it 10.77.0.x.', packets: { from: 'phone', to: 'hub', kind: 'ctl', label: 'http://10.77.0.3' } },
    ] })]);

  topic('radio', 'Radio settings', ['hub', 'device'], [['Radio config', '#/radio'], ['TX test', '#/tx'], ['Remote config', '#/remote']], [
    '**Radio config** changes the hub\'s own radio (frequencies, channels, transmit power), **Remote config** a satellite\'s, over the signed link. Applying restarts the engine and checks that the radio is listening again within 25 s; if not, the previous settings come back by itself (the failed ones are kept as `config.json.failed`). Full duplex is not supported by these boards.',
    '**TX test** sends a packet of your choice to check transmission, for example with an SDR.',
  ], () => [flow({
    title: 'A radio setting that does not work',
    actors: { you: { x: 90, y: 110, label: 'Radio config', sub: 'Apply', kind: 'box' }, eng: A.gw(340, 110, 'raw_lora_app', 'restarting'), hub: A.hub(590, 110, 'watching') },
    links: [['you', 'eng'], ['eng', 'hub']],
    steps: [
      { caption: 'You apply new settings; the engine restarts with them.', packets: { from: 'you', to: 'eng', kind: 'ctl', label: 'config.json' } },
      { caption: 'For 25 s the hub waits for the radio to report LISTENING.', packets: { from: 'eng', to: 'hub', kind: 'bad', label: 'not listening' }, notes: { at: 'hub', text: 'waiting… 25 s', dy: -45 } },
      { caption: 'It does not: the previous settings are put back and the engine starts again.', packets: { from: 'hub', to: 'eng', kind: 'ctl', label: 'previous config.json' } },
      { caption: 'Listening again, as before.', packets: { from: 'eng', to: 'hub', label: 'LISTENING' }, flash: 'eng' },
    ] })]);

  topic('network', 'Network settings and the safety check', ['hub', 'device'], [['Device, Network', '#/device/network']], [
    'Under **Network** you set the wired and the Wi-Fi address (DHCP or fixed, and DNS servers), connect to Wi-Fi networks, and switch the Wi-Fi radio on or off. Typed-in DNS servers replace the router\'s.',
    'Every change is **checked for 60 s**: a satellite pings the hub through the tunnel, the hub pings your router. If there is no answer, the previous setting comes back by itself, so a typo cannot lock you out. Tick **Keep even if the connection check fails** to keep a change anyway. Changes to the connection in use are applied in place, without dropping it.',
  ], () => [flow({
    title: 'A wrong address is undone by itself',
    actors: { you: { x: 90, y: 110, label: 'You', sub: 'Network tab', kind: 'box' }, dev: { x: 340, y: 110, label: 'Bobcat', sub: 'new address', kind: 'hub', w: 140 }, router: { x: 590, y: 110, label: 'Router or hub', sub: 'the safety check', kind: 'router', w: 150 } },
    links: [['you', 'dev'], ['dev', 'router']],
    steps: [
      { caption: 'You set a fixed address with the wrong router.', packets: { from: 'you', to: 'dev', kind: 'ctl', label: '192.168.1.50/24 via 10.0.0.1' } },
      { caption: 'The Bobcat applies it and starts pinging.', packets: { from: 'dev', to: 'router', kind: 'bad', label: 'ping' }, notes: { at: 'dev', text: 'checking… 60 s', dy: -45 } },
      { caption: 'Still no answer after 60 s…', packets: { from: 'dev', to: 'router', kind: 'bad', label: 'ping' }, notes: { at: 'dev', text: 'checking… 3 s left', kind: 'bad', dy: -45 } },
      { caption: '…so the previous setting comes back by itself. The page tells you what happened.', packets: [{ from: 'dev', to: 'router', label: 'ping' }, { from: 'router', to: 'dev', label: 'reply', delay: 1300 }], notes: { at: 'dev', text: 'restored', kind: 'ok', dy: -45, delay: 2600 }, dur: 3600 },
    ] })]);

  topic('ap', 'Wi-Fi access point', ['hub', 'device'], [['Device, Network', '#/device/network']], [
    'The Wi-Fi can be an access point in two ways. **Bridged to the cable**: phones on it are on your home network and get their address from your router. **Own network**: the Bobcat hands out 10.42.0.x addresses and shares its cable; without a cable it is a private network, handy to set a board up in the field (page at http://10.42.0.1).',
    'With **Only while the cable is connected** the access point runs only while the cable is in; unplug it and the Wi-Fi becomes a normal client again, so a satellite can still reach the hub. The hub normally reaches your network over Wi-Fi: plug in its cable before making it an access point.',
  ], () => [flow({
    title: 'Bridged to the cable',
    actors: { phone: { x: 80, y: 110, label: 'Phone', sub: 'joins the Wi-Fi', kind: 'box' }, dev: { x: 300, y: 110, label: 'Bobcat', sub: 'Wi-Fi ⇄ bridge ⇄ cable', kind: 'hub', w: 170 }, router: { x: 560, y: 110, label: 'Your router', sub: 'hands out addresses', kind: 'router', w: 150 } },
    links: [['phone', 'dev'], ['dev', 'router']],
    steps: [
      { caption: 'The phone connects to the Bobcat\'s access point and asks for an address.', packets: { from: 'phone', to: 'dev', kind: 'ctl', label: 'DHCP?' } },
      { caption: 'The bridge passes it through the cable to your router.', packets: { from: 'dev', to: 'router', kind: 'ctl', label: 'DHCP?' } },
      { caption: 'Your router answers: the phone is on your home network.', packets: [{ from: 'router', to: 'dev', kind: 'ctl', label: '192.168.1.57' }, { from: 'dev', to: 'phone', kind: 'ctl', delay: 1300 }], dur: 3200 },
    ] }), flow({
    title: 'Own network, and the cable unplugged',
    actors: { phone: { x: 80, y: 110, label: 'Phone', sub: '10.42.0.x', kind: 'box' }, dev: { x: 300, y: 110, label: 'Bobcat', sub: '10.42.0.1', kind: 'hub', w: 150 }, net: { x: 560, y: 110, label: 'Internet', sub: 'through the cable', kind: 'router', w: 150 } },
    links: [['phone', 'dev'], ['dev', 'net']],
    steps: [
      { caption: 'The Bobcat itself gives the phone an address.', packets: { from: 'dev', to: 'phone', kind: 'ctl', label: '10.42.0.23' } },
      { caption: 'Traffic to the internet is shared through the cable.', packets: [{ from: 'phone', to: 'dev' }, { from: 'dev', to: 'net', delay: 1300 }], dur: 3000 },
      { caption: 'With "only while the cable is connected": unplug the cable…', dim: 'net', packets: { from: 'dev', to: 'net', kind: 'bad', label: 'no cable' } },
      { caption: '…and within seconds the access point stops and the Wi-Fi joins your network as a client again.', dim: 'phone', flash: 'dev', notes: { at: 'dev', text: 'Wi-Fi client again', kind: 'ok', dy: -45 } },
    ] })]);

  topic('lights', 'Status lights', ['hub', 'device'], [['Device, Lights', '#/device/lights']], [
    'The Bobcat\'s light is one RGB LED. Modes: **normal** (heartbeat, a flash per packet), **steady** (lit all the time, flashes), **quiet** (heartbeat only), **solid** (lit, no flashes), **errors** (dark unless something is wrong) and **off**. Pick a colour for the idle light, for packets received and for packets sent; **Try** shows a colour for 5 s.',
    'A **night schedule** switches to another mode between two times. Errors always use their own signals: fast red for a radio error, yellow blinking for no network. Needs raw_lora_app 2.2 (colours) or 2.1 (modes).',
  ], () => [lightDemo()]);

  topic('sd', 'SD card', ['hub', 'device'], [['Device, SD card', '#/device/sd']], [
    'The page notices a card within seconds. **Mount** a partition read-only or for writing (at /media/sdcard, without running programs from it), browse the folders, and download files. On the hub, **Back up LoRaHub to the card** writes a consistent copy of the database while it keeps running. **Unmount** before you take the card out.',
  ], () => [flow({
    title: 'A backup to the card',
    actors: { card: { x: 90, y: 110, label: 'SD card', sub: 'vfat 32 GB', kind: 'box' }, dev: A.hub(340, 110, 'SD card tab'), you: { x: 590, y: 110, label: 'You', sub: 'browser', kind: 'box' } },
    links: [['card', 'dev'], ['dev', 'you']],
    steps: [
      { caption: 'You insert a card: the page shows it and its partitions.', packets: { from: 'card', to: 'dev', kind: 'ctl', label: 'mmcblk1p1' } },
      { caption: 'Mount it for writing.', packets: { from: 'you', to: 'dev', kind: 'ctl', label: 'mount' }, notes: { at: 'dev', text: '/media/sdcard (rw)', dy: -45 } },
      { caption: 'Back up LoRaHub: a copy of the database goes onto the card.', packets: { from: 'dev', to: 'card', label: 'lorahub-backup-….db' } },
      { caption: 'Browse and download any file; unmount before removing the card.', packets: { from: 'card', to: 'you', label: 'download', dur: 2200 }, dur: 3400 },
    ] })]);

  topic('general', 'Name, time, system and logs', ['hub', 'device'], [['Device, General', '#/device/general'], ['Device, System and logs', '#/device/system']], [
    '**General**: the host name, the time zone (used for the logs and the lights\' night schedule) and whether the time is set from the internet. **System and logs**: restart the radio engine, reboot, power off, and read the logs of LoRaHub, the radio engine, the tunnel, the network and, on a satellite, its page.',
    'Power off means off: the Bobcat stays off until its power is unplugged and plugged in again.',
  ], () => [flow({
    title: 'The clock and the night schedule',
    actors: { ntp: { x: 90, y: 110, label: 'Time server', sub: 'internet', kind: 'router', w: 140 }, dev: A.hub(340, 110, 'Europe/Bucharest'), led: { x: 590, y: 110, label: 'Status light', sub: 'night: off', kind: 'box' } },
    links: [['ntp', 'dev'], ['dev', 'led']],
    steps: [
      { caption: 'The Bobcat sets its clock from the internet ("clock in step").', packets: { from: 'ntp', to: 'dev', kind: 'ctl', label: '21:59:58' } },
      { caption: 'At 22:00 local time the night schedule starts…', notes: { at: 'dev', text: '22:00 EET', dy: -45 }, packets: { from: 'dev', to: 'led', kind: 'ctl', label: 'off' } },
      { caption: '…and at 07:00 the day mode is back.', notes: { at: 'dev', text: '07:00 EET', dy: -45 }, packets: { from: 'dev', to: 'led', kind: 'ctl', label: 'normal' } },
    ] })]);

  topic('satellite', 'Adding a satellite Bobcat', ['hub', 'device'], [['Gateways', '#/gateways']], [
    'Run `deploy/satellite/add-satellite.sh <ip>` from the PC (first with `--check`). It copies the radio engine and settings from the hub, sets up the tunnel with the next free address, installs this page, starts the radio and waits for the hub to pair it. A Bobcat whose eMMC was copied from the hub is recognised and first gets its own identity (LoRaHub and Tailscale stopped, new keys, new name).',
    'The satellite\'s page is at http://<its address>/, signed in with its root password; the hub\'s Gateways page links to it ("Device page").',
  ], () => [flow({
    title: 'What the installer does', h: 240,
    actors: { pc: { x: 90, y: 120, label: 'Your PC', sub: 'add-satellite.sh', kind: 'box' }, hub: A.hub(340, 50), sat: { x: 340, y: 190, label: 'New Bobcat', sub: 'Armbian', kind: 'gw', w: 140 }, gwp: { x: 590, y: 50, label: 'Gateways page', sub: 'new satellite', kind: 'box', w: 150 } },
    links: [['pc', 'hub'], ['pc', 'sat'], ['hub', 'sat'], ['hub', 'gwp']],
    steps: [
      { caption: 'Checks both Bobcats: radio present, not the hub itself; a copy of the hub gets its own identity.', packets: [{ from: 'pc', to: 'hub', kind: 'ctl', label: 'check' }, { from: 'pc', to: 'sat', kind: 'ctl', label: 'check' }] },
      { caption: 'Copies the engine and radio settings from the hub to the new Bobcat.', packets: { from: 'hub', to: 'sat', kind: 'ctl', label: 'raw_lora_app + config' } },
      { caption: 'Sets up the tunnel (10.77.0.3) and starts the radio.', packets: { from: 'sat', to: 'hub', kind: 'ctl', label: 'WireGuard' }, flash: 'sat' },
      { caption: 'The hub pairs it; it appears under Gateways.', packets: [{ from: 'sat', to: 'hub', label: 'status' }, { from: 'hub', to: 'gwp', delay: 1300, label: 'paired' }], dur: 3200 },
    ] })]);

  topic('trouble', 'When something looks wrong', ['hub'], [['Logs', '#/logs'], ['Pipeline', '#/pipeline']], [
    'Look at the live system first: **Pipeline** and **Live data** for what arrived and what was sent, **Logs** (also on each Device page) for the engine and the hub, the **Gateways** page for each gateway\'s link.',
  ], () => [table([
    ['Odd values in Home Assistant', 'A decoder or its data offset changed and renamed fields: MQTT & Home Assistant, **Clean up and announce again**.'],
    ['A packet you sent comes back as an uplink', 'The echo guard ignores it for 20 s; nodes with inverted IQ avoid it entirely.'],
    ['Test packets', 'Console test packets show `"simulated": true` and never change the node, Home Assistant or rules.'],
    ['A command "does nothing"', 'Class A waits for the node\'s next uplink (see its queue on the Downlink tab); class C needs a gateway that heard it recently.'],
    ['A gateway stays offline', 'Its tunnel (WireGuard & Tailscale page) and its engine log; it pairs again by itself when it comes back.'],
    ['Lost after a network change', 'Wait 60 s: the previous setting comes back by itself.'],
  ], ['What you see', 'Where to look'])]);

  // ---------- page ----------
  function mount(view, opts) {
    const set = opts.set || 'hub';
    const list = T.filter((t) => t.sets.includes(set));
    const base = opts.base || '#/help/';
    const cur = decodeURIComponent((location.hash.split(base)[1] || '').split('?')[0]) || list[0].id;
    const tp = list.find((t) => t.id === cur) || list[0];
    const search = el('input', { type: 'search', placeholder: 'Search the help', class: 'hs-search' });
    const nav = el('nav', { class: 'hs-nav' });
    const paintNav = () => {
      const q = search.value.trim().toLowerCase();
      nav.replaceChildren(...list.filter((t) => !q || (t.title + ' ' + t.text.join(' ')).toLowerCase().includes(q))
        .map((t) => el('a', { href: base + t.id, class: t.id === tp.id ? 'active' : '' }, t.title)));
    };
    search.addEventListener('input', paintNav);
    paintNav();
    // On a narrow screen a list of topics replaces the side menu.
    const pick = el('select', { class: 'hs-pick', 'aria-label': 'Topic', onchange: (e) => { location.hash = base + e.target.value; } },
      list.map((t) => el('option', { value: t.id }, t.title)));
    pick.value = tp.id;
    const scenes = tp.scenes();
    const i = list.indexOf(tp);
    // On a satellite only its own tabs exist: #/device/x becomes #/x.
    const where = tp.where.filter(([, href]) => href && (set === 'hub' || href.startsWith('#/device/')))
      .map(([l, href]) => set === 'hub' ? el('a', { class: 'btn sm', href }, l) : el('a', { class: 'btn sm', href: href.replace('#/device/', '#/') }, l.replace('Device, ', '')));
    const article = el('article', { class: 'hs-article' },
      el('h2', null, tp.title),
      ...tp.text.map((p) => el('p', null, ...rich(p))),
      where.length ? el('div', { class: 'row hs-where' }, el('span', { class: 'small muted' }, 'Where:'), where) : null,
      ...scenes,
      el('div', { class: 'row between hs-pager' },
        i > 0 ? el('a', { href: base + list[i - 1].id }, '← ' + list[i - 1].title) : el('span'),
        i < list.length - 1 ? el('a', { href: base + list[i + 1].id }, list[i + 1].title + ' →') : el('span')));
    view.replaceChildren(el('div', { class: 'hs-layout' }, el('aside', { class: 'hs-side' }, search, nav, pick), article));
    opts.onLeave(() => scenes.forEach((s) => s._stop && s._stop()));
  }

  return { mount, topics: T };
})();
