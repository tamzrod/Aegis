// app.js - Aegis WebUI

let currentConfig = null;
let selectedDeviceKey = null;
let runtimeState = 'STOPPED';
let statusPollInterval = null;
let editingReadIndex = null; // null = adding, number = editing existing index

// Page initialization
document.addEventListener('DOMContentLoaded', async () => {
    // Attach runtime control button handlers
    document.getElementById('startBtn').addEventListener('click', onStartRuntime);
    document.getElementById('stopBtn').addEventListener('click', onStopRuntime);
    document.getElementById('restartBtn').addEventListener('click', onRestartRuntime);
    document.getElementById('addBtn').addEventListener('click', onAddDevice);
    document.getElementById('deleteBtn').addEventListener('click', onDeleteDevice);

    // Attach read modal button handlers
    document.getElementById('addReadBtn').addEventListener('click', onAddRead);
    document.getElementById('readModalSave').addEventListener('click', onSaveRead);
    document.getElementById('readModalCancel').addEventListener('click', closeReadModal);

    // Start polling runtime status (updates button guards)
    await pollRuntimeStatus();
    statusPollInterval = setInterval(pollRuntimeStatus, 2000);

    // Fetch config view from API
    try {
        const response = await fetch('/api/config/view');
        if (!response.ok) {
            console.error('Failed to fetch config:', response.status);
            return;
        }
        currentConfig = await response.json();
    } catch (error) {
        console.error('Error fetching config:', error);
        return;
    }

    if (!currentConfig || currentConfig.devices.length === 0) {
        document.getElementById('deviceList').innerHTML = '<p>No devices configured</p>';
        return;
    }

    // Render device list
    populateDeviceList();

    // Select first device if none selected
    selectedDeviceKey = currentConfig.selected_key || currentConfig.devices[0].key;
    renderDevicePanel();
});

// ---------------------------------------------------------------------------
// Runtime status polling and button state machine
// ---------------------------------------------------------------------------

async function pollRuntimeStatus() {
    try {
        const response = await fetch('/api/runtime/status');
        if (!response.ok) return;
        const status = await response.json();
        updateRuntimeUI(status);
    } catch (e) {
        // WebUI server unreachable — leave UI as-is
    }
}

function updateRuntimeUI(status) {
    const state = (status.state || (status.running ? 'RUNNING' : 'STOPPED')).toUpperCase();
    runtimeState = state;

    const dot = document.getElementById('statusDot');
    const text = document.getElementById('statusText');
    const errorBar = document.getElementById('errorBar');
    const startBtn = document.getElementById('startBtn');
    const stopBtn = document.getElementById('stopBtn');
    const restartBtn = document.getElementById('restartBtn');

    // Update status dot color
    dot.className = 'status-dot';
    switch (state) {
        case 'RUNNING':
            dot.classList.add('status-running');
            text.textContent = 'Running';
            break;
        case 'STARTING':
            dot.classList.add('status-starting');
            text.textContent = 'Starting…';
            break;
        case 'STOPPING':
            dot.classList.add('status-stopping');
            text.textContent = 'Stopping…';
            break;
        default: // STOPPED
            dot.classList.add('status-stopped');
            text.textContent = status.error ? 'Error' : 'Stopped';
    }

    // Show/hide error bar
    if (status.error) {
        errorBar.textContent = '⚠ ' + status.error;
        errorBar.style.display = 'block';
    } else {
        errorBar.style.display = 'none';
    }

    // Button guards: only enable buttons in valid transition states
    const isStopped  = state === 'STOPPED';
    const isRunning  = state === 'RUNNING';
    const isTransitioning = state === 'STARTING' || state === 'STOPPING';

    startBtn.disabled   = !isStopped || isTransitioning;
    stopBtn.disabled    = !isRunning  || isTransitioning;
    restartBtn.disabled = !isRunning  || isTransitioning;
}

async function onStartRuntime() {
    setButtonsBusy();
    try {
        const res = await fetch('/api/runtime/start', { method: 'POST' });
        if (!res.ok) {
            const body = await res.json().catch(() => ({}));
            showError(body.error || 'Start failed');
        }
    } catch (e) {
        showError('Start request failed: ' + e.message);
    }
    await pollRuntimeStatus();
}

async function onStopRuntime() {
    setButtonsBusy();
    try {
        const res = await fetch('/api/runtime/stop', { method: 'POST' });
        if (!res.ok) {
            const body = await res.json().catch(() => ({}));
            showError(body.error || 'Stop failed');
        }
    } catch (e) {
        showError('Stop request failed: ' + e.message);
    }
    await pollRuntimeStatus();
}

