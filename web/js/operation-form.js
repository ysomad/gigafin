// The operation form checks the amount and the destination account as they change.
(() => {
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
})();
