import { appendFile } from 'node:fs/promises';
import { ensureDir } from './files.js';
import path from 'node:path';
import { oxPath } from './paths.js';

export interface LifecycleEvent {
  schema_version: 1;
  event_id: string;
  type: string;
  work_item_id: string;
  actor: string;
  timestamp: string;
  data?: Record<string, unknown>;
}

export async function appendEvent(root: string, event: Omit<LifecycleEvent, 'schema_version' | 'event_id' | 'timestamp'>): Promise<LifecycleEvent> {
  const complete: LifecycleEvent = {
    schema_version: 1,
    event_id: `evt_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`,
    timestamp: new Date().toISOString(),
    ...event,
  };
  const file = oxPath(root, 'state', 'events.ndjson');
  await ensureDir(path.dirname(file));
  await appendFile(file, `${JSON.stringify(complete)}\n`, 'utf8');
  return complete;
}
