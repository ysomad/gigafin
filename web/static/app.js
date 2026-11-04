// Submits month selects on change: the month-nav pager and the dashboard compare form.
// The listener is on document since hx-boost swaps the body.
(() => {
  document.addEventListener('change', (e) => {
    if (e.target.matches('form.month-nav select, form.compare select')) e.target.form.requestSubmit();
  });
})();
