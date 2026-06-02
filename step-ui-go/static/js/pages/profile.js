/*
 * profile.js — theme selector widget on the profile page.
 */
(function () {
  var sel = document.getElementById('themeSelect');
  if (!sel) { return; }
  var box = document.getElementById('tsBox');
  var drop = document.getElementById('tsDropdown');
  var input = document.getElementById('themeInput');
  var label = document.getElementById('tsLabel');
  var preview = box.querySelector('.ts-preview');
  var LABELS = { dark: 'Тёмная', light: 'Светлая', blue: 'Синяя', auto: 'Авто (системная)' };

  box.addEventListener('click', function () {
    box.classList.toggle('open');
    drop.classList.toggle('open');
  });
  document.addEventListener('click', function (e) {
    if (!sel.contains(e.target)) {
      box.classList.remove('open');
      drop.classList.remove('open');
    }
  });
  drop.querySelectorAll('.ts-opt').forEach(function (opt) {
    opt.addEventListener('click', function () {
      var val = this.getAttribute('data-val');
      input.value = val;
      label.textContent = LABELS[val] || val;
      preview.className = 'ts-preview ts-' + val;
      preview.innerHTML = '<span></span><span></span>';
      drop.querySelectorAll('.ts-opt').forEach(function (o) { o.classList.remove('selected'); });
      this.classList.add('selected');
      box.classList.remove('open');
      drop.classList.remove('open');
    });
  });
})();
