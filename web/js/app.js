// Show the scroll-to-top button only once the page is scrolled past one screen.
addEventListener('scroll', () => {
  for (const button of document.querySelectorAll('.to-top')) {
    button.hidden = scrollY < innerHeight;
  }
}, { passive: true });

// Show the toasts the server rendered into #toasts, on page load and after htmx swaps.
function showToasts() {
  for (const toast of document.querySelectorAll('#toasts > .toast')) {
    ot.toast.el(toast);
    toast.remove();
  }
}
document.addEventListener('DOMContentLoaded', showToasts);
document.addEventListener('htmx:after:settle', showToasts);

// A form that failed validation comes back as the whole page, kept at its scroll
// position by the server, so bring its first invalid field into view and focus it.
function focusInvalid(root) {
  const field = root.querySelector?.(':is(input, select, textarea)[aria-invalid]');
  if (!field) return;
  field.scrollIntoView({ block: 'center' });
  field.focus({ preventScroll: true });
}
document.addEventListener('DOMContentLoaded', () => focusInvalid(document));
document.addEventListener('htmx:after:settle', e => focusInvalid(e.target));

// Hide a field's server message once the user edits the field.
for (const type of ['input', 'change']) {
  document.addEventListener(type, e => {
    const field = e.target.closest('[data-field][aria-invalid]');
    if (!field) return;
    field.removeAttribute('aria-invalid');
    e.target.removeAttribute('aria-invalid');
  });
}

// The history-cache extension restores pages from sessionStorage on back and forward,
// so after a request that changes data, such as archiving an account or editing an
// operation, drop the cached pages to load them anew. Such a request answers with
// HX-Location, and htmx saves the page it leaves, still showing the old data, before
// following it, so that save is skipped too, until the new page is swapped in.
let historyStale = false;
document.addEventListener('htmx:after:request', e => {
  if (e.detail.ctx.request.method === 'GET') return;
  historyStale = true;
  try {
    for (const key of Object.keys(sessionStorage)) {
      if (key.startsWith('htmx-history-')) sessionStorage.removeItem(key);
    }
  } catch {}
});
document.addEventListener('htmx:history:cache:before:save', e => {
  if (historyStale) e.preventDefault();
});
document.addEventListener('htmx:after:swap', () => {
  historyStale = false;
});

// The form of an operation of a list, or of a new one, is swapped into the dialog of the
// layout, which then opens, non-modal so the nav stays usable, see app.css. Saving or deleting it posts the rows the page has loaded, which the
// page is reloaded through in place, not counting the parts of a split the dialog lists.
document.addEventListener('htmx:after:settle', e => {
  if (e.target.id === 'op-dialog' && !e.target.open) e.target.show();
});
// A button of command="--show" opens the dialog it is for non-modal, so the nav stays
// usable, see app.css; there is no built-in command for that.
document.addEventListener('command', e => {
  if (e.command === '--show') e.target.show();
}, true);
// Escape, or a click outside, in a step of the dialog returns to the step before it, as
// its close button does.
document.addEventListener('cancel', e => {
  const back = e.target.querySelector?.(':scope > header [data-dialog-back]');
  if (!back) return;
  e.preventDefault();
  back.click();
}, true);
document.addEventListener('htmx:config:request', e => {
  const ctx = e.detail.ctx;
  if (ctx.request.method === 'GET' || !ctx.sourceElement.closest?.('#op-dialog')) return;
  ctx.request.body.set('rows', String(document.querySelectorAll('[data-op]:not(#op-dialog *)').length));
});

// A [data-copy] button copies its value and shows a check icon for a moment.
document.addEventListener('click', async e => {
  const button = e.target.closest('[data-copy]');
  if (!button) return;
  await navigator.clipboard.writeText(button.dataset.copy);
  button.querySelector('[data-icon=copy]').hidden = true;
  button.querySelector('[data-icon=check]').hidden = false;
  setTimeout(() => {
    button.querySelector('[data-icon=copy]').hidden = false;
    button.querySelector('[data-icon=check]').hidden = true;
  }, 1500);
});

// htmx swaps error responses too. A form that failed validation is re-rendered with a
// 4xx, but a server error is plain text, so show it as a toast instead of swapping it in.
document.addEventListener('htmx:response:error', e => {
  const ctx = e.detail.ctx;
  if (ctx.response.status < 500) return;
  ctx.swap = 'none';
  ot.toast(ctx.text, '', { variant: 'danger' });
});

// In the split form the first part, the operation itself, keeps what the other parts
// leave of the total, so typing a part's amount takes it from the first one. It counts
// in units of the finest digit typed, so a part with more digits than the currency has
// isn't rounded; the server reports those.
function splitRest(form) {
  const [first, ...parts] = form.querySelectorAll('[name=split_amount]');
  const total = form.dataset.splitTotal;
  const minDigits = total.split('.')[1]?.length ?? 0;
  const values = parts.map(p => p.value.replace(',', '.')).filter(v => !Number.isNaN(Number(v)));
  let digits = Math.max(minDigits, ...values.map(v => v.split('.')[1]?.length ?? 0));
  let rest = Math.round(Number(total) * 10 ** digits);
  for (const v of values) rest -= Math.round(Number(v) * 10 ** digits);
  // drop the extra trailing zeros, keeping the currency's digits
  while (digits > minDigits && rest % 10 === 0) {
    rest /= 10;
    digits--;
  }
  const scale = 10 ** digits;
  first.value = rest % scale ? (rest / scale).toFixed(digits) : String(rest / scale);
}

document.addEventListener('input', e => {
  const form = e.target.closest('[data-split-total]');
  if (form && e.target.name === 'split_amount' && e.target !== form.querySelector('[name=split_amount]')) {
    splitRest(form);
  }
});

document.addEventListener('click', e => {
  const button = e.target.closest('[data-split-remove]');
  if (!button) return;
  const form = button.form;
  const part = button.closest('.split');
  if (part === form.querySelector('.split')) return;
  part.remove();
  splitRest(form);
});

// The credit limit field of an account form is shown only for credit cards. Import rows
// post every field in order, so there it is only hidden; the server ignores it then.
document.addEventListener('change', e => {
  if (!e.target.matches('[data-account-kind]')) return;
  const row = e.target.closest('[data-account]');
  const field = (row ?? e.target.form).querySelector('[data-credit-limit]');
  field.hidden = e.target.value !== 'credit';
  if (!row) field.querySelector('input').disabled = field.hidden;
});

// Privacy mode blurs amounts, see web/static/js/privacy.js, which turns it on before the
// page is drawn. The toggle is in the nav, swapped with every boosted page, so its
// pressed state is set anew after each swap.
function syncPrivacy() {
  const on = document.documentElement.classList.contains('private');
  for (const button of document.querySelectorAll('[data-privacy-toggle]')) {
    button.setAttribute('aria-pressed', String(on));
  }
}
document.addEventListener('DOMContentLoaded', syncPrivacy);
document.addEventListener('htmx:after:settle', syncPrivacy);
document.addEventListener('click', e => {
  if (!e.target.closest('[data-privacy-toggle]')) return;
  const on = document.documentElement.classList.toggle('private');
  try {
    localStorage.setItem('privacy', on ? '1' : '0');
  } catch {}
  syncPrivacy();
});
