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
const groupOpenStates = new Map(); // maps group key → open bool (persists across re-renders)

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

// nextAvailableSlot finds the lowest status slot not used by any other device
// sharing the same (port, status_unit_id). The device identified by excludeKey is
// excluded from the check so that re-opening its own edit form does not produce a
// collision. Returns null when all uint16 slots (0–65535) are exhausted.
function nextAvailableSlot(devices, port, statusUnitId, excludeKey) {
  const used = new Set(
    (devices || [])
      .filter(d => d.key !== excludeKey && d.target &&
                   d.target.port === port && d.target.status_unit_id === statusUnitId)
      .map(d => d.target.status_slot)
  );
  for (let slot = 0; slot <= 65535; slot++) {
    if (!used.has(slot)) return slot;
  }
  return null;
}

// nextAvailableUnitId finds the lowest unit_id (1–247) not already used by another
// device on the same target.port. The device identified by excludeKey is excluded
// so that re-opening its own form does not cause a self-conflict.
// Returns null if no unit_id is available in the range 1–247.
function nextAvailableUnitId(devices, port, excludeKey) {
  const used = new Set(
    (devices || [])
      .filter(d => d.key !== excludeKey && d.target && d.target.port === port)
      .map(d => d.target.unit_id)
  );
  for (let id = 1; id <= 247; id++) {
    if (!used.has(id)) return id;
  }
  return null;
}

// validateStatusSlotConflicts returns an error string if any two devices in the
// list share the same (port, status_unit_id, status_slot), or null if there is no
// conflict. This matches the uniqueness constraint enforced by the backend validator.
function validateStatusSlotConflicts(devices) {
  const seen = new Map();
  for (const d of devices) {
    const tgt = d.target || {};
    if (!tgt.status_unit_id) continue;
    const k = `${tgt.port}:${tgt.status_unit_id}:${tgt.status_slot}`;
    if (seen.has(k)) {
      return 'Status slot already used by another device.';
    }
    seen.set(k, d.key);
  }
  return null;
}

// validateStatusUnitIDPortConflicts returns an error string if any two devices
// in the list share the same target.port but have different status_unit_ids,
// or null if there is no conflict.
function validateStatusUnitIDPortConflicts(devices) {
  const seen = new Map(); // port → status_unit_id
  for (const d of devices) {
    const tgt = d.target || {};
    if (!tgt.port || !tgt.status_unit_id) continue;
    if (seen.has(tgt.port)) {
      if (seen.get(tgt.port) !== tgt.status_unit_id) {
        return 'All devices on the same port must share the same Status Unit ID.';
      }
    } else {
      seen.set(tgt.port, tgt.status_unit_id);
    }
  }
  return null;
}

// getSharedStatusUnitId returns the status_unit_id already in use on the given
// port by any device other than the one identified by excludeKey, or null if
// no other device on that port has a status_unit_id set.
function getSharedStatusUnitId(devices, port, excludeKey) {
  for (const d of devices) {
    if (d.key === excludeKey) continue;
    const tgt = d.target || {};
    if (tgt.port === port && tgt.status_unit_id) {
      return tgt.status_unit_id;
    }
  }
  return null;
}

// validateUnitIdConflicts returns an error string if any two devices in the list
// share the same (port, unit_id), or null if there is no conflict.
function validateUnitIdConflicts(devices) {
  const seen = new Map();
  for (const d of devices) {
    const tgt = d.target || {};
    if (!tgt.port || !tgt.unit_id) continue;
    const k = `${tgt.port}:${tgt.unit_id}`;
    if (seen.has(k)) {
      return `Unit ID ${tgt.unit_id} is already in use on port ${tgt.port}.`;
    }
    seen.set(k, d.key);
  }
  return null;
}

// validateDuplicateRead checks whether any read in the list (excluding the entry
// at excludeIndex, pass -1 to check all) shares the same FC, Address, and Quantity.
// Returns an object { msg, index } identifying the conflict, or null if none found.
function validateDuplicateRead(reads, fc, address, quantity, excludeIndex) {
  for (let i = 0; i < reads.length; i++) {
    if (i === excludeIndex) continue;
    if (reads[i].fc === fc && reads[i].address === address && reads[i].quantity === quantity) {
      return { msg: `Duplicate read detected: FC${fc} Address ${address} Quantity ${quantity} already exists.`, index: i };
    }
  }
  return null;
}

