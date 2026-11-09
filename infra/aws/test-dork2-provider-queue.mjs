import test from 'node:test';
import assert from 'node:assert/strict';
import {runActorQueued} from './dork2-provider-queue.mjs';
function fixture({failStarts=0,status='SUCCEEDED',alwaysFail=false}={}) {
 let attempts=0;const delivered=[];let aborts=0;
 const client={actor:()=>({start:async(_,{timeout})=>{assert.equal(timeout,900);attempts++;if(alwaysFail||attempts<=failStarts)throw new Error('exceed your limit of 5 concurrent Actor runs');return{id:'fixture',defaultDatasetId:'dataset'};}}),dataset:()=>({listItems:async({offset=0}={})=>({items:offset?[]:[{organicResults:[{url:'https://example.test/page'}]}]})}),run:()=>({get:async()=>({id:'fixture',defaultDatasetId:'dataset',status}),abort:async()=>{aborts++;}})};
 return {client,delivered,attempts:()=>attempts,aborts:()=>aborts};
}
test('capacity retry respects provider limit then streams results once',async()=>{
 const f=fixture({failStarts:2});let sleeps=0;
 await runActorQueued(f.client,'actor',{},items=>f.delivered.push(...items),{sleep:async()=>{sleeps++;}});
 assert.equal(f.attempts(),3);assert.equal(sleeps,2);assert.equal(f.delivered.length,1);
});
test('capacity retry is bounded',async()=>{
 const f=fixture({alwaysFail:true});await assert.rejects(runActorQueued(f.client,'actor',{},()=>{},{sleep:async()=>{},maxAttempts:1}));assert.equal(f.attempts(),2);
});
test('failed actor is a failure even if SDK returns its run record',async()=>{
 const f=fixture({status:'FAILED'});await assert.rejects(runActorQueued(f.client,'actor',{},()=>{},{sleep:async()=>{}}),/FAILED/);
});
