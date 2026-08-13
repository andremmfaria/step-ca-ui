/* topnav.js — responsive navigation: <details> dropdowns + burger toggle */
(function () {
  /* Close all sibling <details> dropdowns when one opens */
  function closeOthers(current) {
    var root = current.closest('.mobile-topnav, .admin-topnav');
    if (!root) return;
    root.querySelectorAll('details[open]').forEach(function (item) {
      if (item !== current) item.removeAttribute('open');
    });
  }

  /* Collapse sibling dropdowns on toggle (capture phase so it fires before browser default) */
  document.addEventListener('toggle', function (event) {
    var item = event.target;
    if (item.tagName !== 'DETAILS' || !item.open) return;
    if (!item.matches('.mobile-dd, .admin-dd')) return;
    closeOthers(item);
  }, true);

  /* Click outside any open dropdown — close all */
  document.addEventListener('click', function (event) {
    if (event.target.closest('.mobile-dd, .admin-dd')) return;
    document.querySelectorAll('.mobile-dd[open], .admin-dd[open]').forEach(function (item) {
      item.removeAttribute('open');
    });
  });

  /* Esc key — close all open dropdowns */
  document.addEventListener('keydown', function (event) {
    if (event.key !== 'Escape') return;
    document.querySelectorAll('.mobile-dd[open], .admin-dd[open]').forEach(function (item) {
      item.removeAttribute('open');
    });
  });
})();
