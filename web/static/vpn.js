/* LoRaHub console: WireGuard and Tailscale. */
'use strict';

const VPN_STATES = { Running: 'Connected', NeedsLogin: 'Waiting for sign-in', NeedsMachineAuth: 'Waiting for approval', Stopped: 'Turned off', Starting: 'Starting', NoState: 'Starting', NotRunning: 'Service not running' };

function vpnBytes(n) {
  if (!n) return '0 B';
  const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), 4);
  return (n / 1024 ** i).toFixed(i ? 1 : 0) + ' ' + ['B', 'KB', 'MB', 'GB', 'TB'][i];
}
const vpnFacts = (pairs) => h('dl', { class: 'kv small' }, pairs.filter((p) => p[1]).map(([k, v, mono]) => [h('dt', null, k), h('dd', { class: mono ? 'mono' : '', style: { wordBreak: 'break-all' } }, v)]));
const vpnTitle = (text, badge) => h('div', { class: 'row between' }, h('h2', { style: { margin: 0 } }, text), badge);
const vpnBadge = (on, text) => h('span', { class: 'badge ' + (on ? 'ok' : '') }, text);
function vpnButton(label, fn, cls = '') {
  return h('button', { class: 'btn ' + cls, type: 'button', onclick: async (e) => {
    const b = e.currentTarget;
    b.disabled = true;
    try { await fn(); } catch (err) { errToast(err); }
    b.disabled = false;
  } }, label);
}

async function vpnPage(active, path, paint) {
  const body = h('div', { class: 'stack' });
  const refresh = async () => { paint(body, await GET(path), refresh); };
  await refresh();
  const timer = setInterval(() => { if (!document.hidden) refresh().catch(() => {}); }, 5000);
  App.onLeave(() => clearInterval(timer));
  return h('div', { class: 'stack' },
    pageHead([['WireGuard & Tailscale']], 'WireGuard & Tailscale', ['How gateways and your own devices reach this one from any network']),
    tabs('#/vpn', [['', 'WireGuard'], ['tailscale', 'Tailscale']], active), body);
}

function paintWireGuard(body, st, refresh) {
  if (!st.installed) { body.replaceChildren(h('div', { class: 'empty card' }, 'WireGuard is not installed on this gateway.')); return; }
  const service = async (cmd, done) => { await api('POST', '/vpn/wireguard/service', { cmd }); toast(done); await refresh(); };
  const warnStop = 'Gateways connected through the tunnel, like the LPS8, stop reporting until you start it again.';
  const actions = st.up ? [
    vpnButton('Restart', async () => { if (await confirmModal('Restart WireGuard?', 'Gateways on the tunnel drop out for a few seconds.', 'Restart', false)) await service('restart', 'WireGuard restarted'); }),
    vpnButton('Stop', async () => { if (await confirmModal('Stop WireGuard?', warnStop, 'Stop WireGuard')) await service('stop', 'WireGuard stopped'); }),
  ] : [vpnButton('Start', () => service('start', 'WireGuard started'), 'primary')];
  const online = st.peers.filter((p) => p.connected).length;
  body.replaceChildren(
    h('div', { class: 'card card-body stack' },
      vpnTitle('This gateway', vpnBadge(st.up, st.up ? 'Running' : 'Stopped')),
      vpnFacts([['Interface', st.interface, true], ['Tunnel address', st.address, true], ['Listens on', st.listen_port ? 'UDP ' + st.listen_port : '', true], ['Public key', st.public_key, true]]),
      h('div', { class: 'row' }, actions)),
    h('div', { class: 'card' },
      h('div', { class: 'card-body' }, vpnTitle('Devices on the tunnel', h('span', { class: 'small muted' }, `${online} of ${st.peers.length} connected`))),
      st.peers.length ? h('div', { class: 'table-wrap' }, h('table', { class: 't' },
        h('thead', null, h('tr', null, ['Device', 'Tunnel address', 'Status', 'Connecting from', 'Received', 'Sent'].map((x) => h('th', null, x)))),
        h('tbody', null, st.peers.map((p) => h('tr', null,
          h('td', null, h('b', null, p.name || p.public_key.slice(0, 12) + '…')),
          h('td', { class: 'mono small' }, p.allowed_ips.join(', ')),
          h('td', { class: 'small' }, onlineBadge(p.connected, p.last_handshake)),
          h('td', { class: 'mono small' }, p.endpoint || '—'),
          h('td', { class: 'small nowrap' }, vpnBytes(p.rx_bytes)),
          h('td', { class: 'small nowrap' }, vpnBytes(p.tx_bytes))))))) : h('div', { class: 'empty' }, 'No devices are set up on the tunnel yet.')),
    h('div', { class: 'callout' }, st.listen_port ? `For a gateway on another network to connect, forward UDP ${st.listen_port} on your router to this gateway.` : 'Start WireGuard to see how to connect from another network.'));
}

