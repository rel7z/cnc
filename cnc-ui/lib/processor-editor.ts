export type Runtime = "" | "python" | "node" | "bash" | "binary";
export type Output = { path: string; merged_name?: string; send_to?: string; drive?: boolean };
export type Program = { path: string; runtime?: Runtime; input?: string; outputs: (string | Output)[]; files?: string[]; requirements_lock?: string; package_lock?: string };
export type Manifest = { input?: string; files?: string[]; requirements_lock?: string; scripts: Record<string, Program> };
export type Definition = { tool_id: string; incoming: string; folder: string; manifest: Manifest };
export type SavedTemplate = { id: string; name: string; definition: Definition; revision: number; is_default?: boolean };
export type SetupState = { revision: string; definitions: Definition[]; external: string[] };
export const blankDefinition = (): Definition => ({tool_id:"",incoming:"wordpress.txt",folder:"",manifest:{input:"wordpress.txt",scripts:{scanner:{path:"scanner.py",runtime:"",outputs:[{path:"results.txt",drive:true}]}}}});
export const outputObject = (output: string | Output): Output => typeof output === "string" ? {path:output,drive:true} : {...output,drive:output.drive !== false};
// Export one controller-owned configuration, never a per-tool manifest file.
export function serializeControllerConfig(definition: Definition): string {
 return JSON.stringify({routes:{[definition.incoming]:[definition.tool_id]},programs:{[definition.tool_id]:{folder:definition.folder||'tools/'+definition.tool_id,definition:definition.manifest}}},null,2)+'\n';
}
const safePath = (value: unknown): value is string => typeof value === "string" && value.length>0 && !value.startsWith("/") && !value.includes("\\") && value.split("/").every(p=>p!=="" && p!=="." && p!=="..");
const safeID = (value: unknown): value is string => typeof value === "string" && /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/.test(value);
export function validateDefinition(d: Definition): Record<string,string> {
 const errors:Record<string,string>={};
 if(!safeID(d.tool_id))errors.tool_id="Use 1–64 letters, numbers, underscores or hyphens.";
 if(!safePath(d.incoming)||d.incoming.includes("/")||!d.incoming.endsWith(".txt")||d.incoming.startsWith("."))errors.incoming="Enter an incoming .txt filename, without folders.";
 if(d.folder && !safePath(d.folder))errors.folder="Use a relative server folder without ..";
 const scripts=d.manifest?.scripts;
 if(!scripts||typeof scripts!=="object"||Array.isArray(scripts)||Object.keys(scripts).length<1||Object.keys(scripts).length>32){errors.programs="Add between 1 and 32 programs.";return errors;}
 if(d.manifest.input && !safePath(d.manifest.input))errors.input="Use a relative input filename.";
 if(d.manifest.files!==undefined&&!Array.isArray(d.manifest.files)){errors.files="Supporting files must be a list.";return errors;}
 const common=[...(d.manifest.files??[]),...(d.manifest.requirements_lock?[d.manifest.requirements_lock]:[])];
 if(common.some(f=>!safePath(f)))errors.files="Supporting files must be relative paths without ..";
 for(const [key,s] of Object.entries(scripts)){
  if(!safeID(key))errors[key+".key"]="Use a unique program key with letters, numbers, underscores or hyphens.";
  if(!s||!safePath(s.path)){errors[key+".path"]="Enter a relative program filename.";continue;}
  if(!["","python","node","bash","binary",undefined].includes(s.runtime))errors[key+".runtime"]="Choose a supported runtime.";
  if(!s.runtime&&!/\.(py|js|mjs|cjs|sh)$/.test(s.path))errors[key+".runtime"]="Choose a runtime for this filename.";
  const input=s.input||d.manifest.input||"input.txt";
  if(!safePath(input))errors[key+".input"]="Use a relative input filename.";
  if((s.files??[]).some(f=>!safePath(f)))errors[key+".files"]="Supporting files must be relative paths.";
  if(!Array.isArray(s.outputs)||!s.outputs.length){errors[key+".outputs"]="Declare at least one output .txt file.";continue;}
  const seen=new Set<string>();
  s.outputs.forEach((value,index)=>{
   const o=outputObject(value);const base=o.path?.split("/").pop()||"";
   if(!safePath(o.path)||!o.path.endsWith(".txt")||base.startsWith(".")||o.path===input||seen.has(base))errors[`${key}.output.${index}`]="Use a distinct .txt output that does not replace the input.";
   if(o.send_to&&!Object.hasOwn(scripts,o.send_to))errors[`${key}.output.${index}`]="Choose an existing next program.";
   if(o.merged_name&&(!safePath(o.merged_name)||o.merged_name.includes("/")||!o.merged_name.endsWith(".txt")||o.merged_name.startsWith(".")))errors[`${key}.output.${index}`]="Merged name must be a .txt filename without folders.";
   seen.add(base);
  });
 }
 const visiting=new Set<string>(),done=new Set<string>();
 const visit=(key:string):void=>{if(visiting.has(key)){errors.connections="Connections cannot form a cycle.";return;}if(done.has(key))return;visiting.add(key);for(const o of scripts[key]?.outputs??[]){const target=outputObject(o).send_to;if(target&&scripts[target])visit(target);}visiting.delete(key);done.add(key);};
 for(const key of Object.keys(scripts))visit(key);
 return errors;
}
export function parseProcessorTemplate(text:string,current:Definition):{name:string;definition:Definition}{
 if(new TextEncoder().encode(text).length>256*1024)throw new Error("Template exceeds 256 KiB.");
 let raw:any;try{raw=JSON.parse(text);}catch{throw new Error("Choose a valid JSON processor template.");}
 let definition:Definition;
 if(raw?.format==="cnc-processor-template"&&raw.version===1)definition=raw.definition;
 else if(raw?.tool_id&&raw?.manifest)definition=raw;
 else if(raw?.scripts)definition={...current,manifest:raw};
 else throw new Error("This is not a processor template or tool.json manifest.");
 if(!definition||typeof definition.tool_id!=="string"||typeof definition.incoming!=="string"||typeof definition.folder!=="string")throw new Error("Template is missing tool, incoming filename, or folder fields.");
 let errors:Record<string,string>;try{errors=validateDefinition(definition);}catch{throw new Error("Template contains invalid program fields.");}
 // Raw manifests may leave the tool ID to be filled in through the form.
 if(raw.scripts)delete errors.tool_id;
 if(Object.keys(errors).length)throw new Error(Object.values(errors)[0]);
 return {name:typeof raw.name==="string"?raw.name:"",definition:structuredClone(definition)};
}
export function serializeProcessorTemplate(name:string,definition:Definition):string{return JSON.stringify({format:"cnc-processor-template",version:1,name,definition},null,2)+"\n";}
export async function editorRequest(path:string,method="GET",body?:unknown):Promise<any>{
 const response=await fetch('/api/processor/editor/'+path,{method,cache:"no-store",...(body instanceof FormData?{body}:{headers:{"Content-Type":"application/json"},...(body===undefined?{}:{body:JSON.stringify(body)})})});
 const result=await response.json().catch(()=>null);if(!response.ok)throw new Error(result?.error||"Request failed. Your edits are still in the form.");return result;
}

