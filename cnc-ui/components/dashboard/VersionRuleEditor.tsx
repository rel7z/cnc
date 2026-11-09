"use client";

import { componentTypes, componentLabels, comparisonChoices, comparisonOf, clausesForComparison, newVersionCheck, newWordPressCoreCheck, newPHPVersionCheck, createVersionCheckID, validateVersionChecks, versionCheckSummary, type RuleDraft } from "@/lib/version-rules";

import { type ReactNode } from "react";

const field = "w-full rounded-lg border border-gray-700 bg-gray-950 px-3 py-2 text-sm text-white";
type Props = { templates?: ReactNode; rules: RuleDraft[]; onRules: (rules: RuleDraft[]) => void };
export function VersionRuleEditor({ rules, onRules, templates }: Props) {
  const update = (index: number, patch: Partial<RuleDraft>) => onRules(rules.map((rule, i) => i === index ? { ...rule, ...patch } : rule));
  const errors = validateVersionChecks(rules);
  return <div className="space-y-5 rounded-xl border border-gray-700 p-4">
    <div className="flex items-center justify-between gap-3">
      <h3 className="font-semibold text-white">Version checks</h3>
      <button type="button" className="rounded-lg bg-blue-600 px-3 py-2 text-sm text-white" onClick={() => onRules([...rules, newVersionCheck(createVersionCheckID(rules))])}>Add version check</button>
    </div>
    <p className="text-sm text-gray-400">Check PHP runtime, WordPress core, plugins, or Joomla extensions and choose the versions you want to find. Matching disclosed versions are published; PHP banners do not prove patch or vulnerability status. Leave this section empty for ordinary CMS and admin discovery.</p>
    <button type="button" className="rounded-lg border border-blue-500 px-3 py-2 text-sm text-blue-200" onClick={() => onRules([...rules, newWordPressCoreCheck(createVersionCheckID(rules))])}>Scan WordPress core versions</button>
    <p className="text-xs text-gray-400">The core scanner reports any detected WordPress version by default. Choose a comparison to filter versions. Hidden or conflicting versions are not reported as confirmed matches.</p>
    <button type="button" className="rounded-lg border border-blue-500 px-3 py-2 text-sm text-blue-200" onClick={() => onRules([...rules, newPHPVersionCheck(createVersionCheckID(rules))])}>Scan PHP versions</button>
    <p className="text-xs text-gray-400">PHP scanning works independently of CMS detection. It reads the server's disclosed runtime version; hidden or ambiguous versions stay unknown.</p>
    {templates}
    {rules.length > 0 && <button type="button" className="text-sm text-amber-300 underline" onClick={() => onRules([])}>Clear all version checks</button>}
    <details open={rules.length <= 20 || errors.some(error => Object.keys(error).length > 0)}>
      <summary className="cursor-pointer text-sm text-gray-300">{rules.length} version checks {rules.length > 20 ? "— expand to edit" : ""}</summary>
    {rules.map((rule, i) => {
      const [cms, type] = rule.target.split(":");
      const comparison = comparisonOf(rule);
      const error = errors[i];
      const label = `Check ${i + 1}`;
      const message = (key: string) => error[key] ? <p id={`${rule.id}-${key}`} className="mt-1 text-sm text-red-400" role="alert">{error[key]}</p> : null;
      const invalid = (key: string) => ({ "aria-invalid": !!error[key], "aria-describedby": error[key] ? `${rule.id}-${key}` : undefined });
      const setVersion = (index: number, value: string) => update(i, { clauses: rule.clauses.map((c, k) => k === index ? { ...c, version: value } : c) });
      return <fieldset key={rule.id} className="space-y-4 rounded-lg border border-gray-700 bg-gray-950/30 p-4">
        <legend className="px-1 text-sm font-medium text-gray-200">Version check {i + 1}</legend>
        <div className="grid gap-4 sm:grid-cols-2">
          <label className="block text-sm text-gray-300">Technology
            <select aria-label={`${label} CMS`} className={field} value={cms} onChange={e => update(i, { target: e.target.value === "php" ? "php:php-src" : `${e.target.value}:${e.target.value === "wordpress" ? type === "core" ? "core" : "plugin" : type === "php-src" ? "core" : type}`, identifier: "", group: "" })}>
              <option value="php">PHP</option><option value="wordpress">WordPress</option><option value="joomla">Joomla</option>
            </select>
          </label>
          <label className="block text-sm text-gray-300">What to check
            <select aria-label={`${label} component type`} className={field} value={rule.target} {...invalid("component")} onChange={e => update(i, { target: e.target.value, identifier: "", group: "" })}>
              {componentTypes.filter(value => value.startsWith(cms + ":")).map(value => <option key={value} value={value}>{componentLabels[value.split(":")[1]]}</option>)}
            </select>{message("component")}
          </label>
        </div>
        {type !== "core" && rule.target !== "php:php-src" && <label className="block text-sm text-gray-300">{type === "plugin" ? "Plugin folder name" : `${componentLabels[type]} name`}
          <input aria-label={`${label} component name`} className={field} value={rule.identifier} {...invalid("identifier")} placeholder={cms === "wordpress" ? "elementor" : "example"} onChange={e => update(i, { identifier: e.target.value })} />
          <span className="mt-1 block text-xs text-gray-400">Use the installed folder name in lowercase, not the display title.{cms === "joomla" ? " Omit prefixes such as com_, mod_, and plg_." : " For example, Elementor uses elementor."}</span>{message("identifier")}
        </label>}
        {rule.target === "joomla:plugin" && <label className="block text-sm text-gray-300">Plugin group
          <input aria-label={`${label} plugin group`} className={field} value={rule.group} {...invalid("group")} placeholder="system" onChange={e => update(i, { group: e.target.value })} />
          <span className="mt-1 block text-xs text-gray-400">The folder containing the plugin, for example system or content.</span>{message("group")}
        </label>}
        <label className="block text-sm text-gray-300">Version comparison
          <select aria-label={`${label} comparison`} className={field} value={comparison} onChange={e => update(i, { clauses: clausesForComparison(rule, e.target.value) })}>
            {comparisonChoices.map(choice => <option key={choice.value} value={choice.value}>{choice.label}</option>)}
            {comparison === "custom" && <option value="custom">Custom conditions (advanced)</option>}
          </select>
        </label>
        {comparison !== "custom" && comparison !== "any" && <div className="grid gap-4 sm:grid-cols-2">
          {rule.clauses.map((clause, j) => <label key={j} className="block text-sm text-gray-300">{comparison === "between" ? j === 0 ? "Minimum version" : "Maximum version" : "Version"}
            <input aria-label={`${label} ${comparison === "between" ? j === 0 ? "minimum version" : "maximum version" : "version"}`} className={field} value={clause.version} placeholder="3.25.0" {...invalid(`version-${j}`)} onChange={e => setVersion(j, e.target.value)} />{message(`version-${j}`)}
          </label>)}
          {comparison === "between" && <p className="text-xs text-gray-400 sm:col-span-2">Both boundary versions are included. Use Advanced for exclusive boundaries.</p>}
        </div>}
        <p className="rounded-lg bg-blue-950/30 p-3 text-sm text-blue-200" aria-label={`${label} summary`}>{versionCheckSummary(rule)}</p>
        {message("conditions")}{message("duplicate")}
        {error.name && <p id={`${rule.id}-name`} className="text-sm text-red-400" role="alert">{error.name}</p>}
        <details className="rounded-lg border border-gray-800 p-3" open={comparison === "custom" || !!error.name ? true : undefined}>
          <summary className="cursor-pointer text-sm font-medium text-gray-300">Advanced</summary>
          <div className="mt-4 space-y-3">
            <label className="block text-sm text-gray-300">Custom check name (optional)
              <input aria-label={`${label} custom name`} className={field} value={rule.name} placeholder={versionCheckSummary(rule)} {...invalid("name")} onChange={e => update(i, { name: e.target.value })} />
              <span className="mt-1 block text-xs text-gray-400">Leave blank to generate a name from this check.</span>
            </label>
            <p className="text-xs text-gray-400">Every condition below must match. Opening or closing Advanced keeps your conditions unchanged.</p>
            {rule.clauses.map((clause, j) => <div key={j} className="space-y-1">
              <div className="flex flex-wrap items-center gap-2">
                <select aria-label={`${label} advanced condition ${j + 1} comparison`} className={field + " sm:max-w-44"} value={clause.operator} {...invalid(`operator-${j}`)} onChange={e => update(i, { clauses: rule.clauses.map((c, k) => k === j ? { ...c, operator: e.target.value } : c) })}>
                  {comparisonChoices.filter(choice => choice.value !== "between" && choice.value !== "any").map(choice => <option key={choice.value} value={choice.value}>{choice.label}</option>)}
                </select>
                <input aria-label={`${label} advanced condition ${j + 1} version`} className={field + " sm:max-w-52"} value={clause.version} placeholder="3.25.0" {...invalid(`version-${j}`)} onChange={e => setVersion(j, e.target.value)} />
                <button type="button" aria-label={`Remove ${label.toLowerCase()} condition ${j + 1}`} className="text-sm text-red-400" onClick={() => update(i, { clauses: rule.clauses.filter((_, k) => k !== j) })}>Remove condition</button>
              </div>
              {message(`operator-${j}`)}{comparison === "custom" && message(`version-${j}`)}
            </div>)}
            <button type="button" className="text-sm text-blue-400" onClick={() => update(i, { clauses: [...rule.clauses, { operator: "<", version: "" }] })}>Add condition</button>
          </div>
        </details>
        <button type="button" className="text-sm text-red-400" onClick={() => onRules(rules.filter((_, k) => k !== i))}>Remove version check</button>
      </fieldset>;
    })}
    </details>
  </div>;
}
