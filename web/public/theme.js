(() => {
  let mode = 'system';
  try { const saved = localStorage.getItem('collectionTheme'); if (['light', 'dark'].includes(saved)) mode = saved; } catch {}
  const theme = mode === 'system' ? (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light') : mode;
  document.documentElement.dataset.theme = theme;
  document.documentElement.dataset.themeMode = mode;
  document.querySelector('meta[name="theme-color"]')?.setAttribute('content', theme === 'dark' ? '#15181E' : '#F6F7FA');
})();
