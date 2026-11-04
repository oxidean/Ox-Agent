import path from 'node:path';
import { listDirectories, writeText, writeYaml } from '../core/files.js';
import { appendEvent } from '../core/events.js';
import { oxPath } from '../core/paths.js';

function slugify(value: string): string {
  return value.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '').slice(0, 48) || 'work-item';
}

export async function startCommand(root: string, title: string): Promise<string> {
  if (!title?.trim()) throw new Error('start requires a work-item title');
  const active = oxPath(root, 'work', 'active');
  const existing = await listDirectories(active);
  const id = `WI-${String(existing.length + 1).padStart(4, '0')}`;
  const directory = path.join(active, `${id}-${slugify(title)}`);
  const now = new Date().toISOString();
  await writeYaml(path.join(directory, 'index.yaml'), { schema_version: 1, id, title, state: 'intake', profile: 'normal', current_stage: 'intake', created_at: now, open_questions: [], blockers: [] });
  await writeYaml(path.join(directory, 'status.yaml'), { state: 'intake', next_action: 'discover', updated_at: now });
  await writeText(path.join(directory, 'progress.ndjson'), '');
  await appendEvent(root, { type: 'work_item.created', work_item_id: id, actor: 'cli', data: { title, directory: path.relative(root, directory) } });
  console.log(`${id}: ${title}`);
  return id;
}

export async function statusCommand(root: string, id?: string): Promise<void> {
  const active = await listDirectories(oxPath(root, 'work', 'active'));
  const archived = await listDirectories(oxPath(root, 'work', 'archived'));
  const all = [...active, ...archived];
  const selected = id ? all.filter((item) => item.startsWith(id)) : all;
  if (selected.length === 0) {
    console.log(id ? `No work item found: ${id}` : 'No work items.');
    return;
  }
  for (const item of selected) {
    const location = active.includes(item) ? 'active' : 'archived';
    console.log(`${location}: ${item}`);
  }
}
