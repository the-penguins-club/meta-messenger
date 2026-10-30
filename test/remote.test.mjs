import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { RemoteMessengerClient } from '../dist/remote.js';

const token='0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef';
test('remote API sends bearer auth, preserves string IDs, receives resync, and closes locally', async t => {
  let eventConnection;
  const server=createServer((req,res)=>{
    assert.equal(req.headers.authorization,`Bearer ${token}`);
    if(req.url==='/v1/events'){
      eventConnection=res;res.writeHead(200,{'Content-Type':'text/event-stream'});
      setTimeout(()=>res.write('data: {"event":"resync","data":{}}\n\n'),30);return;
    }
    let body='';req.on('data',chunk=>body+=chunk);req.on('end',()=>{
      const request=JSON.parse(body);
      const result=request.method==='hello'?{protocolVersion:1}:request.method==='messages.list'?[{id:'9007199254740993',conversationId:request.params.conversationId}]:{sessionImported:true};
      res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify({id:request.id,result}));
    });
  });
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  t.after(()=>{eventConnection?.end();server.closeAllConnections();server.close();});
  const client=await RemoteMessengerClient.open({url:`http://127.0.0.1:${server.address().port}`,token});
  try {
    const event=new Promise(resolve=>client.onEvent(resolve));
    assert.equal((await event).event,'resync');
    assert.equal((await client.messages('9007199254740995'))[0].id,'9007199254740993');
  } finally {await client.close();}
  await assert.rejects(client.status(),{code:'CLIENT_CLOSED'});
});

test('remote client refuses plaintext remote hosts and URL credentials',async()=>{
  for(const url of ['http://example.com','https://user:pass@example.com','https://example.com/?token=secret']){
    await assert.rejects(RemoteMessengerClient.open({url,token}),{code:'INVALID_SERVER'});
  }
});
