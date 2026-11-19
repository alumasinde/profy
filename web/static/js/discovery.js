// Progressive enhancement for public search forms (§6.3). Pages work fully without it.
// "Near me" asks for location once, on click only, and sends it with this search; it is
// never stored. Kept tiny: no framework, no tracking.
(function () {
  'use strict';
  if (!('geolocation' in navigator)) return;
  document.querySelectorAll('form.search-form').forEach(function (form) {
    var btn = form.querySelector('[data-near-me]');
    var lat = form.querySelector('[data-lat]');
    var lng = form.querySelector('[data-lng]');
    var where = form.querySelector('[data-place-input]');
    var status = form.querySelector('[data-near-status]');
    if (!btn || !lat || !lng) return;
    btn.hidden = false;
    if (where) {
      // Typing a place replaces a previous "near me" position.
      where.addEventListener('input', function () { lat.value = ''; lng.value = ''; });
    }
    btn.addEventListener('click', function () {
      btn.disabled = true;
      if (status) status.textContent = btn.getAttribute('data-locating') || '…';
      navigator.geolocation.getCurrentPosition(function (p) {
        lat.value = p.coords.latitude.toFixed(5);
        lng.value = p.coords.longitude.toFixed(5);
        if (where) where.value = '';
        form.submit();
      }, function () {
        btn.disabled = false;
        if (status) status.textContent = btn.getAttribute('data-denied') || '';
      }, { enableHighAccuracy: false, timeout: 15000, maximumAge: 600000 });
    });
  });
})();
