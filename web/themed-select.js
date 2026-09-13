/* Theme-aware single selects. The original control remains the form's source of truth. */
(() => {
  'use strict';
  const controls = new Map();
  const selectPrototype = HTMLSelectElement.prototype;
  let serial = 0, opened = null, started = false, observer;
  const optionDisabled = option => option.disabled || option.parentElement?.disabled || option.hidden;
  const optionText = option => option.label || option.textContent || option.value || '';
  const optionAvailable = (control, option) => !optionDisabled(option) && (!control.filter || optionText(option).toLocaleLowerCase().includes(control.filter));
  const visible = element => element.isConnected && element.getClientRects().length && !element.closest('[hidden]');
  const element = (tag, className) => { const result = document.createElement(tag); result.className = className; return result; };

  function labelFor(select) {
    if (select.getAttribute('aria-label')) return select.getAttribute('aria-label');
    const labels = [...(select.labels || [])].map(label => {
      const copy = label.cloneNode(true);
      copy.querySelectorAll('input,select,textarea,button,.themed-select').forEach(child => child.remove());
      return copy.textContent.trim();
    }).filter(Boolean);
    return labels.join(' ') || select.title || select.name || '选择选项';
  }

  function renderSelection(control) {
    const { select, trigger, value, wrapper } = control;
    const selected = select.options[select.selectedIndex];
    if (!control.editable) { value.textContent = selected?.label || selected?.textContent || '请选择'; trigger.title = select.title || value.textContent; }
    const disabled = select.matches(':disabled');
    if (trigger.disabled !== disabled) trigger.disabled = disabled;
    if (control.toggle && control.toggle.disabled !== disabled) control.toggle.disabled = disabled;
    trigger.setAttribute('aria-required', String(select.required));
    if (control.invalid && select.validity.valid) control.invalid = false;
    trigger.setAttribute('aria-invalid', String(!!control.invalid));
    if (wrapper.hidden !== select.hidden) wrapper.hidden = select.hidden;
    if (trigger.disabled && opened === control) close();
    if (opened === control) markOptions(control);
  }

  function markOptions(control) {
    for (const row of control.popup.querySelectorAll('[role="option"]')) {
      const index = Number(row.dataset.index);
      row.setAttribute('aria-selected', String(index === control.select.selectedIndex));
      row.classList.toggle('is-active', index === control.active);
    }
    if (opened === control && control.active >= 0) control.trigger.setAttribute('aria-activedescendant', `${control.popup.id}-${control.active}`);
    else control.trigger.removeAttribute('aria-activedescendant');
  }

  function refresh(control) {
    if (!control?.select.isConnected) return;
    const { select } = control;
    const signature = JSON.stringify([control.filter, ...[...select.options].map(option => [option.value, optionText(option), optionDisabled(option), option.parentElement?.tagName === 'OPTGROUP' ? option.parentElement.label : ''])]);
    if (signature !== control.signature) {
      const oldValue = control.activeValue;
      control.signature = signature;
      const children = []; let previousGroup = null;
      [...select.options].forEach((option, index) => {
        if (option.hidden || control.filter && !optionText(option).toLocaleLowerCase().includes(control.filter)) return;
        const group = option.parentElement?.tagName === 'OPTGROUP' ? option.parentElement : null;
        if (group && group !== previousGroup) { const heading = element('div', 'themed-select-group'); heading.textContent = group.label; heading.setAttribute('role', 'presentation'); children.push(heading); }
        previousGroup = group;
        const row = element('div', 'themed-select-option');
        row.id = `${control.popup.id}-${index}`; row.dataset.index = String(index); row.setAttribute('role', 'option'); row.setAttribute('aria-disabled', String(!!optionDisabled(option)));
        const text = element('span', 'themed-select-option-label'); text.textContent = optionText(option); row.append(text); row.title = optionText(option);
        children.push(row);
      });
      if (!children.length) { const empty = element('div', 'themed-select-empty'); empty.textContent = control.editable ? '可直接输入新分组' : '暂无可选项'; children.push(empty); }
      control.popup.replaceChildren(...children);
      const previous = [...select.options].findIndex(option => option.value === oldValue && optionAvailable(control, option));
      control.active = opened === control && previous >= 0 ? previous : select.selectedIndex;
      control.activeValue = select.options[control.active]?.value;
      if (opened === control) position(control);
    }
    renderSelection(control);
  }

  function position(control) {
    const { popup, trigger } = control;
    if (!visible(trigger)) { close(); return; }
    const anchor = trigger.getBoundingClientRect(), viewport = window.visualViewport;
    const leftEdge = viewport?.offsetLeft || 0, topEdge = viewport?.offsetTop || 0;
    const rightEdge = leftEdge + (viewport?.width || innerWidth), bottomEdge = topEdge + (viewport?.height || innerHeight);
    popup.style.left = '0px'; popup.style.top = '0px'; popup.style.minWidth = '0px'; popup.style.maxWidth = 'none'; popup.style.width = '100px';
    const probe = popup.getBoundingClientRect(), scale = probe.width / 100 || 1;
    const margin = 8, gap = 5 * scale;
    popup.style.width = 'max-content'; popup.style.minWidth = `${Math.min(Math.max(anchor.width, 145 * scale), rightEdge - leftEdge - 2 * margin) / scale}px`;
    popup.style.maxWidth = `${Math.min(380 * scale, rightEdge - leftEdge - 2 * margin) / scale}px`;
    const below = bottomEdge - anchor.bottom - gap - margin, above = anchor.top - topEdge - gap - margin;
    const available = Math.max(below, above, 48);
    popup.style.maxHeight = `${Math.min(282 * scale, available) / scale}px`;
    const size = popup.getBoundingClientRect();
    const useAbove = size.height > below && above > below;
    const x = Math.max(leftEdge + margin, Math.min(anchor.left, rightEdge - size.width - margin));
    const y = Math.max(topEdge + margin, Math.min(useAbove ? anchor.top - gap - size.height : anchor.bottom + gap, bottomEdge - size.height - margin));
    popup.style.left = `${(x - probe.left) / scale}px`; popup.style.top = `${(y - probe.top) / scale}px`;
    control.anchor = [anchor.left, anchor.top, anchor.width, anchor.height].join(',');
  }

  function setActive(control, index, scroll = true) {
    if (index < 0 || !control.select.options[index] || !optionAvailable(control, control.select.options[index])) return;
    control.active = index; control.activeValue = control.select.options[index].value;
    markOptions(control);
    if (scroll) {
      const row = control.popup.querySelector(`[data-index="${index}"]`);
      if (row) { const top = row.offsetTop, bottom = top + row.offsetHeight; if (top < control.popup.scrollTop) control.popup.scrollTop = top; else if (bottom > control.popup.scrollTop + control.popup.clientHeight) control.popup.scrollTop = bottom - control.popup.clientHeight; }
    }
  }

  function close(restoreFocus = false) {
    if (!opened) return;
    const control = opened; opened = null;
    cancelAnimationFrame(control.frame);
    if (typeof control.popup.hidePopover === 'function' && control.popup.matches(':popover-open')) control.popup.hidePopover();
    control.popup.hidden = true;
    control.trigger.setAttribute('aria-expanded', 'false'); control.trigger.removeAttribute('aria-activedescendant');
    control.wrapper.classList.remove('is-open'); control.typed = '';
    if (restoreFocus && visible(control.trigger)) control.trigger.focus({ preventScroll: true });
  }

  function open(control, selectFirst = true) {
    refresh(control);
    if (control.trigger.disabled || !visible(control.trigger)) return;
    close(); opened = control;
    // Keep dialog focus ownership; the Popover API also escapes scroll clipping.
    (control.select.closest('dialog[open]') || document.body).append(control.popup);
    control.popup.hidden = false;
    if (typeof control.popup.showPopover === 'function') { control.popup.setAttribute('popover', 'manual'); control.popup.showPopover(); }
    else control.popup.removeAttribute('popover');
    control.trigger.setAttribute('aria-expanded', 'true'); control.wrapper.classList.add('is-open');
    position(control);
    const options = [...control.select.options];
    if (selectFirst) setActive(control, optionAvailable(control, options[control.select.selectedIndex] || { disabled: true }) ? control.select.selectedIndex : options.findIndex(option => optionAvailable(control, option)));
    else { control.active = -1; control.activeValue = undefined; markOptions(control); }
    const track = () => {
      if (opened !== control) return;
      if (!visible(control.trigger) || control.trigger.disabled) { close(); return; }
      const rect = control.trigger.getBoundingClientRect();
      if ([rect.left, rect.top, rect.width, rect.height].join(',') !== control.anchor) position(control);
      control.frame = requestAnimationFrame(track);
    };
    control.frame = requestAnimationFrame(track);
  }

  function commit(control, index) {
    const option = control.select.options[index];
    if (!option || optionDisabled(option)) return;
    const changed = control.select.selectedIndex !== index;
    control.select.selectedIndex = index;
    close(true);
    if (changed) {
      control.committing = true;
      control.select.dispatchEvent(new Event('input', { bubbles: true }));
      control.select.dispatchEvent(new Event('change', { bubbles: true }));
      control.committing = false;
    }
    refresh(control);
  }

  function enhanceSuggestions(input) {
    if (controls.has(input)) return;
    const list = input.list;
    if (!list) return;
    const wrapper = element('span', 'themed-select themed-select-editable'), toggle = element('button', 'themed-select-toggle'), caret = element('span', 'themed-select-caret');
    const popup = element('div', 'themed-select-popup'); popup.id = `deng-select-${++serial}`; popup.hidden = true; popup.setAttribute('role', 'listbox'); popup.setAttribute('aria-label', labelFor(input));
    toggle.type = 'button'; toggle.tabIndex = -1; toggle.setAttribute('aria-label', `展开${labelFor(input)}`); caret.setAttribute('aria-hidden', 'true'); toggle.append(caret);
    input.before(wrapper); wrapper.append(input, toggle);
    input.dataset.dengList = input.getAttribute('list'); input.removeAttribute('list');
    input.setAttribute('role', 'combobox'); input.setAttribute('aria-haspopup', 'listbox'); input.setAttribute('aria-autocomplete', 'list'); input.setAttribute('aria-expanded', 'false'); input.setAttribute('aria-controls', popup.id); input.setAttribute('autocomplete', 'off');
    // An adapter lets editable suggestions share the popup without adding a second form control.
    const select = {
      get options() { return list.options; },
      get selectedIndex() { return [...list.options].findIndex(option => option.value === input.value); },
      set selectedIndex(index) { input.value = list.options[index].value; },
      get value() { return input.value; }, get isConnected() { return input.isConnected; },
      get required() { return input.required; }, get hidden() { return input.hidden; }, get validity() { return input.validity; },
      matches: query => input.matches(query), closest: query => input.closest(query), dispatchEvent: event => input.dispatchEvent(event),
    };
    const control = { select, wrapper, trigger: input, toggle, popup, editable: true, active: -1, typed: '', typedAt: 0, filter: '' };
    controls.set(input, control);
    control.observer = new MutationObserver(() => refresh(control));
    control.observer.observe(list, { subtree: true, childList: true, characterData: true, attributes: true });
    control.observer.observe(input, { attributes: true, attributeFilter: ['disabled', 'required', 'hidden'] });
    input.addEventListener('input', () => {
      if (control.committing) return;
      control.filter = input.value.toLocaleLowerCase(); control.activeValue = undefined;
      if (opened !== control) open(control, false); else { refresh(control); control.active = -1; markOptions(control); }
    });
    input.addEventListener('change', () => { if (!control.committing) refresh(control); });
    input.addEventListener('click', () => { if (opened !== control) { control.filter = ''; open(control, false); } });
    input.addEventListener('keydown', event => {
      if (event.isComposing) return;
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
        event.preventDefault(); event.stopPropagation();
        if (opened !== control) { control.filter = ''; open(control, false); }
        const enabled = [...list.options].map((option, index) => optionAvailable(control, option) ? index : -1).filter(index => index >= 0);
        const index = enabled.indexOf(control.active), next = index < 0 ? (event.key === 'ArrowDown' ? 0 : enabled.length - 1) : Math.max(0, Math.min(enabled.length - 1, index + (event.key === 'ArrowDown' ? 1 : -1)));
        setActive(control, enabled[next]);
      } else if (opened === control && event.key === 'Enter' && control.active >= 0) { event.preventDefault(); event.stopPropagation(); commit(control, control.active); }
      else if (event.key === 'Tab' || event.key === 'Enter') close();
    });
    toggle.addEventListener('pointerdown', event => event.preventDefault());
    toggle.addEventListener('click', event => { event.preventDefault(); event.stopPropagation(); input.focus({ preventScroll: true }); if (opened === control) close(); else { control.filter = ''; open(control, false); } });
    popup.addEventListener('pointerdown', event => event.preventDefault());
    popup.addEventListener('click', event => { event.preventDefault(); event.stopPropagation(); const row = event.target.closest('[role="option"]'); if (row) commit(control, Number(row.dataset.index)); });
    popup.addEventListener('pointermove', event => { const row = event.target.closest('[role="option"]'); if (row) setActive(control, Number(row.dataset.index), false); });
    input.form?.addEventListener('reset', () => queueMicrotask(() => { control.filter = ''; refresh(control); }));
    refresh(control);
  }

  function keydown(event, control) {
    if (control.trigger.disabled || event.isComposing) return;
    if (Date.now() - control.typedAt > 700) control.typed = '';
    const key = event.key, isOpen = opened === control;
    if (key === 'Tab') { if (isOpen) { commit(control, control.active); } return; }
    if (key === 'Escape') { if (isOpen) { event.preventDefault(); event.stopPropagation(); close(true); } return; }
    const arrows = ['ArrowDown', 'ArrowUp', 'Home', 'End', 'PageDown', 'PageUp'];
    if (arrows.includes(key)) {
      event.preventDefault(); event.stopPropagation();
      if (event.altKey && key === 'ArrowUp') { close(true); return; }
      if (!isOpen) open(control);
      const enabled = [...control.select.options].map((option, index) => optionDisabled(option) ? -1 : index).filter(index => index >= 0);
      if (!enabled.length || (!isOpen && ['ArrowDown', 'ArrowUp'].includes(key))) return;
      const current = enabled.indexOf(control.active), delta = key === 'PageDown' ? 8 : key === 'PageUp' ? -8 : key === 'ArrowDown' ? 1 : -1;
      setActive(control, enabled[key === 'Home' ? 0 : key === 'End' ? enabled.length - 1 : Math.max(0, Math.min(enabled.length - 1, current + delta))]);
    } else if (key === 'Enter' || key === ' ' && !control.typed) {
      event.preventDefault(); event.stopPropagation();
      if (isOpen) commit(control, control.active); else open(control);
    } else if (key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
      event.preventDefault(); event.stopPropagation();
      if (!isOpen) open(control);
      const now = Date.now(); control.typed = now - control.typedAt < 700 ? control.typed + key : key; control.typedAt = now;
      const repeated = [...control.typed].every(char => char.toLocaleLowerCase() === key.toLocaleLowerCase());
      const query = (repeated ? key : control.typed).toLocaleLowerCase(), options = [...control.select.options];
      const start = repeated ? control.active + 1 : control.active;
      for (let i = 0; i < options.length; i++) { const index = (Math.max(0, start) + i) % options.length; if (!optionDisabled(options[index]) && optionText(options[index]).trim().toLocaleLowerCase().startsWith(query)) { setActive(control, index); break; } }
    }
  }

  function enhance(select) {
    if (controls.has(select) || select.multiple || select.size > 1 || select.dataset.nativeSelect !== undefined) return;
    const wrapper = element('span', 'themed-select'), trigger = element('button', 'themed-select-trigger'), value = element('span', 'themed-select-value'), caret = element('span', 'themed-select-caret');
    const popup = element('div', 'themed-select-popup'); popup.id = `deng-select-${++serial}`; popup.hidden = true; popup.setAttribute('role', 'listbox');
    trigger.type = 'button'; trigger.setAttribute('role', 'combobox'); trigger.setAttribute('aria-haspopup', 'listbox'); trigger.setAttribute('aria-expanded', 'false'); trigger.setAttribute('aria-controls', popup.id);
    trigger.setAttribute('aria-label', labelFor(select)); popup.setAttribute('aria-label', labelFor(select));
    for (const attribute of ['aria-labelledby', 'aria-describedby']) if (select.hasAttribute(attribute)) trigger.setAttribute(attribute, select.getAttribute(attribute));
    if (select.hasAttribute('tabindex')) trigger.tabIndex = select.tabIndex;
    caret.setAttribute('aria-hidden', 'true'); trigger.append(value, caret);
    if (select.id) { wrapper.dataset.selectId = select.id; popup.dataset.selectId = select.id; }
    select.before(wrapper); wrapper.append(select, trigger);
    select.classList.add('themed-select-native'); select.tabIndex = -1; select.setAttribute('aria-hidden', 'true');
    const control = { select, wrapper, trigger, value, popup, active: select.selectedIndex, activeValue: select.value, typed: '', typedAt: 0 };
    controls.set(select, control);
    // Programmatic updates in profile editors and monitor refreshes do not emit change.
    // Override only these instances; platform prototypes and unrelated controls stay intact.
    for (const property of ['value', 'selectedIndex']) {
      const descriptor = Object.getOwnPropertyDescriptor(selectPrototype, property);
      Object.defineProperty(select, property, { configurable: true, get() { return descriptor.get.call(this); }, set(next) { descriptor.set.call(this, next); renderSelection(control); } });
    }
    control.observer = new MutationObserver(() => refresh(control));
    control.observer.observe(select, { subtree: true, childList: true, characterData: true, attributes: true });
    select.addEventListener('change', () => refresh(control));
    select.addEventListener('input', () => refresh(control));
    select.addEventListener('focus', () => trigger.focus({ preventScroll: true }));
    select.addEventListener('invalid', event => {
      event.preventDefault(); control.invalid = true; renderSelection(control);
      trigger.title = select.validationMessage;
      if (!select.form || select.form.querySelector(':invalid') === select) trigger.focus({ preventScroll: true });
    });
    select.form?.addEventListener('reset', () => queueMicrotask(() => { control.invalid = false; refresh(control); }));
    trigger.addEventListener('focus', () => refresh(control));
    trigger.addEventListener('click', event => { event.preventDefault(); event.stopPropagation(); opened === control ? close() : open(control); });
    trigger.addEventListener('keydown', event => keydown(event, control));
    popup.addEventListener('pointerdown', event => event.preventDefault());
    popup.addEventListener('click', event => { event.preventDefault(); event.stopPropagation(); const row = event.target.closest('[role="option"]'); if (row) commit(control, Number(row.dataset.index)); });
    popup.addEventListener('pointermove', event => { const row = event.target.closest('[role="option"]'); if (row) setActive(control, Number(row.dataset.index), false); });
    refresh(control);
  }

  function start() {
    if (started) return; started = true;
    document.querySelectorAll('select').forEach(enhance);
    document.querySelectorAll('input[list]').forEach(enhanceSuggestions);
    observer = new MutationObserver(records => {
      for (const record of records) for (const added of record.addedNodes) {
        if (added.nodeType !== Node.ELEMENT_NODE) continue;
        if (added.matches('select')) enhance(added);
        else added.querySelectorAll('select').forEach(enhance);
        if (added.matches('input[list]')) enhanceSuggestions(added);
        else added.querySelectorAll('input[list]').forEach(enhanceSuggestions);
      }
      if (records.some(record => record.type === 'attributes')) for (const control of controls.values()) renderSelection(control);
      for (const [select, control] of controls) if (!select.isConnected) { if (opened === control) close(); control.observer.disconnect(); control.popup.remove(); controls.delete(select); }
    });
    observer.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ['disabled', 'hidden', 'open'] });
    document.addEventListener('pointerdown', event => { if (opened && !opened.wrapper.contains(event.target) && !opened.popup.contains(event.target)) close(); }, true);
    document.addEventListener('focusin', event => { if (opened && !opened.wrapper.contains(event.target) && !opened.popup.contains(event.target)) close(); });
    document.addEventListener('keydown', event => { if (opened && event.key === 'Escape') { event.preventDefault(); event.stopImmediatePropagation(); close(true); } }, true);
    window.addEventListener('resize', () => close());
    window.visualViewport?.addEventListener('resize', () => close());
  }
  window.DengSelect = { refresh(select) { if (!started) start(); if (select) { enhance(select); refresh(controls.get(select)); } else controls.forEach(refresh); }, close };
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start, { once: true }); else start();
})();
