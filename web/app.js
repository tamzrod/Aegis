// app.js - Aegis WebUI

let currentConfig = null;
let selectedDeviceKey = null;
let runtimeState = 'STOPPED';
let statusPollInterval = null;

// Page initialization
document.addEventListener('DOMContentLoaded', async () => {
    // Attach runtime control button handlers
    document.getElementById('startBtn').addEventListener('click', onStartRuntime);
    document.getElementById('stopBtn').addEventListener('click', onStopRuntime);
    document.getElementById('restartBtn').addEventListener('click', onRestartRuntime);
    document.getElementById('addBtn').addEventListener('click', onAddDevice);
    document.getElementById('deleteBtn').addEventListener('click', onDeleteDevice);

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
        device.reads.forEach(read => {
            const readItem = document.createElement('div');
            readItem.className = 'read-item';
            readItem.textContent = `FC${read.fc} | Address: ${read.address} | Quantity: ${read.quantity} | Interval: ${read.interval_ms} ms`;
            readsList.appendChild(readItem);
        });
    } else {
        const emptyItem = document.createElement('div');
        emptyItem.className = 'read-item';
        emptyItem.textContent = 'No reads configured';
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
