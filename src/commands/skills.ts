import { spawn } from 'node:child_process';
import path from 'node:path';
import { listDirectories, readYaml, writeYaml } from '../core/files.js';
import { oxPath } from '../core/paths.js';

function runNpx(args: string[], cwd: string): Promise<number> {
  return new Promise((resolve, reject) => {
    const child = spawn(process.platform === 'win32' ? 'npx.cmd' : 'npx', ['skills', ...args], { cwd, stdio: 'inherit' });
    child.on('error', reject);
    child.on('exit', (code: number | null) => resolve(code ?? 1));
  });
}

export async function skillsListCommand(root: string): Promise<void> {
  const skillsDir = oxPath(root, 'skills');
  for (const skill of await listDirectories(skillsDir)) {
    try {
      const manifest = await readYaml<Record<string, unknown>>(path.join(skillsDir, skill, 'skill.yaml'));
      console.log(`${manifest.name ?? skill}\t${manifest.version ?? 'unknown'}\t${manifest.kind ?? 'unknown'}`);
    } catch {
      console.log(`${skill}\tinvalid`);
    }
  }
}

export async function skillsInstallCommand(root: string, source?: string, selected: string[] = []): Promise<void> {
  const configured = await readYaml<{ framework?: { source?: string }; skills?: { enabled?: string[] } }>(oxPath(root, 'config.yaml'));
  const packageSource = source ?? configured.framework?.source ?? 'oxidean/ox-agent';
  const skills = selected.length > 0 ? selected : configured.skills?.enabled ?? ['*'];
  const args = ['add', packageSource, '-y', '--skill', ...skills];
  const code = await runNpx(args, root);
  if (code !== 0) throw new Error(`npx skills exited with code ${code}`);
  await writeYaml(oxPath(root, 'skills.lock'), { schema_version: 1, source: packageSource, skills, installed_at: new Date().toISOString(), installation: 'npx skills symlinks' });
  console.log('Skills installed through npx skills.');
}

export async function skillsCheckCommand(root: string): Promise<void> {
  const code = await runNpx(['check'], root);
  if (code !== 0) throw new Error('Skill updates are available or the skills check failed.');
}

export async function skillsUpdateCommand(root: string, yes = false): Promise<void> {
  if (!yes && !process.stdin.isTTY) throw new Error('skills update requires --yes in non-interactive mode');
  const code = await runNpx(['update', ...(yes ? ['-y'] : [])], root);
  if (code !== 0) throw new Error(`npx skills update exited with code ${code}`);
}
