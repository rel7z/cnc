"use client";

import { useState, type FormEvent } from "react";
import { loginDestination } from "@/lib/login-destination";
import { ArrowRight, Eye, EyeOff, LockKeyhole, UserRound, Layers3, LoaderCircle, Network } from "lucide-react";

export default function LoginPage() {
  const [visible,setVisible]=useState(false);
  const [busy,setBusy]=useState(false);
  const [error,setError]=useState("");
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();setBusy(true);setError("");
    const fields=new FormData(event.currentTarget);
    try {
      const response=await fetch("/api/auth/login",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({username:fields.get("username"),password:fields.get("password")})});
      const result=await response.json();
      if (!response.ok) throw new Error(result.error || "Sign-in failed. Please try again.");
      const next=new URLSearchParams(window.location.search).get("next") || "/dashboard";
      window.location.assign(loginDestination(next));
    } catch(e) {setError(e instanceof Error ? e.message : "Unable to connect. Please try again.");setBusy(false);}
  }
  return <main className="min-h-screen flex flex-col bg-[#080b12] text-slate-100 selection:bg-cyan-400/30">
    <header className="flex items-center justify-between px-6 py-7 sm:px-12">
      <a href="/login" className="flex items-center gap-3 font-semibold tracking-tight"><span className="flex h-9 w-9 items-center justify-center rounded-xl bg-cyan-300 text-slate-950"><Layers3 size={20}/></span>CNC <span className="font-normal text-slate-500">Console</span></a>
      <span className="text-xs text-slate-500">Private workspace</span>
    </header>
    <div className="flex flex-1 items-center justify-center px-6 py-10 sm:px-12">
      <div className="grid w-full max-w-5xl items-center gap-16 lg:grid-cols-2 lg:gap-24">
        <section className="hidden lg:block">
          <p className="mb-6 flex items-center gap-2 text-xs font-medium uppercase tracking-[0.22em] text-cyan-300"><span className="h-px w-8 bg-cyan-300/60"/>Your operations, connected</p>
          <h1 className="text-5xl font-semibold leading-[1.12] tracking-tight">One workspace.<br/><span className="text-slate-500">Everything connected.</span></h1>
          <p className="mt-6 max-w-sm text-base leading-7 text-slate-400">Monitor servers, manage workers, and run processing workflows from one dashboard.</p>
          <div aria-hidden="true" className="relative mt-12 h-44 max-w-sm overflow-hidden rounded-2xl border border-white/10 bg-white/[0.02]">
            <div className="absolute inset-0 bg-[radial-gradient(ellipse_at_center,rgba(34,211,238,0.09),transparent_70%)]"/>
            <div className="absolute inset-x-14 top-1/2 h-px bg-cyan-300/20"/>
            <div className="relative flex h-full items-center justify-around px-5">{["SERVER","PROCESSOR","WORKERS"].map((item,index)=><div key={item} className="flex flex-col items-center gap-4"><span className={`flex h-12 w-12 items-center justify-center rounded-2xl border ${index===1?"border-cyan-300/40 bg-[#133039] text-cyan-300":"border-slate-700 bg-[#111722] text-slate-500"}`}><Network size={20}/></span><span className="text-[9px] tracking-[0.18em] text-slate-500">{item}</span></div>)}</div>
          </div>
        </section>
        <section className="w-full max-w-md justify-self-center rounded-3xl border border-white/10 bg-[#10151f] p-7 shadow-2xl shadow-black/20 sm:p-10">
          <div className="mb-7 flex h-11 w-11 items-center justify-center rounded-xl border border-white/10 bg-white/5 text-cyan-300"><LockKeyhole size={21}/></div>
          <h2 className="text-3xl font-semibold tracking-tight">Welcome back.</h2>
          <p className="mt-3 text-sm leading-6 text-slate-400">Sign in to manage your CNC workspace.</p>
          <form onSubmit={submit} className="mt-8 space-y-5">
            <div><label htmlFor="username" className="mb-2 block text-sm font-medium text-slate-300">Username</label><div className="relative"><UserRound size={17} className="pointer-events-none absolute left-4 top-3.5 text-slate-500"/><input id="username" name="username" autoComplete="username" autoCapitalize="none" spellCheck={false} required maxLength={128} placeholder="Enter your username" className="w-full rounded-xl border border-slate-700/70 bg-[#0b1019] py-3 pl-11 pr-4 text-sm outline-none transition focus:border-cyan-300 focus:ring-4 focus:ring-cyan-300/10 placeholder:text-slate-600"/></div></div>
            <div><label htmlFor="password" className="mb-2 block text-sm font-medium text-slate-300">Password</label><div className="relative"><LockKeyhole size={17} className="pointer-events-none absolute left-4 top-3.5 text-slate-500"/><input id="password" name="password" type={visible?"text":"password"} autoComplete="current-password" required maxLength={256} placeholder="Enter your password" className="w-full rounded-xl border border-slate-700/70 bg-[#0b1019] py-3 pl-11 pr-12 text-sm outline-none transition focus:border-cyan-300 focus:ring-4 focus:ring-cyan-300/10 placeholder:text-slate-600"/><button type="button" aria-label={visible?"Hide password":"Show password"} onClick={()=>setVisible(!visible)} className="absolute right-3 top-2.5 rounded-md p-1 text-slate-500 hover:text-white focus-visible:outline-2 focus-visible:outline-cyan-300">{visible?<EyeOff size={18}/>:<Eye size={18}/>}</button></div></div>
            {error && <p role="alert" className="rounded-xl border border-red-400/20 bg-red-400/5 px-4 py-3 text-sm text-red-300">{error}</p>}
            <button disabled={busy} className="flex w-full items-center justify-center gap-3 rounded-xl bg-cyan-300 px-5 py-3.5 text-sm font-semibold text-slate-950 transition hover:bg-cyan-200 focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-cyan-300 disabled:opacity-60">{busy?<><LoaderCircle size={17} className="animate-spin"/>Signing in…</>:<>Sign in to dashboard<ArrowRight size={17}/></>}</button>
          </form>
          <p className="mt-7 text-center text-xs leading-5 text-slate-500">For authorized users only.</p>
        </section>
      </div>
    </div>
    <footer className="px-6 py-6 text-center text-xs text-slate-600">CNC Console · Workspace management</footer>
  </main>;
}
