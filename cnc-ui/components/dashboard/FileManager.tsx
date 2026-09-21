"use client";

import { useState, useEffect } from "react";
import { Folder, File as FileIcon, ChevronRight, Trash2, ArrowLeft, RefreshCw, X } from "lucide-react";

interface FileInfo {
  name: string;
  path: string;
  is_dir: boolean;
  size: number;
  mod_time: string;
}

export function FileManager() {
  const [currentDir, setCurrentDir] = useState<string>(".");
  const [files, setFiles] = useState<FileInfo[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  
  const [viewingFile, setViewingFile] = useState<{name: string, path: string, content: string} | null>(null);
  const [viewLoading, setViewLoading] = useState(false);

  const fetchFiles = async (dir: string) => {
    setLoading(true);
    setError(null);
    try {
      const res = await fetch(`/api/fs/list?dir=${encodeURIComponent(dir)}`);
      if (!res.ok) throw new Error("Failed to load directory");
      const data = await res.json();
      setCurrentDir(data.current_dir);
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
    fetchFiles(path);
  };

  const handleNavigateUp = () => {
    // Basic trick: append /.. 
    handleNavigate(currentDir + "/..");
  };

  const handleOpenFile = async (file: FileInfo) => {
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

  const formatSize = (bytes: number) => {
    if (bytes === 0) return "0 B";
    const k = 1024;
    const sizes = ["B", "KB", "MB", "GB", "TB"];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + " " + sizes[i];
  };

  return (
    <div className="flex flex-col h-full bg-gray-950 text-gray-200">
      
      {/* Toolbar */}
      <div className="flex items-center gap-3 p-4 bg-gray-900 border-b border-gray-800 shrink-0">
        <button 
          onClick={handleNavigateUp}
          className="p-1.5 hover:bg-gray-800 rounded-md transition-colors text-gray-400 hover:text-white"
          title="Go Up"
        >
          <ArrowLeft className="w-4 h-4" />
        </button>
        <button 
          onClick={() => fetchFiles(currentDir)}
          className="p-1.5 hover:bg-gray-800 rounded-md transition-colors text-gray-400 hover:text-white"
          title="Refresh"
        >
          <RefreshCw className={`w-4 h-4 ${loading ? 'animate-spin' : ''}`} />
        </button>
        
        {/* Breadcrumb pseudo-view */}
        <div className="flex-1 px-3 py-1.5 bg-gray-950 border border-gray-800 rounded-md font-mono text-sm text-gray-400 truncate">
          {currentDir}
        </div>
      </div>

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
                      onClick={(e) => handleDelete(file, e)}
                      className="p-1.5 text-gray-500 hover:text-red-400 opacity-0 group-hover:opacity-100 transition-all rounded"
                      title="Delete"
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
