import { createVersionCheckID, normalizeVersionChecks, type RuleDraft } from "./version-rules";

export type VersionTemplate = {
  id: string; name: string; checks: RuleDraft[]; revision: number;
  created_at: string; updated_at: string; is_default?: boolean;
};
export function templateChecksKey(checks: RuleDraft[]): string {
  return JSON.stringify(checks.map(({ name, target, identifier, group, clauses }) => ({ name, target, identifier, group, clauses })));
}
export function validateTemplateDraft(name: string, checks: RuleDraft[]): void {
  if (!name.trim() || Array.from(name.trim()).length > 120) throw new Error("Enter a template name of up to 120 characters.");
  if (!checks.length) throw new Error("Add at least one valid version check before saving a template.");
  if (checks.some(c => c.clauses.length > 32)) throw new Error("Each check supports up to 32 conditions.");
  normalizeVersionChecks(checks);
}
export function loadTemplateChecks(template: VersionTemplate, existing: RuleDraft[]): RuleDraft[] {
  const checks: RuleDraft[] = [];
  for (const check of template.checks) checks.push({ ...check, id: createVersionCheckID([...existing, ...checks]), clauses: check.clauses.map(clause => ({ ...clause })) });
  return checks;
}
export async function templateRequest(path = "", method = "GET", body?: unknown): Promise<any> {
  const response = await fetch("/api/version-check-templates" + path, {
    method, cache: "no-store", headers: { "Content-Type": "application/json" },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
  const data = await response.json().catch(() => null);
  if (!response.ok) throw new Error(data?.error || "Template request failed. Your edits are still in the form.");
  return data;
}
