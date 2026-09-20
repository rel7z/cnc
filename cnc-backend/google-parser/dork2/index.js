#!/usr/bin/env node

// Set Apify log level to ERROR only (1) to suppress verbose info logs
process.env.APIFY_LOG_LEVEL = '1';

// Redirect console.log / info / warn to stderr so stdout is exclusively reserved for CNC domain streaming
console.log = console.error;
console.info = console.error;
console.warn = console.error;

import { ApifyClient } from 'apify-client';
import fs from 'fs';
import path from 'path';

const DEFAULT_API_TOKEN = "apify_api_87e0keim4LdlPQcNn2p3c3ROlyEAJN2k2cZL";

const args = process.argv.slice(2);

let help = false;
for (const arg of args) {
    if (arg === '-h' || arg === '--help') help = true;
}

if (help) {
    console.error('Usage: dork2 [options]');
    console.error('');
    console.error('Options:');
    console.error('  -h, --help              Show this help message');
    console.error('  -f, --file <file>       Path to queries .txt file (one per line)');
    console.error('  -o, --out <file>        Output file for discovered domains');
    console.error('  -k <query>              Single search query');
    console.error('  -p, --pages <n>         Max pages per query (default: 5)');
    console.error('  -c, --country <code>    Country code: us, uk, ca, de, etc (default: us)');
    console.error('  -l, --lang <code>       Language code: en, de, fr, es, etc (default: en)');
    console.error('  -m, --mobile            Enable mobile results (default: false)');
    console.error('  -s, --site <domain>     Limit results to specific site');
    console.error('  --token <token>         Apify API token override');
    console.error('  -ai                     Enable AI overview (Gemini) (default: false)');
    process.exit(0);
}

let queries = [];
let outputFile = 'domains.txt';
let maxPages = 5;
let countryCode = 'us';
let searchLanguage = 'en';
let languageCode = 'en';
let mobileResults = false;
let enableAiOverview = false;
let siteFilter = '';
let apiToken = process.env.APIFY_TOKEN || DEFAULT_API_TOKEN;

for (let i = 0; i < args.length; i++) {
    if (args[i] === '-f' || args[i] === '--file') {
        const filePath = args[++i];
        if (filePath && fs.existsSync(filePath)) {
            const data = fs.readFileSync(filePath, 'utf-8');
            queries = data.split('\n').map(q => q.trim()).filter(q => q && !q.startsWith('#'));
        }
    } else if (args[i] === '-o' || args[i] === '-out' || args[i] === '--out' || args[i] === '--output') {
        outputFile = args[++i] || outputFile;
    } else if (args[i] === '-k') {
        const q = args[++i];
        if (q) queries = [q];
    } else if (args[i] === '-p' || args[i] === '--pages') {
        maxPages = parseInt(args[++i], 10) || 5;
    } else if (args[i] === '-c' || args[i] === '--country') {
        countryCode = (args[++i] || 'us').toLowerCase();
    } else if (args[i] === '-l' || args[i] === '--lang') {
        searchLanguage = (args[++i] || 'en').toLowerCase();
        languageCode = searchLanguage;
    } else if (args[i] === '-m' || args[i] === '--mobile') {
        mobileResults = true;
    } else if (args[i] === '-s' || args[i] === '--site') {
        siteFilter = args[++i] || '';
    } else if (args[i] === '-ai') {
        enableAiOverview = true;
    } else if (args[i] === '--token' || args[i] === '-token') {
        apiToken = args[++i] || apiToken;
    } else if (!queries.length && fs.existsSync(args[i])) {
        // Positional argument for file
        const data = fs.readFileSync(args[i], 'utf-8');
        queries = data.split('\n').map(q => q.trim()).filter(q => q && !q.startsWith('#'));
    }
}

if (!apiToken || apiToken === '<YOUR_API_TOKEN>') {
    console.error('[-] Error: Please set APIFY_TOKEN environment variable or pass --token <token>');
    process.exit(1);
}

if (queries.length === 0) {
    console.error('[-] Error: Please specify queries via -f <file> or -k <query>');
    process.exit(1);
}

const client = new ApifyClient({
    token: apiToken,
    timeoutSecs: 600,
});

// Ensure parent directory exists if outputFile is in a subfolder
if (outputFile && path.dirname(outputFile) !== '.') {
    try {
        fs.mkdirSync(path.dirname(outputFile), { recursive: true });
    } catch (e) {}
}

// Create write streams for output files
const domainsStream = fs.createWriteStream(outputFile, { flags: 'a' });
const urlsStream = fs.createWriteStream('urls.txt', { flags: 'a' });

// Track unique domains and URLs
const seenDomains = new Set();
const seenUrls = new Set();

let totalDomains = 0;
let totalUrls = 0;

