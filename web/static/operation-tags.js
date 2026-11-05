// Oat manages chips; the adapter preserves the server's comma-separated field.
(() => {
  function synchronize(field) {
    const component = field.querySelector('ot-taginput');
    const input = component.querySelector('input');
    const tags = [...new Set([...(component.value || []), ...input.value.split(',')]
      .map(tag => tag.trim()).filter(Boolean))];
    const value = tags.join(', ');
    field.querySelector('[name="tags"]').value = value;
    return value;
  }

  document.addEventListener('click', e => {
    const cancel = e.target.closest?.('[data-operation-cancel]');
    const dialog = cancel?.closest('dialog');
    if (!dialog) return;
    e.preventDefault();
    e.stopPropagation();
    dialog.close();
  }, true);

  // Let native constraints drive Oat's field error presentation.
  document.addEventListener('invalid', e => {
    if (e.target.closest('form[id^="op-form-"]')) {
      e.target.setAttribute('aria-invalid', 'true');
    }
  }, true);

  for (const event of ['input', 'change']) {
    document.addEventListener(event, e => {
      const form = e.target.closest?.('form[id^="op-form-"]');
      if (!form) return;
      const amount = form.elements.amount;
      if (e.target === amount && form.elements.type.value !== 'adjustment') {
        amount.setCustomValidity(amount.value && Number(amount.value.replace(',', '.')) <= 0
          ? form.querySelector(`#amount-error-${form.elements.type.value}`).textContent : '');
      }
      const destination = form.elements.to_account;
      if (destination && ['account', 'to_account'].includes(e.target.name)) {
        destination.setCustomValidity(destination.value === form.elements.account.value
          ? form.querySelector(`#to-account-error-${form.elements.type.value}`).textContent : '');
        if (destination.hasAttribute('aria-invalid')) {
          destination.setAttribute('aria-invalid', String(!destination.validity.valid));
        }
      }
      if (e.target.validity && e.target.hasAttribute('aria-invalid')) {
        e.target.setAttribute('aria-invalid', String(!e.target.validity.valid));
      }
    });
  }

  // Capture typing before Oat stops propagation of the native input event.
  for (const event of ['input', 'focus']) {
    document.addEventListener(event, e => {
      const field = e.target.closest?.('[data-operation-tags]');
      if (!field) return;
      synchronize(field);
      const component = field.querySelector('ot-taginput');
      const input = component.querySelector('input');
      const selected = component.value || [];
      const query = input.value.toLocaleLowerCase();
      input.list.replaceChildren(...[...field.querySelector('template').content.children]
        .filter(option => !selected.includes(option.value) &&
          option.value.toLocaleLowerCase().startsWith(query))
        .map(option => option.cloneNode(true)));
    }, true);
  }

  document.addEventListener('submit', e => {
    const field = e.target.querySelector('[data-operation-tags]');
    if (field) synchronize(field);
  }, true);

  document.addEventListener('formdata', e => {
    const field = e.target.querySelector('[data-operation-tags]');
    if (field) e.formData.set('tags', synchronize(field));
  }, true);

  document.addEventListener('htmx:config:request', e => {
    const request = e.detail.ctx.request;
    const field = request.form?.querySelector('[data-operation-tags]');
    if (field) request.body.set('tags', synchronize(field));
  });
})();
