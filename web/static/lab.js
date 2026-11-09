/* LoRaHub console: Decoder lab, a workbench for packets and decoders. */
'use strict';

const hexToBytes = (x) => (cleanHex(x).match(/../g) || []).map((b) => parseInt(b, 16));
const bytesToHex = (b) => b.map((x) => x.toString(16).padStart(2, '0')).join('').toUpperCase();

// Interpretations shown for the selected byte. Names match the field types of
// the byte-field decoder so a row can become a field.
const LAB_TYPES = [
  ['uint8', 1, (d) => d.getUint8(0)], ['int8', 1, (d) => d.getInt8(0)],
  ['uint16_be', 2, (d) => d.getUint16(0)], ['uint16_le', 2, (d) => d.getUint16(0, true)],
  ['int16_be', 2, (d) => d.getInt16(0)], ['int16_le', 2, (d) => d.getInt16(0, true)],
  ['uint24_be', 3, (d) => (d.getUint8(0) << 16) | d.getUint16(1)],
  ['int24_be', 3, (d) => ((d.getUint8(0) << 16) | d.getUint16(1)) << 8 >> 8],
  ['uint32_be', 4, (d) => d.getUint32(0)], ['uint32_le', 4, (d) => d.getUint32(0, true)],
  ['int32_be', 4, (d) => d.getInt32(0)], ['int32_le', 4, (d) => d.getInt32(0, true)],
  ['float32_be', 4, (d) => Number(d.getFloat32(0).toPrecision(7))], ['float32_le', 4, (d) => Number(d.getFloat32(0, true).toPrecision(7))],
  ['uint24_le', 3, (d) => d.getUint8(0) | (d.getUint16(1, true) << 8)],
  ['uint64_be', 8, (d) => d.getBigUint64(0)], ['uint64_le', 8, (d) => d.getBigUint64(0, true)],
  ['float64_be', 8, (d) => d.getFloat64(0)], ['float64_le', 8, (d) => d.getFloat64(0, true)],
  ['unix_s_be', 4, (d) => new Date(d.getUint32(0) * 1000).toISOString()], ['unix_s_le', 4, (d) => new Date(d.getUint32(0, true) * 1000).toISOString()],
];

function crcTable(bytes, width, poly, init, reflect, xorOut) {
  let crc = init;
  const top = 2 ** (width - 1), mask = 2 ** width - 1;
  const rev = (v, n) => { let r = 0; for (let i = 0; i < n; i++) if (v & (1 << i)) r |= 1 << (n - 1 - i); return r >>> 0; };
  for (const raw of bytes) {
    const b = reflect ? rev(raw, 8) : raw;
    for (let i = 7; i >= 0; i--) {
      const bit = ((crc & top) !== 0) !== (((b >> i) & 1) === 1);
      crc = (crc * 2) % (mask + 1);
      if (bit) crc = (crc ^ poly) >>> 0;
      crc = crc % (mask + 1);
    }
  }
  if (reflect) crc = rev(crc, width);
  return ((crc ^ xorOut) >>> 0) % (mask + 1);
}
const CHECKSUMS = [
  ['XOR', 8, (b) => b.reduce((a, x) => a ^ x, 0)],
  ['Sum 8', 8, (b) => b.reduce((a, x) => (a + x) & 255, 0)],
  ['CRC-8', 8, (b) => crcTable(b, 8, 0x07, 0, false, 0)],
  ['CRC-8/Maxim', 8, (b) => crcTable(b, 8, 0x31, 0, true, 0)],
  ['CRC-16/CCITT', 16, (b) => crcTable(b, 16, 0x1021, 0xFFFF, false, 0)],
  ['CRC-16/Modbus', 16, (b) => crcTable(b, 16, 0x8005, 0xFFFF, true, 0)],
  ['CRC-32', 32, (b) => crcTable(b, 32, 0x04C11DB7, 0xFFFFFFFF, true, 0xFFFFFFFF)],
];

