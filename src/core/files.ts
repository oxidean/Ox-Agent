import { createHash } from 'node:crypto';
import { readFile, writeFile, mkdir, readdir, lstat } from 'node:fs/promises';
import path from 'node:path';
import { parse, stringify } from 'yaml';

export async function ensureDir(directory: string): Promise<void> {
  await mkdir(directory, { recursive: true });
}

export async function readYaml<T = unknown>(file: string): Promise<T> {
  return parse(await readFile(file, 'utf8')) as T;
}

export async function writeYaml(file: string, value: unknown): Promise<void> {
  await ensureDir(path.dirname(file));
  await writeFile(file, stringify(value), 'utf8');
}

export async function writeText(file: string, content: string): Promise<void> {
  await ensureDir(path.dirname(file));
  await writeFile(file, content, 'utf8');
}

export async function sha256File(file: string): Promise<string> {
  const digest = createHash('sha256').update(await readFile(file)).digest('hex');
  return `sha256:${digest}`;
}

export async function listDirectories(directory: string): Promise<string[]> {
  try {
    const entries = await readdir(directory, { withFileTypes: true });
    return entries.filter((entry: import('node:fs').Dirent) => entry.isDirectory() || entry.isSymbolicLink()).map((entry: import('node:fs').Dirent) => entry.name);
  } catch {
    return [];
  }
}

export async function isSymlink(file: string): Promise<boolean> {
  try {
    return (await lstat(file)).isSymbolicLink();
  } catch {
    return false;
  }
}
