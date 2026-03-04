// app.js - Aegis WebUI Phase 0 (VIEW ONLY)

let currentConfig = null;
let selectedDeviceKey = null;

// Page initialization
document.addEventListener('DOMContentLoaded', async () => {
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

    // Attach button handlers
    document.getElementById('addBtn').addEventListener('click', onAddDevice);
    document.getElementById('deleteBtn').addEventListener('click', onDeleteDevice);
});

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
