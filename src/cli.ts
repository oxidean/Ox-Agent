#!/usr/bin/env node
import path from 'node:path';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { findProjectRoot } from './core/paths.js';
import { initCommand } from './commands/init.js';
import { startCommand, statusCommand } from './commands/work.js';
import { skillsCheckCommand, skillsInstallCommand, skillsListCommand, skillsUpdateCommand } from './commands/skills.js';
import { validateProject } from './core/validation.js';

async function packageVersion(): Promise<string> {
  const packageFile = fileURLToPath(new URL('../package.json', import.meta.url));
  try {
    const packageJson = JSON.parse(await readFile(packageFile, 'utf8')) as { version: string };
    return packageJson.version;
  } catch (error) {
    throw new Error(`Could not read Ox-Agent version: ${error instanceof Error ? error.message : String(error)}`);
  }
}

function help(): void {
  console.log(`ox-agent\n\nUsage:\n  ox-agent init\n  ox-agent doctor\n  ox-agent start <title>\n  ox-agent status [work-item]\n  ox-agent skills list\n  ox-agent skills install [source] [--skill name]\n  ox-agent skills check\n  ox-agent skills update [--yes]`);
}

async function main(): Promise<void> {
  const [, , command, ...args] = process.argv;
  if (command === '--version' || command === '-v' || command === 'version') {
    console.log(await packageVersion());
    return;
  }
  if (!command || command === '--help' || command === '-h') return help();
  if (command === 'init') return initCommand(path.resolve(process.cwd()));
  const root = await findProjectRoot();
  if (command === 'doctor') {
    const issues = await validateProject(root);
    if (issues.length > 0) {
      for (const issue of issues) console.error(`${issue.file}: ${issue.message}`);
      process.exitCode = 1;
      return;
    }
    console.log('Ox-Agent doctor: healthy');
    return;
  }
  if (command === 'start') { await startCommand(root, args.filter((arg: string) => !arg.startsWith('--')).join(' ')); return; }
  if (command === 'status') return statusCommand(root, args[0]);
  if (command === 'skills') {
    const subcommand = args[0];
    if (subcommand === 'list') return skillsListCommand(root);
    if (subcommand === 'check') return skillsCheckCommand(root);
    if (subcommand === 'update') return skillsUpdateCommand(root, args.includes('--yes'));
    if (subcommand === 'install') {
      const source = args.find((arg: string) => !arg.startsWith('--') && arg !== 'install');
      const selected = args.flatMap((arg: string, index: number) => arg === '--skill' && args[index + 1] ? [args[index + 1]] : []).filter(Boolean);
      return skillsInstallCommand(root, source, selected);
    }
  }
  help();
  process.exitCode = 1;
}

main().catch((error) => {
  console.error(error instanceof Error ? error.message : String(error));
  process.exitCode = 1;
});
