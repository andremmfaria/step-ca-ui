/*
 * dashboard.js — confirm dialogs for the renew links on the dashboard.
 */
document.querySelectorAll('.confirm-renew').forEach(function (el) {
  el.addEventListener('click', function (e) {
    e.preventDefault();
    var href = this.getAttribute('href');
    var name = this.getAttribute('data-name') || 'сертификат';
    var f = document.createElement('form');
    f.setAttribute('data-confirm', 'Перевыпустить сертификат "' + name + '"? Текущий файл будет заменён новым.');
    f.setAttribute('data-confirm-btn', 'Перевыпустить');
    f.setAttribute('data-confirm-class', 'warning');
    f.setAttribute('data-confirm-title', 'Перевыпуск сертификата');
    f.submit = function () { window.location.href = href; };
    document.body.appendChild(f);
    f.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
  });
});
