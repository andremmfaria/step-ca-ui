/*
 * history.js — multi-select filter for the cert-history page.
 * Initial selection is read from the #history-data JSON block written by the
 * template (type="application/json"), so no inline JS is needed.
 */
(function () {
  var dataEl = document.getElementById('history-data');
  var selected = [];
  if (dataEl) {
    try {
      var parsed = JSON.parse(dataEl.textContent);
      if (Array.isArray(parsed.actions)) { selected = parsed.actions; }
    } catch (e) { /* ignore parse error, start with empty selection */ }
  }

  var LABELS = { issue: 'Выпуск', renew: 'Перевыпуск', revoke: 'Отзыв', import: 'Импорт' };
  var ms = document.getElementById('actionsMulti');
  if (!ms) { return; }
  var box = ms.querySelector('.ms-box');
  var drop = ms.querySelector('.ms-dropdown');
  var chips = document.getElementById('msChips');
  var ph = document.getElementById('msPlaceholder');
  var form = document.getElementById('historyForm');
  var submitTimer = null;

  function render() {
    chips.innerHTML = '';
    if (selected.length === 0) {
      ph.style.display = '';
    } else {
      ph.style.display = 'none';
      selected.forEach(function (v) {
        var chip = document.createElement('span');
        chip.className = 'ms-chip';
        chip.innerHTML = (LABELS[v] || v) + ' <span class="ms-chip-x" data-v="' + v + '">&times;</span>';
        chips.appendChild(chip);
      });
    }
    drop.querySelectorAll('.ms-opt').forEach(function (o) {
      o.classList.toggle('selected', selected.indexOf(o.getAttribute('data-val')) !== -1);
    });
  }

  function rebuildInputs() {
    Array.from(form.querySelectorAll('input[type=hidden][name=action]')).forEach(function (n) { n.remove(); });
    selected.forEach(function (v) {
      var inp = document.createElement('input');
      inp.type = 'hidden';
      inp.name = 'action';
      inp.value = v;
      form.appendChild(inp);
    });
  }

  function submitSoon() {
    clearTimeout(submitTimer);
    submitTimer = setTimeout(function () { rebuildInputs(); form.submit(); }, 250);
  }

  box.addEventListener('click', function (e) {
    if (e.target.classList.contains('ms-chip-x')) {
      var v = e.target.getAttribute('data-v');
      selected = selected.filter(function (x) { return x !== v; });
      render();
      submitSoon();
      return;
    }
    box.classList.toggle('open');
    drop.classList.toggle('open');
  });

  drop.querySelectorAll('.ms-opt').forEach(function (opt) {
    opt.addEventListener('click', function (e) {
      e.stopPropagation();
      var v = this.getAttribute('data-val');
      var i = selected.indexOf(v);
      if (i === -1) { selected.push(v); } else { selected.splice(i, 1); }
      render();
      submitSoon();
    });
  });

  document.addEventListener('click', function (e) {
    if (!ms.contains(e.target)) {
      box.classList.remove('open');
      drop.classList.remove('open');
    }
  });

  render();
})();
