'use strict';

// ---------- constants ----------
const DEFAULT_TARGET_MODE = 'B';

// FC options for the read editor dropdown.
const FC_OPTIONS = [
  { value: 1, label: 'Read Coils (1)' },
  { value: 2, label: 'Read Discrete Inputs (2)' },
  { value: 3, label: 'Read Holding Registers (3)' },
  { value: 4, label: 'Read Input Registers (4)' },
];

// ---------- state ----------
let originalConfig    = null;   // last-applied config (from server)
let workingConfig     = null;   // working copy (modified locally before apply)
let selectedDeviceKey = null;   // key of the device currently shown in the right panel
let deviceStatuses    = {};     // maps device key → status string ("online", "error", "offline", "warning")

// ---------- helpers ----------
function deepCopy(obj) {
  return JSON.parse(JSON.stringify(obj));
}

function getSelectedDevice() {
  if (!workingConfig || !selectedDeviceKey) return null;
  return workingConfig.devices.find(d => d.key === selectedDeviceKey) || null;
}

function getSelectedDeviceIndex() {
  if (!workingConfig || !selectedDeviceKey) return -1;
  return workingConfig.devices.findIndex(d => d.key === selectedDeviceKey);
}

// ---------- toast ----------
function showToast(msg) {
  const toast = document.getElementById('toast');
  toast.textContent = msg;
  toast.classList.remove('hidden');
  clearTimeout(toast._timer);
  toast._timer = setTimeout(() => toast.classList.add('hidden'), 2500);
}

// ---------- Source section ----------

function renderSourceView(device) {
  const src = device.source || {};

  const actEl = document.getElementById('src-actions');
  actEl.innerHTML = '';
  const btnEdit = document.createElement('button');
  btnEdit.className = 'btn-sm';
  btnEdit.textContent = 'Edit';
  btnEdit.addEventListener('click', () => renderSourceEdit(device));
  actEl.appendChild(btnEdit);

  const content = document.getElementById('source-content');
  const table = document.createElement('table');
  table.className = 'field-table';
  [
    ['Endpoint',     src.endpoint    || '—'],
    ['Unit ID',      src.unit_id     ?? '—'],
    ['Timeout (ms)', src.timeout_ms  ?? '—'],
    ['Device Name',  src.device_name || '—'],
  ].forEach(([label, value]) => {
    const tr = document.createElement('tr');
    tr.innerHTML = `<th>${label}</th><td>${value}</td>`;
    table.appendChild(tr);
  });
  content.innerHTML = '';
  content.appendChild(table);
}

function renderSourceEdit(device) {
  const src = device.source || {};

  const actEl = document.getElementById('src-actions');
  actEl.innerHTML = '';
  const btnSave   = document.createElement('button');
  btnSave.className = 'btn-sm btn-save';
  btnSave.textContent = 'Save';
  const btnCancel = document.createElement('button');
  btnCancel.className = 'btn-sm';
  btnCancel.textContent = 'Cancel';
  actEl.appendChild(btnSave);
  actEl.appendChild(btnCancel);

  const content = document.getElementById('source-content');
  const table   = document.createElement('table');
  table.className = 'field-table';

  const fieldDefs = [
    { label: 'Endpoint',     key: 'endpoint',    type: 'text',   value: src.endpoint    || '' },
    { label: 'Unit ID',      key: 'unit_id',     type: 'number', value: src.unit_id     ?? 0 },
    { label: 'Timeout (ms)', key: 'timeout_ms',  type: 'number', value: src.timeout_ms  ?? 0 },
    { label: 'Device Name',  key: 'device_name', type: 'text',   value: src.device_name || '' },
  ];
  const inputs = {};
  fieldDefs.forEach(f => {
    const tr  = document.createElement('tr');
    const th  = document.createElement('th');
    th.textContent = f.label;
    const td  = document.createElement('td');
    const inp = document.createElement('input');
    inp.className = 'field-input';
    inp.type      = f.type;
    inp.value     = f.value;
    if (f.type === 'number') inp.min = '0';
    inputs[f.key] = inp;
    td.appendChild(inp);
    tr.appendChild(th);
    tr.appendChild(td);
    table.appendChild(tr);
  });
  content.innerHTML = '';
  content.appendChild(table);
  inputs['endpoint'].focus();

  btnSave.addEventListener('click', () => {
    const idx = getSelectedDeviceIndex();
    if (idx < 0) return;
    workingConfig.devices[idx].source = {
      endpoint:    inputs['endpoint'].value.trim(),
      unit_id:     parseInt(inputs['unit_id'].value,    10) || 0,
      timeout_ms:  parseInt(inputs['timeout_ms'].value, 10) || 0,
      device_name: inputs['device_name'].value.trim(),
    };
    if (workingConfig.devices[idx].source.device_name) {
      workingConfig.devices[idx].display_name = workingConfig.devices[idx].source.device_name;
    }
    renderDeviceList();
    renderSourceView(workingConfig.devices[idx]);
  });

  btnCancel.addEventListener('click', () => renderSourceView(device));
}

