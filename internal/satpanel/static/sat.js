'use strict';
// LoRaHub satellite page: sign-in, top bar and tabs. The pages themselves are
// in host-ui.js, shared with the hub console's Device section.

const $ = (s, r = document) => r.querySelector(s);

function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v == null || v === false) continue;
    if (k === 'class') el.className = v;
    else if (k === 'style') Object.assign(el.style, v);
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else if (k === 'value') el.value = v;
    else el.setAttribute(k, v === true ? '' : v);
  }
  for (const c of kids.flat(Infinity)) {
    if (c == null || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

function logo() {
  const s = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  s.setAttribute('viewBox', '0 0 32 32');
  s.innerHTML = '<rect width="32" height="32" rx="7" fill="#1a73e8"/><path d="M9 22a10 10 0 0 1 14 0M12 19a6 6 0 0 1 8 0" stroke="white" stroke-width="2.4" fill="none" stroke-linecap="round"/><circle cx="16" cy="24" r="2" fill="white"/>';
  return s;
}

class HttpError extends Error { constructor(msg, status) { super(msg); this.status = status; } }

async function api(method, path, body) {
  const opt = { method, credentials: 'same-origin', headers: { 'X-Requested-With': 'lorahub-sat' } };
  if (body !== undefined) { opt.headers['Content-Type'] = 'application/json'; opt.body = JSON.stringify(body); }
  const r = await fetch('/api' + path, opt);
  let j = null;
  try { j = await r.json(); } catch (e) { /* empty */ }
  if (r.status === 401 && path !== '/login' && path !== '/session') { App.showLogin(); throw new HttpError('sign in first', 401); }
  if (!r.ok) throw new HttpError((j && j.error) || r.statusText, r.status);
  return j;
}

function toast(msg, err) {
  const t = h('div', { class: 'toast' + (err ? ' err' : '') }, msg);
  $('#toasts').append(t);
  setTimeout(() => t.remove(), err ? 7000 : 3500);
}

const App = {
  cleanups: [],
  session: null,

  async start() {
    let s;
    try { s = await api('GET', '/session'); } catch (e) {
      if (e.status === 401) { this.showLogin(); return; }
      document.body.textContent = 'The satellite page does not answer: ' + e.message; return;
    }
    this.session = s;
    this.shell();
    window.addEventListener('hashchange', () => this.route());
    this.route();
  },

  stop() { this.cleanups.forEach((f) => { try { f(); } catch (e) { /* ignore */ } }); this.cleanups = []; },

  async showLogin() {
    this.stop();
    let host = '';
    try { const r = await fetch('/api/session'); host = (await r.json()).hostname || ''; } catch (e) { /* offline */ }
    const pw = h('input', { type: 'password', autocomplete: 'current-password' });
    const err = h('div', { class: 'small', style: { color: 'var(--danger)', minHeight: '18px' } });
    const btn = h('button', { class: 'btn primary', type: 'submit' }, 'Sign in');
    const submit = async (e) => {
      e.preventDefault(); btn.disabled = true; err.textContent = '';
      try { await api('POST', '/login', { password: pw.value }); location.reload(); } catch (ex) { err.textContent = ex.message; btn.disabled = false; pw.select(); }
    };
    $('#app').replaceChildren(h('div', { class: 'auth-wrap' }, h('div', { class: 'auth' },
      h('div', { class: 'brand' }, logo(), 'LoRaHub satellite'),
      h('form', { class: 'card card-body stack', onsubmit: submit },
        h('h2', null, 'Sign in to ' + (host || 'this satellite')),
        h('p', { class: 'muted small', style: { margin: 0 } }, 'Use the Bobcat\'s root password, the one you use for SSH or the console.'),
        h('div', { class: 'field' }, h('label', null, 'User'), h('input', { type: 'text', value: 'root', disabled: true })),
        h('div', { class: 'field' }, h('label', null, 'Password'), pw), err, btn))));
    pw.focus();
  },

  shell() {
    const s = this.session;
    this.tabs = h('div', { class: 'tabs' });
    this.view = h('div', { class: 'stack' });
    $('#app').replaceChildren(
      h('div', { class: 'topbar' },
        h('a', { class: 'brand', href: '#/' }, logo(), 'LoRaHub satellite', h('small', null, s.hostname)),
        h('span', { class: 'spacer' }),
        this.dot = h('span', { class: 'conn-dot', style: { background: 'var(--faint)' }, title: 'checking the tunnel to the hub' }),
        h('button', { class: 'tb-btn', onclick: async () => { await api('POST', '/logout', {}); location.reload(); } }, 'Sign out')),
      h('div', { class: 'sat-main' },
        h('div', { class: 'page-head' }, h('div', null, h('h1', null, s.hostname),
          h('div', { class: 'sub' }, 'Satellite of the LoRaHub Bobcat. Radio settings are changed on the hub: Gateways, Remote config.', s.version ? h('span', { class: 'badge' }, 'v' + s.version) : null))),
        this.tabs, this.view));
  },

  route() {
    this.stop();
    const tab = (location.hash.replace(/^#\/?/, '') || 'status').split('/')[0];
    this.tabs.replaceChildren(...HostUI.tabs('satellite').map(([k, l]) => h('a', { href: '#/' + k, class: k === tab ? 'active' : '' }, l)));
    HostUI.render(tab, this.view, {
      api, toast, role: 'satellite', hostname: this.session.hostname,
      url: (p) => '/api' + p,
      onLeave: (f) => this.cleanups.push(f),
      setDot: (ok) => this.setDot(ok),
    });
  },

  setDot(ok) {
    if (!this.dot) return;
    this.dot.style.background = '';
    this.dot.className = 'conn-dot' + (ok ? ' on' : '');
    this.dot.title = ok ? 'the hub answers through the tunnel' : 'the hub does not answer';
  },
};

document.addEventListener('DOMContentLoaded', () => App.start());
