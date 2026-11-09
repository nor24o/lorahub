/* LoRaHub console: mark values directly on a packet's bytes. */
'use strict';

const BYTE_PRESETS = [
  ['Temperature ÷10', 'temperature', 0.1, '°C'], ['Temperature ÷100', 'temperature', 0.01, '°C'], ['Humidity', 'humidity', 1, '%'], ['Humidity ÷2', 'humidity', 0.5, '%'],
  ['Battery mV', 'battery', 0.001, 'V'], ['Voltage ÷100', 'voltage', 0.01, 'V'], ['Pressure ÷10', 'pressure', 0.1, 'hPa'], ['Counter', 'counter', 1, ''],
];

function printable(bytes) { return bytes.length > 0 && bytes.every((b) => (b >= 32 && b < 127) || b === 0); }
function isBcd(bytes) { return bytes.every((b) => (b >> 4) <= 9 && (b & 15) <= 9); }

/** What a run of bytes could mean, with the value each reading gives. */
function byteReadings(bytes) {
  const n = bytes.length;
  const v = new DataView(Uint8Array.from(bytes).buffer);
  const out = [];
  const add = (type, label, value) => out.push({ type, label, value: String(value) });
  const num = (x) => Number(x.toPrecision(7));
  const hex = bytes.map((b) => b.toString(16).padStart(2, '0')).join('').toUpperCase();
  if (n === 1) {
    add('uint8', 'Number', v.getUint8(0));
    add('int8', 'Signed number', v.getInt8(0));
    add('bool', 'On / off', v.getUint8(0) ? 'on' : 'off');
    add('bits', 'Part of a byte', 'choose bits');
  } else if (n === 2) {
    add('uint16_be', 'Number', v.getUint16(0));
    add('int16_be', 'Signed number', v.getInt16(0));
    add('uint16_le', 'Number, low byte first', v.getUint16(0, true));
    add('int16_le', 'Signed, low byte first', v.getInt16(0, true));
  } else if (n === 3) {
    const be = (bytes[0] << 16) | (bytes[1] << 8) | bytes[2], le = (bytes[2] << 16) | (bytes[1] << 8) | bytes[0];
    add('uint24_be', 'Number', be);
    add('int24_be', 'Signed number', (be << 8) >> 8);
    add('uint24_le', 'Number, low byte first', le);
    add('int24_le', 'Signed, low byte first', (le << 8) >> 8);
  } else if (n === 4) {
    add('uint32_be', 'Number', v.getUint32(0));
    add('int32_be', 'Signed number', v.getInt32(0));
    add('uint32_le', 'Number, low byte first', v.getUint32(0, true));
    add('int32_le', 'Signed, low byte first', v.getInt32(0, true));
    [['float32_be', false], ['float32_le', true]].forEach(([t, le]) => { const f = v.getFloat32(0, le); if (Number.isFinite(f) && Math.abs(f) < 1e7 && (f === 0 || Math.abs(f) > 1e-4)) add(t, le ? 'Decimal, low byte first' : 'Decimal number', num(f)); });
    [['unix_s_be', false], ['unix_s_le', true]].forEach(([t, le]) => { const s = v.getUint32(0, le); if (s > 1.4e9 && s < 2.3e9) add(t, 'Date and time', new Date(s * 1000).toISOString().slice(0, 19).replace('T', ' ')); });
  } else if (n === 8) {
    add('uint64_be', 'Number', v.getBigUint64(0));
    add('uint64_le', 'Number, low byte first', v.getBigUint64(0, true));
    [['float64_be', false], ['float64_le', true]].forEach(([t, le]) => { const f = v.getFloat64(0, le); if (Number.isFinite(f) && Math.abs(f) < 1e12 && (f === 0 || Math.abs(f) > 1e-6)) add(t, le ? 'Decimal, low byte first' : 'Decimal number', num(f)); });
  }
  if (n > 1 && n <= 8 && isBcd(bytes)) add('bcd', 'Digits (BCD)', Number(hex));
  if (n > 1 && printable(bytes)) add('ascii', 'Text', String.fromCharCode(...bytes.filter(Boolean)));
  add('hex', 'Raw hex', n > 8 ? hex.slice(0, 16) + '…' : hex);
  return out;
}

/**
 * byteMapper: drag across bytes to create fields.
 * opts: { bytes() -> [hex...], fields, onChange(), region(i) -> ''|'node'|'serial'|'head', readonly }
 * Returns { el, setValues(map), redraw() }.
 */