// ---------- Target section ----------

function renderTargetView(device) {
  const tgt = device.target || {};

  const actEl = document.getElementById('tgt-actions');
  actEl.innerHTML = '';
  const btnEdit = document.createElement('button');
  btnEdit.className = 'btn-sm';
  btnEdit.textContent = 'Edit';
  btnEdit.addEventListener('click', () => renderTargetEdit(device));
  actEl.appendChild(btnEdit);

  const content = document.getElementById('target-content');
  const table   = document.createElement('table');
  table.className = 'field-table';
  [
    ['Port',           tgt.port            ?? '—'],
    ['Unit ID',        tgt.unit_id         ?? '—'],
    ['Status Unit ID', tgt.status_unit_id  ?? '—'],
    ['Status Slot',    tgt.status_slot     ?? '—'],
    ['Mode',           tgt.mode            || '—'],
  ].forEach(([label, value]) => {
    const tr = document.createElement('tr');
    tr.innerHTML = `<th>${label}</th><td>${value}</td>`;
    table.appendChild(tr);
  });
  content.innerHTML = '';
  content.appendChild(table);
}

function renderTargetEdit(device) {
  const tgt = device.target || {};

  const actEl = document.getElementById('tgt-actions');
  actEl.innerHTML = '';
  const btnSave   = document.createElement('button');
  btnSave.className = 'btn-sm btn-save';
  btnSave.textContent = 'Save';
  const btnCancel = document.createElement('button');
  btnCancel.className = 'btn-sm';
  btnCancel.textContent = 'Cancel';
  actEl.appendChild(btnSave);
  actEl.appendChild(btnCancel);

  const content = document.getElementById('target-content');
  const table   = document.createElement('table');
  table.className = 'field-table';

  const numFields = [
    { label: 'Port',           key: 'port',           value: tgt.port           ?? 0 },
    { label: 'Unit ID',        key: 'unit_id',        value: tgt.unit_id        ?? 0 },
    { label: 'Status Unit ID', key: 'status_unit_id', value: tgt.status_unit_id ?? 100 },
    { label: 'Status Slot',    key: 'status_slot',    value: tgt.status_slot    ?? 0 },
  ];
  const inputs = {};
  numFields.forEach(f => {
    const tr  = document.createElement('tr');
    const th  = document.createElement('th');
    th.textContent = f.label;
    const td  = document.createElement('td');
    const inp = document.createElement('input');
    inp.className = 'field-input';
    inp.type      = 'number';
    inp.min       = '0';
    inp.value     = f.value;
    inputs[f.key] = inp;
    td.appendChild(inp);
    tr.appendChild(th);
    tr.appendChild(td);
    table.appendChild(tr);
  });

  // Mode dropdown
  const modeTr = document.createElement('tr');
  const modeTh = document.createElement('th');
  modeTh.textContent = 'Mode';
  const modeTd  = document.createElement('td');
  const modeSel = document.createElement('select');
  modeSel.className = 'field-input';
  ['A', 'B', 'C'].forEach(m => {
    const opt = document.createElement('option');
    opt.value = m;
    opt.textContent = m;
    opt.selected = (tgt.mode === m);
    modeSel.appendChild(opt);
  });
  inputs['mode'] = modeSel;
  modeTd.appendChild(modeSel);
  modeTr.appendChild(modeTh);
  modeTr.appendChild(modeTd);
  table.appendChild(modeTr);

  content.innerHTML = '';
  content.appendChild(table);
  inputs['port'].focus();

  btnSave.addEventListener('click', () => {
    const idx = getSelectedDeviceIndex();
    if (idx < 0) return;
    const prev = workingConfig.devices[idx].target || {};
    workingConfig.devices[idx].target = {
      ...prev,
      port:           parseInt(inputs['port'].value,           10) || 0,
      unit_id:        parseInt(inputs['unit_id'].value,        10) || 0,
      status_unit_id: parseInt(inputs['status_unit_id'].value, 10) || 0,
      status_slot:    parseInt(inputs['status_slot'].value,    10) || 0,
      mode:           inputs['mode'].value,
    };
    renderTargetView(workingConfig.devices[idx]);
  });

  btnCancel.addEventListener('click', () => renderTargetView(device));
}

