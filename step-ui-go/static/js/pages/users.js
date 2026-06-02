/*
 * users.js — client-side user-table filter.
 * Used by users.html and admin_users.html (identical logic, shared file).
 */
(function () {
  var table = document.getElementById('usersTable');
  if (!table) { return; }
  var rows = Array.prototype.slice.call(
    table.querySelectorAll('tbody tr')
  ).filter(function (r) { return r.hasAttribute('data-username'); });
  var fSearch = document.getElementById('uf-search');
  var fRole = document.getElementById('uf-role');
  var fStatus = document.getElementById('uf-status');
  var fReset = document.getElementById('uf-reset');
  var counter = document.getElementById('uf-counter');
  var total = rows.length;

  function applyFilters() {
    var q = (fSearch.value || '').toLowerCase().trim();
    var role = fRole.value;
    var status = fStatus.value;
    var shown = 0;
    rows.forEach(function (r) {
      var uname = (r.getAttribute('data-username') || '').toLowerCase();
      var rRole = r.getAttribute('data-role') || '';
      var rStatus = r.getAttribute('data-status') || '';
      var okQuery = !q || uname.indexOf(q) !== -1;
      var okRole = !role || rRole === role;
      var okStatus = !status || rStatus === status;
      var visible = okQuery && okRole && okStatus;
      r.style.display = visible ? '' : 'none';
      if (visible) { shown++; }
    });
    counter.textContent = (shown === total)
      ? ('Всего: ' + total)
      : ('Найдено: ' + shown + ' из ' + total);
  }

  fSearch.addEventListener('input', applyFilters);
  fRole.addEventListener('change', applyFilters);
  fStatus.addEventListener('change', applyFilters);
  fReset.addEventListener('click', function () {
    fSearch.value = '';
    fRole.value = '';
    fStatus.value = '';
    fRole.dispatchEvent(new Event('change'));
    fStatus.dispatchEvent(new Event('change'));
    applyFilters();
  });

  applyFilters();
})();
