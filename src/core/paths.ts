import { access } from 'node:fs/promises';
import path from 'node:path';

export async function findProjectRoot(start = process.cwd()): Promise<string> {
  let current = path.resolve(start);
  while (true) {
    try {
      await access(path.join(current, '.ox-agent'));
      return current;
    } catch {
      const parent = path.dirname(current);
      if (parent === current) {
        throw new Error(`Could not find .ox-agent from ${start}`);
      }
      current = parent;
    }
  }
}

export function oxPath(root: string, ...parts: string[]): string {
  return path.join(root, '.ox-agent', ...parts);
}