// validateIPv4Port returns an error message string if value is not a valid strict
// IPv4:port endpoint, or null if it is valid.
// Rules: trim whitespace, reject hostnames, octets 0–255, port 1–65535.
function validateIPv4Port(value) {
  const s = (value || '').trim();
  const colonIdx = s.lastIndexOf(':');
  if (colonIdx < 0) return 'Must be in ip:port format (e.g. 192.168.1.1:502)';
  const host    = s.substring(0, colonIdx);
  const portStr = s.substring(colonIdx + 1);
  if (!host)    return 'IP address is required';
  if (!portStr) return 'Port is required';
  if (!/^\d+$/.test(portStr)) return 'Port must be a number';
  const port = parseInt(portStr, 10);
  if (port < 1 || port > 65535) return 'Port must be between 1 and 65535';
  const parts = host.split('.');
  if (parts.length !== 4) return 'Must be a valid IPv4 address (e.g. 192.168.1.1:502)';
  for (const part of parts) {
    if (!/^\d+$/.test(part)) return 'Must be a valid IPv4 address (e.g. 192.168.1.1:502)';
    const octet = parseInt(part, 10);
    if (octet > 255) return 'Each IP octet must be 0–255';
  }
  return null;
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
    ['Device Name',  src.device_name || '—'],
    ['Endpoint',     src.endpoint    || '—'],
    ['Unit ID',      src.unit_id     ?? '—'],
    ['Timeout (ms)', src.timeout_ms  ?? '—'],
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
    { label: 'Device Name',  key: 'device_name', type: 'text',   value: src.device_name || '' },
    { label: 'Endpoint',     key: 'endpoint',    type: 'text',   value: src.endpoint    || '127.0.0.1:502' },
    { label: 'Unit ID',      key: 'unit_id',     type: 'number', value: src.unit_id     ?? 0 },
    { label: 'Timeout (ms)', key: 'timeout_ms',  type: 'number', value: src.timeout_ms  ?? 0 },
  ];
  const inputs = {};
  let endpointErrSpan = null;
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
    if (f.key === 'endpoint') {
      endpointErrSpan = document.createElement('span');
      endpointErrSpan.className = 'field-error';
      td.appendChild(endpointErrSpan);
    }
    tr.appendChild(th);
    tr.appendChild(td);
    table.appendChild(tr);
  });
  content.innerHTML = '';
  content.appendChild(table);
  inputs['device_name'].focus();

  // Inline endpoint validation: show error and guard the Save button.
  function checkEndpoint() {
    const err = validateIPv4Port(inputs['endpoint'].value);
    endpointErrSpan.textContent = err || '';
    btnSave.disabled = !!err;
  }
  inputs['endpoint'].addEventListener('input', checkEndpoint);
  checkEndpoint();

  btnSave.addEventListener('click', () => {
    const endpointErr = validateIPv4Port(inputs['endpoint'].value);
    if (endpointErr) {
      endpointErrSpan.textContent = endpointErr;
      return;
    }
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

  const currentPort = tgt.port || 502;
  const sharedSuid = getSharedStatusUnitId(workingConfig.devices, currentPort, device.key);
  const statusUnitIdDefault = sharedSuid || tgt.status_unit_id || 100;
  const autoSlot = nextAvailableSlot(workingConfig.devices, currentPort, statusUnitIdDefault, device.key);
  const numFields = [
    { label: 'Port',           key: 'port',           value: currentPort },
    { label: 'Unit ID',        key: 'unit_id',        value: tgt.unit_id        ?? 0 },
    { label: 'Status Unit ID', key: 'status_unit_id', value: statusUnitIdDefault },
    { label: 'Status Slot',    key: 'status_slot',    value: autoSlot },
  ];
  const inputs = {};
  let slotHintEl = null;
  let statusUnitIdHintEl = null;
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
    if (f.key === 'status_unit_id') {
      statusUnitIdHintEl = document.createElement('span');
      statusUnitIdHintEl.className = 'slot-hint';
      td.appendChild(statusUnitIdHintEl);
    }
    if (f.key === 'status_slot') {
      slotHintEl = document.createElement('span');
      slotHintEl.className = 'slot-hint';
      td.appendChild(slotHintEl);
    }
    tr.appendChild(th);
    tr.appendChild(td);
    table.appendChild(tr);
  });

  // Apply shared-port lock: disable Status Unit ID when another device on the
  // same port has already claimed a status_unit_id.
  function applyStatusUnitIdLock(port) {
    const shared = getSharedStatusUnitId(workingConfig.devices, port, device.key);
    if (shared !== null) {
      inputs['status_unit_id'].value    = shared;
      inputs['status_unit_id'].disabled = true;
      statusUnitIdHintEl.textContent    = ' Status Unit ID is shared for this port';
    } else {
      inputs['status_unit_id'].disabled = false;
      statusUnitIdHintEl.textContent    = '';
    }
  }

  applyStatusUnitIdLock(currentPort);

  function updateSlotHint(port, statusUnitId) {
    if (!slotHintEl) return;
    const used = (workingConfig.devices || [])
      .filter(d => d.key !== device.key && d.target &&
                   d.target.port === port && d.target.status_unit_id === statusUnitId)
      .map(d => d.target.status_slot)
      .sort((a, b) => a - b);
    const available = nextAvailableSlot(workingConfig.devices, port, statusUnitId, device.key);
    if (available === null) {
      slotHintEl.textContent = ' No available status slots for this port and status_unit_id.';
    } else {
      slotHintEl.textContent = used.length > 0 ? ' Used: ' + used.join(', ') : '';
    }
  }

  updateSlotHint(currentPort, statusUnitIdDefault);

  // When the port changes, re-evaluate the shared status_unit_id lock.
  inputs['port'].addEventListener('input', () => {
    const port = parseInt(inputs['port'].value, 10) || 0;
    applyStatusUnitIdLock(port);
    const uid = parseInt(inputs['status_unit_id'].value, 10) || 0;
    const next = nextAvailableSlot(workingConfig.devices, port, uid, device.key);
    if (next !== null) {
      inputs['status_slot'].value = next;
    }
    updateSlotHint(port, uid);
  });

  inputs['status_unit_id'].addEventListener('input', () => {
    const port = parseInt(inputs['port'].value, 10) || 0;
    const uid = parseInt(inputs['status_unit_id'].value, 10) || 0;
    const next = nextAvailableSlot(workingConfig.devices, port, uid, device.key);
    if (next !== null) {
      inputs['status_slot'].value = next;
    }
    updateSlotHint(port, uid);
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
    const next = {
      ...prev,
      port:           parseInt(inputs['port'].value,           10) || 0,
      unit_id:        parseInt(inputs['unit_id'].value,        10) || 0,
      status_unit_id: parseInt(inputs['status_unit_id'].value, 10) || 0,
      status_slot:    parseInt(inputs['status_slot'].value,    10) || 0,
      mode:           inputs['mode'].value,
    };
    // Build a temporary devices list with the proposed target to check for conflicts.
    const proposed = workingConfig.devices.map((d, i) => i === idx ? { ...d, target: next } : d);
    const unitIdErr = validateUnitIdConflicts(proposed);
    if (unitIdErr) {
      showToast(unitIdErr);
      return;
    }
    const conflictErr = validateStatusSlotConflicts(proposed);
    if (conflictErr) {
      showToast(conflictErr);
      return;
    }
    workingConfig.devices[idx].target = next;
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

      const dupErrSpan = document.createElement('span');
      dupErrSpan.className = 'field-error';
      dupErrSpan.style.display = 'none';

      li.appendChild(fieldDiv);
      li.appendChild(actDiv);
      li.appendChild(dupErrSpan);

      btnSave.addEventListener('click', () => {
        const devIdx = getSelectedDeviceIndex();
        if (devIdx < 0) return;
        const fc          = parseInt(inp['fc'].value,          10) || 1;
        const address     = parseInt(inp['address'].value,     10) || 0;
        const quantity    = parseInt(inp['quantity'].value,    10) || 1;
        const interval_ms = parseInt(inp['interval_ms'].value, 10) || 1000;
        const dupResult = validateDuplicateRead(
          workingConfig.devices[devIdx].reads, fc, address, quantity, i
        );
        if (dupResult) {
          dupErrSpan.textContent = dupResult.msg;
          dupErrSpan.style.display = 'block';
          // Highlight the conflicting view row.
          ul.querySelectorAll('li').forEach((rowLi, rowIdx) => {
            if (rowIdx === dupResult.index) {
              rowLi.classList.add('read-row-duplicate');
            } else {
              rowLi.classList.remove('read-row-duplicate');
            }
          });
          return;
        }
        dupErrSpan.style.display = 'none';
        workingConfig.devices[devIdx].reads[i] = { fc, address, quantity, interval_ms };
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
  const container = document.createElement('div');
  container.className = 'reads-container';
  container.appendChild(ul);
  content.appendChild(container);
}

// ---------- Device Status panel ----------

let _deviceStatusTimer = null;

// Latency history ring buffer (last 20 samples, one per successful poll update).
const SPARKLINE_MAX = 20;
let _latencyHistory = [];

// Cached last-valid diagnostic values — updated only when a positive reading arrives.
let _diagLastMs = 0;
let _diagAvgMs  = 0;
let _diagMaxMs  = 0;

// healthColor returns a CSS class name matching the health string.
function healthColor(health) {
  switch ((health || '').toUpperCase()) {
    case 'OK':       return 'ds-health-ok';
    case 'ERROR':    return 'ds-health-error';
    case 'STALE':    return 'ds-health-stale';
    case 'DISABLED': return 'ds-health-disabled';
    default:         return 'ds-health-unknown';
  }
}

// latencyColorClass returns a CSS class based on poll latency in ms.
// green → fast, yellow → moderate, red → degraded.
function latencyColorClass(ms) {
  if (ms === 0) return 'ds-latency-unknown';
  if (ms <= 50)  return 'ds-latency-ok';
  if (ms <= 150) return 'ds-latency-warn';
  return 'ds-latency-error';
}

// renderDeviceSummaryCard populates the Device Summary section from the device
// config object and the current runtime status string.
function renderDeviceSummaryCard(device, runtimeState) {
  const section = document.getElementById('device-summary-section');
  const content = document.getElementById('device-summary-content');
  const actEl   = document.getElementById('device-summary-actions');
  if (!device) {
    section.style.display = 'none';
    content.innerHTML = '';
    actEl.innerHTML = '';
    return;
  }
  section.style.display = '';

  actEl.innerHTML = '';
  const btnEdit = document.createElement('button');
  btnEdit.className = 'btn-sm';
  btnEdit.textContent = 'Edit';
  btnEdit.addEventListener('click', () => renderDeviceSummaryEdit(device));
  actEl.appendChild(btnEdit);

  const src = device.source || {};
  const statusObj = deviceStatuses[device.key] || { status: 'offline' };
  const runtimeLabel = runtimeState || statusObj.status || 'offline';
  const groupLabel = device.group || '—';

  const table = document.createElement('table');
  table.className = 'field-table';
  [
    ['Name',     src.device_name || device.display_name || device.key],
    ['Endpoint', src.endpoint    || '—'],
    ['Unit ID',  src.unit_id     ?? '—'],
    ['Group',    groupLabel],
    ['Runtime',  runtimeLabel],
  ].forEach(([label, value]) => {
    const tr = document.createElement('tr');
    const th = document.createElement('th');
    th.textContent = label;
    const td = document.createElement('td');
    if (label === 'Runtime') {
      const badge = document.createElement('span');
      badge.className = 'ds-runtime-badge ds-runtime-' + (statusObj.status || 'offline');
      badge.textContent = runtimeLabel;
      td.appendChild(badge);
    } else {
      td.textContent = value;
    }
    tr.appendChild(th);
    tr.appendChild(td);
    table.appendChild(tr);
  });

  content.innerHTML = '';
  content.appendChild(table);
}

// renderDeviceSummaryEdit puts the Device summary section into edit mode,
// allowing the user to assign or create a group for the selected device.
function renderDeviceSummaryEdit(device) {
  const actEl = document.getElementById('device-summary-actions');
  actEl.innerHTML = '';
  const btnSave   = document.createElement('button');
  btnSave.className = 'btn-sm btn-save';
  btnSave.textContent = 'Save';
  const btnCancel = document.createElement('button');
  btnCancel.className = 'btn-sm';
  btnCancel.textContent = 'Cancel';
  actEl.appendChild(btnSave);
  actEl.appendChild(btnCancel);

  const content = document.getElementById('device-summary-content');
  const table   = document.createElement('table');
  table.className = 'field-table';

  // Collect unique, non-empty group names from all devices in the working config.
  const existingGroups = [...new Set(
    (workingConfig.devices || [])
      .map(d => d.group || '')
      .filter(g => g !== '')
  )].sort();

  const groupTr = document.createElement('tr');
  const groupTh = document.createElement('th');
  groupTh.textContent = 'Group';
  const groupTd = document.createElement('td');

  const groupSel = document.createElement('select');
  groupSel.className = 'field-input';

  const noneOpt = document.createElement('option');
  noneOpt.value = '';
  noneOpt.textContent = '— None (Ungrouped) —';
  groupSel.appendChild(noneOpt);

  existingGroups.forEach(g => {
    const opt = document.createElement('option');
    opt.value = g;
    opt.textContent = g;
    groupSel.appendChild(opt);
  });

  const createOpt = document.createElement('option');
  createOpt.value = '__create__';
  createOpt.textContent = '+ Create new group';
  groupSel.appendChild(createOpt);

  groupSel.value = device.group || '';
  groupTd.appendChild(groupSel);

  const newGroupInp = document.createElement('input');
  newGroupInp.className = 'field-input';
  newGroupInp.type = 'text';
  newGroupInp.placeholder = 'New group name';
  newGroupInp.style.display = 'none';
  newGroupInp.style.marginTop = '0.3rem';
  groupTd.appendChild(newGroupInp);

  groupTr.appendChild(groupTh);
  groupTr.appendChild(groupTd);
  table.appendChild(groupTr);

  content.innerHTML = '';
  content.appendChild(table);

  groupSel.addEventListener('change', () => {
    const isCreate = groupSel.value === '__create__';
    newGroupInp.style.display = isCreate ? '' : 'none';
    if (isCreate) newGroupInp.focus();
  });

  btnSave.addEventListener('click', () => {
    const groupValue = groupSel.value === '__create__'
      ? newGroupInp.value.trim()
      : groupSel.value;
    if (groupSel.value === '__create__' && !groupValue) {
      newGroupInp.focus();
      return;
    }
    const idx = getSelectedDeviceIndex();
    if (idx < 0) return;
    if (groupValue) {
      workingConfig.devices[idx].group = groupValue;
    } else {
      delete workingConfig.devices[idx].group;
    }
    renderDeviceList();
    renderDeviceSummaryCard(workingConfig.devices[idx], null);
  });

  btnCancel.addEventListener('click', () => renderDeviceSummaryCard(device, null));
}

function renderDeviceStatusPanel(data) {
  const section = document.getElementById('device-status-section');
  const content = document.getElementById('device-status-content');
  if (!data) {
    section.style.display = 'none';
    content.innerHTML = '';
    renderDeviceDiagnostics(null);
    renderSparkline(null);
    return;
  }
  section.style.display = '';

  const table = document.createElement('table');
  table.className = 'field-table';

  const healthSpan = document.createElement('span');
  healthSpan.className = 'ds-health-badge ' + healthColor(data.health);
  healthSpan.textContent = data.health || 'UNKNOWN';

  const rows = [
    ['Health',            healthSpan],
    ['Online',            data.online ? 'Yes' : 'No'],
    ['Seconds in Error',  data.seconds_in_error ?? '—'],
    ['Requests Total',    data.requests_total    ?? '—'],
    ['Responses Valid',   data.responses_valid   ?? '—'],
    ['Timeouts',          data.timeouts_total    ?? '—'],
    ['Transport Errors',  data.transport_errors  ?? '—'],
    ['Consec. Fails (now)', data.consecutive_fail_curr ?? '—'],
    ['Consec. Fails (max)', data.consecutive_fail_max  ?? '—'],
  ];

  rows.forEach(([label, value]) => {
    const tr = document.createElement('tr');
    const th = document.createElement('th');
    th.textContent = label;
    const td = document.createElement('td');
    if (value instanceof Element) {
      td.appendChild(value);
    } else {
      td.textContent = value;
    }
    tr.appendChild(th);
    tr.appendChild(td);
    table.appendChild(tr);
  });

  content.innerHTML = '';
  content.appendChild(table);

  // Update diagnostics and sparkline with the same data refresh.
  renderDeviceDiagnostics(data);

  // Update latency history ring buffer.
  const lastMs = data.last_poll_ms ?? 0;
  if (lastMs > 0) {
    _latencyHistory.push(lastMs);
    if (_latencyHistory.length > SPARKLINE_MAX) {
      _latencyHistory.shift();
    }
  }
  renderSparkline(_latencyHistory.length > 0 ? _latencyHistory : null);
}

// renderDeviceDiagnostics populates the Device Diagnostics section.
function renderDeviceDiagnostics(data) {
  const section = document.getElementById('device-diagnostics-section');
  const content = document.getElementById('device-diagnostics-content');
  if (!data) {
    section.style.display = 'none';
    content.innerHTML = '';
    // Reset cache when device goes offline.
    _diagLastMs = 0;
    _diagAvgMs  = 0;
    _diagMaxMs  = 0;
    return;
  }
  section.style.display = '';

  // Only update cached values when the incoming reading is positive — this
  // prevents UI flicker when a poll cycle returns undefined/zero temporarily.
  if ((data.last_poll_ms ?? 0) > 0) _diagLastMs = data.last_poll_ms;
  if ((data.avg_poll_ms  ?? 0) > 0) _diagAvgMs  = data.avg_poll_ms;
  if ((data.max_poll_ms  ?? 0) > 0) _diagMaxMs  = data.max_poll_ms;

  const lastMs = _diagLastMs;
  const avgMs  = _diagAvgMs;
  const maxMs  = _diagMaxMs;

  // Build the diagnostics table.
  const table = document.createElement('table');
  table.className = 'field-table';

  const fmtMs = ms => ms > 0 ? ms + ' ms' : '—';
  const rowMetrics = [
    ['Avg Response Time', fmtMs(avgMs), avgMs],
    ['Last Response',     fmtMs(lastMs), lastMs],
    ['Max Response',      fmtMs(maxMs), maxMs],
  ];
  rowMetrics.forEach(([label, value, metric]) => {
    const tr = document.createElement('tr');
    const th = document.createElement('th');
    th.textContent = label;
    const td = document.createElement('td');
    td.className = latencyColorClass(metric);
    td.textContent = value;
    tr.appendChild(th);
    tr.appendChild(td);
    table.appendChild(tr);
  });

  // Poll Performance bar.
  const perf = document.createElement('div');
  perf.className = 'ds-poll-perf';

  const perfLabel = document.createElement('div');
  perfLabel.className = 'ds-poll-perf-label';
  perfLabel.textContent = 'Poll Performance';

  const barWrap = document.createElement('div');
  barWrap.className = 'ds-poll-perf-bar-wrap';

  const bar = document.createElement('div');
  bar.className = 'ds-poll-perf-bar';
  // Bar width uses an exponential decay: width% = 100 * exp(-ms / 200).
  // This gives ~61% at 100ms, ~37% at 200ms, ~7% at 500ms, and near-zero beyond 1s.
  // A floor of 5% keeps the bar visible even for very slow devices.
  // The 200ms baseline was chosen to match typical Modbus timeout thresholds.
  const refMs = avgMs > 0 ? avgMs : lastMs;
  const pct = refMs > 0 ? Math.max(5, Math.round(100 * Math.exp(-refMs / 200))) : 0;
  bar.style.width = pct + '%';
  bar.className += ' ' + latencyColorClass(refMs);

  barWrap.appendChild(bar);
  perf.appendChild(perfLabel);
  perf.appendChild(barWrap);

  content.innerHTML = '';
  content.appendChild(table);
  content.appendChild(perf);
}

// renderSparkline draws a tiny SVG sparkline of poll latency history.
function renderSparkline(history) {
  const section = document.getElementById('device-sparkline-section');
  const content = document.getElementById('device-sparkline-content');
  if (!history || history.length === 0) {
    section.style.display = 'none';
    content.innerHTML = '';
    return;
  }
  section.style.display = '';

  const W = 200, H = 36, PAD = 2;
  const minVal = Math.min(...history);
  const maxVal = Math.max(...history);
  const range  = maxVal - minVal || 1;

  // Map each sample to an SVG point.
  const points = history.map((v, i) => {
    const x = PAD + (i / Math.max(history.length - 1, 1)) * (W - PAD * 2);
    const y = PAD + (1 - (v - minVal) / range) * (H - PAD * 2);
    return `${x.toFixed(1)},${y.toFixed(1)}`;
  });

  const lastMs = history[history.length - 1];
  const colorMap = { 'ds-latency-ok': '#22c55e', 'ds-latency-warn': '#f59e0b', 'ds-latency-error': '#ef4444', 'ds-latency-unknown': '#94a3b8' };
  const stroke = colorMap[latencyColorClass(lastMs)] || '#94a3b8';

  const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${W} ${H}" class="ds-sparkline">` +
    `<polyline points="${points.join(' ')}" fill="none" stroke="${stroke}" stroke-width="1.5" stroke-linejoin="round" stroke-linecap="round"/>` +
    `</svg>`;

  const labelDiv = document.createElement('div');
  labelDiv.className = 'ds-sparkline-label ' + latencyColorClass(lastMs);
  labelDiv.textContent = lastMs + ' ms (last)';

  content.innerHTML = svg;
  content.appendChild(labelDiv);
}

function stopDeviceStatusPolling() {
  if (_deviceStatusTimer !== null) {
    clearInterval(_deviceStatusTimer);
    _deviceStatusTimer = null;
  }
}

function startDeviceStatusPolling(port, statusUnitId, statusSlot) {
  stopDeviceStatusPolling();

  if (!port || !statusUnitId) {
    renderDeviceStatusPanel(null);
    return;
  }

  async function poll() {
    try {
      const res = await fetch(
        `/api/device/status?port=${port}&unit_id=${statusUnitId}&slot=${statusSlot}`
      );
      if (!res.ok) {
        renderDeviceStatusPanel(null);
        return;
      }
      const data = await res.json();
      renderDeviceStatusPanel(data);
    } catch (e) {
      // Server may be temporarily unavailable — keep the panel visible but stale.
    }
  }

  poll();
  _deviceStatusTimer = setInterval(poll, 1000);
}

// ---------- Render selected device ----------

function renderDevice(device) {
  if (!device) {
    ['source-content', 'reads-content', 'target-content',
     'src-actions', 'reads-actions', 'tgt-actions'].forEach(id => {
      document.getElementById(id).innerHTML = '';
    });
    stopDeviceStatusPolling();
    renderDeviceSummaryCard(null);
    renderDeviceStatusPanel(null);
    _latencyHistory = [];
    return;
  }
  renderSourceView(device);
  renderReadsList(device);
  renderTargetView(device);

  // Render the summary card immediately from config (no async needed).
  const statusObj = deviceStatuses[device.key] || { status: 'offline' };
  renderDeviceSummaryCard(device, statusObj.status);

  // Reset latency history and diagnostics cache when switching devices.
  _latencyHistory = [];
  _diagLastMs = 0;
  _diagAvgMs  = 0;
  _diagMaxMs  = 0;

  const tgt = device.target || {};
  startDeviceStatusPolling(tgt.port, tgt.status_unit_id, tgt.status_slot);
}

// ---------- Device list ----------

function makeDeviceLi(d) {
  const li = document.createElement('li');

  // Status dot
  const dot = document.createElement('span');
  const statusObj = deviceStatuses[d.key] || { status: 'offline', polling: false };
  dot.className = 'device-status-dot device-' + statusObj.status;
  if (statusObj.polling) {
    dot.classList.add('device-blink');
    setTimeout(() => dot.classList.remove('device-blink'), 250);
  }
  li.appendChild(dot);

  const nameSpan = document.createElement('span');
  nameSpan.textContent = d.display_name || d.key;
  li.appendChild(nameSpan);

  li.dataset.key = d.key;
  if (d.key === selectedDeviceKey) li.classList.add('selected');
  li.addEventListener('click', () => selectDevice(d.key));
  return li;
}

const MAX_SEGMENTS = 20;

// buildGroupHealthBar returns a document fragment containing a segmented health
// bar and an online/total count label for the supplied devices.
// EXACT MODE  (total ≤ MAX_SEGMENTS): one segment per device.
// PERCENTAGE MODE (total > MAX_SEGMENTS): bar normalised to MAX_SEGMENTS.
function buildGroupHealthBar(devices) {
  const total  = devices.length;
  const online = devices.filter(d => (deviceStatuses[d.key] || {}).status === 'online').length;

  const bar = document.createElement('span');
  bar.className = 'group-health-bar';

  if (total <= MAX_SEGMENTS) {
    // EXACT MODE — one segment per device
    devices.forEach(d => {
      const seg = document.createElement('span');
      const isOnline = (deviceStatuses[d.key] || {}).status === 'online';
      seg.className = 'group-health-segment ' + (isOnline ? 'ghs-on' : 'ghs-off');
      bar.appendChild(seg);
    });
  } else {
    // PERCENTAGE MODE — normalise to MAX_SEGMENTS
    const greenCount = Math.round((online / total) * MAX_SEGMENTS);
    for (let i = 0; i < MAX_SEGMENTS; i++) {
      const seg = document.createElement('span');
      seg.className = 'group-health-segment ' + (i < greenCount ? 'ghs-on' : 'ghs-off');
      bar.appendChild(seg);
    }
  }

  const countLabel = document.createElement('span');
  countLabel.className = 'group-health-count';
  countLabel.textContent = online + '/' + total;

  const wrap = document.createElement('span');
  wrap.className = 'group-health-wrap';
  wrap.appendChild(bar);
  wrap.appendChild(countLabel);
  return wrap;
}

// autoCollapseGroups collapses device groups from the bottom of the list
// until the device-list element no longer overflows its container.
function autoCollapseGroups() {
  const list = document.getElementById('device-list');
  if (!list) return;

  const allDetails = Array.from(list.querySelectorAll('li.device-group > details'));
  for (let i = allDetails.length - 1; i >= 0; i--) {
    if (list.scrollHeight <= list.clientHeight) break;
    if (!allDetails[i].open) continue;
    allDetails[i].open = false;
    groupOpenStates.set(allDetails[i].dataset.groupKey, false);
  }
}

function renderDeviceList() {
  const list = document.getElementById('device-list');
  list.innerHTML = '';

  // Always use grouped view. Devices without a group appear under "Ungrouped" last.
  const groups = new Map();
  workingConfig.devices.forEach(d => {
    const g = d.group || '';
    if (!groups.has(g)) groups.set(g, []);
    groups.get(g).push(d);
  });

  // Named groups alphabetically, then the ungrouped bucket last.
  const namedGroups   = [...groups.keys()].filter(g => g !== '').sort();
  const orderedGroups = groups.has('') ? [...namedGroups, ''] : namedGroups;

  orderedGroups.forEach(gName => {
    const devices = groups.get(gName);
    const label   = gName || 'Ungrouped';

    const groupLi  = document.createElement('li');
    groupLi.className = 'device-group';

    const details  = document.createElement('details');
    details.dataset.groupKey = gName;
    // Restore previously saved open state; new groups default to open.
    details.open = groupOpenStates.has(gName) ? groupOpenStates.get(gName) : true;
    details.addEventListener('toggle', () => {
      groupOpenStates.set(details.dataset.groupKey, details.open);
    });

    const summary  = document.createElement('summary');
    summary.className = 'device-group-header';

    const labelSpan = document.createElement('span');
    labelSpan.className = 'group-label';
    labelSpan.textContent = label;
    summary.appendChild(labelSpan);

    summary.appendChild(buildGroupHealthBar(devices));

    details.appendChild(summary);

    const subUl = document.createElement('ul');
    subUl.className = 'device-group-list';
    devices.forEach(d => subUl.appendChild(makeDeviceLi(d)));
    details.appendChild(subUl);

    groupLi.appendChild(details);
    list.appendChild(groupLi);
  });

  // After the browser has applied the new layout, collapse bottom groups if
  // the list overflows the sidebar height.
  requestAnimationFrame(autoCollapseGroups);
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
      list.forEach(s => { deviceStatuses[s.id] = { status: s.status, polling: !!s.polling }; });
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
  const conflictErr = validateStatusSlotConflicts(workingConfig.devices);
  if (conflictErr) {
    showToast(conflictErr);
    return;
  }
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

// suggestNextName returns the name with its trailing numeric suffix incremented by 1,
// preserving zero-padding width (e.g. "SCB01" → "SCB02", "Device9" → "Device10").
// If there is no numeric suffix, "2" is appended (e.g. "SCB" → "SCB2").
function suggestNextName(name) {
  if (!name) return '';
  const m = name.match(/^(.*?)(\d+)$/);
  if (!m) return name + '2';
  const prefix = m[1];
  const numStr = m[2];
  const next = parseInt(numStr, 10) + 1;
  return prefix + String(next).padStart(numStr.length, '0');
}

// getActiveGroup returns the group of the currently selected device, or '' if none.
function getActiveGroup() {
  if (!workingConfig || !selectedDeviceKey) return '';
  const d = workingConfig.devices.find(d => d.key === selectedDeviceKey);
  return (d && d.group) ? d.group : '';
}

// cloneDataviewConfig copies all dataview register entries from fromKey to toKey
// via the /api/dataview endpoint. Failures are silently ignored (best-effort).
async function cloneDataviewConfig(fromKey, toKey) {
  try {
    const res = await fetch('/api/dataview');
    if (!res.ok) return;
    const data = await res.json();
    const srcRegs = (data.registers || {})[fromKey];
    if (!srcRegs) return;
    const puts = [];
    for (const [fcKey, addrs] of Object.entries(srcRegs)) {
      const fcNum = parseInt(fcKey.replace('fc', ''), 10);
      if (isNaN(fcNum) || fcNum < 1 || fcNum > 4) continue;
      for (const [addrKey, entry] of Object.entries(addrs)) {
        const address = parseInt(addrKey, 10);
        if (isNaN(address)) continue;
        puts.push(fetch('/api/dataview', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            device:     toKey,
            fc:         fcNum,
            address,
            name:       entry.name       || '',
            type:       entry.type       || '',
            word_order: entry.word_order || '',
          }),
        }));
      }
    }
    await Promise.all(puts);
  } catch (e) {
    // Dataview config is non-critical at device creation time — log for diagnostics.
    console.warn('cloneDataviewConfig failed:', e);
  }
}

document.getElementById('btn-add').addEventListener('click', () => {
  if (!workingConfig) return;

  // Determine the active group from the currently selected device.
  const activeGroup = getActiveGroup();

  // Find the last device in the active group to use as the clone source.
  const groupDevices = workingConfig.devices.filter(d => (d.group || '') === activeGroup);
  const sourceDevice = groupDevices.length > 0 ? groupDevices[groupDevices.length - 1] : null;

  // Generate a unique internal key.
  let n = workingConfig.devices.length + 1;
  let key = `new-device-${n}`;
  while (workingConfig.devices.find(d => d.key === key)) {
    n++;
    key = `new-device-${n}`;
  }

  const defaultPort = 502;
  const autoUnitId = nextAvailableUnitId(workingConfig.devices, defaultPort, key);
  if (autoUnitId === null) {
    showToast('No available unit_id for port 502 (all 1–247 are in use).');
    return;
  }
  const defaultStatusUnitId = 100;
  const autoSlot = nextAvailableSlot(workingConfig.devices, defaultPort, defaultStatusUnitId, key);
  if (autoSlot === null) {
    showToast('No available status slots for this port and status_unit_id.');
    return;
  }

  // Build suggested name and clone reads when a source device exists.
  let suggestedName = '';
  let clonedReads   = [];
  if (sourceDevice) {
    const srcName = (sourceDevice.source && sourceDevice.source.device_name)
      || sourceDevice.display_name
      || sourceDevice.key;
    suggestedName = suggestNextName(srcName);
    // Clone reads (covers poll interval, function code, address, quantity).
    clonedReads = deepCopy(sourceDevice.reads || []);
  }

  const newDevice = {
    key,
    display_name: suggestedName || key,
    source: { endpoint: '127.0.0.1:502', unit_id: 0, timeout_ms: 1000, device_name: suggestedName },
    reads:  clonedReads,
    target: { port: defaultPort, unit_id: autoUnitId, status_unit_id: defaultStatusUnitId, status_slot: autoSlot, mode: DEFAULT_TARGET_MODE },
  };
  if (activeGroup) {
    newDevice.group = activeGroup;
  }

  workingConfig.devices.push(newDevice);
  selectedDeviceKey = key;
  renderDeviceList();
  renderDevice(newDevice);
  // Open source in edit mode immediately.
  renderSourceEdit(workingConfig.devices[workingConfig.devices.length - 1]);

  // Clone dataview config (word order, parsing, register labels) — best-effort, async.
  if (sourceDevice) {
    cloneDataviewConfig(sourceDevice.key, key).catch(e => console.warn('cloneDataviewConfig:', e));
  }
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

// ---------- User menu ----------
(function () {
  const trigger  = document.getElementById('user-menu-trigger');
  const dropdown = document.getElementById('user-menu-dropdown');
  const nameEl   = document.getElementById('user-menu-name');

  const stored = sessionStorage.getItem('aegis_username');
  if (stored && typeof stored === 'string' && stored.trim().length > 0) {
    nameEl.textContent = stored.trim();
  }

  trigger.addEventListener('click', function (e) {
    e.stopPropagation();
    dropdown.classList.toggle('open');
  });

  document.addEventListener('click', function () {
    dropdown.classList.remove('open');
  });

  document.getElementById('user-menu-settings').addEventListener('click', function () {
    dropdown.classList.remove('open');
    window.location.href = '/settings';
  });

  document.getElementById('user-menu-logout').addEventListener('click', async function () {
    try {
      await fetch('/api/logout', { method: 'POST' });
    } catch {
      // ignore network errors — redirect regardless
    }
    window.location.href = '/login';
  });
}());

// ---------- Sidebar resize ----------
(function () {
  const handle   = document.getElementById('sidebar-resize-handle');
  const panel    = document.getElementById('left-panel');
  if (!handle || !panel) return;

  const MIN_WIDTH     = 240;
  const MAX_WIDTH     = 380;
  const STORAGE_KEY   = 'aegis_sidebar_width';

  // Restore persisted width.
  const saved = parseInt(localStorage.getItem(STORAGE_KEY), 10);
  if (saved >= MIN_WIDTH && saved <= MAX_WIDTH) {
    panel.style.width = saved + 'px';
  }

  let dragging   = false;
  let startX     = 0;
  let startWidth = 0;
  let rafPending = false;
  let lastClientX = 0;

  function applyWidth() {
    rafPending = false;
    if (!dragging) return;
    const delta    = lastClientX - startX;
    const newWidth = Math.max(MIN_WIDTH, Math.min(MAX_WIDTH, startWidth + delta));
    panel.style.width = newWidth + 'px';
  }

  function stopDrag() {
    if (!dragging) return;
    dragging = false;
    handle.classList.remove('resizing');
    document.body.style.cursor     = '';
    document.body.style.userSelect = '';
    localStorage.setItem(STORAGE_KEY, String(panel.offsetWidth));
  }

  handle.addEventListener('mousedown', function (e) {
    dragging   = true;
    startX     = e.clientX;
    startWidth = panel.offsetWidth;
    handle.classList.add('resizing');
    document.body.style.cursor     = 'col-resize';
    document.body.style.userSelect = 'none';
    e.preventDefault();
  });

  document.addEventListener('mousemove', function (e) {
    if (!dragging) return;
    lastClientX = e.clientX;
    if (!rafPending) {
      rafPending = true;
      requestAnimationFrame(applyWidth);
    }
  });

  document.addEventListener('mouseup', stopDrag);

  // Commit the final size when the pointer leaves the browser window.
  document.addEventListener('mouseleave', stopDrag);
}());
