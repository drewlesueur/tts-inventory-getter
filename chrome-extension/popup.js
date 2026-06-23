const serverUrlEl = document.getElementById("serverUrl");
const serviceKeyEl = document.getElementById("serviceKey");
const saveBtn      = document.getElementById("save");
const statusEl     = document.getElementById("status");
const lastSyncEl   = document.getElementById("lastSync");

// Load saved settings
chrome.storage.sync.get(["serverUrl", "serviceKey"], (data) => {
  if (data.serverUrl)  serverUrlEl.value = data.serverUrl;
  if (data.serviceKey) serviceKeyEl.value = data.serviceKey;
});

// Load last sync info
chrome.storage.local.get(["lastSync", "lastStatus"], (data) => {
  if (data.lastSync) {
    const d = new Date(data.lastSync);
    lastSyncEl.textContent = `Last sync: ${d.toLocaleString()} — ${data.lastStatus || ""}`;
  }
});

saveBtn.addEventListener("click", () => {
  const serverUrl  = serverUrlEl.value.trim().replace(/\/$/, "");
  const serviceKey = serviceKeyEl.value.trim();

  if (!serverUrl || !serviceKey) {
    statusEl.className = "err";
    statusEl.textContent = "Both fields are required.";
    return;
  }

  chrome.storage.sync.set({ serverUrl, serviceKey }, () => {
    statusEl.className = "ok";
    statusEl.textContent = "✓ Saved. Cookie will sync automatically on next site visit.";
  });
});
