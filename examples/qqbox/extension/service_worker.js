// qqbox witness MV3 service worker. All network traffic goes to the local
// witness server on loopback; no external host, no credentials, no tabs changed.
const WITNESS = 'http://127.0.0.1:38765/sw';

function witness(event, detail) {
  const line = event + (detail ? ':' + detail : '');
  console.log('[qqbox-ext] ' + line);
  fetch(WITNESS + '?event=' + encodeURIComponent(line)).catch((e) => {
    console.log('[qqbox-ext] witness_fetch_failed:' + String(e));
  });
}

chrome.runtime.onInstalled.addListener(() => {
  chrome.action.setBadgeText({ text: 'qq' });
  chrome.action.setBadgeBackgroundColor({ color: '#1a73e8' });
  chrome.storage.local.set({ installedAt: Date.now(), badge: 'qqbox' });
  witness('installed', chrome.runtime.id);
});

chrome.runtime.onStartup.addListener(() => witness('startup', chrome.runtime.id));

chrome.tabs.onUpdated.addListener((tabId, changeInfo, tab) => {
  if (changeInfo.status === 'complete' && tab && tab.url) {
    witness('tab_complete', tab.url);
  }
});

witness('service_worker_evaluated', chrome.runtime.id);
