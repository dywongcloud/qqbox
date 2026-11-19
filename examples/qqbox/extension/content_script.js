// Runs on the local witness page only. dump-dom of that page shows this marker
// attribute, which proves the extension's content script really executed.
document.documentElement.setAttribute('data-qqbox-extension', 'loaded');
console.log('[qqbox-ext] content_script_ran:' + location.href);
