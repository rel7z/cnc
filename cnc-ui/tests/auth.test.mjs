import { test } from 'node:test';
import assert from 'node:assert/strict';
import { checkCredentials, createSession, validSession, sameOrigin } from '../lib/auth.ts';

test('default login, signed session, tampering, expiry, and credential rotation',()=>{
 process.env.CNC_SESSION_SECRET='test-only-secret-01234567890123456789';
 delete process.env.CNC_LOGIN_USERNAME;delete process.env.CNC_LOGIN_PASSWORD;
 assert.equal(checkCredentials('fahrel','fahrel'),true);
 assert.equal(checkCredentials('fahrel','wrong'),false);
 assert.equal(validSession(),false);
 const session=createSession();assert.equal(validSession(session),true);
 assert.equal(validSession(session+'tampered'),false);
 const now=Date.now;Date.now=()=>now()+13*60*60*1000;
 try {assert.equal(validSession(session),false);} finally {Date.now=now;}
 process.env.CNC_LOGIN_PASSWORD='changed';assert.equal(validSession(session),false);
 delete process.env.CNC_LOGIN_PASSWORD;delete process.env.CNC_SESSION_SECRET;
 assert.equal(validSession(session),false);
 assert.throws(()=>createSession());
});
test('login and logout require same-origin requests',()=>{
 assert.equal(sameOrigin(new Request('http://localhost/api/auth/login',{headers:{host:'localhost',origin:'http://localhost'}})),true);
 assert.equal(sameOrigin(new Request('http://localhost/api/auth/login',{headers:{host:'localhost',origin:'http://other.example'}})),false);
 assert.equal(sameOrigin(new Request('http://localhost/api/auth/login')),false);
});
