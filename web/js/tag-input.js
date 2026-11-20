// A [data-tags] input holds comma separated tags. The tag after its last comma, being
// typed, is completed from the list of tags in a popover under it, which leaves out the
// ones typed before it.
(() => {
  let seq = 0;

  const key = name => name.trim().toLocaleLowerCase();

  function parts(field) {
    return {
      input: field.querySelector('[role=combobox]'),
      list: field.querySelector('[role=listbox]'),
    };
  }

  function visibleOptions(list) {
    return [...list.querySelectorAll('[role=option]:not([hidden])')];
  }

  // typing is the tag after the last comma of the input, and head the text up to it.
  function split(input) {
    const comma = input.value.lastIndexOf(',');
    return { head: input.value.slice(0, comma + 1), typing: input.value.slice(comma + 1).trim() };
  }

  function setActive(field, li) {
    const { input, list } = parts(field);
    for (const o of list.querySelectorAll('[aria-selected]')) o.removeAttribute('aria-selected');
    if (!li) {
      input.removeAttribute('aria-activedescendant');
      return;
    }
    li.id ||= `tags-option-${++seq}`;
    li.setAttribute('aria-selected', 'true');
    input.setAttribute('aria-activedescendant', li.id);
    li.scrollIntoView({ block: 'nearest' });
  }

  // filter shows the tags containing the one being typed, but those typed before it,
  // making the first one active while a tag is typed.
  function filter(field) {
    const { input, list } = parts(field);
    const { head, typing } = split(input);
    const typed = new Set(head.split(',').map(key));
    const query = key(typing);
    for (const li of list.children) {
      const name = key(li.dataset.label);
      li.hidden = typed.has(name) || !name.includes(query);
    }
    setActive(field, typing ? visibleOptions(list)[0] : null);
  }

  function open(field) {
    const { input, list } = parts(field);
    if (!list.matches(':popover-open')) {
      list.id ||= `tags-list-${++seq}`;
      input.setAttribute('aria-controls', list.id);
      list.showPopover();
      input.setAttribute('aria-expanded', 'true');
    }
    filter(field);
  }

  function close(field) {
    const { input, list } = parts(field);
    if (list.matches(':popover-open')) list.hidePopover();
    input.setAttribute('aria-expanded', 'false');
    setActive(field, null);
  }

  // complete puts name in place of the tag being typed, followed by a comma for the next.
  function complete(field, name) {
    const { input } = parts(field);
    const { head } = split(input);
    input.value = `${head}${head ? ' ' : ''}${name}, `;
    filter(field);
  }

  const fieldOf = el => (el.role === 'combobox' || el.closest?.('[role=listbox]')) && el.closest('[data-tags]');

  document.addEventListener('focusin', e => {
    const field = fieldOf(e.target);
    if (!field) return;
    open(field);
    e.target.setSelectionRange(e.target.value.length, e.target.value.length);
  });

  document.addEventListener('focusout', e => {
    const field = fieldOf(e.target);
    if (field && !field.contains(e.relatedTarget)) close(field);
  });

  document.addEventListener('input', e => {
    const field = fieldOf(e.target);
    if (field) open(field);
  });

  document.addEventListener('keydown', e => {
    const field = fieldOf(e.target);
    if (!field) return;
    const { list } = parts(field);
    const isOpen = list.matches(':popover-open');
    const options = visibleOptions(list);
    const i = options.findIndex(o => o.hasAttribute('aria-selected'));
    switch (e.key) {
      case 'ArrowDown':
      case 'ArrowUp':
        e.preventDefault();
        if (!isOpen) {
          open(field);
          return;
        }
        if (options.length === 0) return;
        setActive(field, options[Math.max(0, Math.min(i + (e.key === 'ArrowDown' ? 1 : -1), options.length - 1))]);
        break;
      case 'Enter': {
        // a typed tag is ended as the active option, or as typed when none is, and the
        // form is submitted with none typed
        const { typing } = split(e.target);
        if (i < 0 && !typing) return;
        e.preventDefault();
        complete(field, i < 0 ? typing : options[i].dataset.label);
        break;
      }
      case 'Escape':
        if (!isOpen) return;
        // keep a dialog around the input open
        e.preventDefault();
        e.stopPropagation();
        close(field);
        break;
    }
  });

  // A tag is picked on click; pressing it keeps the focus in the input.
  document.addEventListener('mousedown', e => {
    if (fieldOf(e.target) && e.target.role !== 'combobox') e.preventDefault();
  });
  document.addEventListener('click', e => {
    const field = fieldOf(e.target);
    if (!field) return;
    if (e.target.role === 'combobox') {
      open(field);
      return;
    }
    const li = e.target.closest('[role=option]');
    if (!li) return;
    // the input is in a label, which would pass the click on to it
    e.preventDefault();
    complete(field, li.dataset.label);
  });
})();
