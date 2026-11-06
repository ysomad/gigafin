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
