import { Ajv, type ErrorObject, type ValidateFunction } from 'ajv';
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { parse } from 'yaml';
import { listDirectories, readYaml } from './files.js';
import { oxPath } from './paths.js';

const schemaNames = ['config', 'policy', 'skill-manifest', 'discussion', 'evaluation'] as const;
type SchemaName = (typeof schemaNames)[number];

async function loadSchema(name: SchemaName): Promise<object> {
  const file = new URL(`../../schemas/${name}.schema.json`, import.meta.url);
  try {
    return JSON.parse(await readFile(file, 'utf8')) as object;
  } catch (error) {
    throw new Error(`Could not load schema ${name}: ${error instanceof Error ? error.message : String(error)}`);
  }
}

async function validator(name: SchemaName): Promise<ValidateFunction> {
  const ajv = new Ajv({ allErrors: true, strict: false });
  return ajv.compile(await loadSchema(name));
}

export interface ValidationIssue { file: string; message: string; }

export async function validateDocument(name: SchemaName, file: string): Promise<ValidationIssue[]> {
  const check = await validator(name);
  const document = await readYaml(file);
  if (check(document)) return [];
  return (check.errors ?? []).map((error: ErrorObject) => ({ file, message: `${error.instancePath || '/'} ${error.message ?? 'is invalid'}` }));
}

export async function validateProject(root: string): Promise<ValidationIssue[]> {
  const issues: ValidationIssue[] = [];
  const ox = path.join(root, '.ox-agent');
  for (const [name, relative] of [['config', 'config.yaml'], ['policy', 'policy.yaml']] as const) {
    try { issues.push(...await validateDocument(name, path.join(ox, relative))); }
    catch (error) { issues.push({ file: path.join(ox, relative), message: error instanceof Error ? error.message : String(error) }); }
  }
  const skillsDir = oxPath(root, 'skills');
  for (const skill of await listDirectories(skillsDir)) {
    const manifest = path.join(skillsDir, skill, 'skill.yaml');
    try { issues.push(...await validateDocument('skill-manifest', manifest)); }
    catch (error) { issues.push({ file: manifest, message: error instanceof Error ? error.message : String(error) }); }
    const evaluation = path.join(skillsDir, skill, 'evals', 'evals.yaml');
    try { issues.push(...await validateDocument('evaluation', evaluation)); }
    catch (error) { issues.push({ file: evaluation, message: error instanceof Error ? error.message : String(error) }); }
  }
  for (const workState of ['active', 'archived']) {
    for (const item of await listDirectories(path.join(ox, 'work', workState))) {
      const discussionRoot = path.join(ox, 'work', workState, item, 'discussions');
      for (const thread of await listDirectories(discussionRoot)) {
        const summary = path.join(discussionRoot, thread, 'summary.yaml');
        try { issues.push(...await validateDocument('discussion', summary)); }
        catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ENOENT') issues.push({ file: summary, message: error instanceof Error ? error.message : String(error) }); }
      }
    }
  }
  return issues;
}

export async function readConfig(root: string): Promise<Record<string, unknown>> { return readYaml<Record<string, unknown>>(oxPath(root, 'config.yaml')); }
export function parseFrontmatter(content: string): Record<string, unknown> | undefined {
  const match = content.match(/^---\s*\n([\s\S]*?)\n---\s*(?:\n|$)/);
  if (!match) return undefined;
  const value: unknown = parse(match[1]);
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
}
