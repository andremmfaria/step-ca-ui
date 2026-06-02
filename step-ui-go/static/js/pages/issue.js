/*
 * issue.js — interactive certificate-issue form: template cards, key-type
 * cards, duration chips, and the live preview panel.
 */
(function () {
  var DURATIONS = {
    '720h': { label: '1 месяц', days: 30 },
    '4380h': { label: '6 месяцев', days: 182 },
    '8760h': { label: '1 год', days: 365 },
    '87600h': { label: '10 лет', days: 3650 }
  };
  var KEYTYPE_LABEL = {
    'EC:P-256': 'EC P-256',
    'EC:P-384': 'EC P-384',
    'RSA:2048': 'RSA 2048',
    'RSA:4096': 'RSA 4096'
  };
  var TEMPLATE_LABEL = {
    server: 'Server TLS',
    internal: 'Internal service',
    wildcard: 'Wildcard',
    client: 'Client identity'
  };

  var pvName = document.getElementById('pvName');
  var pvTemplate = document.getElementById('pvTemplate');
  var pvDomain = document.getElementById('pvDomain');
  var pvType = document.getElementById('pvType');
  var pvDuration = document.getElementById('pvDuration');
  var pvExpires = document.getElementById('pvExpires');
  var tplInput = document.getElementById('templateInput');
  var keyInput = document.getElementById('keyTypeInput');
  var durInput = document.getElementById('durationInput');
  var fName = document.getElementById('fName');
  var fDomain = document.getElementById('fDomain');

  if (!pvName) { return; }

  function updatePreview() {
    pvName.textContent = fName.value || '—';
    pvName.style.color = fName.value ? 'var(--text)' : 'var(--muted)';
    pvDomain.textContent = fDomain.value || '—';
    pvTemplate.textContent = TEMPLATE_LABEL[tplInput.value] || tplInput.value;
    var kt = keyInput.value;
    pvType.textContent = KEYTYPE_LABEL[kt] || kt;
    var d = DURATIONS[durInput.value];
    if (d) {
      pvDuration.textContent = d.label;
      var exp = new Date(Date.now() + d.days * 86400000);
      pvExpires.textContent = exp.toISOString().slice(0, 10);
    }
  }

  function setKeyType(value) {
    document.querySelectorAll('.key-card').forEach(function (c) {
      c.classList.toggle('active', c.getAttribute('data-val') === value);
    });
    keyInput.value = value;
  }

  function setDuration(value) {
    document.querySelectorAll('.chip').forEach(function (c) {
      c.classList.toggle('active', c.getAttribute('data-val') === value);
    });
    durInput.value = value;
  }

  document.querySelectorAll('.template-card').forEach(function (card) {
    card.addEventListener('click', function () {
      document.querySelectorAll('.template-card').forEach(function (c) { c.classList.remove('active'); });
      this.classList.add('active');
      tplInput.value = this.getAttribute('data-template');
      fDomain.placeholder = this.getAttribute('data-placeholder') || 'example.com';
      setKeyType(this.getAttribute('data-key') || 'EC:P-256');
      setDuration(this.getAttribute('data-duration') || '8760h');
      updatePreview();
    });
  });

  document.querySelectorAll('.key-card').forEach(function (card) {
    card.addEventListener('click', function () {
      document.querySelectorAll('.key-card').forEach(function (c) { c.classList.remove('active'); });
      this.classList.add('active');
      keyInput.value = this.getAttribute('data-val');
      updatePreview();
    });
  });

  document.querySelectorAll('.chip').forEach(function (chip) {
    chip.addEventListener('click', function () {
      document.querySelectorAll('.chip').forEach(function (c) { c.classList.remove('active'); });
      this.classList.add('active');
      durInput.value = this.getAttribute('data-val');
      updatePreview();
    });
  });

  fName.addEventListener('input', updatePreview);
  fDomain.addEventListener('input', updatePreview);
  updatePreview();
})();
