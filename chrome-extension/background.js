// background.js — service worker
// Watches for datadome cookie changes and syncs them to the configured server.

const COOKIE_NAME = "datadome";

async function getConfig() {
  return new Promise((resolve) => {
    chrome.storage.sync.get(["serverUrl", "serviceKey"], (data) => {
      resolve({
        serverUrl: (data.serverUrl || "").trim(),
        serviceKey: (data.serviceKey || "").trim(),
      });
    });
  });
}

async function syncCookie(cookieValue) {
  const { serverUrl, serviceKey } = await getConfig();
  if (!serverUrl || !serviceKey) return;

  try {
    const resp = await fetch(`${serverUrl}/v1/cookies/datadome`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Service-Key": serviceKey,
      },
      body: JSON.stringify({ cookie: cookieValue }),
    });
    const data = await resp.json();
    if (resp.ok) {
      console.log("[DataDome Sync] ✓ synced cookie to server:", data.message);
      chrome.storage.local.set({ lastSync: new Date().toISOString(), lastStatus: "ok" });
    } else {
      console.warn("[DataDome Sync] server error:", data);
      chrome.storage.local.set({ lastSync: new Date().toISOString(), lastStatus: "error: " + JSON.stringify(data) });
    }
  } catch (e) {
    console.warn("[DataDome Sync] fetch failed:", e.message);
    chrome.storage.local.set({ lastSync: new Date().toISOString(), lastStatus: "failed: " + e.message });
  }
}

// Listen for any datadome cookie change across all sites.
chrome.cookies.onChanged.addListener((changeInfo) => {
  const { cookie, removed } = changeInfo;
  if (removed) return;
  if (cookie.name !== COOKIE_NAME) return;
  if (!cookie.value) return;

  console.log(`[DataDome Sync] cookie updated on ${cookie.domain}`);
  syncCookie(cookie.value);
});
