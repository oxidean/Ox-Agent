import { access } from 'node:fs/promises';
import path from 'node:path';
import { ensureDir, writeText, writeYaml } from '../core/files.js';

export async function initCommand(root: string): Promise<void> {
  const ox = path.join(root, '.ox-agent');
  try {
    await access(ox);
    throw new Error(`${ox} already exists; refusing to overwrite it`);
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error;
  }

  await ensureDir(path.join(ox, 'skills'));
  await ensureDir(path.join(ox, 'context', 'standards'));
  await ensureDir(path.join(ox, 'work', 'active'));
  await ensureDir(path.join(ox, 'work', 'archived'));
  await ensureDir(path.join(ox, 'state'));
  await ensureDir(path.join(ox, 'runtime', 'packets'));
  await ensureDir(path.join(ox, 'runtime', 'runs'));
  await ensureDir(path.join(ox, 'runtime', 'cache'));
  await ensureDir(path.join(ox, 'runtime', 'locks'));
  await ensureDir(path.join(ox, 'integrations'));

  await writeYaml(path.join(ox, 'config.yaml'), {
    schema_version: 1,
    framework: { name: 'ox-agent', source: 'oxidean/ox-agent', ref: 'main' },
    project: { name: path.basename(root), test_command: '' },
    workflow: { default_profile: 'normal', profiles: { normal: ['discover', 'brainstorm', 'specify', 'plan', 'implement', 'verify', 'ship'] } },
    skills: { enabled: ['brainstorm', 'specify', 'adr', 'multi-stage-plan', 'tdd', 'code-review', 'shipping', 'discussion', 'progress'] },
  });
  await writeYaml(path.join(ox, 'policy.yaml'), {
    schema_version: 1,
    autonomy: { profile_changes: 'managed', allow_downgrade: false },
    approvals: { production_deploy: 'human', destructive_change: 'human', policy_change: 'human' },
    budgets: { max_parallel_agents: 4, max_child_depth: 1, max_fix_cycles: 3 },
  });
  await writeYaml(path.join(ox, 'lifecycle', 'workflow.yaml'), { schema_version: 1, states: ['intake', 'discovering', 'brainstorming', 'specifying', 'planning', 'implementing', 'verifying', 'shipping', 'shipped', 'blocked'] });
  await writeYaml(path.join(ox, 'context', 'project.yaml'), { schema_version: 1, name: path.basename(root), standards: [] });
  await writeText(path.join(ox, 'README.md'), '# Ox-Agent project state\n\nThis directory contains the repository-local Ox-Agent configuration and work artifacts.\n');
  await writeText(path.join(ox, 'state', 'events.ndjson'), '');
  console.log(`Initialized ${ox}`);
}
