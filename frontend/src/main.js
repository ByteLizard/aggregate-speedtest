import './style.css';
import { CLIStatus, InstallCLI, NearbyServers, Run, Stop } from '../wailsjs/go/main/App';
import { EventsOn } from '../wailsjs/runtime/runtime';

const app = document.getElementById('app');
app.innerHTML = `
  <header>
    <h1>Aggregate Speedtest</h1>
    <p class="sub">Multi-gig lines outrun single test servers. Run several Ookla tests
    in parallel and read the <em>sum</em>. Hardwired ethernet only — Wi-Fi bottlenecks
    before your line does.</p>
  </header>

  <section id="setup" class="card">
    <div id="cli-row">
      <span id="cli-status">Checking for Ookla CLI…</span>
      <button id="install-btn" hidden>Download Ookla CLI</button>
    </div>
    <p class="fine" id="license-note" hidden>
      This downloads Speedtest® CLI from Ookla and runs it with
      <code>--accept-license --accept-gdpr</code>. By continuing you accept
      <a href="https://www.speedtest.net/about/eula" target="_blank">Ookla's EULA</a>,
      <a href="https://www.speedtest.net/about/terms" target="_blank">Terms of Use</a> and
      <a href="https://www.speedtest.net/about/privacy" target="_blank">Privacy Policy</a>.
    </p>
  </section>

  <section id="servers" class="card" hidden>
    <div class="row">
      <h2>Servers</h2>
      <button id="load-btn">Load nearby servers</button>
      <input id="manual-id" type="number" placeholder="server id" min="1" />
      <button id="add-btn">Add</button>
    </div>
    <div id="server-list"></div>
    <p class="fine">Pick 2–4 servers. More legs ≠ more truth once the line saturates,
    and each test moves multiple GB.</p>
  </section>

  <section id="runbox" class="card" hidden>
    <div class="row">
      <button id="run-btn" class="primary">Run aggregate test</button>
      <button id="stop-btn" hidden>Stop</button>
    </div>
    <div id="aggregate" hidden>
      <div class="agg-item"><span class="agg-label" id="agg-down-label">↓ live sum</span><span id="agg-down" class="agg-val">0.00</span><span class="agg-unit">Gbps</span></div>
      <div class="agg-item"><span class="agg-label" id="agg-up-label">↑ live sum</span><span id="agg-up" class="agg-val">0.00</span><span class="agg-unit">Gbps</span></div>
    </div>
    <div id="legs"></div>
    <p class="fine" id="run-note" hidden>The aggregate is the <strong>peak concurrent sum</strong> —
    the highest the legs' instantaneous rates ever summed to at one moment. Unlike adding per-leg
    averages (whose phases don't align), it can never exceed what your line actually carried.</p>
  </section>
`;

const $ = (id) => document.getElementById(id);
const selected = new Map(); // id -> label
const legs = new Map();     // id -> {curDown, curUp, finalDown, finalUp, done, name}
let peakDown = 0, peakUp = 0;

function gbps(bytesPerSec) { return (bytesPerSec * 8) / 1e9; }
function fmt(v) { return v.toFixed(2); }

function renderSelected() {
  const list = $('server-list');
  const chips = [...selected.entries()]
    .map(([id, label]) => `<span class="chip" data-id="${id}">${label} <b>×</b></span>`)
    .join('');
  list.querySelector('.chips')?.remove();
  list.insertAdjacentHTML('afterbegin', `<div class="chips">${chips || '<span class="fine">none selected</span>'}</div>`);
  $('runbox').hidden = selected.size === 0;
}

function addServer(id, label) {
  selected.set(Number(id), label);
  renderSelected();
}

$('server-list').addEventListener('click', (e) => {
  const chip = e.target.closest('.chip');
  if (chip) { selected.delete(Number(chip.dataset.id)); renderSelected(); }
  const opt = e.target.closest('.server-opt');
  if (opt) addServer(opt.dataset.id, opt.dataset.label);
});

async function refreshCLI() {
  const st = await CLIStatus();
  if (st.present) {
    $('cli-status').textContent = `✓ ${st.version}`;
    $('install-btn').hidden = true;
    $('license-note').hidden = true;
    $('servers').hidden = false;
  } else {
    $('cli-status').textContent = 'Ookla Speedtest CLI not installed.';
    $('install-btn').hidden = false;
    $('license-note').hidden = false;
  }
}

