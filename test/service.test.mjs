import test from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { once } from 'node:events';
import os from 'node:os';
import path from 'node:path';
import { RemoteMessengerClient } from '../dist/remote.js';

async function start(profile) {
  const child=spawn(path.resolve('bin/messenger-service'),['--data-dir',profile,'--listen','127.0.0.1:0','--connect=false']);
  child.stdout.resume();
  const url=await new Promise((resolve,reject)=>{
    const timer=setTimeout(()=>{child.kill();reject(new Error('Service startup timed out'));},5000);
    let log='';
    child.stderr.on('data',chunk=>{log+=chunk;const match=log.match(/listening on (127\.0\.0\.1:\d+)/);if(match){clearTimeout(timer);resolve('http://'+match[1]);}});
    child.once('error',error=>{clearTimeout(timer);reject(error);});
    child.once('exit',()=>{clearTimeout(timer);reject(new Error('Service exited during startup'));});
  });
  return {child,url,token:(await readFile(path.join(profile,'service-token'),'utf8')).trim()};
}
async function stop(child) { const done=once(child,'close');child.kill('SIGTERM');await done; }

test('real service authenticates clients and persists sessions/token across restarts',async t=>{
  const profile=await mkdtemp(path.join(os.tmpdir(),'messenger-service-'));
  t.after(()=>rm(profile,{recursive:true,force:true}));
  let server=await start(profile);
  const token=server.token;
  try {
    assert.equal((await fetch(server.url+'/v1/rpc',{method:'POST',body:'{}'})).status,401);
    const client=await RemoteMessengerClient.open(server);
    try {
      assert.equal((await client.status()).sessionImported,false);
      await client.importSession({c_user:'9007199254740993',xs:'fake-session',datr:'fake-device'});
      assert.equal((await client.status()).sessionImported,true);
      await assert.rejects(client.send('123','unsent test'),{code:'UNKNOWN_CONVERSATION'});
    } finally {await client.close();}
    const second=await RemoteMessengerClient.open(server);
    try {assert.equal((await second.status()).sessionImported,true);}finally{await second.close();}
  } finally {await stop(server.child);}
  server=await start(profile);
  try {
    assert.equal(server.token,token);
    const client=await RemoteMessengerClient.open(server);
    try {assert.equal((await client.status()).sessionImported,true);}finally{await client.close();}
  } finally {await stop(server.child);}
});
