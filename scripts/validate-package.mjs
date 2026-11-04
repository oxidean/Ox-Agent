import { readFileSync } from 'node:fs';
import { readdir, rm } from 'node:fs/promises';
import { $ } from 'bun';

try {
  const packageJson = JSON.parse(readFileSync('package.json', 'utf8'));
  const archive = (await readdir('.')).find((entry) => entry.endsWith('.tgz') && entry.includes('oxidean-ox-agent'));
  if (!archive) throw new Error('Package archive was not created');

  const listing = (await $`tar -tzf ${archive}`.text()).split('\n').filter(Boolean);
  for (const required of ['package/dist/cli.js', 'package/package.json', 'package/schemas/config.schema.json', 'package/skills/brainstorm/SKILL.md']) {
    if (!listing.includes(required)) throw new Error(`Package is missing ${required}`);
  }
  for (const forbidden of ['package/.git/', 'package/node_modules/', 'package/test/']) {
    if (listing.some((entry) => entry.startsWith(forbidden))) throw new Error(`Package includes ${forbidden}`);
  }
  const metadata = JSON.parse(await $`tar -xOf ${archive} package/package.json`.text());
  if (metadata.name !== packageJson.name || metadata.version !== packageJson.version) throw new Error('Packed metadata does not match package.json');
  await rm(archive);
  console.log(`Validated ${packageJson.name}@${packageJson.version}`);
} catch (error) {
  console.error(error instanceof Error ? error.message : String(error));
  process.exitCode = 1;
}