// ---------- Reads section ----------

function renderReadsList(device, editIndex) {
  // Header: [+ Add] button
  const actEl = document.getElementById('reads-actions');
  actEl.innerHTML = '';
  const btnAdd = document.createElement('button');
  btnAdd.className = 'btn-sm';
  btnAdd.textContent = '+ Add';
  btnAdd.addEventListener('click', () => {
    const idx = getSelectedDeviceIndex();
    if (idx < 0) return;
    workingConfig.devices[idx].reads = workingConfig.devices[idx].reads || [];
    workingConfig.devices[idx].reads.push({ fc: 3, address: 0, quantity: 1, interval_ms: 1000 });
    renderReadsList(workingConfig.devices[idx], workingConfig.devices[idx].reads.length - 1);
  });
  actEl.appendChild(btnAdd);

  // Read rows
  const content = document.getElementById('reads-content');
  const ul = document.createElement('ul');
  ul.className = 'reads-list';

  (device.reads || []).forEach((r, i) => {
    const li = document.createElement('li');

    if (i === editIndex) {
      // Inline editor
      li.className = 'read-editor';

      const fieldDiv = document.createElement('div');
      fieldDiv.className = 'read-editor-fields';
      const edDefs = [
        { label: 'FC',          key: 'fc',          value: r.fc          ?? 3    },
        { label: 'Address',     key: 'address',     value: r.address     ?? 0    },
        { label: 'Quantity',    key: 'quantity',    value: r.quantity    ?? 1    },
        { label: 'Interval ms', key: 'interval_ms', value: r.interval_ms ?? 1000 },
      ];
      const inp = {};
      edDefs.forEach(f => {
        const lbl = document.createElement('label');
        lbl.textContent = f.label;

        if (f.key === 'fc') {
          // FC field: dropdown selector
          const sel = document.createElement('select');
          sel.className = 'field-input';
          FC_OPTIONS.forEach(opt => {
            const option = document.createElement('option');
            option.value = opt.value;
            option.textContent = opt.label;
            option.selected = (f.value === opt.value);
            sel.appendChild(option);
          });
          inp[f.key] = sel;
          fieldDiv.appendChild(lbl);
          fieldDiv.appendChild(sel);
        } else {
          const input = document.createElement('input');
          input.className = 'field-input';
          input.type  = 'number';
          input.min   = '0';
          input.value = f.value;
          inp[f.key] = input;
          fieldDiv.appendChild(lbl);
          fieldDiv.appendChild(input);
        }
      });

      const actDiv = document.createElement('div');
      actDiv.className = 'read-editor-actions';
      const btnSave   = document.createElement('button');
      btnSave.className = 'btn-sm btn-save';
      btnSave.textContent = 'Save';
      const btnCancel = document.createElement('button');
      btnCancel.className = 'btn-sm';
      btnCancel.textContent = 'Cancel';
      actDiv.appendChild(btnSave);
      actDiv.appendChild(btnCancel);

      li.appendChild(fieldDiv);
      li.appendChild(actDiv);

      btnSave.addEventListener('click', () => {
        const devIdx = getSelectedDeviceIndex();
        if (devIdx < 0) return;
        workingConfig.devices[devIdx].reads[i] = {
          fc:          parseInt(inp['fc'].value,          10) || 1,
          address:     parseInt(inp['address'].value,     10) || 0,
          quantity:    parseInt(inp['quantity'].value,    10) || 1,
          interval_ms: parseInt(inp['interval_ms'].value, 10) || 1000,
        };
        renderReadsList(workingConfig.devices[devIdx]);
      });

      btnCancel.addEventListener('click', () => {
        const devIdx = getSelectedDeviceIndex();
        if (devIdx >= 0) renderReadsList(workingConfig.devices[devIdx]);
      });

    } else {
      // View row
      li.className = 'read-row';

      const span = document.createElement('span');
      span.className = 'read-row-text';
      span.textContent = `FC${r.fc} | Address: ${r.address} | Quantity: ${r.quantity} | Interval: ${r.interval_ms} ms`;

      const actDiv = document.createElement('div');
      actDiv.className = 'read-actions';
      const btnEdit = document.createElement('button');
      btnEdit.className = 'btn-sm';
      btnEdit.textContent = 'Edit';
      const btnDel = document.createElement('button');
      btnDel.className = 'btn-sm btn-danger';
      btnDel.textContent = 'Delete';
      actDiv.appendChild(btnEdit);
      actDiv.appendChild(btnDel);

      li.appendChild(span);
      li.appendChild(actDiv);

      btnEdit.addEventListener('click', () => {
        const devIdx = getSelectedDeviceIndex();
        if (devIdx >= 0) renderReadsList(workingConfig.devices[devIdx], i);
      });

      btnDel.addEventListener('click', () => {
        const devIdx = getSelectedDeviceIndex();
        if (devIdx < 0) return;
        workingConfig.devices[devIdx].reads.splice(i, 1);
        renderReadsList(workingConfig.devices[devIdx]);
      });
    }

    ul.appendChild(li);
  });

  content.innerHTML = '';
  content.appendChild(ul);
}

