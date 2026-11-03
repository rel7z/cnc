"use client";

import { useState, useEffect, useRef } from "react";
import { Folder, File as FileIcon, ChevronRight, Trash2, ArrowLeft, RefreshCw, X, Upload, CheckCircle2, AlertCircle } from "lucide-react";

interface FileInfo {
  name: string;
  path: string;
  is_dir: boolean;
  size: number;
  mod_time: string;
}

type UploadItem = { name: string; progress: number; state: "waiting" | "uploading" | "done" | "failed"; error?: string };
const MAX_UPLOAD_BYTES = 90 * 1024 * 1024;

export function FileManager() {
  const [currentDir, setCurrentDir] = useState<string>(".");
  const [files, setFiles] = useState<FileInfo[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [pathInput, setPathInput] = useState(".");
  const [uploading, setUploading] = useState(false);
  const [uploads, setUploads] = useState<UploadItem[]>([]);
  const [dragging, setDragging] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const uploadLock = useRef(false);
  
  const [viewingFile, setViewingFile] = useState<{name: string, path: string, content: string} | null>(null);
  const [viewLoading, setViewLoading] = useState(false);

  const fetchFiles = async (dir: string) => {
    setLoading(true);
    setError(null);
    try {
      const res = await fetch(`/api/fs/list?dir=${encodeURIComponent(dir)}`);
      if (!res.ok) throw new Error(await res.text() || "Failed to load directory");
      const data = await res.json();
      setCurrentDir(data.current_dir);
      setPathInput(data.current_dir);
      setFiles(data.files || []);
    } catch (err: any) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchFiles(currentDir);
  }, []); // Initial load

  const handleNavigate = (path: string) => {
    if (uploadLock.current) return;
    fetchFiles(path);
  };

  const handleNavigateUp = () => {
    // Basic trick: append /.. 
    handleNavigate(currentDir + "/..");
  };

  const handleOpenFile = async (file: FileInfo) => {
    if (uploadLock.current) return;
    if (file.is_dir) {
      handleNavigate(file.path);
      return;
    }
    
    // Attempt to read file content
    setViewLoading(true);
    try {
      const res = await fetch(`/api/fs/read?file=${encodeURIComponent(file.path)}`);
      if (!res.ok) throw new Error("Failed to read file");
      const text = await res.text();
      setViewingFile({ name: file.name, path: file.path, content: text });
    } catch (err: any) {
      alert(err.message);
    } finally {
      setViewLoading(false);
    }
  };

  const handleDelete = async (file: FileInfo, e: React.MouseEvent) => {
    e.stopPropagation();
    if (!confirm(`Are you sure you want to delete ${file.name}?`)) return;

    try {
      const res = await fetch(`/api/fs/delete?file=${encodeURIComponent(file.path)}`, {
        method: "DELETE",
      });
      if (!res.ok) throw new Error("Delete failed");
      fetchFiles(currentDir); // Refresh
    } catch (err: any) {
      alert(err.message);
    }
  };

  async function uploadFiles(selected: File[]) {
    if (!selected.length || uploadLock.current || loading) return;
    uploadLock.current = true;
    setUploading(true); setDragging(false); setError(null);
    const destination = currentDir;
    setUploads(selected.map(file => ({name: file.name, progress: 0, state: "waiting"})));
    const update = (index: number, patch: Partial<UploadItem>) => setUploads(items => items.map((item, i) => i === index ? {...item, ...patch} : item));
    try {
      for (let index = 0; index < selected.length; index++) {
        const file = selected[index];
        if (file.size > MAX_UPLOAD_BYTES) { update(index, {state:"failed", error:"File exceeds 90 MB"}); continue; }
        update(index, {state:"uploading"});
        try {
          await new Promise<void>((resolve, reject) => {
            const xhr = new XMLHttpRequest();
            xhr.open("POST", `/api/fs/upload?dir=${encodeURIComponent(destination)}`);
            xhr.timeout = 10 * 60 * 1000;
            xhr.upload.onprogress = event => {if (event.lengthComputable) update(index, {progress:Math.round(event.loaded / event.total * 100)});};
            xhr.onload = () => {
              if (xhr.status >= 200 && xhr.status < 300) resolve();
              else reject(new Error(xhr.status === 401 ? "Session expired. Please sign in again." : xhr.responseText.trim() || "Upload failed"));
            };
            xhr.onerror = () => reject(new Error("Connection lost during upload"));
            xhr.ontimeout = () => reject(new Error("Upload timed out"));
            xhr.onabort = () => reject(new Error("Upload cancelled"));
            const body = new FormData(); body.append("file", file); xhr.send(body);
          });
          update(index, {state:"done",progress:100});
        } catch (err) { update(index, {state:"failed",error:err instanceof Error ? err.message : "Upload failed"}); }
      }
      await fetchFiles(destination);
    } finally {
      uploadLock.current = false; setUploading(false);
      if (fileInput.current) fileInput.current.value = "";
    }
  }

  const formatSize = (bytes: number) => {
    if (bytes === 0) return "0 B";
    const k = 1024;
    const sizes = ["B", "KB", "MB", "GB", "TB"];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + " " + sizes[i];
  };

  return (
    <div className="relative flex flex-col h-full bg-gray-950 text-gray-200"
      onDragOver={event => {if (event.dataTransfer.types.includes("Files")) {event.preventDefault(); if (!uploading && !loading) setDragging(true);}}}
      onDragLeave={event => {if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDragging(false);}}
      onDrop={event => {event.preventDefault();setDragging(false);void uploadFiles(Array.from(event.dataTransfer.files));}}>
      <input ref={fileInput} type="file" multiple className="hidden" aria-label="Choose files to upload" onChange={event => {void uploadFiles(Array.from(event.target.files || []));}} />
      
      {/* Toolbar */}
      <div className="flex items-center gap-3 p-4 bg-gray-900 border-b border-gray-800 shrink-0">
        <button 
          disabled={uploading || loading}
          onClick={handleNavigateUp}
          className="p-1.5 hover:bg-gray-800 rounded-md transition-colors text-gray-400 hover:text-white"
          title="Go Up"
        >
          <ArrowLeft className="w-4 h-4" />
        </button>
        <button 
          disabled={uploading || loading}
          onClick={() => fetchFiles(currentDir)}
          className="p-1.5 hover:bg-gray-800 rounded-md transition-colors text-gray-400 hover:text-white"
          title="Refresh"
        >
          <RefreshCw className={`w-4 h-4 ${loading ? 'animate-spin' : ''}`} />
        </button>
        
        <form className="flex flex-1 min-w-0 gap-2" onSubmit={event => {event.preventDefault();handleNavigate(pathInput);}}>
          <input aria-label="Server directory" value={pathInput} onChange={event => setPathInput(event.target.value)} disabled={uploading || loading} className="min-w-0 flex-1 px-3 py-1.5 bg-gray-950 border border-gray-800 rounded-md font-mono text-sm text-gray-300 focus:outline-none focus:border-blue-500" />
          <button disabled={uploading || loading} className="rounded-md border border-gray-700 px-2 text-xs hover:bg-gray-800 disabled:opacity-40">Go</button>
        </form>
        <button onClick={() => fileInput.current?.click()} disabled={uploading || loading} className="flex shrink-0 items-center gap-2 rounded-lg bg-blue-600 px-3 py-2 text-sm font-medium text-white hover:bg-blue-500 disabled:opacity-50">
          <Upload size={16}/><span>{uploading ? "Uploading…" : "Upload files"}</span>
        </button>
      </div>

      <div className="flex items-center justify-between gap-3 border-b border-gray-800 px-4 py-2 text-xs text-gray-500">
        <span>Drop files here or choose Upload files · up to 90 MB each</span>
        {uploads.length > 0 && !uploading && <button onClick={() => setUploads([])} className="text-gray-400 hover:text-white">Dismiss uploads</button>}
      </div>
      {uploads.length > 0 && <div aria-live="polite" className="max-h-48 overflow-y-auto border-b border-gray-800 bg-gray-900/50 px-4 py-3 space-y-3">
        {uploads.map((item,index) => <div key={`${item.name}-${index}`}>
          <div className="flex items-center gap-2 text-xs">
            {item.state === "done" ? <CheckCircle2 size={14} className="text-emerald-400"/> : item.state === "failed" ? <AlertCircle size={14} className="text-red-400"/> : <Upload size={14} className="text-blue-400"/>}
            <span className="min-w-0 flex-1 truncate">{item.name}</span>
            <span className={item.state === "failed" ? "text-red-400" : "text-gray-400"}>{item.state === "done" ? "Uploaded" : item.state === "failed" ? "Failed" : item.state === "waiting" ? "Waiting" : `${item.progress}%`}</span>
          </div>
          {item.state === "uploading" && <div role="progressbar" aria-label={`Uploading ${item.name}`} aria-valuenow={item.progress} aria-valuemin={0} aria-valuemax={100} className="mt-2 h-1 rounded bg-gray-800"><div className="h-full rounded bg-blue-500 transition-all" style={{width:`${item.progress}%`}} /></div>}
          {item.error && <p className="mt-1 break-words text-xs text-red-300">{item.error}</p>}
        </div>)}
      </div>}
      {dragging && <div className="pointer-events-none absolute inset-2 z-40 flex items-center justify-center rounded-xl border-2 border-dashed border-blue-400 bg-gray-950/90"><div className="text-center"><Upload size={36} className="mx-auto mb-3 text-blue-400"/><p className="font-medium">Drop files to upload</p><p className="mt-2 max-w-sm break-all text-xs text-gray-400">{currentDir}</p></div></div>}

      {/* File List */}
      <div className="flex-1 overflow-y-auto relative">
        {error ? (
          <div className="flex items-center justify-center h-full text-red-400 text-sm">{error}</div>
        ) : (
          <table className="w-full text-sm text-left">
            <thead className="text-xs text-gray-500 uppercase bg-gray-900/50 sticky top-0 z-10 shadow-sm border-b border-gray-800">
              <tr>
                <th className="px-4 py-3 font-medium">Name</th>
                <th className="px-4 py-3 font-medium w-32">Size</th>
                <th className="px-4 py-3 font-medium w-48">Modified</th>
                <th className="px-4 py-3 font-medium w-24 text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {files.length === 0 && !loading && (
                <tr>
                  <td colSpan={4} className="px-4 py-8 text-center text-gray-500">
                    Directory is empty
                  </td>
                </tr>
              )}
              {files.map((file, idx) => (
                <tr 
                  key={idx} 
                  onClick={() => handleOpenFile(file)}
                  className="border-b border-gray-800/50 hover:bg-gray-800/40 cursor-pointer group transition-colors"
                >
                  <td className="px-4 py-2.5 font-medium flex items-center gap-3">
                    {file.is_dir ? (
                      <Folder className="w-4 h-4 text-blue-400" />
                    ) : (
                      <FileIcon className="w-4 h-4 text-gray-400" />
                    )}
                    <span className="truncate">{file.name}</span>
                  </td>
                  <td className="px-4 py-2.5 text-gray-400">
                    {file.is_dir ? "--" : formatSize(file.size)}
                  </td>
                  <td className="px-4 py-2.5 text-gray-400 text-xs">
                    {new Date(file.mod_time).toLocaleString()}
                  </td>
                  <td className="px-4 py-2.5 text-right">
                    <button 
                      disabled={uploading}
                      onClick={(e) => handleDelete(file, e)}
                      className="p-1.5 text-gray-500 hover:text-red-400 opacity-0 group-hover:opacity-100 transition-all rounded"
                      title="Delete"
                      aria-label={`Delete ${file.name}`}
                    >
                      <Trash2 className="w-4 h-4" />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {/* File Viewer Modal */}
      {viewingFile && (
        <div className="absolute inset-0 z-50 bg-gray-950 flex flex-col">
          <div className="flex items-center justify-between px-4 py-3 bg-gray-900 border-b border-gray-800 shadow-sm">
            <div className="flex items-center gap-2">
              <FileIcon className="w-4 h-4 text-gray-400" />
              <h3 className="font-medium text-gray-200">{viewingFile.name}</h3>
              <span className="text-xs text-gray-500 ml-2">{viewingFile.path}</span>
            </div>
            <button 
              onClick={() => setViewingFile(null)}
              className="p-1.5 hover:bg-gray-800 rounded-md text-gray-400 hover:text-white transition-colors"
            >
              <X className="w-5 h-5" />
            </button>
          </div>
          <div className="flex-1 overflow-hidden bg-[#0d0d0d] p-4">
            <textarea 
              readOnly
              value={viewingFile.content}
              className="w-full h-full bg-transparent border-none outline-none text-gray-300 font-mono text-sm resize-none whitespace-pre"
            />
          </div>
        </div>
      )}
      
      {viewLoading && (
        <div className="absolute inset-0 z-50 bg-gray-950/50 backdrop-blur-sm flex items-center justify-center">
          <RefreshCw className="w-8 h-8 text-blue-500 animate-spin" />
        </div>
      )}
    </div>
  );
}