async function onRestartRuntime() {
    setButtonsBusy();
    try {
        const res = await fetch('/api/restart', { method: 'POST' });
        if (!res.ok) {
            const body = await res.json().catch(() => ({}));
            showError(body.error || 'Restart failed');
        }
    } catch (e) {
        showError('Restart request failed: ' + e.message);
    }
    // Restart is async server-side; poll a few times to pick up the new state
    setTimeout(pollRuntimeStatus, 300);
    await pollRuntimeStatus();
}

function setButtonsBusy() {
    document.getElementById('startBtn').disabled   = true;
    document.getElementById('stopBtn').disabled    = true;
    document.getElementById('restartBtn').disabled = true;
}

function showError(msg) {
    const bar = document.getElementById('errorBar');
    bar.textContent = '⚠ ' + msg;
    bar.style.display = 'block';
}

// ---------------------------------------------------------------------------
// Device list and detail panel
// ---------------------------------------------------------------------------

function populateDeviceList() {
    const deviceList = document.getElementById('deviceList');
    deviceList.innerHTML = '';

    currentConfig.devices.forEach(device => {
        const deviceItem = document.createElement('div');
        deviceItem.className = 'device-item';
        if (device.key === selectedDeviceKey) {
            deviceItem.classList.add('selected');
        }
        deviceItem.textContent = device.display_name;
        deviceItem.addEventListener('click', () => selectDevice(device.key));
        deviceList.appendChild(deviceItem);
    });
}

function selectDevice(key) {
    selectedDeviceKey = key;
    populateDeviceList();
    renderDevicePanel();
}

function renderDevicePanel() {
    const device = currentConfig.devices.find(d => d.key === selectedDeviceKey);
    if (!device) return;

    // Source section
    document.getElementById('sourceEndpoint').textContent = device.source.endpoint || '-';
    document.getElementById('sourceUnitId').textContent = device.source.unit_id ?? '-';
    document.getElementById('sourceTimeout').textContent = `${device.source.timeout_ms || '-'} ms`;
    document.getElementById('sourceDeviceName').textContent = device.source.device_name || '-';

    // Reads section
    const readsList = document.getElementById('readsList');
    readsList.innerHTML = '';
    if (device.reads && device.reads.length > 0) {
        device.reads.forEach((read, index) => {
            const readItem = document.createElement('div');
            readItem.className = 'read-item';
            readItem.dataset.readIndex = index;

            const textSpan = document.createElement('span');
            textSpan.className = 'read-item-text';
            textSpan.textContent = `FC${read.fc} | Address: ${read.address} | Quantity: ${read.quantity} | Interval: ${read.interval_ms} ms`;

            const actions = document.createElement('div');
            actions.className = 'read-item-actions';

            const editBtn = document.createElement('button');
            editBtn.className = 'btn btn-secondary';
            editBtn.textContent = 'Edit';
            editBtn.addEventListener('click', () => onEditRead(index));

            const deleteBtn = document.createElement('button');
            deleteBtn.className = 'btn btn-danger';
            deleteBtn.textContent = 'Delete';
            deleteBtn.addEventListener('click', () => onDeleteRead(index));

            actions.appendChild(editBtn);
            actions.appendChild(deleteBtn);
            readItem.appendChild(textSpan);
            readItem.appendChild(actions);
            readsList.appendChild(readItem);
        });
    } else {
        const emptyItem = document.createElement('div');
        emptyItem.className = 'read-item';
        emptyItem.innerHTML = '<span class="read-item-text">No reads configured</span>';
        readsList.appendChild(emptyItem);
    }

    // Target section
    document.getElementById('targetPort').textContent = device.target.port ?? '-';
    document.getElementById('targetUnitId').textContent = device.target.unit_id ?? '-';
    document.getElementById('targetStatusUnitId').textContent = device.target.status_unit_id ?? '-';
    document.getElementById('targetStatusSlot').textContent = device.target.status_slot ?? '-';
    document.getElementById('targetMode').textContent = getModeDisplayName(device.target.mode);
}

function getModeDisplayName(mode) {
    const modeMap = {
        'A': 'A (Standalone)',
        'B': 'B (Strict)',
        'C': 'C (Buffered)'
    };
    return modeMap[mode] || mode || '-';
}

function onAddDevice() {
    alert('Editing available in next phase');
}

function onDeleteDevice() {
    alert('Editing available in next phase');
}

// ---------------------------------------------------------------------------
// Read management
// ---------------------------------------------------------------------------

