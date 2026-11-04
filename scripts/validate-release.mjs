import { execFileSync } from 'node:child_process';
import { readFile } from 'node:fs/promises';

const commit = process.argv[2] ?? 'HEAD';
const tag = process.argv[3];
const semver = '([0-9]+)\\.([0-9]+)\\.([0-9]+)';
const commitPattern = new RegExp(`^v${semver}: release \\1\\.\\2\\.\\3$`);
const tagPattern = new RegExp(`^v${semver}$`);

try {
  const packageJson = JSON.parse(await readFile(new URL('../package.json', import.meta.url), 'utf8'));
  const subject = execFileSync('git', ['show', '-s', '--format=%s', commit], { encoding: 'utf8' }).trim();
  const subjectMatch = commitPattern.exec(subject);
  if (!subjectMatch || subjectMatch[1] === '0' && subjectMatch[2].startsWith('0')) {
    throw new Error(`Invalid release commit message: ${subject}`);
  }
  const version = packageJson.version;
  const expectedSubject = `v${version}: release ${version}`;
  if (subject !== expectedSubject) throw new Error(`Release commit must be: ${expectedSubject}`);
  if (tag !== undefined) {
    const tagMatch = tagPattern.exec(tag);
    if (!tagMatch || tag !== `v${version}`) throw new Error(`Tag ${tag} does not match package version ${version}`);
  }
  console.log(`Validated release ${version}`);
} catch (error) {
  console.error(error instanceof Error ? error.message : String(error));
  process.exit(1);
}