function byteMapper(opts) {
  const fields = opts.fields;
  const strip = h('div', { class: 'bm-strip' });
  const picker = h('div');
  const cards = h('div', { class: 'bm-cards' });
  const hint = h('div', { class: 'small muted' }, matchMedia('(pointer: coarse)').matches ? 'Tap the first and the last byte of a value, then choose what it means.' : 'Drag across the bytes that belong together, then choose what they mean.');
  let lastPointer = 'mouse';
  let values = {};
  let drag = null;
  let focusIndex = -1;

  const color = (i) => FIELD_COLORS[i % FIELD_COLORS.length];
  const rangeOf = (f, total) => {
    let start = Number(f.offset) || 0;
    if (start < 0) start += total;
    let len = Number(f.length) || 0;
    if (len === 0) len = typeBytes(f.type);
    else if (len < 0) len += total - start + 1;
    return [start, Math.max(len, 1)];
  };
  const owner = (i, total) => fields.findIndex((f) => { if (f.name === undefined) return false; const [s, l] = rangeOf(f, total); return i >= s && i < s + l; });
  const shown = (f) => { const v = values[f.name]; return v === undefined ? '—' : typeof v === 'object' ? JSON.stringify(v) : String(v); };
  const changed = () => { drawStrip(); drawCards(); opts.onChange(); };

  function cell(hexv, i, cls) {
    return h('span', { class: 'bm-byte ' + cls, 'data-i': i, title: 'Byte ' + i }, h('small', null, i), hexv);
  }

  function drawStrip() {
    const bytes = opts.bytes();
    if (!bytes.length) { strip.replaceChildren(h('span', { class: 'small muted' }, 'Pick or paste a packet first.')); return; }
    const kids = [];
    let i = 0;
    while (i < bytes.length) {
      const own = owner(i, bytes.length);
      if (own >= 0) {
        const [s, l] = rangeOf(fields[own], bytes.length);
        const end = Math.min(s + l, bytes.length);
        const f = fields[own];
        kids.push(h('div', { class: 'bm-seg' + (focusIndex === own ? ' focus' : ''), style: { '--c': color(own) }, 'data-f': own },
          h('div', { class: 'bm-seg-bytes' }, bytes.slice(i, end).map((b, k) => cell(b, i + k, ''))),
          h('div', { class: 'bm-seg-label', title: `${f.name} = ${shown(f)}` }, h('b', null, f.name || 'unnamed'), h('span', null, shown(f)))));
        i = end;
        continue;
      }
      const r = opts.region ? opts.region(i) : '';
      const inDrag = drag && i >= Math.min(drag.a, drag.b) && i <= Math.max(drag.a, drag.b);
      kids.push(h('div', { class: 'bm-seg free' }, cell(bytes[i], i, (r ? 'r-' + r : '') + (inDrag ? ' picking' : '')), h('div', { class: 'bm-seg-label' }, r === 'node' ? 'NODE' : r === 'serial' ? 'SERIAL' : ' ')));
      i++;
    }
    strip.replaceChildren(...kids);
  }

  function addField(start, len, type) {
    const n = fields.length + 1;
    fields.push({ name: 'value_' + n, offset: start, length: len, type, scale: 1, add: 0, unit: '' });
    if (type === 'bits') { fields[fields.length - 1].bit = 0; fields[fields.length - 1].bit_length = 4; }
    focusIndex = fields.length - 1;
    picker.replaceChildren();
    changed();
    const input = cards.querySelector(`[data-card="${focusIndex}"] input`);
    if (input) { input.focus(); input.select(); }
  }

  function showPicker(a, b) {
    const start = Math.min(a, b), end = Math.max(a, b), len = end - start + 1;
    const hexes = opts.bytes().slice(start, end + 1);
    const bytes = hexes.map((x) => parseInt(x, 16));
    picker.replaceChildren(h('div', { class: 'bm-picker' },
      h('div', { class: 'row between' }, h('b', null, len === 1 ? `Byte ${start} is…` : `Bytes ${start}–${end} are…`), h('button', { class: 'icon-btn', title: 'Cancel', onclick: () => { drag = null; picker.replaceChildren(); drawStrip(); } }, '✕')),
      h('div', { class: 'bm-readings' }, byteReadings(bytes).map((r) => h('button', { class: 'bm-reading', type: 'button', onclick: () => { drag = null; addField(start, len, r.type); } }, h('b', null, r.value), h('span', null, r.label))))));
  }

  function byteAt(e) {
    const el = document.elementFromPoint(e.clientX, e.clientY);
    const cellEl = el && el.closest ? el.closest('.bm-byte') : null;
    return cellEl ? Number(cellEl.getAttribute('data-i')) : null;
  }
  if (!opts.readonly) {
    const focusField = (seg) => {
      focusIndex = Number(seg.getAttribute('data-f'));
      drawStrip(); drawCards();
      const c = cards.querySelector(`[data-card="${focusIndex}"]`);
      if (c) c.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
    };
    strip.addEventListener('click', (e) => {   // touch: tap the first byte, then the last
      if (lastPointer !== 'touch') return;
      const seg = e.target.closest('.bm-seg');
      if (!seg) return;
      if (seg.hasAttribute('data-f')) { focusField(seg); return; }
      const cellEl = e.target.closest('.bm-byte');
      if (!cellEl) return;
      const i = Number(cellEl.getAttribute('data-i'));
      const total = opts.bytes().length;
      const extending = drag && drag.a === drag.b && i !== drag.a;
      let clear = extending;
      if (extending) for (let k = Math.min(drag.a, i); k <= Math.max(drag.a, i); k++) if (owner(k, total) >= 0) clear = false;
      if (clear) drag.b = i;
      else if (drag && drag.a === i && drag.b === i) { drag = null; picker.replaceChildren(); drawStrip(); return; }
      else drag = { a: i, b: i, active: false };
      drawStrip();
      showPicker(drag.a, drag.b);
    });
    strip.addEventListener('pointerdown', (e) => {
      lastPointer = e.pointerType || 'mouse';
      if (lastPointer === 'touch') return;
      const seg = e.target.closest('.bm-seg');
      if (!seg) return;
      if (seg.hasAttribute('data-f')) { focusField(seg); return; }
      const i = byteAt(e);
      if (i === null) return;
      e.preventDefault();
      drag = { a: i, b: i, active: true };
      picker.replaceChildren();
      drawStrip();
    });
    const move = (e) => {
      if (!drag || !drag.active) return;
      const i = byteAt(e);
      if (i === null || i === drag.b) return;
      const total = opts.bytes().length, lo = Math.min(drag.a, i), hi = Math.max(drag.a, i);
      for (let k = lo; k <= hi; k++) if (owner(k, total) >= 0) return; // stop at an existing field
      drag.b = i;
      drawStrip();
    };
    const up = () => { if (drag && drag.active) { drag.active = false; showPicker(drag.a, drag.b); } };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', up);
    App.onLeave(() => { window.removeEventListener('pointermove', move); window.removeEventListener('pointerup', up); });
  }

  function drawCards() {
    if (opts.readonly) { cards.replaceChildren(); return; }
    cards.replaceChildren(...fields.map((f, i) => {
      const set = (k, conv) => (e) => { f[k] = conv ? conv(e.target.value) : e.target.value; drawStrip(); opts.onChange(); };
      const presets = h('div', { class: 'bm-presets' }, BYTE_PRESETS.map(([label, name, scale, unit]) => h('button', { class: 'chip bm-preset', type: 'button', onclick: () => { f.name = name; f.scale = scale; f.unit = unit; changed(); } }, label)));
      return h('div', { class: 'bm-card' + (focusIndex === i ? ' focus' : ''), style: { '--c': color(i) }, 'data-card': i, onmouseenter: () => { const seg = strip.querySelector(`[data-f="${i}"]`); if (seg) seg.classList.add('focus'); }, onmouseleave: () => { const seg = strip.querySelector(`[data-f="${i}"]`); if (seg && focusIndex !== i) seg.classList.remove('focus'); } },
        h('div', { class: 'bm-card-top' }, inp(f.name, { class: 'bm-name', placeholder: 'name', oninput: set('name') }), h('div', { class: 'bm-card-value' }, shown(f), f.unit ? h('small', null, ' ' + f.unit) : null),
          fieldOptionsButton(f, changed), h('button', { class: 'icon-btn', title: 'Remove', onclick: () => { fields.splice(i, 1); focusIndex = -1; changed(); } }, icon('trash'))),
        h('div', { class: 'bm-card-row' }, h('label', null, 'Type', sel(f.type, FIELD_TYPES, { onchange: (e) => { f.type = e.target.value; changed(); } })),
          h('label', null, 'Multiply', inp(f.scale || 1, { type: 'number', step: 'any', oninput: set('scale', Number) })), h('label', null, 'Add', inp(f.add || 0, { type: 'number', step: 'any', oninput: set('add', Number) })), h('label', null, 'Unit', inp(f.unit || '', { oninput: set('unit') }))),
        presets);
    }));
    if (!fields.length) cards.append(h('div', { class: 'empty' }, 'No values marked yet.'));
  }

  drawStrip(); drawCards();
  return {
    el: h('div', { class: 'stack' }, opts.readonly ? null : hint, strip, picker, opts.readonly ? null : cards),
    setValues(map) {
      values = map || {};
      drawStrip();
      fields.forEach((f, i) => { const el = cards.querySelector(`[data-card="${i}"] .bm-card-value`); if (el) el.replaceChildren(shown(f), ...(f.unit ? [h('small', null, ' ' + f.unit)] : [])); });
    },
    redraw() { drawStrip(); },
  };
}

const DEC_HELP = {
  fields: 'Mark bytes yourself', ctv: 'Channel, type, value triplets', cayenne_lpp: 'Standard Cayenne format', js: 'Short script', javascript: 'Full decodeUplink script', ascii: 'Readable text',
  tlv_custom: 'Your own tags and lengths', bitstream: 'Values of any bit length', csv: 'Values with a separator', kv: 'name=value text', regex: 'Pick parts out of text', json: 'JSON objects', cbor: 'Compact binary objects', msgpack: 'Compact binary objects',
};
