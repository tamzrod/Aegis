'use strict';

// ---------- state ----------
let originalConfig = null;   // last-applied config (from server)
let workingConfig  = null;   // working copy (modified locally before apply)

// ---------- helpers ----------
function deepCopy(obj) {
  return JSON.parse(JSON.stringify(obj));
}

function configsEqual(a, b) {
  return JSON.stringify(a) === JSON.stringify(b);
}

// ---------- pending-change banner ----------
function setPendingState(isPending) {
  const banner = document.getElementById('pending-banner');
  if (isPending) {
    banner.classList.remove('hidden');
  } else {
    banner.classList.add('hidden');
  }
}

function checkPending() {
  setPendingState(!configsEqual(originalConfig, workingConfig));
}

// ---------- toast ----------
function showToast(msg) {
  const toast = document.getElementById('toast');
  toast.textContent = msg;
  toast.classList.remove('hidden');
  clearTimeout(toast._timer);
  toast._timer = setTimeout(() => toast.classList.add('hidden'), 2500);
}

// ---------- render ----------
function renderDevice(device) {
  const src = device.source;
  document.getElementById('src-endpoint').textContent    = src.endpoint    || '—';
  document.getElementById('src-unit-id').textContent     = src.unit_id     ?? '—';
  document.getElementById('src-timeout').textContent     = src.timeout_ms  ?? '—';
  document.getElementById('src-device-name').textContent = src.device_name || '—';

  const readsList = document.getElementById('reads-list');
  readsList.innerHTML = '';
  (device.reads || []).forEach(r => {
    const li = document.createElement('li');
    li.textContent = `FC${r.fc} | Address: ${r.address} | Quantity: ${r.quantity} | Interval: ${r.interval_ms} ms`;
    readsList.appendChild(li);
  });

  const tgt = device.target;
  document.getElementById('tgt-port').textContent           = tgt.port            ?? '—';
  document.getElementById('tgt-unit-id').textContent        = tgt.unit_id         ?? '—';
  document.getElementById('tgt-status-unit-id').textContent = tgt.status_unit_id  ?? '—';
  document.getElementById('tgt-status-slot').textContent    = tgt.status_slot     ?? '—';
  document.getElementById('tgt-mode').textContent           = tgt.mode            || '—';
}

function selectDevice(key) {
  if (!workingConfig) return;
  const device = workingConfig.devices.find(d => d.key === key);
  if (!device) return;

  document.querySelectorAll('#device-list li').forEach(li => {
    li.classList.toggle('selected', li.dataset.key === key);
  });

  renderDevice(device);
}

function renderDeviceList() {
  const list = document.getElementById('device-list');
  list.innerHTML = '';
  workingConfig.devices.forEach(d => {
    const li = document.createElement('li');
    li.textContent = d.display_name || d.key;
    li.dataset.key = d.key;
    li.addEventListener('click', () => selectDevice(d.key));
    list.appendChild(li);
  });
}

function renderAll() {
  renderDeviceList();
  if (workingConfig.selected_key) {
    selectDevice(workingConfig.selected_key);
  }
}

// ---------- load ----------
async function loadView() {
  try {
    const res = await fetch('/api/config/view');
    if (!res.ok) throw new Error('HTTP ' + res.status);
    originalConfig = await res.json();
    workingConfig  = deepCopy(originalConfig);
    renderAll();
    setPendingState(false);
  } catch (e) {
    showToast('Load failed: ' + e.message);
  }
}

// ---------- Apply Config ----------
document.getElementById('btn-apply-config').addEventListener('click', async () => {
  try {
    const res = await fetch('/api/config/apply', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(workingConfig),
    });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) {
      showToast('Apply failed: ' + (body.error || 'HTTP ' + res.status));
      return;
    }
    originalConfig = deepCopy(workingConfig);
    setPendingState(false);
    showToast('Configuration applied.');
  } catch (e) {
    showToast('Apply failed: ' + e.message);
  }
});

// ---------- Discard Changes ----------
document.getElementById('btn-discard').addEventListener('click', () => {
  workingConfig = deepCopy(originalConfig);
  renderAll();
  setPendingState(false);
  showToast('Changes discarded.');
});

// ---------- disabled action buttons ----------
function disabledClick() {
  showToast('Editing available in next phase');
}

document.getElementById('btn-add').addEventListener('click', disabledClick);
document.getElementById('btn-delete').addEventListener('click', disabledClick);
document.getElementById('btn-add-read').addEventListener('click', disabledClick);

loadView();
