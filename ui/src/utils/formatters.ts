// Utility functions for formatting display data

export function formatRelativeTime(isoTimestamp: string): string {
  const now = new Date();
  const then = new Date(isoTimestamp);
  const diffMs = now.getTime() - then.getTime();
  const diffSecs = Math.floor(diffMs / 1000);
  const diffMins = Math.floor(diffSecs / 60);
  const diffHours = Math.floor(diffMins / 60);

  if (diffSecs < 60) return 'just now';
  if (diffMins < 60) return `${diffMins}m ago`;
  if (diffHours < 24) return `${diffHours}h ago`;
  
  return then.toLocaleTimeString('en-US', { 
    hour: '2-digit', 
    minute: '2-digit',
    hour12: false 
  });
}

export function getScoreTier(score: number): 'high' | 'medium' | 'low' {
  if (score >= 8) return 'high';
  if (score >= 5) return 'medium';
  return 'low';
}

export function getSourceFromSignal(source: string): 'MTPROTO' | 'HABR' | 'WEB' {
  const sourceLower = source.toLowerCase();
  if (sourceLower.includes('telegram') || sourceLower.includes('mtproto')) return 'MTPROTO';
  if (sourceLower.includes('habr')) return 'HABR';
  return 'WEB';
}

export function extractOriginName(source: string, url: string): string {
  // Try to extract channel/platform name
  if (source.toLowerCase().includes('telegram')) {
    const match = url.match(/@([a-zA-Z0-9_]+)/);
    return match ? `@${match[1]}` : 'Telegram';
  }
  if (source.toLowerCase().includes('habr')) {
    return 'habr.freelance';
  }
  try {
    const urlObj = new URL(url);
    return urlObj.hostname.replace('www.', '');
  } catch {
    return source;
  }
}
