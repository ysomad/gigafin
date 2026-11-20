// Oat manages chips; the adapter preserves the server's comma-separated field, or
// posts each tag in a field of the same name when it is [data-repeat].
(() => {
  function synchronize(field) {
    const component = field.querySelector('ot-taginput');
    const input = component.querySelector('input');
    const tags = [...new Set([...(component.value || []), ...input.value.split(',')]
      .map(tag => tag.trim()).filter(Boolean))];
    field.querySelector('input[type=hidden]').value = tags.join(', ');
    return tags;
  }

  // fill sets the tags of field in data, form data or search params.
  function fill(field, data) {
    const name = field.querySelector('input[type=hidden]').name;
    const tags = synchronize(field);
    if (!field.hasAttribute('data-repeat')) {
      data.set(name, tags.join(', '));
      return;
    }
    data.delete(name);
    for (const tag of tags) data.append(name, tag);
  }

  for (const event of ['input', 'change']) {
    document.addEventListener(event, e => {
      const form = e.target.closest?.('form[id^="op-form-"]');
      if (!form) return;
      const amount = form.elements.amount;
      if (e.target === amount && form.elements.type.value !== 'adjustment') {
        amount.setCustomValidity(amount.value && Number(amount.value.replace(',', '.')) <= 0
          ? form.querySelector('[id^="amount-error-"]').textContent : '');
      }
      const destination = form.elements.to_account;
      if (destination && ['account', 'to_account'].includes(e.target.name)) {
        destination.setCustomValidity(destination.value === form.elements.account.value
          ? form.querySelector('[id^="to-account-error-"]').textContent : '');
        if (destination.hasAttribute('aria-invalid')) {
          destination.setAttribute('aria-invalid', String(!destination.validity.valid));
        }
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
    if (field) fill(field, e.formData);
  }, true);

  document.addEventListener('htmx:config:request', e => {
    const request = e.detail.ctx.request;
    const field = request.form?.querySelector('[data-operation-tags]');
    if (field) fill(field, request.body);
  });
})();