// ---------- Render selected device ----------

function renderDevice(device) {
  if (!device) {
    ['source-content', 'reads-content', 'target-content',
     'src-actions', 'reads-actions', 'tgt-actions'].forEach(id => {
      document.getElementById(id).innerHTML = '';
    });
    return;
  }
  renderSourceView(device);
  renderReadsList(device);
  renderTargetView(device);
}

// ---------- Device list ----------

function renderDeviceList() {
  const list = document.getElementById('device-list');
  list.innerHTML = '';
  workingConfig.devices.forEach(d => {
    const li = document.createElement('li');

    // Status dot
    const dot = document.createElement('span');
    dot.className = 'device-status-dot device-' + (deviceStatuses[d.key] || 'offline');
    li.appendChild(dot);

    const nameSpan = document.createElement('span');
    nameSpan.textContent = d.display_name || d.key;
    li.appendChild(nameSpan);

    li.dataset.key = d.key;
    if (d.key === selectedDeviceKey) li.classList.add('selected');
    li.addEventListener('click', () => selectDevice(d.key));
    list.appendChild(li);
  });
}

function selectDevice(key) {
  if (!workingConfig) return;
  selectedDeviceKey = key;
  const device = workingConfig.devices.find(d => d.key === key);
  if (!device) return;
  document.querySelectorAll('#device-list li').forEach(li => {
    li.classList.toggle('selected', li.dataset.key === key);
  });
  renderDevice(device);
}

