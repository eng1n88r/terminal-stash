// stash login — external file so the CSP can stay strict (no inline scripts).

// Apply the saved theme before paint; this script loads synchronously in <head>.
(function () {
  var t = localStorage.getItem('hc_theme');
  if (t && t !== 'green') document.documentElement.setAttribute('data-theme', t);
})();

document.addEventListener('DOMContentLoaded', () => {
  const form = document.getElementById('login-form');
  const errEl = document.getElementById('error');
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    errEl.textContent = '';
    const password = document.getElementById('password').value;
    try {
      const res = await fetch('/api/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ password }),
      });
      if (res.ok) {
        window.location.href = '/';
      } else if (res.status === 429) {
        errEl.textContent = '✗ too many attempts — try again later';
      } else {
        errEl.textContent = '✗ access denied';
      }
    } catch (_) {
      errEl.textContent = '✗ connection error';
    }
  });
});