App.route('/lab', async () => {
  const [s, groups, recent] = await Promise.all([GET('/settings'), GET('/applications'), GET('/events?names=as.up,ns.up.unmatched&limit=60')]);
  const pf = s.packet_format;
  const packets = recent.map((e) => ({ hex: e.data.payload_hex || e.data.raw_payload_hex || '', label: `${fmtTime(e.time)} · ${e.data.node_id || ''} ${e.data.serial || e.device_id || ''} · ${e.data.size || ''} B` })).filter((p) => p.hex);

  let bytes = hexToBytes((packets[0] && packets[0].hex) || 'CAFE96B840EB0102014A0271FFF500170429036700FC');
  let sel0 = Math.min(pf.data_offset, Math.max(bytes.length - 1, 0)), sel1 = sel0;
  let mode = 'pipeline';

  const hexIn = h('textarea', { class: 'mono', rows: 2, spellcheck: 'false', value: bytesToHex(bytes) });
  const picker = sel('', [['', packets.length ? 'Recent packets…' : 'No recent packets'], ...packets.map((p, i) => [i, p.label])], { onchange: (e) => { if (e.target.value !== '') setPacket(packets[Number(e.target.value)].hex); } });
  const map = h('div', { class: 'lab-map' });
  const facts = h('div', { class: 'chips' });
  const inspector = h('div');
  const checks = h('div');
  const result = h('div', { class: 'stack' });
  const seg = h('div', { class: 'seg' });

  const dataBytes = () => bytes.slice(pf.data_offset);
  const region = (i) => (i >= pf.node_id_offset && i < pf.node_id_offset + pf.node_id_length ? 'r-node' : i >= pf.serial_offset && i < pf.serial_offset + pf.serial_length ? 'r-serial' : i >= pf.data_offset ? 'r-data' : '');
  const selection = () => bytes.slice(Math.min(sel0, sel1), Math.max(sel0, sel1) + 1);

  // Decoder under construction; survives switching tabs and packets.
  let draft = { type: 'fields', fields: [] };
  let editor = null;

  function addField(type, size) {
    if (editor) draft = editor.getState();
    draft.type = 'fields';
    draft.fields = draft.fields || [];
    const start = Math.min(sel0, sel1);
    draft.fields.push({ name: `field_${draft.fields.length + 1}`, offset: start - pf.data_offset, length: size, type, scale: 1, add: 0, unit: '' });
    mode = 'build';
    paint();
    toast('Added to the decoder');
  }

  function paintMap() {
    const lo = Math.min(sel0, sel1), hi = Math.max(sel0, sel1);
    map.replaceChildren(...bytes.map((b, i) => h('button', { class: `lab-byte ${region(i)}${i >= lo && i <= hi ? ' sel' : ''}`, title: `Byte ${i}`, onclick: (e) => {
      if (e.shiftKey) sel1 = i; else sel0 = sel1 = i;
      paintMap(); paintInspector();
    } }, h('small', null, i), b.toString(16).padStart(2, '0').toUpperCase())));
    const node = bytesToHex(bytes.slice(pf.node_id_offset, pf.node_id_offset + pf.node_id_length));
    const serial = bytesToHex(bytes.slice(pf.serial_offset, pf.serial_offset + pf.serial_length));
    facts.replaceChildren(h('span', { class: 'chip' }, h('b', null, 'length '), bytes.length + ' B'), h('span', { class: 'chip c-node' }, h('b', null, 'NODE_ID '), node || '—'), h('span', { class: 'chip c-serial' }, h('b', null, 'SERIAL '), serial || '—'), h('span', { class: 'chip c-data' }, h('b', null, 'data '), Math.max(0, bytes.length - pf.data_offset) + ' B'),
      h('span', { class: 'small muted' }, 'Click a byte to inspect it. Shift-click selects a range.'));
  }

  function paintInspector() {
    const start = Math.min(sel0, sel1), sel = selection();
    const view = (n) => (start + n <= bytes.length ? new DataView(Uint8Array.from(bytes.slice(start, start + n)).buffer) : null);
    const rows = LAB_TYPES.map(([name, size, read]) => {
      const d = view(size);
      return h('tr', null, h('td', { class: 'mono small' }, name), h('td', { class: 'mono' }, d ? String(read(d)) : '—'),
        h('td', { class: 'right' }, d && start >= pf.data_offset ? h('button', { class: 'btn sm', onclick: () => addField(name, size) }, 'Use') : null));
    });
    const text = String.fromCharCode(...sel.map((x) => (x >= 32 && x < 127 ? x : 46)));
    const bits = sel.slice(0, 4).map((x) => x.toString(2).padStart(8, '0')).join(' ');
    inspector.replaceChildren(h('div', { class: 'small muted', style: { marginBottom: '6px' } }, sel.length > 1 ? `Bytes ${start}-${start + sel.length - 1}` : `Byte ${start}`, start >= pf.data_offset ? ` · data offset ${start - pf.data_offset}` : ''),
      h('div', { class: 'table-wrap' }, h('table', { class: 't compact' }, h('tbody', null, rows,
        h('tr', null, h('td', { class: 'mono small' }, 'bits'), h('td', { class: 'mono', colspan: 2 }, bits)),
        h('tr', null, h('td', { class: 'mono small' }, 'ascii'), h('td', { class: 'mono', colspan: 2 }, text))))));
    checks.replaceChildren(h('div', { class: 'small muted', style: { marginBottom: '6px' } }, `Over the ${sel.length} selected byte(s)`),
      h('div', { class: 'table-wrap' }, h('table', { class: 't compact' }, h('tbody', null, CHECKSUMS.map(([name, width, fn]) => h('tr', null, h('td', { class: 'small' }, name), h('td', { class: 'mono right' }, fn(sel).toString(16).padStart(width / 4, '0').toUpperCase())))))));
  }

  function setPacket(x) {
    bytes = hexToBytes(x);
    hexIn.value = bytesToHex(bytes);
    sel0 = sel1 = Math.min(pf.data_offset, Math.max(bytes.length - 1, 0));
    paint();
  }
  hexIn.addEventListener('input', () => {
    bytes = hexToBytes(hexIn.value);
    sel0 = sel1 = Math.min(sel0, Math.max(bytes.length - 1, 0));
    paintMap(); paintInspector();
    clearTimeout(hexIn.t);
    hexIn.t = setTimeout(paintResult, 300);
  });

  const failure = (e) => h('div', { class: 'callout warn' }, e.message);
  const decodedBlock = (data) => (data && Object.keys(data).length ? jsonView(data) : h('p', { class: 'muted', style: { margin: 0 } }, 'Nothing decoded.'));
  const msgs = (r) => [...(r.errors || []).map((m) => h('div', { class: 'callout warn small' }, m)), ...(r.warnings || []).map((m) => h('div', { class: 'callout small' }, m))];

  async function runPipeline() {
    const r = await api('POST', '/schemes/test', { payload_hex: bytesToHex(bytes) });
    const trace = r.trace || [];
    return [h('div', { class: 'grid g2' },
      h('div', { class: 'card card-body' }, h('h3', null, 'Path through Decoder Studio'), trace.length ? h('div', { class: 'table-wrap' }, h('table', { class: 't compact' }, h('thead', null, h('tr', null, ['#', 'Scheme', 'Matches', 'Used'].map((x) => h('th', null, x)))),
        h('tbody', null, trace.map((t, i) => h('tr', { class: t.decoded ? 'hl' : '' }, h('td', null, i + 1), h('td', null, t.name), h('td', null, h('span', { class: 'badge ' + (t.matched ? 'ok' : '') }, t.matched ? 'yes' : 'no')), h('td', null, t.decoded ? h('span', { class: 'badge info' }, 'decoded') : '—')))))) : h('p', { class: 'muted' }, 'No schemes are enabled.')),
      h('div', { class: 'card card-body stack' }, h('h3', { style: { margin: 0 } }, r.scheme_name ? 'Result from ' + r.scheme_name : 'No scheme decoded this packet'), ...msgs(r), decodedBlock(r.data)))];
  }

  async function runCompare() {
    const doc = await GET('/schemes');
    const hexStr = bytesToHex(bytes);
    const results = await Promise.all((doc.schemes || []).map(async (sc) => {
      try { return { sc, r: await api('POST', '/schemes/test', { payload_hex: hexStr, scheme: sc }) }; } catch (e) { return { sc, r: { errors: [e.message] } }; }
    }));
    return [h('div', { class: 'card' }, h('div', { class: 'table-wrap' }, h('table', { class: 't compact' }, h('thead', null, h('tr', null, ['Scheme', 'Type', 'Matches', 'Output'].map((x) => h('th', null, x)))),
      h('tbody', null, results.map(({ sc, r }) => h('tr', null, h('td', null, sc.name), h('td', null, h('span', { class: 'badge' }, sc.type)), h('td', null, h('span', { class: 'badge ' + (r.matched ? 'ok' : '') }, r.matched ? 'yes' : 'no')),
        h('td', null, r.errors && r.errors.length ? h('span', { class: 'small' }, r.errors.join('; ')) : valueChips(r.data)))))))),
    h('p', { class: 'small muted', style: { margin: 0 } }, 'Each scheme is run on this packet even if it would not be selected.')];
  }

  function applyBar() {
    const group = sel('', [['', 'Choose a node group…'], ...groups.map((a) => [a.application_id, `${a.node_id || ''} ${a.name || a.application_id}`])]);
    const nodeHex = bytesToHex(bytes.slice(pf.node_id_offset, pf.node_id_offset + pf.node_id_length));
    const match = groups.find((a) => (a.node_id || '').toUpperCase() === nodeHex);
    if (match) group.value = match.application_id;
    const schemeName = inp(nodeHex ? `Packets ${nodeHex}` : '', { placeholder: 'Scheme name' });
    return h('div', { class: 'grid g2' },
      h('div', { class: 'card card-body stack' }, h('h3', { style: { margin: 0 } }, 'Use for a node group'), group, h('div', null, h('button', { class: 'btn', onclick: async () => {
        if (!group.value) { toast('Choose a node group first'); return; }
        try { await api('PUT', '/applications/' + group.value, { uplink_formatter: editor.getState() }); toast('Decoder applied to the group'); } catch (e) { errToast(e); }
      } }, 'Apply to group'))),
      h('div', { class: 'card card-body stack' }, h('h3', { style: { margin: 0 } }, 'Save as a Decoder Studio scheme'), schemeName, h('div', null, h('button', { class: 'btn', onclick: async () => {
        const st = editor.getState();
        if (st.type !== 'fields' && !isMethod(st.type)) { toast('Only byte-field decoders and decoding methods can be saved as a scheme'); return; }
        if (st.type === 'fields' && !(st.fields || []).length) { toast('Add a field first'); return; }
        try {
          const doc = await GET('/schemes');
          const id = 'lab_' + Date.now().toString(36);
          const shift = (v) => ((v || 0) >= 0 ? (v || 0) + pf.data_offset : v);
          doc.schemes = doc.schemes || [];
          doc.schemes.unshift({ id, name: schemeName.value || id, matchType: 'prefix', matchValue: nodeHex.toLowerCase(), type: st.type, dataOffset: pf.data_offset, sampleHex: bytesToHex(bytes), options: st.options,
            fields: st.type === 'fields' ? st.fields.map((f) => Object.assign({}, f, { offset: shift(f.offset) }, f.type === 'checksum' ? { from: shift(f.from) } : {})) : st.fields });
          await api('PUT', '/schemes', doc);
          toast('Scheme saved. It is now first in Decoder Studio');
        } catch (e) { errToast(e); }
      } }, 'Save scheme'))));
  }

  function buildTab() {
    editor = formatterEditor(draft, { samplePayload: bytesToHex(dataBytes()) });
    return [h('div', { class: 'card card-body' }, editor), applyBar()];
  }

  async function runBatch() {
    const count = sel('50', ['20', '50', '100'].map((n) => [n, `Last ${n} packets`]));
    const source = sel('pipeline', [['pipeline', 'Saved Decoder Studio schemes'], ['draft', 'Decoder from the Build tab']]);
    const out = h('div', { class: 'stack' });
    const run = async () => {
      out.replaceChildren(h('div', { class: 'muted' }, 'Running…'));
      if (editor) draft = editor.getState();
      const evs = await GET(`/events?names=as.up,ns.up.unmatched&limit=${count.value}`);
      const list = evs.map((e) => ({ e, hex: e.data.payload_hex || e.data.raw_payload_hex || '' })).filter((x) => x.hex);
      const rows = await Promise.all(list.map(async ({ e, hex }) => {
        try {
          const r = source.value === 'pipeline' ? await api('POST', '/schemes/test', { payload_hex: hex })
            : await api('POST', '/formatters/test', { direction: 'up', formatter: draft, payload_hex: bytesToHex(hexToBytes(hex).slice(pf.data_offset)), f_port: 1 });
          return { e, hex, r };
        } catch (err) { return { e, hex, r: { errors: [err.message] } }; }
      }));
      const ok = rows.filter((x) => x.r.data && Object.keys(x.r.data).length && !(x.r.errors || []).length).length;
      const failed = rows.filter((x) => (x.r.errors || []).length).length;
      out.replaceChildren(h('div', { class: 'grid g3' }, statTile('Decoded', ok), statTile('No result', rows.length - ok - failed), statTile('Errors', failed)),
        h('div', { class: 'card' }, h('div', { class: 'table-wrap' }, h('table', { class: 't compact' }, h('thead', null, h('tr', null, ['Time', 'Packet', 'Output'].map((x) => h('th', null, x)))),
          h('tbody', null, rows.map(({ e, hex, r }) => h('tr', { class: 'click', title: 'Open this packet', onclick: () => setPacket(hex) }, h('td', { class: 'small nowrap' }, fmtTime(e.time)), h('td', { class: 'mono small' }, hex.slice(0, 28) + (hex.length > 28 ? '…' : '')),
            h('td', null, (r.errors || []).length ? h('span', { class: 'small' }, r.errors.join('; ')) : valueChips(r.data)))))))));
    };
    return [h('div', { class: 'row' }, h('div', { style: { minWidth: '160px' } }, count), h('div', { style: { minWidth: '240px' } }, source), h('button', { class: 'btn primary', onclick: () => run().catch(errToast) }, icon('play'), 'Run')), out];
  }

  async function paintResult() {
    const seq = (paintResult.seq = (paintResult.seq || 0) + 1);
    let kids;
    try {
      if (!bytes.length) kids = [h('div', { class: 'empty card' }, 'Enter a packet to begin.')];
      else if (mode === 'pipeline') kids = await runPipeline();
      else if (mode === 'compare') kids = await runCompare();
      else if (mode === 'build') kids = buildTab();
      else kids = await runBatch();
    } catch (e) { kids = [failure(e)]; }
    if (seq === paintResult.seq) result.replaceChildren(...kids);
  }

  function paintSeg() {
    seg.replaceChildren(...[['pipeline', 'Pipeline'], ['compare', 'Compare schemes'], ['build', 'Build decoder'], ['batch', 'Batch test']].map(([k, label]) => h('button', { class: k === mode ? 'on' : '', onclick: () => {
      if (editor) draft = editor.getState();
      editor = null;
      mode = k; paint();
    } }, label)));
  }

  function paint() { paintMap(); paintInspector(); paintSeg(); paintResult(); }
  paint();

  return h('div', { class: 'stack' }, pageHead([['Decoder lab']], 'Decoder lab', ['Take a packet apart and test how it decodes']),
    h('div', { class: 'card card-body stack' },
      h('div', { class: 'row between' }, h('h2', { style: { margin: 0 } }, 'Packet'), h('div', { style: { minWidth: '280px' } }, picker)),
      hexIn, map, facts),
    h('div', { class: 'grid g2' },
      h('div', { class: 'card card-body' }, h('h2', null, 'Value inspector'), inspector),
      h('div', { class: 'card card-body' }, h('h2', null, 'Checksums'), checks)),
    h('div', null, seg), result);
});