function renderAll() {
  renderDeviceList();
  const keyToSelect = selectedDeviceKey || workingConfig.selected_key;
  if (keyToSelect && workingConfig.devices.find(d => d.key === keyToSelect)) {
    selectDevice(keyToSelect);
  } else if (workingConfig.devices.length > 0) {
    selectDevice(workingConfig.devices[0].key);
  } else {
    selectedDeviceKey = null;
    renderDevice(null);
  }
}

// ---------- Load ----------

async function loadDeviceStatuses() {
  try {
    const res = await fetch('/api/runtime/devices');
    if (!res.ok) return;
    const list = await res.json();
    deviceStatuses = {};
    if (Array.isArray(list)) {
      list.forEach(s => { deviceStatuses[s.id] = s.status; });
    }
  } catch (e) {
    // ignore — server may be unavailable
  }
}

async function loadView() {
  try {
    const res = await fetch('/api/config/view');
    if (!res.ok) throw new Error('HTTP ' + res.status);
    originalConfig = await res.json();
    workingConfig  = deepCopy(originalConfig);
    renderAll();
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
    showToast('Configuration saved.');
  } catch (e) {
    showToast('Apply failed: ' + e.message);
  }
});

// ---------- Restart Runtime ----------

document.getElementById('btn-restart-runtime').addEventListener('click', async () => {
  try {
    const res = await fetch('/api/restart', { method: 'POST' });
    if (!res.ok) {
      showToast('Restart failed: HTTP ' + res.status);
      return;
    }
    showToast('Runtime restarting…');
    setTimeout(() => loadView(), 1500);
  } catch (e) {
    showToast('Restart failed: ' + e.message);
  }
});

// ---------- Add Device ----------

document.getElementById('btn-add').addEventListener('click', () => {
  if (!workingConfig) return;
  let n = workingConfig.devices.length + 1;
  let key = `new-device-${n}`;
  while (workingConfig.devices.find(d => d.key === key)) {
    n++;
    key = `new-device-${n}`;
  }
  const newDevice = {
    key,
    display_name: key,
    source: { endpoint: '', unit_id: 0, timeout_ms: 1000, device_name: '' },
    reads:  [],
    target: { port: 0, unit_id: 0, status_unit_id: 0, status_slot: 0, mode: DEFAULT_TARGET_MODE },
  };
  workingConfig.devices.push(newDevice);
  selectedDeviceKey = key;
  renderDeviceList();
  renderDevice(newDevice);
  // Open source in edit mode immediately
  renderSourceEdit(workingConfig.devices[workingConfig.devices.length - 1]);
});

// ---------- Delete Device ----------

document.getElementById('btn-delete').addEventListener('click', () => {
  if (!workingConfig || !selectedDeviceKey) {
    showToast('No device selected.');
    return;
  }
  const idx = workingConfig.devices.findIndex(d => d.key === selectedDeviceKey);
  if (idx < 0) return;
  workingConfig.devices.splice(idx, 1);
  selectedDeviceKey = null;
  renderAll();
});

// ---------- Export Config ----------

document.getElementById('btn-export-config').addEventListener('click', () => {
  window.location.href = '/api/config/export';
});

// ---------- Import Config ----------

document.getElementById('btn-import-config').addEventListener('click', () => {
  document.getElementById('input-import-file').click();
});

document.getElementById('input-import-file').addEventListener('change', async (e) => {
  const file = e.target.files[0];
  if (!file) return;
  try {
    const text = await file.text();
    const res = await fetch('/api/config/import', {
      method: 'POST',
      headers: { 'Content-Type': 'text/yaml' },
      body: text,
    });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) {
      showToast('Import failed: ' + (body.error || 'HTTP ' + res.status));
      return;
    }
    showToast('Configuration imported.');
    await loadView();
  } catch (err) {
    showToast('Import failed: ' + err.message);
  }
  // Reset so the same file can be re-imported if needed.
  e.target.value = '';
});

loadView();
loadDeviceStatuses();
const _statusPollId = setInterval(async () => {
  await loadDeviceStatuses();
  if (workingConfig) renderDeviceList();
}, 5000);
