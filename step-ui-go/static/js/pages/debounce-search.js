/*
 * debounce-search.js — live search: auto-submit form 400 ms after last input.
 * Used by admin_security.html and security_log.html.
 * Looks for #secSearch and #secForm on the page.
 */
(function () {
  var inp = document.getElementById('secSearch');
  var form = document.getElementById('secForm');
  if (!inp || !form) { return; }
  var timer = null;
  inp.addEventListener('input', function () {
    clearTimeout(timer);
    timer = setTimeout(function () { form.submit(); }, 400);
  });
})();
