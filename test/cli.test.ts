import assert from 'node:assert/strict';
import { mkdtemp, readFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { initCommand } from '../src/commands/init.js';
import { startCommand } from '../src/commands/work.js';
import { validateProject } from '../src/core/validation.js';

test('initializes and validates a project, then records a work item event', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'ox-agent-'));

  await initCommand(root);
  assert.deepEqual(await validateProject(root), []);

  const workItemId = await startCommand(root, 'Test work item');
  assert.equal(workItemId, 'WI-0001');

  const events = await readFile(path.join(root, '.ox-agent', 'state', 'events.ndjson'), 'utf8');
  assert.match(events, /"type":"work_item\.created"/);
  assert.match(events, /"work_item_id":"WI-0001"/);
});
