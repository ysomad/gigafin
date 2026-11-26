// Turns privacy mode on before the page is drawn, so amounts never show unblurred.
// Loaded blocking in <head>: the app bundle is deferred. See web/js/app.js.
try {
  if (localStorage.getItem('privacy') === '1') document.documentElement.classList.add('private');
} catch {}
