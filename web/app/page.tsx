"use client";
import {useEffect,useState} from "react";
type Site={url:string;host:string};
type Observation={source:string;imported_at:string;evidence:string[]};
export default function Page(){
 const [stats,setStats]=useState<{urls:number;imports:number;wordpress:number}|null>(null);
 const [sites,setSites]=useState<Site[]>([]),[host,setHost]=useState(""),[filter,setFilter]=useState(""),[wordpress,setWordpress]=useState(false),[page,setPage]=useState(1),[more,setMore]=useState(false),[error,setError]=useState(""),[busy,setBusy]=useState(false),[refresh,setRefresh]=useState(0);
 const [selected,setSelected]=useState(""),[observations,setObservations]=useState<Observation[]>([]);
 useEffect(()=>{const abort=new AbortController();setBusy(true);setError("");
 async function load(){try{
 const query=new URLSearchParams({host:filter,page:String(page),wordpress:wordpress?"1":"0"});
 const responses=await Promise.all([fetch("/api/catalog/stats",{signal:abort.signal}),fetch("/api/catalog/sites?"+query,{signal:abort.signal})]);
 const values=await Promise.all(responses.map(r=>r.json()));
 for(let i=0;i<responses.length;i++)if(!responses[i].ok)throw Error(values[i].error);
 setStats(values[0]);setSites(values[1].items);setMore(values[1].has_more);
 }catch(e){if(!abort.signal.aborted)setError(e instanceof Error?e.message:"Request failed");}finally{if(!abort.signal.aborted)setBusy(false);}}
 void load();return()=>abort.abort();},[filter,page,wordpress,refresh]);
 useEffect(()=>{if(!selected)return;const abort=new AbortController();setObservations([]);
 async function load(){try{const response=await fetch("/api/catalog/observations?"+new URLSearchParams({url:selected}),{signal:abort.signal});const result=await response.json();if(!response.ok)throw Error(result.error);setObservations(result.items);}catch(e){if(!abort.signal.aborted)setError(e instanceof Error?e.message:"Request failed");}}
 void load();return()=>abort.abort();},[selected]);
 return <><header><h1>Internet Map</h1><span>Dataset intelligence</span></header><main>
 <div className="controls"><h2 style={{flex:1}}>Catalog</h2><button disabled={busy} onClick={()=>setRefresh(v=>v+1)}>Refresh</button></div>
 {error&&<p role="alert">{error}</p>}
 <div id="metrics">{[["URLs",stats?.urls],["Dataset imports",stats?.imports],["WordPress evidence",stats?.wordpress]].map(([label,value])=><div key={String(label)}><strong>{typeof value==="number"?value.toLocaleString():"—"}</strong>{label}</div>)}</div>
 <form onSubmit={e=>{e.preventDefault();setPage(1);setFilter(host);}}><input type="search" aria-label="Search hostname" value={host} onChange={e=>setHost(e.target.value)} placeholder="Search hostname"/><label><input type="checkbox" checked={wordpress} onChange={e=>{setWordpress(e.target.checked);setPage(1);}}/>WordPress evidence</label><button className="primary" disabled={busy}>Search</button></form>
 <p role="status" aria-live="polite">{busy?"Loading catalog…":!error?sites.length+" URLs on this page":""}</p>
 <table><thead><tr><th>URL</th><th>Hostname</th></tr></thead><tbody>{sites.map(site=><tr key={site.url}><td><button onClick={()=>setSelected(site.url)}>{site.url}</button></td><td>{site.host}</td></tr>)}{!busy&&!error&&!sites.length&&<tr><td colSpan={2}>No matching URLs. Import a dataset to populate the catalog.</td></tr>}</tbody></table>
 <footer><button disabled={busy||page===1} onClick={()=>setPage(v=>v-1)}>Previous</button><span>Page {page}</span><button disabled={busy||!more} onClick={()=>setPage(v=>v+1)}>Next</button></footer>
 {selected&&<aside aria-label="Source observations"><button onClick={()=>setSelected("")} style={{float:"right"}}>Close</button><h2>{selected}</h2>{observations.map((o,i)=><article key={i}><p>{o.source}</p><small>{o.imported_at}</small><p>{o.evidence.join(" · ")||"No WordPress evidence recorded"}</p></article>)}</aside>}
 </main></>;
}
