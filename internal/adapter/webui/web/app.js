'use strict';

const editor = document.getElementById('editor');
const status = document.getElementById('status');

function setStatus(msg, isError) {
  status.textContent = msg;
  status.className = 'status ' + (isError ? 'error' : 'ok');
}

async function loadConfig() {
  try {
    const res = await fetch('/api/config/raw');
    if (!res.ok) throw new Error('HTTP ' + res.status);
    editor.value = await res.text();
    setStatus('Config loaded.');
  } catch (e) {
    setStatus('Load failed: ' + e.message, true);
  }
}

document.getElementById('btn-apply').addEventListener('click', async () => {
  setStatus('Applying...');
  try {
    const res = await fetch('/api/config/raw', {
      method: 'PUT',
      headers: { 'Content-Type': 'text/yaml' },
      body: editor.value,
    });
    if (!res.ok) {
      const body = await res.json().catch(() => ({ error: 'HTTP ' + res.status }));
      throw new Error(body.error || 'HTTP ' + res.status);
    }
    setStatus('Applied successfully.');
  } catch (e) {
    setStatus('Apply failed: ' + e.message, true);
  }
});

document.getElementById('btn-reload').addEventListener('click', async () => {
  setStatus('Reloading...');
  try {
    const res = await fetch('/api/reload', { method: 'POST' });
    if (!res.ok) {
      const body = await res.json().catch(() => ({ error: 'HTTP ' + res.status }));
      throw new Error(body.error || 'HTTP ' + res.status);
    }
    await loadConfig();
    setStatus('Reloaded from disk.');
  } catch (e) {
    setStatus('Reload failed: ' + e.message, true);
  }
});

document.getElementById('btn-restart').addEventListener('click', async () => {
  if (!confirm('Restart Aegis? The supervisor will restart the process.')) return;
  setStatus('Restarting...');
  try {
    await fetch('/api/restart', { method: 'POST' });
    setStatus('Restart signal sent.');
  } catch (e) {
    setStatus('Restart failed: ' + e.message, true);
  }
});

loadConfig();
