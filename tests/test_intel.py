import sqlite3
import unittest
from intel import (
    init_intel_tables,
    extract_title,
    detect_signatures,
    extract_snippets,
    index_page,
    search_intel,
)


class IntelTests(unittest.TestCase):
    def setUp(self):
        self.db = sqlite3.connect(":memory:")
        self.db.execute("PRAGMA foreign_keys=ON")
        self.db.executescript("""
            CREATE TABLE IF NOT EXISTS sites (
                url TEXT PRIMARY KEY, host TEXT NOT NULL
            );
        """)
        init_intel_tables(self.db)

    def tearDown(self):
        self.db.close()

    def test_extract_title(self):
        html = "<html><head><title>  Example Page &amp; Portal  </title></head><body><h1>Hello</h1></body></html>"
        self.assertEqual(extract_title(html), "Example Page & Portal")
        self.assertEqual(extract_title("<html><body>No title here</body></html>"), "")

    def test_detect_signatures(self):
        html = """
        <html>
        <head>
            <meta name="generator" content="WordPress 6.4.2" />
            <link rel="stylesheet" href="/wp-content/themes/theme/style.css" />
            <script>
                gtag('config', 'G-12345ABC');
                (adsbygoogle = window.adsbygoogle || []).push({});
            </script>
        </head>
        <body class="bg-gray-100 tailwindcss">
        </body>
        </html>
        """
        sigs = detect_signatures(html)
        self.assertIn("WordPress", sigs)
        self.assertIn("Google Analytics", sigs)
        self.assertIn("Google AdSense", sigs)
        self.assertIn("Tailwind CSS", sigs)

    def test_extract_snippets(self):
        html = (
            "<!DOCTYPE html>\n"
            "<html>\n"
            "<head>\n"
            "  <script src=\"https://www.googletagmanager.com/gtag/js?id=UA-99999-1\"></script>\n"
            "  <script>\n"
            "    gtag('config', 'UA-99999-1');\n"
            "  </script>\n"
            "</head>\n"
            "<body>\n"
            "  <h1>PublicWWW Intel</h1>\n"
            "</body>\n"
            "</html>"
        )
        res = extract_snippets(html, "gtag('config'")
        self.assertEqual(res["match_count"], 1)
        self.assertEqual(len(res["snippets"]), 1)
        snip = res["snippets"][0]
        self.assertEqual(snip["line_number"], 6)
        self.assertIn("gtag('config', 'UA-99999-1');", snip["matched_line"])
        # Check context lines
        ctx = snip["context"]
        self.assertTrue(any(c["line_number"] == 5 for c in ctx))
        self.assertTrue(any(c["line_number"] == 6 and c["is_match"] for c in ctx))
        self.assertTrue(any(c["line_number"] == 7 for c in ctx))

    def test_index_and_search_intel(self):
        html1 = """
        <html>
        <head><title>WordPress Tech Blog</title></head>
        <body>
            <link rel="stylesheet" href="https://blog.org/wp-content/themes/twentytwenty/style.css">
            <script>gtag('config', 'UA-11111-1');</script>
        </body>
        </html>
        """
        html2 = """
        <html>
        <head><title>E-commerce Store</title></head>
        <body>
            <script src="https://cdn.shopify.com/s/files/store.js"></script>
            <script>(adsbygoogle = window.adsbygoogle || []).push({});</script>
        </body>
        </html>
        """
        index_page(self.db, "https://blog.org/", html1, status_code=200)
        index_page(self.db, "https://store.org/", html2, status_code=200)

        # Search for WordPress marker
        res = search_intel(self.db, "wp-content")
        self.assertEqual(res["total"], 1)
        self.assertEqual(res["items"][0]["url"], "https://blog.org/")
        self.assertEqual(res["items"][0]["title"], "WordPress Tech Blog")
        self.assertIn("WordPress", res["items"][0]["signatures"])
        self.assertTrue(len(res["items"][0]["snippets"]) > 0)

        # Search for AdSense marker
        res_ads = search_intel(self.db, "adsbygoogle")
        self.assertEqual(res_ads["total"], 1)
        self.assertEqual(res_ads["items"][0]["url"], "https://store.org/")
        self.assertIn("Google AdSense", res_ads["items"][0]["signatures"])

        # Search for non-existent keyword
        res_empty = search_intel(self.db, "nonexistentkeyword999")
        self.assertEqual(res_empty["total"], 0)
        self.assertEqual(len(res_empty["items"]), 0)


if __name__ == "__main__":
    unittest.main()
