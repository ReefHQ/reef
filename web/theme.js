'use strict';
// This script runs before the stylesheet to apply a stored theme before first paint.
(() => {
 const key = 'reef.theme';
 const allowed = ['system', 'light', 'dark'];
 const media = window.matchMedia('(prefers-color-scheme: dark)');
 let preference = 'system';
 try { const saved = localStorage.getItem(key); if (allowed.includes(saved)) preference = saved; } catch (_) { /* Theme switching still works without storage. */ }
 function apply() {
  document.documentElement.dataset.theme = preference === 'system' ? (media.matches ? 'dark' : 'light') : preference;
  document.querySelectorAll('[data-theme-choice]').forEach(button => {
   button.setAttribute('aria-pressed', String(button.dataset.themeChoice === preference));
  });
 }
 apply();
 function setup() {
  document.querySelectorAll('[data-theme-choice]').forEach(button => button.addEventListener('click', () => {
   const next = button.dataset.themeChoice;
   if (!allowed.includes(next)) return;
   preference = next;
   try { if (next === 'system') localStorage.removeItem(key); else localStorage.setItem(key, next); } catch (_) { /* Private or restricted browser storage. */ }
   apply();
  }));
  apply();
 }
 if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', setup, {once: true}); else setup();
 if (media.addEventListener) media.addEventListener('change', () => { if (preference === 'system') apply(); });
 window.addEventListener('storage', event => {
  if (event.key !== key && event.key !== null) return;
  preference = allowed.includes(event.newValue) ? event.newValue : 'system';
  apply();
 });
})();
