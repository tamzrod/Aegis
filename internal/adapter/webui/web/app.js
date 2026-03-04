'use strict';

let currentView = null;

function showToast(msg) {
  const toast = document.getElementById('toast');
  toast.textContent = msg;
  toast.classList.remove('hidden');
  clearTimeout(toast._timer);
  toast._timer = setTimeout(() => toast.classList.add('hidden'), 2500);
}

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
  if (!currentView) return;
  const device = currentView.devices.find(d => d.key === key);
  if (!device) return;

  document.querySelectorAll('#device-list li').forEach(li => {
    li.classList.toggle('selected', li.dataset.key === key);
  });

  renderDevice(device);
}

function renderDeviceList(view) {
  const list = document.getElementById('device-list');
  list.innerHTML = '';
  view.devices.forEach(d => {
    const li = document.createElement('li');
    li.textContent = d.display_name || d.key;
    li.dataset.key = d.key;
    li.addEventListener('click', () => selectDevice(d.key));
    list.appendChild(li);
  });
}

async function loadView() {
  try {
    const res = await fetch('/api/config/view');
    if (!res.ok) throw new Error('HTTP ' + res.status);
    currentView = await res.json();
    renderDeviceList(currentView);
    if (currentView.selected_key) {
      selectDevice(currentView.selected_key);
    }
  } catch (e) {
    showToast('Load failed: ' + e.message);
  }
}

function disabledClick() {
  showToast('Editing available in next phase');
}

document.getElementById('btn-add').addEventListener('click', disabledClick);
document.getElementById('btn-delete').addEventListener('click', disabledClick);
document.getElementById('btn-add-read').addEventListener('click', disabledClick);

loadView();
