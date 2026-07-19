// Apply the saved theme before paint to avoid a flash of the default palette.
(function () {
  var themes = ['green', 'amber', 'cyan', 'ice', 'ultraviolet', 'synthwave', 'matrix', 'mono'];
  var theme = localStorage.getItem('hc_theme');
  if (themes.indexOf(theme) === -1) theme = 'green';
  if (theme !== 'green') document.documentElement.setAttribute('data-theme', theme);
})();
