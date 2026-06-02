/*
 * certificates.js — client-side filter and confirm dialogs for the
 * certificates list page.
 */
(function () {
  var table = document.getElementById('certsTable');
  if (!table) { return; }
  var rows = Array.prototype.slice.call(
    table.querySelectorAll('tbody tr')
  ).filter(function (r) { return r.hasAttribute('data-name'); });
  var fSearch = document.getElementById('f-search');
  var fStatus = document.getElementById('f-status');
  var fType = document.getElementById('f-type');
  var fReset = document.getElementById('f-reset');
  var counter = document.getElementById('f-counter');
  var total = rows.length;

  /* populate key-type options from the table itself */
  var types = {};
  rows.forEach(function (r) {
    var t = r.getAttribute('data-type');
    if (t) { types[t] = 1; }
  });
  Object.keys(types).sort().forEach(function (t) {
    var opt = document.createElement('option');
    opt.value = t;
    opt.textContent = t;
    fType.appendChild(opt);
  });

  function applyFilters() {
    var q = (fSearch.value || '').toLowerCase().trim();
    var status = fStatus.value;
    var keytype = fType.value;
    var shown = 0;
    rows.forEach(function (r) {
      var name = (r.getAttribute('data-name') || '').toLowerCase();
      var domain = (r.getAttribute('data-domain') || '').toLowerCase();
      var rStatus = r.getAttribute('data-status') || '';
      var rType = r.getAttribute('data-type') || '';
      var okQuery = !q || name.indexOf(q) !== -1 || domain.indexOf(q) !== -1;
      var okStatus = !status || rStatus === status;
      var okType = !keytype || rType === keytype;
      var visible = okQuery && okStatus && okType;
      r.style.display = visible ? '' : 'none';
      if (visible) { shown++; }
    });
    counter.textContent = (shown === total)
      ? ('Всего: ' + total)
      : ('Найдено: ' + shown + ' из ' + total);
  }

  fSearch.addEventListener('input', applyFilters);
  fStatus.addEventListener('change', applyFilters);
  fType.addEventListener('change', applyFilters);
  fReset.addEventListener('click', function () {
    fSearch.value = '';
    fStatus.value = '';
    fType.value = '';
    fStatus.dispatchEvent(new Event('change'));
    fType.dispatchEvent(new Event('change'));
    applyFilters();
  });

  applyFilters();
})();

/* Confirm dialogs for renew/revoke POST forms */
function certFormConfirm(formCls, isRevoke) {
  document.querySelectorAll('.' + formCls).forEach(function (form) {
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      var name = form.getAttribute('data-name') || 'сертификат';
      var msg = isRevoke
        ? 'ОТОЗВАТЬ "' + name + '"? Действие необратимо.'
        : 'Перевыпустить сертификат "' + name + '"? Файл будет заменён.';
      var btn = isRevoke ? 'Отозвать' : 'Перевыпустить';
      var title = isRevoke ? 'Отзыв сертификата' : 'Перевыпуск';
      var confirmClass = isRevoke ? 'danger' : 'warning';
      var proxy = document.createElement('form');
      proxy.setAttribute('data-confirm', msg);
      proxy.setAttribute('data-confirm-btn', btn);
      proxy.setAttribute('data-confirm-class', confirmClass);
      proxy.setAttribute('data-confirm-title', title);
      proxy.submit = function () { form.submit(); };
      document.body.appendChild(proxy);
      proxy.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    });
  });
}

certFormConfirm('confirm-renew-form', false);
certFormConfirm('confirm-revoke-form', true);
