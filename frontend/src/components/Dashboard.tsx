"use client"
import React, { useEffect, useState } from 'react';

type NodeStatus = {
  replicaId: string;
  state: string; // LEADER, FOLLOWER, CANDIDATE
  term: number;
  commitIndex: number;
  logLength: number;
};

type ClusterStatusRes = {
  nodes: { peer: string; status: NodeStatus }[];
};

const GATEWAY_URL = process.env.NEXT_PUBLIC_GATEWAY_URL || 'http://localhost:8080';

export default function Dashboard() {
  const [nodes, setNodes] = useState<{ peer: string; status: NodeStatus }[]>([]);

  useEffect(() => {
    const fetchStatus = async () => {
      try {
        const res = await fetch(`${GATEWAY_URL}/cluster-status`);
        if (!res.ok) return;
        const data = (await res.json()) as ClusterStatusRes;
        
        // Sort nodes by replicaId for consistent ordering
        const sortedNodes = data.nodes.sort((a, b) => 
          a.status.replicaId.localeCompare(b.status.replicaId)
        );
        setNodes(sortedNodes);
      } catch {
        // Silently ignore polling errors if gateway is down
      }
    };

    fetchStatus();
    const interval = setInterval(fetchStatus, 1000);
    return () => clearInterval(interval);
  }, []);

  return (
    <div className="fixed top-6 left-6 z-20 bg-white/90 backdrop-blur-sm shadow-xl border border-neutral-200 rounded-xl p-4 w-72 pointer-events-auto transition-all">
      <h2 className="text-xs font-bold text-neutral-800 mb-3 uppercase tracking-wider flex items-center justify-between">
        <div className="flex items-center gap-2">
          <div className="w-2 h-2 rounded-full bg-green-500 animate-pulse shadow-[0_0_8px_rgba(34,197,94,0.8)]" />
          Cluster Status
        </div>
        <span className="bg-neutral-100 text-neutral-500 px-2 py-0.5 rounded text-[10px]">LIVE</span>
      </h2>
      
      <div className="flex flex-col gap-2">
        {nodes.length === 0 ? (
           <div className="text-xs text-neutral-500 italic p-2 text-center bg-neutral-50 rounded-lg border border-neutral-100">
             Discovering RAFT Nodes...
           </div>
        ) : (
          nodes.map(({ status }) => {
            const isLeader = status.state === 'LEADER';
            return (
              <div 
                key={status.replicaId} 
                className={`p-3 rounded-lg border transition-all ${
                  isLeader ? 'bg-blue-50/80 border-blue-200 shadow-sm' : 'bg-neutral-50/80 border-neutral-100'
                }`}
              >
                <div className="flex justify-between items-center mb-1.5">
                  <span className={`font-semibold text-sm ${isLeader ? 'text-blue-900' : 'text-neutral-700'}`}>
                    {status.replicaId}
                  </span>
                  <span className={`text-[10px] font-bold px-2 py-0.5 rounded-full uppercase tracking-wider ${
                    isLeader ? 'bg-blue-500 text-white shadow-sm' : 
                    status.state === 'CANDIDATE' ? 'bg-amber-100 text-amber-700 border border-amber-200' : 
                    'bg-neutral-200 text-neutral-600'
                  }`}>
                    {status.state}
                  </span>
                </div>
                <div className="flex justify-between text-xs text-neutral-500 bg-white/50 p-1.5 rounded border border-neutral-100/50">
                  <span className="flex items-center gap-1">
                    Term: <strong className="text-neutral-700 font-mono">{status.term}</strong>
                  </span>
                  <span className="flex items-center gap-1">
                    Logs: <strong className="text-neutral-700 font-mono">{status.logLength}</strong>
                  </span>
                </div>
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}
