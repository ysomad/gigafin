// Show the scroll-to-top button only once the page is scrolled past one screen.
addEventListener('scroll', () => {
  for (const button of document.querySelectorAll('.to-top')) {
    button.hidden = scrollY < innerHeight;
  }
}, { passive: true });

// Browser validation shown the Oat way: a field failing its constraints gets
// aria-invalid, which reveals the .error message of its [data-field], instead of the
// browser's bubble, and the first invalid field of the form is focused. Fields
// without an .error message keep the bubble.
document.addEventListener('invalid', e => {
  if (!e.target.closest('[data-field]')?.querySelector('.error')) return;
  e.preventDefault();
  e.target.setAttribute('aria-invalid', 'true');
  if (e.target === e.target.form?.querySelector(':is(input, select, textarea):invalid')) {
    e.target.focus();
  }
}, true);

// Hide the message once the field is valid, and show it again if it is not.
for (const type of ['input', 'change']) {
  document.addEventListener(type, e => {
    if (e.target.validity && e.target.hasAttribute('aria-invalid')) {
      e.target.setAttribute('aria-invalid', String(!e.target.validity.valid));
    }
  });
}

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