$('install-btn').onclick = async () => {
  $('install-btn').disabled = true;
  $('cli-status').textContent = 'Downloading…';
  try { await InstallCLI(); } catch (e) { $('cli-status').textContent = `Failed: ${e}`; }
  $('install-btn').disabled = false;
  refreshCLI();
};

$('load-btn').onclick = async () => {
  $('load-btn').disabled = true;
  try {
    const servers = await NearbyServers();
    const html = servers.slice(0, 10).map((s) =>
      `<button class="server-opt" data-id="${s.id}" data-label="${s.name} (${s.location})">
         ${s.name} — ${s.location} <span class="fine">#${s.id}</span></button>`).join('');
    $('server-list').insertAdjacentHTML('beforeend', `<div class="opts">${html}</div>`);
  } catch (e) { alert(e); }
  $('load-btn').disabled = false;
};

$('add-btn').onclick = () => {
  const v = $('manual-id').value;
  if (v) { addServer(v, `#${v}`); $('manual-id').value = ''; }
};

function renderLegs(final = false) {
  let down = 0, up = 0;
  const rows = [...legs.entries()].map(([id, l]) => {
    down += l.curDown; up += l.curUp;
    const showDown = l.done ? l.finalDown : l.curDown;
    const showUp = l.done ? l.finalUp : l.curUp;
    return `<div class="leg ${l.done ? 'done' : ''} ${l.error ? 'err' : ''}">
      <span class="leg-name">${l.name || '#' + id}</span>
      <span class="leg-phase">${l.error ? 'error' : l.phase || '…'}</span>
      <span>↓ ${fmt(showDown)}</span><span>↑ ${fmt(showUp)}</span>
    </div>`;
  }).join('');
  $('legs').innerHTML = rows;
  // The honest aggregate: instantaneous rates summed at the same moment,
  // tracked at their peak. Sums of per-leg averages double-count time
  // windows where phases didn't overlap and can exceed the physical wire.
  peakDown = Math.max(peakDown, down);
  peakUp = Math.max(peakUp, up);
  if (final) {
    $('agg-down-label').textContent = '↓ peak concurrent';
    $('agg-up-label').textContent = '↑ peak concurrent';
    $('agg-down').textContent = fmt(peakDown);
    $('agg-up').textContent = fmt(peakUp);
  } else {
    $('agg-down').textContent = fmt(down);
    $('agg-up').textContent = fmt(up);
  }
}

EventsOn('leg', (ev) => {
  const l = legs.get(ev.id) || { curDown: 0, curUp: 0, finalDown: 0, finalUp: 0 };
  if (ev.type === 'testStart' && ev.server) l.name = `${ev.server.name} (${ev.server.location})`;
  if (ev.type === 'download') { l.curDown = gbps(ev.download.bandwidth); l.phase = 'download'; }
  if (ev.type === 'upload') {
    l.curUp = gbps(ev.upload.bandwidth);
    l.curDown = 0; // download phase over — stop counting it in the live sum
    l.phase = 'upload';
  }
  if (ev.type === 'result') {
    l.finalDown = gbps(ev.download.bandwidth); l.finalUp = gbps(ev.upload.bandwidth);
    l.curDown = 0; l.curUp = 0; // finished legs contribute nothing live
    l.done = true; l.phase = 'done';
  }
  if (ev.type === 'error') { l.error = true; l.message = ev.message; l.curDown = 0; l.curUp = 0; }
  legs.set(ev.id, l);
  renderLegs();
});

EventsOn('run:done', () => {
  $('run-btn').hidden = false;
  $('stop-btn').hidden = true;
  renderLegs(true);
});

$('run-btn').onclick = async () => {
  legs.clear();
  peakDown = 0; peakUp = 0;
  $('agg-down-label').textContent = '↓ live sum';
  $('agg-up-label').textContent = '↑ live sum';
  for (const id of selected.keys()) legs.set(id, { curDown: 0, curUp: 0, finalDown: 0, finalUp: 0, name: selected.get(id) });
  renderLegs();
  $('aggregate').hidden = false;
  $('run-note').hidden = false;
  try {
    await Run([...selected.keys()]);
    $('run-btn').hidden = true;
    $('stop-btn').hidden = false;
  } catch (e) { alert(e); }
};

$('stop-btn').onclick = async () => { await Stop(); };

refreshCLI();
