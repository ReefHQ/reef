'use strict';
// Run inside the Renovate image against synthetic source, not the host checkout.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const rows = fs.readFileSync(process.argv[2], 'utf8').split('\n').flatMap(line => {
  try { return [JSON.parse(line)]; } catch { return []; }
});
const extraction = rows.find(row => row.msg === 'Extracted dependencies');
assert.ok(extraction?.packageFiles, 'Renovate did not report extracted dependencies');
const packages = Object.values(extraction.packageFiles).flat();
const deps = packages.flatMap(file => file.deps.map(dep => ({...dep, file: file.packageFile})));
const find = (file, name) => deps.find(dep => dep.file === file && dep.depName === name && !dep.skipReason);
for (const [file, name] of [
  ['Dockerfile', 'golang'], ['Dockerfile', 'docker'], ['deploy-agent/Dockerfile', 'docker'],
  ['scripts/ci.sh', 'golang'], ['scripts/ci.sh', 'node'], ['scripts/local.sh', 'golang'],
  ['scripts/ensure-buildx.sh', 'docker'], ['scripts/renovate.sh', 'ghcr.io/renovatebot/renovate'],
  ['internal/app/volume_backups.go', 'docker'], ['examples/compose.install.yaml', 'docker'],
  ['.github/workflows/plumber.yml', 'actions/checkout'], ['.github/workflows/publish-containers.yml', 'actions/checkout']
]) assert.ok(find(file, name), `${file}: missing usable ${name}`);
assert.ok(find('Dockerfile', 'golang').replaceString.startsWith('ARG GO_IMAGE='), 'Image ARG was not understood');
assert.ok(deps.some(dep => dep.file.endsWith('_integration_test.go') && dep.depName === 'docker' && !dep.skipReason));
const plumber = deps.filter(dep => dep.file === '.github/workflows/plumber.yml' && dep.depName === 'getplumber/plumber');
assert.equal(plumber.length, 2, 'Both action SHA and explicit binary version must be maintained');
assert.equal(plumber[0].currentValue, plumber[1].currentValue);
assert.ok(plumber.every(dep => !dep.skipReason));
assert.ok(plumber.some(dep => /^[0-9a-f]{40}$/.test(dep.currentDigest || '')));
assert.ok(rows.some(row => row.msg === 'HTTP statistics' && row.requests === 0), 'Extraction attempted network requests');
assert.ok(!rows.some(row => row.level >= 50), 'Renovate logged an error');
console.log(`PASS: actual Renovate extracted ${deps.length} dependency occurrences, including ARGs, inline images, action SHAs and the Plumber binary; zero HTTP requests.`);
