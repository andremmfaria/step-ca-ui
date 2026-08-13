/*
 * debounce-search.js — live search helpers for the security log page.
 *
 * 1. Auto-submits #secForm 400 ms after the last keystroke in #secSearch.
 * 2. Any <select data-autosubmit="FORM_ID"> submits the named form on change.
 */
(function () {
  // Debounced text search
  var inp = document.getElementById('secSearch');
  var form = document.getElementById('secForm');
  if (inp && form) {
    var timer = null;
    inp.addEventListener('input', function () {
      clearTimeout(timer);
      timer = setTimeout(function () { form.submit(); }, 400);
    });
  }

  // data-autosubmit select → submit the form whose id matches the attribute value
  var selects = document.querySelectorAll('select[data-autosubmit]');
  for (var i = 0; i < selects.length; i++) {
    (function (sel) {
      sel.addEventListener('change', function () {
        var target = document.getElementById(sel.getAttribute('data-autosubmit'));
        if (target) { target.submit(); }
      });
    })(selects[i]);
  }
})();
