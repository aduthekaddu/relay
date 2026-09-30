// Runs synchronously before first paint (CSP forbids inline scripts, so this
// is a tiny external file). Applies the saved theme so there is no flash,
// and exposes the visual-viewport height for keyboard-aware layouts.
;(() => {
  var root = document.documentElement
  var pref = 'carbon'
  try {
    var q = new URLSearchParams(location.search).get('theme')
    var v = q || JSON.parse(localStorage.getItem('relay.theme') || '"carbon"')
    if (v === 'carbon' || v === 'paper' || v === 'auto') pref = v
  } catch (_) {}
  var light = pref === 'paper' || (pref === 'auto' && matchMedia('(prefers-color-scheme: light)').matches)
  root.dataset.theme = light ? 'paper' : 'carbon'
  root.dataset.themePref = pref
  var metas = document.querySelectorAll('meta[name="theme-color"]')
  for (var i = 0; i < metas.length; i++) metas[i].content = light ? '#f3f0e8' : '#0b0b0c'
})()
