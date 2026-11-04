import { readFile, writeFile } from 'node:fs/promises';

const bump = process.argv[2];
const validBumps = new Set(['patch', 'minor', 'major']);
if (!validBumps.has(bump)) {
  console.error(`Usage: bun scripts/release-version.mjs <${[...validBumps].join('|')}>`);
  process.exit(1);
}

const packageFile = new URL('../package.json', import.meta.url);
try {
  const packageJson = JSON.parse(await readFile(packageFile, 'utf8'));
  const match = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.exec(packageJson.version);
  if (!match) throw new Error(`package.json contains invalid stable SemVer: ${packageJson.version}`);

  let [major, minor, patch] = match.slice(1).map(Number);
  if (bump === 'major') { major += 1; minor = 0; patch = 0; }
  if (bump === 'minor') { minor += 1; patch = 0; }
  if (bump === 'patch') patch += 1;

  const version = `${major}.${minor}.${patch}`;
  packageJson.version = version;
  await writeFile(packageFile, `${JSON.stringify(packageJson, null, 2)}\n`);
  console.log(version);
} catch (error) {
  console.error(error instanceof Error ? error.message : String(error));
  process.exit(1);
}
