// A [data-category-select] picks a category by typing: its search input filters the
// list of categories in a popover under it, and its hidden input holds the picked value.
// A [data-multiple] one picks any number of them, each option holding a checkbox; its
// search input lists the picked ones while the list is closed.
(() => {
  let seq = 0;

  function parts(picker) {
    return {
      value: picker.querySelector('input[type=hidden]'),
      input: picker.querySelector('[role=combobox]'),
      list: picker.querySelector('[role=listbox]'),
    };
  }

  function visibleOptions(list) {
    return [...list.querySelectorAll('[role=option]:not([hidden])')];
  }

  // filter shows the options whose label contains query, with the cat1 of a shown cat2
  // and the kind of a shown option. Options left out by kind, [data-off], stay hidden.
  function filter(list, query) {
    query = query.trim().toLocaleLowerCase();
    let group;
    let parent;
    for (const li of list.children) {
      if (li.hasAttribute('data-group')) {
        group = li;
        li.hidden = true;
        continue;
      }
      li.hidden = li.hasAttribute('data-off') || !li.dataset.label.toLocaleLowerCase().includes(query);
      if (!li.hasAttribute('data-child')) {
        parent = li;
      } else if (!li.hidden && parent && li.dataset.label.startsWith(parent.dataset.label + ' / ')) {
        parent.hidden = false;
      }
      if (!li.hidden && group && li.dataset.kind) group.hidden = false;
    }
  }

  function setActive(picker, li) {
    const { input, list } = parts(picker);
    for (const o of list.querySelectorAll('[aria-selected]')) o.removeAttribute('aria-selected');
    if (!li) {
      input.removeAttribute('aria-activedescendant');
      return;
    }
    li.id ||= `category-option-${++seq}`;
    li.setAttribute('aria-selected', 'true');
    input.setAttribute('aria-activedescendant', li.id);
    li.scrollIntoView({ block: 'nearest' });
  }

  const isMultiple = picker => picker.hasAttribute('data-multiple');

  // summary lists the own names of the checked options of a multiple picker, a cat2
  // without its cat1.
  function summary(picker) {
    return [...parts(picker).list.querySelectorAll('[role=option]')]
      .filter(o => o.querySelector('input').checked)
      .map(o => o.textContent.trim())
      .join(', ');
  }

  function open(picker) {
    const { value, input, list } = parts(picker);
    if (list.matches(':popover-open')) return;
    list.id ||= `category-list-${++seq}`;
    input.setAttribute('aria-controls', list.id);
    filter(list, '');
    list.showPopover();
    input.setAttribute('aria-expanded', 'true');
    setActive(picker, visibleOptions(list).find(o => isMultiple(picker) || o.dataset.value === value.value));
  }

  // close hides the list, putting the label of the picked value back in the input. An
  // emptied input unpicks the category, taking the first choice, such as "All", if any.
  function close(picker) {
    const { value, input, list } = parts(picker);
    if (list.matches(':popover-open')) list.hidePopover();
    input.setAttribute('aria-expanded', 'false');
    setActive(picker, null);
    if (isMultiple(picker)) {
      input.value = summary(picker);
      return;
    }
    if (input.value.trim() === '') {
      value.value = list.querySelector('[role=option]:not([data-kind])')?.dataset.value ?? '';
    }
    const picked = [...list.querySelectorAll('[role=option]')].find(o => o.dataset.value === value.value);
    input.value = picked?.dataset.label ?? '';
  }

  function pick(picker, li) {
    const { value, input } = parts(picker);
    if (isMultiple(picker)) {
      const box = li.querySelector('input');
      box.checked = !box.checked;
      box.dispatchEvent(new Event('change', { bubbles: true }));
      return;
    }
    value.value = li.dataset.value;
    // an empty input would unpick it on close
    input.value = li.dataset.label;
    close(picker);
    input.dispatchEvent(new Event('change', { bubbles: true }));
  }

  const pickerOf = el => el.closest?.('[data-category-select]');

  document.addEventListener('focusin', e => {
    const picker = pickerOf(e.target);
    if (!picker || e.target.role !== 'combobox') return;
    open(picker);
    e.target.select();
  });

  document.addEventListener('focusout', e => {
    const picker = pickerOf(e.target);
    if (picker && !picker.contains(e.relatedTarget)) close(picker);
  });

  document.addEventListener('input', e => {
    const picker = pickerOf(e.target);
    if (!picker || e.target.role !== 'combobox') return;
    const { list } = parts(picker);
    open(picker);
    filter(list, e.target.value);
    setActive(picker, visibleOptions(list)[0]);
  });

  document.addEventListener('keydown', e => {
    const picker = pickerOf(e.target);
    if (!picker || e.target.role !== 'combobox') return;
    const { list } = parts(picker);
    const isOpen = list.matches(':popover-open');
    const options = visibleOptions(list);
    const i = options.findIndex(o => o.hasAttribute('aria-selected'));
    switch (e.key) {
      case 'ArrowDown':
      case 'ArrowUp':
        e.preventDefault();
        if (!isOpen) {
          open(picker);
          return;
        }
        if (options.length === 0) return;
        if (e.key === 'ArrowDown') setActive(picker, options[Math.min(i + 1, options.length - 1)]);
        else setActive(picker, options[Math.max(i - 1, 0)]);
        break;
      case 'Enter':
        if (!isOpen || i < 0) return;
        e.preventDefault();
        pick(picker, options[i]);
        break;
      case 'Escape':
        if (!isOpen) return;
        // keep a dialog around the picker open
        e.preventDefault();
        e.stopPropagation();
        close(picker);
        break;
    }
  });

  // An option is picked on click; pressing it keeps the focus in the input.
  document.addEventListener('mousedown', e => {
    if (pickerOf(e.target) && e.target.closest('[role=listbox]')) e.preventDefault();
  });
  document.addEventListener('click', e => {
    const picker = pickerOf(e.target);
    if (!picker) return;
    if (e.target.role === 'combobox') {
      open(picker);
      return;
    }
    if (!e.target.closest('[role=listbox]')) return;
    // a checkbox toggles itself
    if (e.target.type === 'checkbox') return;
    // the picker is in a label, which would pass the click on to the input
    e.preventDefault();
    const li = e.target.closest('[role=option]');
    if (li) pick(picker, li);
  });

  // A checkbox changed while the list is closed, such as by the type filter, is listed.
  document.addEventListener('change', e => {
    const picker = pickerOf(e.target);
    if (picker && isMultiple(picker) && !parts(picker).list.matches(':popover-open')) {
      parts(picker).input.value = summary(picker);
    }
  });
})();