function paintTailscale(body, st, refresh) {
  if (!st.installed) { body.replaceChildren(h('div', { class: 'empty card' }, 'Tailscale is not installed on this gateway.')); return; }
  const run = async (action, done) => { await api('POST', '/vpn/tailscale/' + action); if (done) toast(done); await refresh(); };
  const running = st.state === 'Running';
  const cards = [];
  if (st.state === 'NeedsLogin') {
    cards.push(h('div', { class: 'card card-body stack' },
      vpnTitle('Sign in to Tailscale', vpnBadge(false, VPN_STATES.NeedsLogin)),
      st.auth_url ? [h('p', { style: { margin: 0 } }, 'Open the link and sign in. If the next page asks you to connect this device, press Connect. This page updates by itself once it is done.'),
        h('div', { class: 'mono small', style: { wordBreak: 'break-all' } }, st.auth_url),
        h('div', { class: 'row' }, h('a', { class: 'btn primary', href: st.auth_url, target: '_blank', rel: 'noopener' }, 'Open sign-in page'), vpnButton('Get a new link', () => run('new-link', 'New link ready')))]
        : [h('p', { style: { margin: 0 } }, 'Get a link to add this gateway to your Tailscale account.'), h('div', { class: 'row' }, vpnButton('Get sign-in link', () => run('connect'), 'primary'))]));
  } else {
    const routes = st.advertised_routes.map((r) => h('div', { class: 'row', style: { gap: '8px' } }, h('span', { class: 'mono' }, r), st.approved_routes.includes(r) ? vpnBadge(true, 'Approved') : vpnBadge(false, 'Needs approval')));
    const pending = running && st.advertised_routes.some((r) => !st.approved_routes.includes(r));
    cards.push(h('div', { class: 'card card-body stack' },
      vpnTitle('This gateway', vpnBadge(running, VPN_STATES[st.state] || st.state)),
      vpnFacts([['Name', st.dns_name || st.hostname, true], ['Tailscale address', st.ips.join(', '), true], ['Version', st.version]]),
      routes.length ? h('div', { class: 'stack', style: { gap: '4px' } }, h('div', { class: 'small muted' }, 'Tunnel shared with your Tailscale devices'), routes) : null,
      pending ? h('div', { class: 'callout warn' }, 'Approve this route so your Tailscale devices can reach the gateways on the tunnel: ', h('a', { href: 'https://login.tailscale.com/admin/machines', target: '_blank', rel: 'noopener' }, 'open Machines'), ', then this gateway, then Edit route settings.') : null,
      h('div', { class: 'row' },
        running ? vpnButton('Turn off', async () => { if (await confirmModal('Turn off Tailscale?', 'Your Tailscale devices lose access to this gateway. WireGuard keeps working.', 'Turn off', false)) await run('disconnect', 'Tailscale turned off'); }) : vpnButton('Turn on', () => run('connect'), 'primary'),
        vpnButton('Sign out', async () => { if (await confirmModal('Sign out of Tailscale?', 'This gateway leaves your Tailscale account until you sign in again.', 'Sign out')) await run('signout', 'Signed out'); }))));
    if (st.health.length) cards.push(h('div', { class: 'callout warn' }, st.health.map((x) => h('div', null, x))));
  }
  if (running) {
    cards.push(h('div', { class: 'card' },
      h('div', { class: 'card-body' }, vpnTitle('Your Tailscale devices', h('span', { class: 'small muted' }, `${st.peers.filter((p) => p.online).length} of ${st.peers.length} online`))),
      st.peers.length ? h('div', { class: 'table-wrap' }, h('table', { class: 't' },
        h('thead', null, h('tr', null, ['Device', 'Address', 'Status', 'System'].map((x) => h('th', null, x)))),
        h('tbody', null, st.peers.slice().sort((a, b) => Number(b.online) - Number(a.online) || a.name.localeCompare(b.name)).map((p) => h('tr', null,
          h('td', null, h('b', null, p.name)), h('td', { class: 'mono small' }, p.ip || '—'),
          h('td', { class: 'small' }, onlineBadge(p.online, p.last_seen)), h('td', { class: 'small' }, p.os || '—')))))) : h('div', { class: 'empty' }, 'No other devices on your Tailscale account yet.')));
  }
  cards.push(h('div', { class: 'callout' }, 'Tailscale runs on the Bobcat only, because the LPS8 is too small for it. Once the route is approved, your Tailscale devices reach the LPS8 at 10.77.0.2 through the Bobcat.'));
  body.replaceChildren(...cards);
}

App.route('/vpn', () => vpnPage('', '/vpn/wireguard', paintWireGuard));
App.route('/vpn/tailscale', () => vpnPage('tailscale', '/vpn/tailscale', paintTailscale));
