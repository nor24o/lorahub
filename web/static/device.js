'use strict';
// Device: this Bobcat's own settings (network and Wi-Fi in every mode, name and
// time, SD card, lights, system and logs). The pages are host-ui.js, the same
// ones a satellite shows on its own page; the API is internal/satpanel at /api/host.

async function devicePage(tab) {
  const body = h('div', { class: 'stack' });
  let hostname = '';
  try { hostname = (await GET('/host/general')).hostname; } catch (e) { /* shown by the page */ }
  HostUI.render(tab, body, {
    api: (method, path, b) => api(method, '/host' + path, b),
    url: (p) => '/api/host' + p,
    toast: (m, isErr) => toast(m, isErr),
    onLeave: (f) => App.onLeave(f),
    role: 'hub',
    hostname,
  });
  const items = HostUI.tabs('hub').map(([k, l]) => [k === 'status' ? '' : k, l]);
  return h('div', { class: 'stack' },
    pageHead([['Device']], 'Device' + (hostname ? ': ' + hostname : ''), ['This Bobcat\'s own settings: network, Wi-Fi, name and time, SD card, lights and system']),
    tabs('#/device', items, tab === 'status' ? '' : tab), body);
}

// Help: every function explained, with an animated example (help.js).
async function helpPage() {
  const body = h('div');
  LHHelp.mount(body, { set: 'hub', onLeave: (f) => App.onLeave(f), base: '#/help/' });
  return h('div', { class: 'stack' }, pageHead([['Help']], 'Help', ['Every function of LoRaHub and the Bobcats, each with an animated example']), body);
}
App.route('/help', helpPage);
App.route('/help/:topic', helpPage);

App.route('/device', () => devicePage('status'));
App.route('/device/:tab', ({ tab }) => devicePage(tab));
