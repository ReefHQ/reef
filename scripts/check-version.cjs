'use strict';
const fs=require('node:fs');
const version=fs.readFileSync(require('node:path').join(__dirname,'../VERSION'),'utf8').trim();
// SemVer identifiers: numeric prerelease components cannot contain leading zeroes.
const numeric='(?:0|[1-9][0-9]*)',pre='(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)';
const semver=new RegExp(`^${numeric}\\.${numeric}\\.${numeric}(?:-${pre}(?:\\.${pre})*)?(?:\\+[0-9A-Za-z-]+(?:\\.[0-9A-Za-z-]+)*)?$`);
if(!semver.test(version)){console.error('VERSION must contain a valid SemVer version.');process.exit(1);}
if(process.env.CI_COMMIT_TAG&&process.env.CI_COMMIT_TAG!==`v${version}`){console.error(`Release tag must match VERSION (v${version}).`);process.exit(1);}
console.log(`Reef version: ${version}`);
