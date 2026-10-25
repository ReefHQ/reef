'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),os=require('node:os'),path=require('node:path'),{spawnSync}=require('node:child_process');
test('version check accepts SemVer and rejects malformed versions and mismatched release tags',()=>{
 const dir=fs.mkdtempSync(path.join(os.tmpdir(),'reef-version-'));
 try{
  fs.mkdirSync(path.join(dir,'scripts'));fs.copyFileSync(path.join(__dirname,'../scripts/check-version.cjs'),path.join(dir,'scripts/check-version.cjs'));
  function check(version,tag=''){fs.writeFileSync(path.join(dir,'VERSION'),version+'\n');return spawnSync(process.execPath,[path.join(dir,'scripts/check-version.cjs')],{env:{CI_COMMIT_TAG:tag},encoding:'utf8'}).status;}
  for(const version of ['0.2.0','1.0.0','1.2.3-rc.1','1.2.3-alpha.0+build.01'])assert.equal(check(version),0,version);
  for(const version of ['v0.2.0','01.2.0','1.2','1.2.3-01','1.2.3-','1.2.3+'])assert.equal(check(version),1,version);
  assert.equal(check('0.2.0','v0.2.0'),0);assert.equal(check('0.2.0','v0.1.0'),1);
 }finally{fs.rmSync(dir,{recursive:true,force:true});}
});