// Function to extract hostname safely
function getHostname(link) {
    try {
        if (!link || typeof link !== 'string') return null;
        const url = new URL(link.trim());
        return url.hostname.toLowerCase();
    } catch (e) {
        return null;
    }
}

// Function to save domain in real-time
function saveDomain(domain) {
    if (domain && !seenDomains.has(domain)) {
        seenDomains.add(domain);
        domainsStream.write(domain + '\n');
        // Stream clean domain directly to stdout for CNC capture
        process.stdout.write(domain + '\n');
        totalDomains++;
        process.stderr.write(`\r[*] Discovered: ${totalDomains} domains | ${totalUrls} URLs`);
    }
}

// Function to save URL in real-time
function saveUrl(url) {
    if (url && !seenUrls.has(url) && typeof url === 'string') {
        seenUrls.add(url);
        urlsStream.write(url + '\n');
        totalUrls++;
        process.stderr.write(`\r[*] Discovered: ${totalDomains} domains | ${totalUrls} URLs`);
    }
}

(async () => {
    try {
        console.error(`[*] Starting Google Search scraping for ${queries.length} queries...`);

        for (const query of queries) {
            console.error(`\n[*] Processing query: "${query}"`);

            try {
                // Build the search query with site filter if provided
                let searchQuery = query;
                if (siteFilter) {
                    searchQuery = `${query} site:${siteFilter}`;
                }

                // Prepare Actor input
                const input = {
                    queries: searchQuery,
                    maxPagesPerQuery: maxPages,
                    countryCode: countryCode,
                    searchLanguage: searchLanguage,
                    languageCode: languageCode,
                    mobileResults: mobileResults,
                    focusOnPaidAds: false,
                    includeUnfilteredResults: true,
                    aiOverview: {
                        scrapeFullAiOverview: enableAiOverview
                    },
                    aiModeSearch: {
                        enableAiMode: false
                    },
                    geminiSearch: {
                        enableGemini: enableAiOverview
                    },
                    perplexitySearch: {
                        enablePerplexity: false
                    },
                    chatGptSearch: {
                        enableChatGpt: false
                    },
                    copilotSearch: {
                        enableCopilot: false
                    },
                    websiteContentScraper: {
                        enable: false
                    },
                    saveHtml: false,
                    saveHtmlToKeyValueStore: false,
                    includeIcons: false
                };

                // Run the Actor
                const run = await client.actor('nFJndFXA5zjCTuudP').call(input);
                const { items } = await client.dataset(run.defaultDatasetId).listItems();

                if (items.length === 0) {
                    console.error('\n[!] No results found for this query.');
                    continue;
                }

                // Process results
                items.forEach((item) => {
                    // Handle regular search results
                    if (item.organicResults && Array.isArray(item.organicResults)) {
                        item.organicResults.forEach((result) => {
                            if (result.url) {
                                saveUrl(result.url);
                                const domain = getHostname(result.url);
                                if (domain) {
                                    saveDomain(domain);
                                }
                            }
                        });
                    }

                    // Handle paid ads results
                    if (item.paidResults && Array.isArray(item.paidResults)) {
                        item.paidResults.forEach((result) => {
                            if (result.url) {
                                saveUrl(result.url);
                                const domain = getHostname(result.url);
                                if (domain) {
                                    saveDomain(domain);
                                }
                            }
                        });
                    }

                    // Handle news results
                    if (item.newsResults && Array.isArray(item.newsResults)) {
                        item.newsResults.forEach((result) => {
                            if (result.url) {
                                saveUrl(result.url);
                                const domain = getHostname(result.url);
                                if (domain) {
                                    saveDomain(domain);
                                }
                            }
                        });
                    }

                    // Handle knowledge panel results
                    if (item.knowledgePanel && item.knowledgePanel.url) {
                        const url = item.knowledgePanel.url;
                        saveUrl(url);
                        const domain = getHostname(url);
                        if (domain) {
                            saveDomain(domain);
                        }
                    }

                    // Handle related searches
                    if (item.relatedSearches && Array.isArray(item.relatedSearches)) {
                        item.relatedSearches.forEach((search) => {
                            if (search.url) {
                                saveUrl(search.url);
                                const domain = getHostname(search.url);
                                if (domain) {
                                    saveDomain(domain);
                                }
                            }
                        });
                    }
                });

            } catch (queryError) {
                console.error(`\n[-] Query error for "${query}":`, queryError.message);
                continue;
            }
        }

        // Close streams and wait for flush
        await Promise.all([
            new Promise((resolve) => domainsStream.end(resolve)),
            new Promise((resolve) => urlsStream.end(resolve)),
        ]);

        console.error(`\n[+] Done. ${totalDomains} unique domains discovered.`);
        process.exit(0);
    } catch (err) {
        console.error('\n[-] Fatal error:', err.message);
        domainsStream.destroy();
        urlsStream.destroy();
        process.exit(1);
    }
})();