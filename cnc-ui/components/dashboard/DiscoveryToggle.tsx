'use client';

import React, { useState, useEffect } from 'react';
import {
  Play,
  Pause,
  Radio,
  TrendingUp,
  Zap,
  Activity,
  Globe
} from 'lucide-react';

interface DiscoveryStatus {
  enabled: boolean;
  found_count: number;
  scan_count: number;
  active_workers: number;
  last_update: string;
}

export default function DiscoveryToggle() {
  const [status, setStatus] = useState<DiscoveryStatus>({
    enabled: false,
    found_count: 0,
    scan_count: 0,
    active_workers: 0,
    last_update: new Date().toISOString(),
  });

  const [loading, setLoading] = useState(false);
  const [pollInterval, setPollInterval] = useState<NodeJS.Timeout | null>(null);

  // Fetch discovery status
  const fetchStatus = async () => {
    try {
      const res = await fetch('/api/discovery/status');
      if (res.ok) {
        const data = await res.json();
        setStatus(data);
      }
    } catch (error) {
      console.error('Failed to fetch discovery status:', error);
    }
  };

  // Poll status when enabled
  useEffect(() => {
    fetchStatus();

    if (status.enabled) {
      const interval = setInterval(fetchStatus, 3000); // Update every 3 seconds
      setPollInterval(interval);
      return () => clearInterval(interval);
    } else if (pollInterval) {
      clearInterval(pollInterval);
      setPollInterval(null);
    }
  }, [status.enabled]);

  // Toggle discovery service
  const toggleDiscovery = async () => {
    setLoading(true);
    try {
      const endpoint = status.enabled ? '/api/discovery/stop' : '/api/discovery/start';
      const res = await fetch(endpoint, { method: 'POST' });

      if (res.ok) {
        const data = await res.json();
        setStatus(data);
      }
    } catch (error) {
      console.error('Failed to toggle discovery:', error);
    } finally {
      setLoading(false);
    }
  };

  const formatNumber = (num: number) => {
    if (num >= 1000000) return (num / 1000000).toFixed(1) + 'M';
    if (num >= 1000) return (num / 1000).toFixed(1) + 'K';
    return num.toString();
  };

  const getLastUpdateTime = () => {
    const lastUpdate = new Date(status.last_update);
    const now = new Date();
    const seconds = Math.floor((now.getTime() - lastUpdate.getTime()) / 1000);

    if (seconds < 60) return `${seconds}s ago`;
    if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
    return `${Math.floor(seconds / 3600)}h ago`;
  };

  return (
    <div className="w-full bg-gradient-to-br from-slate-900 to-slate-800 border border-slate-700 rounded-lg p-6 shadow-xl">
      {/* Header */}
      <div className="flex items-center justify-between mb-6">
        <div className="flex items-center gap-3">
          <div className={`p-2 rounded-lg ${status.enabled ? 'bg-green-500/20' : 'bg-slate-700'}`}>
            <Radio className={`w-6 h-6 ${status.enabled ? 'text-green-400' : 'text-slate-400'}`} />
          </div>
          <div>
            <h2 className="text-xl font-bold text-white">WordPress Discovery</h2>
            <p className="text-sm text-slate-400">Continuous global reconnaissance</p>
          </div>
        </div>

        {/* Status Badge */}
        <div className={`px-4 py-2 rounded-full font-semibold text-sm ${
          status.enabled
            ? 'bg-green-500/20 text-green-400 border border-green-500/50'
            : 'bg-slate-700 text-slate-300 border border-slate-600'
        }`}>
          {status.enabled ? '🟢 ACTIVE' : '⚫ INACTIVE'}
        </div>
      </div>

      {/* Main Toggle Button */}
      <button
        onClick={toggleDiscovery}
        disabled={loading}
        className={`w-full py-4 px-6 rounded-lg font-bold text-lg transition-all duration-300 flex items-center justify-center gap-3 mb-6 ${
          status.enabled
            ? 'bg-red-600 hover:bg-red-700 text-white shadow-lg shadow-red-500/50'
            : 'bg-green-600 hover:bg-green-700 text-white shadow-lg shadow-green-500/50'
        } ${loading ? 'opacity-50 cursor-not-allowed' : ''}`}
      >
        {loading ? (
          <>
            <Activity className="w-5 h-5 animate-spin" />
            Loading...
          </>
        ) : status.enabled ? (
          <>
            <Pause className="w-5 h-5" />
            STOP DISCOVERY
          </>
        ) : (
          <>
            <Play className="w-5 h-5" />
            START DISCOVERY
          </>
        )}
      </button>

      {/* Stats Grid */}
      <div className="grid grid-cols-2 gap-4 mb-6">
        {/* Found Sites */}
        <div className="bg-slate-800/50 border border-slate-700 rounded-lg p-4">
          <div className="flex items-center justify-between mb-2">
            <span className="text-slate-400 text-sm font-medium">WordPress Found</span>
            <Globe className="w-4 h-4 text-blue-400" />
          </div>
          <div className="text-3xl font-bold text-blue-400">
            {formatNumber(status.found_count)}
          </div>
          <p className="text-xs text-slate-500 mt-1">Active discoveries</p>
        </div>

        {/* Scan Count */}
        <div className="bg-slate-800/50 border border-slate-700 rounded-lg p-4">
          <div className="flex items-center justify-between mb-2">
            <span className="text-slate-400 text-sm font-medium">Targets Scanned</span>
            <TrendingUp className="w-4 h-4 text-purple-400" />
          </div>
          <div className="text-3xl font-bold text-purple-400">
            {formatNumber(status.scan_count)}
          </div>
          <p className="text-xs text-slate-500 mt-1">Probed endpoints</p>
        </div>

        {/* Active Workers */}
        <div className="bg-slate-800/50 border border-slate-700 rounded-lg p-4">
          <div className="flex items-center justify-between mb-2">
            <span className="text-slate-400 text-sm font-medium">Workers Active</span>
            <Zap className="w-4 h-4 text-yellow-400" />
          </div>
          <div className="text-3xl font-bold text-yellow-400">
            {status.active_workers}
          </div>
          <p className="text-xs text-slate-500 mt-1">Concurrent threads</p>
        </div>

        {/* Last Update */}
        <div className="bg-slate-800/50 border border-slate-700 rounded-lg p-4">
          <div className="flex items-center justify-between mb-2">
            <span className="text-slate-400 text-sm font-medium">Last Update</span>
            <Activity className="w-4 h-4 text-green-400" />
          </div>
          <div className="text-xl font-bold text-green-400">
            {getLastUpdateTime()}
          </div>
          <p className="text-xs text-slate-500 mt-1">Service activity</p>
        </div>
      </div>

      {/* Discovery Methods */}
      {status.enabled && (
        <div className="bg-slate-800/50 border border-slate-700 rounded-lg p-4">
          <h3 className="text-sm font-bold text-white mb-3">Active Discovery Methods</h3>
          <div className="grid grid-cols-2 gap-2 text-xs">
            <div className="flex items-center gap-2 text-slate-300">
              <div className="w-2 h-2 bg-green-500 rounded-full animate-pulse"></div>
              CT Log Monitoring
            </div>
            <div className="flex items-center gap-2 text-slate-300">
              <div className="w-2 h-2 bg-green-500 rounded-full animate-pulse"></div>
              Reverse DNS
            </div>
            <div className="flex items-center gap-2 text-slate-300">
              <div className="w-2 h-2 bg-green-500 rounded-full animate-pulse"></div>
              Passive DNS
            </div>
            <div className="flex items-center gap-2 text-slate-300">
              <div className="w-2 h-2 bg-green-500 rounded-full animate-pulse"></div>
              BGP Monitoring
            </div>
            <div className="flex items-center gap-2 text-slate-300">
              <div className="w-2 h-2 bg-green-500 rounded-full animate-pulse"></div>
              Port Scanning
            </div>
            <div className="flex items-center gap-2 text-slate-300">
              <div className="w-2 h-2 bg-green-500 rounded-full animate-pulse"></div>
              2000+ Threads
            </div>
          </div>
        </div>
      )}

      {/* Description */}
      <div className="mt-4 text-xs text-slate-400 bg-slate-800/30 border border-slate-700 rounded p-3">
        <p>
          🌍 <strong>Continuous global reconnaissance</strong> - Automatically discovers fresh WordPress sites worldwide using multiple methods (CT logs, DNS enumeration, port scanning, BGP monitoring) with 2000+ concurrent threads running in the backend.
        </p>
      </div>
    </div>
  );
}