// A collection keeps tool IDs, routes and source folders independent.
export function parseProcessorTemplates(text:string,current:Definition):{name:string;definitions:Definition[]}{
 if(new TextEncoder().encode(text).length>256*1024)throw new Error("Template exceeds 256 KiB.");
 let raw:any;try{raw=JSON.parse(text);}catch{throw new Error("Choose a valid JSON processor template.");}
 if(raw?.format!=="cnc-processor-template"||raw.version!==2){const single=parseProcessorTemplate(text,current);return {name:single.name,definitions:[single.definition]};}
 if(!Array.isArray(raw.definitions)||raw.definitions.length<1||raw.definitions.length>32)throw new Error("Combined templates need 1–32 tool setups.");
 const ids=new Set<string>();
 const definitions=raw.definitions.map((definition:Definition)=>{
  const parsed=parseProcessorTemplate(JSON.stringify({format:"cnc-processor-template",version:1,definition}),current).definition;
  if(ids.has(parsed.tool_id))throw new Error("Combined template has duplicate tool ID: "+parsed.tool_id);
  ids.add(parsed.tool_id);return parsed;
 });
 return {name:typeof raw.name==="string"?raw.name:"",definitions};
}
export function serializeProcessorTemplates(name:string,definitions:Definition[]):string{
 if(definitions.length===1)return serializeProcessorTemplate(name,definitions[0]);
 const text=JSON.stringify({format:"cnc-processor-template",version:2,name,definitions},null,2)+"\n";
 parseProcessorTemplates(text,blankDefinition());return text;
}