function onAddRead() {
    editingReadIndex = null;
    document.getElementById('readModalTitle').textContent = 'Add Read';
    document.getElementById('readFc').value = '';
    document.getElementById('readAddress').value = '';
    document.getElementById('readQuantity').value = '';
    document.getElementById('readInterval').value = '';
    setReadModalError('');
    clearReadHighlights();
    document.getElementById('readModal').style.display = 'flex';
}

function onEditRead(index) {
    const device = currentConfig.devices.find(d => d.key === selectedDeviceKey);
    if (!device || !device.reads[index]) return;

    const read = device.reads[index];
    editingReadIndex = index;
    document.getElementById('readModalTitle').textContent = 'Edit Read';
    document.getElementById('readFc').value = read.fc;
    document.getElementById('readAddress').value = read.address;
    document.getElementById('readQuantity').value = read.quantity;
    document.getElementById('readInterval').value = read.interval_ms;
    setReadModalError('');
    clearReadHighlights();
    document.getElementById('readModal').style.display = 'flex';
}

function onDeleteRead(index) {
    const device = currentConfig.devices.find(d => d.key === selectedDeviceKey);
    if (!device) return;

    device.reads.splice(index, 1);
    renderDevicePanel();
    saveConfigFromView();
}

function closeReadModal() {
    document.getElementById('readModal').style.display = 'none';
    clearReadHighlights();
}

function setReadModalError(msg) {
    const el = document.getElementById('readModalError');
    if (msg) {
        el.textContent = msg;
        el.style.display = 'block';
    } else {
        el.textContent = '';
        el.style.display = 'none';
    }
}

// checkDuplicateRead returns the index of a conflicting read, or -1 if none.
// When excludeIndex >= 0, that index is skipped (used during edit).
function checkDuplicateRead(reads, fc, address, quantity, excludeIndex) {
    for (let i = 0; i < reads.length; i++) {
        if (i === excludeIndex) continue;
        if (reads[i].fc === fc && reads[i].address === address && reads[i].quantity === quantity) {
            return i;
        }
    }
    return -1;
}

function highlightDuplicateRead(index) {
    clearReadHighlights();
    const items = document.querySelectorAll('#readsList .read-item');
    if (items[index]) {
        items[index].classList.add('read-item-duplicate');
    }
}

function clearReadHighlights() {
    document.querySelectorAll('#readsList .read-item').forEach(el => {
        el.classList.remove('read-item-duplicate');
    });
}

function onSaveRead() {
    const fc = parseInt(document.getElementById('readFc').value, 10);
    const address = parseInt(document.getElementById('readAddress').value, 10);
    const quantity = parseInt(document.getElementById('readQuantity').value, 10);
    const intervalMs = parseInt(document.getElementById('readInterval').value, 10);

    if (isNaN(fc) || fc < 1 || fc > 4) {
        setReadModalError('FC must be 1, 2, 3, or 4.');
        return;
    }
    if (isNaN(address) || address < 0 || address > 65535) {
        setReadModalError('Address must be between 0 and 65535.');
        return;
    }
    if (isNaN(quantity) || quantity < 1 || quantity > 65535) {
        setReadModalError('Quantity must be between 1 and 65535.');
        return;
    }
    if (isNaN(intervalMs) || intervalMs < 1) {
        setReadModalError('Interval must be greater than 0.');
        return;
    }

    const device = currentConfig.devices.find(d => d.key === selectedDeviceKey);
    if (!device) return;

    const dupIndex = checkDuplicateRead(device.reads || [], fc, address, quantity, editingReadIndex);
    if (dupIndex !== -1) {
        setReadModalError(`Duplicate read detected: FC${fc} Address ${address} Quantity ${quantity} already exists.`);
        highlightDuplicateRead(dupIndex);
        return;
    }

    const newRead = { fc, address, quantity, interval_ms: intervalMs };

    if (editingReadIndex === null) {
        if (!device.reads) device.reads = [];
        device.reads.push(newRead);
    } else {
        device.reads[editingReadIndex] = newRead;
    }

    closeReadModal();
    renderDevicePanel();
    saveConfigFromView();
}

// saveConfigFromView persists the in-memory currentConfig back to the server.
async function saveConfigFromView() {
    try {
        const res = await fetch('/api/config/apply', {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(currentConfig),
        });
        if (!res.ok) {
            const body = await res.json().catch(() => ({}));
            showError(body.error || 'Save failed');
        }
    } catch (e) {
        showError('Save request failed: ' + e.message);
    }
}
