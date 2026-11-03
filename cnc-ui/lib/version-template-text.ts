import { componentOf, componentTypes, createVersionCheckID, normalizeVersionChecks, type RuleDraft } from "./version-rules";
import { validateTemplateDraft } from "./version-templates";

export const TEMPLATE_TEXT_LIMIT = 4 * 1024 * 1024;
export type TextTemplate = { name: string; checks: RuleDraft[] };
function labelText(value: string): string {
  return /[\r\n]|^"/.test(value) ? JSON.stringify(value) : value;
}
function parseLabel(value: string): string {
  if (!value.startsWith('"')) return value.trim();
  try { const result: unknown = JSON.parse(value); if (typeof result === "string") return result; } catch { /* Report a format error below. */ }
  throw new Error('Use plain text or a quoted name such as "My check".');
}
function draftForComponent(component: string): Pick<RuleDraft, "target" | "identifier" | "group"> {
  if (/^[a-z0-9_-]+$/.test(component)) return { target: "wordpress:plugin", identifier: component, group: "" };
  const parts = component.split(":"); const target = parts.slice(0, 2).join(":");
  if (!componentTypes.includes(target)) throw new Error("Use a plugin folder name or a supported CMS component identifier.");
  if ((target === "php:php-src" || target.endsWith(":core")) && parts.length === 2) return { target, identifier: "", group: "" };
  if (target === "joomla:plugin" && parts.length === 4) return { target, group: parts[2], identifier: parts[3] };
  if (!target.endsWith(":core") && target !== "joomla:plugin" && parts.length === 3) return { target, identifier: parts[2], group: "" };
  throw new Error("The component identifier is incomplete or has extra fields.");
}
export function parseTemplateText(text: string, filename = "Uploaded template.txt", existing: RuleDraft[] = []): TextTemplate {
  if (new TextEncoder().encode(text).byteLength > TEMPLATE_TEXT_LIMIT) throw new Error("Template files must be at most 4 MiB.");
  let name = filename.replace(/\.txt$/i, "").trim() || "Uploaded template"; let header = false;
  const checks: RuleDraft[] = [];
  const ids = new Set(existing.map(check => check.id));
  const seenNames = new Set<string>(), seenChecks = new Set<string>();
  const lines = text.replace(/^\uFEFF/, "").split(/\r\n|\n|\r/);
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i].trim(); if (!line) continue;
    try {
      if (/^#\s*template:/i.test(line)) {
        if (header) throw new Error("Use only one # template: name header.");
        name = parseLabel(line.replace(/^#\s*template:\s*/i, "")); header = true;
        if (!name.trim()) throw new Error("Template name cannot be empty.");
        continue;
      }
      if (line.startsWith("#")) continue;
      const separator = line.indexOf("|");
      const condition = separator < 0 ? line : line.slice(0, separator).trim();
      const custom = separator < 0 ? "" : parseLabel(line.slice(separator + 1).trim());
      if (separator >= 0 && !custom.trim()) throw new Error("Enter a custom name after |, or remove | for automatic naming.");
      const match = /^(\S+)\s+([<>=].*)$/.exec(condition);
      if (!match) throw new Error("Expected plugin < version, for example elementor < 3.25.0.");
      const clauses = match[2].split("&").map(raw => {
        const clause = /^\s*(<=|>=|=|<|>)\s*([^\s&]+)\s*$/.exec(raw);
        if (!clause) throw new Error("Use =, <, <=, >, or >= followed by a version; join conditions with &.");
        return { operator: clause[1], version: clause[2] };
      });
      if (clauses.length > 32) throw new Error("Each check supports at most 32 conditions.");
      let id = createVersionCheckID([]);
      while (ids.has(id)) id = createVersionCheckID([]);
      ids.add(id);
      const draft: RuleDraft = { id, name: custom, ...draftForComponent(match[1]), clauses };
      const normalized = normalizeVersionChecks([draft])[0];
      const checkKey = normalized.component + JSON.stringify(normalized.clauses);
      if (seenChecks.has(checkKey)) throw new Error("Duplicate component and clause set.");
      if (seenNames.has(normalized.name)) throw new Error("Check names must be unique.");
      seenChecks.add(checkKey); seenNames.add(normalized.name);
      checks.push(draft);
    } catch (error) { throw new Error(`Line ${i + 1}: ${(error as Error).message}`); }
  }
  validateTemplateDraft(name, checks);
  return { name, checks };
}
export function serializeTemplateText(name: string, checks: RuleDraft[]): string {
  validateTemplateDraft(name, checks);
  const lines = checks.map(check => {
    const component = check.target === "wordpress:plugin" ? check.identifier : componentOf(check);
    const conditions = check.clauses.map(c => `${c.operator} ${c.version}`).join(" & ");
    return `${component} ${conditions}${check.name.trim() ? " | " + labelText(check.name) : ""}`;
  });
  const text = [`# template: ${labelText(name.trim())}`, ...lines, ""].join("\n");
  if (new TextEncoder().encode(text).byteLength > TEMPLATE_TEXT_LIMIT) throw new Error("Template files must be at most 4 MiB.");
  return text;
}
export async function readTemplateTextFile(file: Pick<File, "name" | "size" | "arrayBuffer">, existing: RuleDraft[]): Promise<TextTemplate> {
  if (!/\.txt$/i.test(file.name)) throw new Error("Choose a .txt template file.");
  if (file.size > TEMPLATE_TEXT_LIMIT) throw new Error("Template files must be at most 4 MiB.");
  let text: string;
  try { text = new TextDecoder("utf-8", { fatal: true }).decode(await file.arrayBuffer()); }
  catch { throw new Error("Could not read this file as UTF-8 text."); }
  return parseTemplateText(text, file.name, existing);
}
export function templateTextFilename(name: string): string {
  return (name.normalize("NFKC").replace(/[^a-zA-Z0-9_-]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 100) || "version-check-template") + ".txt";
}
