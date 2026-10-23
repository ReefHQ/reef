'use strict';
// Offline preference tests; no browser, Docker engine, or external packages needed.
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(require('node:path').join(__dirname, '../web/theme.js'), 'utf8');
function page({dark = false, stored = null, blocked = false} = {}) {
 const storage = new Map(stored === null ? [] : [['reef.theme', stored]]);
 const docEvents = {}, windowEvents = {}, mediaEvents = {};
 const buttons = ['system', 'light', 'dark'].map(value => ({
  dataset: {themeChoice: value}, attributes: {}, events: {},
  setAttribute(name, value) { this.attributes[name] = value; },
  addEventListener(name, fn) { this.events[name] = fn; }
 }));
 const root = {dataset: {}};
 let ready = false;
 const media = {matches: dark, addEventListener(name, fn) { mediaEvents[name] = fn; }};
 const context = {
  document: {documentElement: root, readyState: 'loading', querySelectorAll() { return ready ? buttons : []; }, addEventListener(name, fn) { docEvents[name] = fn; }},
  window: {matchMedia() { return media; }, addEventListener(name, fn) { windowEvents[name] = fn; }},
  localStorage: {
   getItem(key) { if (blocked) throw Error('Storage denied'); return storage.get(key) ?? null; },
   setItem(key, value) { if (blocked) throw Error('Storage denied'); storage.set(key, value); },
   removeItem(key) { if (blocked) throw Error('Storage denied'); storage.delete(key); }
  }
 };
 vm.runInNewContext(source, context);
 return {
  root, storage, buttons,
  ready() { ready = true; docEvents.DOMContentLoaded(); },
  choose(value) { buttons.find(b => b.dataset.themeChoice === value).events.click(); },
  system(dark) { media.matches = dark; mediaEvents.change(); },
  otherTab(value) { windowEvents.storage({key: 'reef.theme', newValue: value}); }
 };
}
test('system default is applied before DOM readiness and follows system changes', () => {
 const p = page({dark: true});
 assert.equal(p.root.dataset.theme, 'dark');
 p.ready(); assert.equal(p.buttons[0].attributes['aria-pressed'], 'true');
 p.system(false); assert.equal(p.root.dataset.theme, 'light');
 assert.equal(p.storage.has('reef.theme'), false);
});
test('explicit choice overrides system, persists after reload, and exposes selection', () => {
 const p = page({dark: false}); p.ready(); p.choose('dark');
 assert.equal(p.root.dataset.theme, 'dark'); assert.equal(p.storage.get('reef.theme'), 'dark');
 assert.equal(p.buttons[2].attributes['aria-pressed'], 'true');
 assert.equal(p.buttons[0].attributes['aria-pressed'], 'false');
 p.system(false); assert.equal(p.root.dataset.theme, 'dark');
 const reloaded = page({dark: false, stored: p.storage.get('reef.theme')});
 assert.equal(reloaded.root.dataset.theme, 'dark');
 reloaded.ready(); reloaded.choose('light'); assert.equal(reloaded.root.dataset.theme, 'light');
 assert.equal(reloaded.storage.get('reef.theme'), 'light');
});
test('System clears explicit choice and resumes following the OS', () => {
 const p = page({dark: true, stored: 'light'}); p.ready();
 assert.equal(p.root.dataset.theme, 'light');
 p.choose('system'); assert.equal(p.storage.has('reef.theme'), false);
 assert.equal(p.root.dataset.theme, 'dark');
 p.system(false); assert.equal(p.root.dataset.theme, 'light');
});
test('theme switching works if storage is unavailable', () => {
 const p = page({blocked: true, dark: false}); p.ready();
 p.choose('dark'); assert.equal(p.root.dataset.theme, 'dark');
 p.choose('system'); assert.equal(p.root.dataset.theme, 'light');
});
test('invalid saved preference falls back to system and changes synchronize across tabs', () => {
 const p = page({stored: 'invalid', dark: true}); p.ready();
 assert.equal(p.root.dataset.theme, 'dark');
 p.otherTab('light'); assert.equal(p.root.dataset.theme, 'light');
 assert.equal(p.buttons[1].attributes['aria-pressed'], 'true');
 p.otherTab(null); assert.equal(p.root.dataset.theme, 'dark');
 assert.equal(p.buttons[0].attributes['aria-pressed'], 'true');
});
