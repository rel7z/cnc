export type Clause = { operator: string; version: string };
export type VersionRule = { id: string; name: string; component: string; clauses: Clause[] };
export type RuleDraft = { id: string; name: string; target: string; identifier: string; group: string; clauses: Clause[] };
export const componentTypes = ["php:php-src", "wordpress:core", "wordpress:plugin", "joomla:core", "joomla:component", "joomla:module", "joomla:plugin", "joomla:library", "joomla:template", "joomla:package"];
export const operators = ["=", "<", "<=", ">", ">="];

type Version = { core: string[]; suffix: string; pre: string[] };
function parse(raw: string): Version {
  const value = raw.startsWith("v") ? raw.slice(1) : raw;
  const dash = value.indexOf("-");
  const core = (dash < 0 ? value : value.slice(0, dash)).split(".");
  const pre = dash < 0 ? [] : value.slice(dash + 1).split(".");
  let suffix = "";
  if (core.length === 3) {
    const match = /^(.*?)([A-Za-z]+)$/.exec(core[2]);
    if (match) { core[2] = match[1]; suffix = match[2].toLowerCase(); }
  }
  if (core.length < 1 || core.length > 4 || core.some(n => !/^(0|[1-9][0-9]*)$/.test(n)) || (dash >= 0 && core.length < 3) || (suffix && dash >= 0) || pre.some(n => !/^[A-Za-z0-9-]+$/.test(n) || /^0[0-9]+$/.test(n))) throw new Error(`Invalid version: ${raw}`);
  while (core.length < 4) core.push("0");
  return { core, suffix, pre };
}
export function normalizeVersion(raw: string): string { const v = parse(raw); return v.core.slice(0,v.core[3] === "0" ? 3 : 4).join(".") + v.suffix + (v.pre.length ? "-" + v.pre.join(".") : ""); }
function numericCompare(a: string, b: string): number { return a.length !== b.length ? Math.sign(a.length - b.length) : a < b ? -1 : a > b ? 1 : 0; }
function compare(a: Version, b: Version): number {
  for (let i = 0; i < 4; i++) { const n = numericCompare(a.core[i], b.core[i]); if (n) return n; }
  if (a.suffix !== b.suffix) return !a.suffix ? -1 : !b.suffix ? 1 : a.suffix < b.suffix ? -1 : 1;
  if (!a.pre.length || !b.pre.length) return !a.pre.length && !b.pre.length ? 0 : !a.pre.length ? 1 : -1;
  for (let i = 0; i < Math.min(a.pre.length, b.pre.length); i++) {
    const x = a.pre[i], y = b.pre[i], xn = /^[0-9]+$/.test(x), yn = /^[0-9]+$/.test(y);
    const n = xn && yn ? numericCompare(x, y) : xn !== yn ? xn ? -1 : 1 : x < y ? -1 : x > y ? 1 : 0;
    if (n) return n;
  }
  return Math.sign(a.pre.length - b.pre.length);
}
function matches(v: Version, c: Clause): boolean { const n = compare(v, parse(c.version)); return c.operator === "=" ? n === 0 : c.operator === "<" ? n < 0 : c.operator === "<=" ? n <= 0 : c.operator === ">" ? n > 0 : c.operator === ">=" && n >= 0; }
function consistent(clauses: Clause[]): boolean {
  let lower: Version | undefined, upper: Version | undefined, lowOpen = false, upOpen = false;
  for (const c of clauses) {
    const v = parse(c.version);
    if (c.operator === "=") return clauses.every(other => matches(v, other));
    if (c.operator.startsWith(">")) { if (!lower || compare(v, lower) > 0) { lower = v; lowOpen = c.operator === ">"; } else if (!compare(v, lower)) lowOpen ||= c.operator === ">"; }
    else { if (!upper || compare(v, upper) < 0) { upper = v; upOpen = c.operator === "<"; } else if (!compare(v, upper)) upOpen ||= c.operator === "<"; }
  }
  if (!upper) return true;
  let minimum = lower || parse("0.0.0-0");
  if (lower && lowOpen) minimum = lower.pre.length ? { ...lower, pre: [...lower.pre, "0"] } : lower.suffix ? { ...lower, suffix: lower.suffix + "a" } : { core: [lower.core[0], lower.core[1], lower.core[2], (BigInt(lower.core[3]) + BigInt(1)).toString()], suffix:"", pre: ["0"] };
  const n = compare(minimum, upper); return n < 0 || (n === 0 && !upOpen);
}
export function componentOf(draft: RuleDraft): string {
  if (!componentTypes.includes(draft.target)) throw new Error("Choose a supported component type");
  if (draft.target === "php:php-src" || draft.target.endsWith(":core")) return draft.target;
  if (!/^[a-z0-9_-]+$/.test(draft.identifier)) throw new Error("Component names require lowercase letters, digits, underscores, or hyphens");
  const prefix: Record<string, string> = { "joomla:component": "com_", "joomla:module": "mod_", "joomla:plugin": "plg_", "joomla:library": "lib_", "joomla:template": "tpl_", "joomla:package": "pkg_" };
  if (prefix[draft.target] && draft.identifier.startsWith(prefix[draft.target])) throw new Error("Enter the component name without its type prefix");
  if (draft.target === "joomla:plugin") { if (!/^[a-z0-9_-]+$/.test(draft.group)) throw new Error("Enter a lowercase Joomla plugin group"); return `${draft.target}:${draft.group}:${draft.identifier}`; }
  return `${draft.target}:${draft.identifier}`;
}
export function normalizeRules(drafts: RuleDraft[]): VersionRule[] {
  const ids = new Set<string>(), names = new Set<string>(), sets = new Set<string>();
  return drafts.map(draft => {
    if (!draft.id.trim() || !draft.name.trim() || ids.has(draft.id) || names.has(draft.name)) throw new Error("Rule IDs and names must be nonempty and unique");
    const component = componentOf(draft);
    if (!draft.clauses.length) throw new Error("Each rule needs at least one clause");
    const clauses = draft.clauses.map(c => { if (!operators.includes(c.operator)) throw new Error("Invalid comparison operator"); return { operator: c.operator, version: normalizeVersion(c.version) }; });
    const unique = [...new Map(clauses.map(c => [c.operator + " " + c.version, c])).values()].sort((a, b) => a.operator < b.operator ? -1 : a.operator > b.operator ? 1 : a.version < b.version ? -1 : a.version > b.version ? 1 : 0);
    if (!consistent(unique)) throw new Error(`Rule ${draft.name} has contradictory clauses`);
    const key = component + JSON.stringify(unique);
    if (sets.has(key)) throw new Error("Duplicate component and clause set");
    ids.add(draft.id); names.add(draft.name); sets.add(key);
    return { id: draft.id, name: draft.name, component, clauses: unique };
  }).sort((a, b) => a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
}

export const comparisonChoices = [
  { value: "any", label: "Any detected version" }, { value: "=", label: "Exactly" }, { value: "<", label: "Older than" },
  { value: "<=", label: "At most" }, { value: ">", label: "Newer than" },
  { value: ">=", label: "At least" }, { value: "between", label: "Between" },
];
export const componentLabels: Record<string, string> = {
  "php-src": "PHP runtime", core: "Core", plugin: "Plugin", component: "Component", module: "Module",
  library: "Library", template: "Template", package: "Package",
};
let checkIDSequence = 0;
// These IDs identify editable checks, not credentials. Public HTTP dashboards
// may lack randomUUID, so adding a check must not depend on a secure context.
export function createVersionCheckID(existing: readonly Pick<RuleDraft, "id">[] = [], source: { randomUUID?: () => string } | null = globalThis.crypto ?? null): string {
  const used = new Set(existing.map(rule => rule.id));
  if (typeof source?.randomUUID === "function") {
    try {
      const id = "rule-" + source.randomUUID();
      if (!used.has(id)) return id;
    } catch { /* Fall back when a browser exposes an unusable implementation. */ }
  }
  let id: string;
  do { id = `rule-${Date.now().toString(36)}-${(++checkIDSequence).toString(36)}`; } while (used.has(id));
  return id;
}

export function newVersionCheck(id: string): RuleDraft {
  return { id, name: "", target: "wordpress:plugin", identifier: "", group: "", clauses: [{ operator: "=", version: "" }] };
}
export function newWordPressCoreCheck(id: string): RuleDraft {
  return { ...newVersionCheck(id), target: "wordpress:core", clauses: [{ operator: ">=", version: "0.0.0-0" }] };
}
export function newPHPVersionCheck(id: string): RuleDraft {
 return { ...newWordPressCoreCheck(id), target: "php:php-src" };
}
export function comparisonOf(rule: RuleDraft): string {
  if (rule.clauses.length === 1 && rule.clauses[0].operator === ">=" && rule.clauses[0].version === "0.0.0-0") return "any";
  if (rule.clauses.length === 1 && operators.includes(rule.clauses[0].operator)) return rule.clauses[0].operator;
  if (rule.clauses.length === 2 && rule.clauses[0].operator === ">=" && rule.clauses[1].operator === "<=") return "between";
  return "custom";
}
export function clausesForComparison(rule: RuleDraft, comparison: string): Clause[] {
  if (comparison === "custom") return rule.clauses;
  if (comparison === "any") return [{ operator: ">=", version: "0.0.0-0" }];
  const first = comparisonOf(rule) === "any" ? "" : rule.clauses[0]?.version || "";
  return comparison === "between" ? [{ operator: ">=", version: first }, { operator: "<=", version: rule.clauses[1]?.version || "" }] : [{ operator: comparison, version: first }];
}
export function versionCheckSummary(rule: RuleDraft): string {
  const [cms, type] = rule.target.split(":");
  const subject = rule.target === "php:php-src" ? "PHP runtime" : `${cms === "wordpress" ? "WordPress" : "Joomla"} ${(componentLabels[type] || type).toLowerCase()}${type === "core" ? "" : " " + (rule.identifier || "…")}${rule.target === "joomla:plugin" ? " (" + (rule.group || "group") + ")" : ""}`;
  if (comparisonOf(rule) === "any") return `${subject}, any detected version`;
  if (comparisonOf(rule) === "between") return `${subject}, between ${rule.clauses[0].version || "…"} and ${rule.clauses[1].version || "…"} (inclusive)`;
  const conditions = rule.clauses.map(c => `${comparisonChoices.find(option => option.value === c.operator)?.label.toLowerCase() || c.operator} ${c.version || "…"}`).join(" and ");
  return `${subject}, ${conditions || "no version conditions"}`;
}
function namedCheck(rule: RuleDraft): RuleDraft {
  return { ...rule, name: rule.name.trim() || versionCheckSummary(rule) };
}
export type CheckErrors = Record<string, string>;
export function validateVersionChecks(rules: RuleDraft[]): CheckErrors[] {
  const errors: CheckErrors[] = rules.map(rule => {
    const error: CheckErrors = {};
    if (!componentTypes.includes(rule.target)) error.component = "Choose a CMS and component type.";
    else if (rule.target !== "php:php-src" && !rule.target.endsWith(":core")) {
      if (!rule.identifier) error.identifier = `Enter the installed folder name, for example ${rule.target.startsWith("wordpress:") ? "elementor" : "example"}.`;
      else {
        try { componentOf({ ...rule, group: "system" }); }
        catch (e) { error.identifier = (e as Error).message; }
      }
      if (rule.target === "joomla:plugin" && !/^[a-z0-9_-]+$/.test(rule.group)) error.group = "Enter the plugin group, for example system or content.";
    }
    if (!rule.clauses.length) error.conditions = "Add at least one version condition.";
    rule.clauses.forEach((clause, i) => {
      if (!operators.includes(clause.operator)) error[`operator-${i}`] = "Choose a comparison.";
      try { normalizeVersion(clause.version); }
      catch { error[`version-${i}`] = clause.version ? "Use a version like 3.25.0, 3.25.0.1, or 2.6.8c; wildcards and build metadata are not supported." : "Enter a version, for example 3.25.0."; }
    });
    if (!Object.keys(error).length) {
      try { normalizeRules([namedCheck(rule)]); }
      catch { error.conditions = "These conditions cannot match a version. Check the range boundaries."; }
    }
    return error;
  });
  const seenNames = new Map<string, number>(), seenChecks = new Map<string, number>();
  rules.forEach((rule, i) => {
    if (Object.keys(errors[i]).length) return;
    const normalized = normalizeRules([namedCheck(rule)])[0];
    const key = normalized.component + JSON.stringify(normalized.clauses);
    const duplicate = seenChecks.get(key);
    if (duplicate !== undefined) {
      errors[i].duplicate = `This repeats version check ${duplicate + 1}. Change the conditions or remove this check.`;
    } else if (seenNames.has(normalized.name)) {
      errors[i].name = "Another check uses this name. Choose a different custom name under Advanced.";
    }
    seenChecks.set(key, i); seenNames.set(normalized.name, i);
  });
  return errors;
}
export function normalizeVersionChecks(rules: RuleDraft[]): VersionRule[] {
  const errors = validateVersionChecks(rules);
  const index = errors.findIndex(error => Object.keys(error).length);
  if (index >= 0) throw new Error(`Version check ${index + 1}: ${Object.values(errors[index])[0]}`);
  return normalizeRules(rules.map(namedCheck));
}
