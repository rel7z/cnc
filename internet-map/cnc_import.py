#!/usr/bin/env python3
"""Import an existing CNC output through its authenticated REST API."""
import argparse
import os
from pathlib import Path
import tempfile
import urllib.parse
import urllib.request

from main import connect, ingest


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, message, headers, new_url):
        return None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--remote-file", required=True)
    parser.add_argument("--database", type=Path, default=Path("internet-map/data/catalog.db"))
    args = parser.parse_args()
    base = urllib.parse.urlsplit(args.base_url)
    if base.scheme != "https" and not (
        base.scheme == "http" and base.hostname in ("localhost", "127.0.0.1", "::1")
    ):
        parser.error("Use HTTPS or a localhost tunnel.")
    if base.username or base.password or base.query or base.fragment or base.path not in ("", "/"):
        parser.error("Supply a server origin without credentials, path, query or fragment.")
    key = os.environ.get("CNC_API_KEY", "")
    if not key or any(c in key for c in "\r\n"):
        parser.error("Set CNC_API_KEY in the environment.")
    endpoint = args.base_url.rstrip("/") + "/api/rest/v1/fs/read?" + urllib.parse.urlencode(
        {"file": args.remote_file}
    )
    request = urllib.request.Request(endpoint, headers={"Authorization": "Bearer " + key})
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    with tempfile.TemporaryDirectory(prefix="internet-map-import-") as directory:
        path = Path(directory) / "result.txt"
        with opener.open(request, timeout=30) as response, path.open("wb") as output:
            if response.status != 200:
                raise RuntimeError("CNC did not return a successful file response")
            size = 0
            while chunk := response.read(65536):
                size += len(chunk)
                if size > 256 * 1024 * 1024:
                    raise ValueError("Output exceeds the 256 MiB import limit")
                output.write(chunk)
        db = connect(args.database)
        try:
            ingest(db, argparse.Namespace(
                input=path, format="txt",
                source="cnc:" + args.base_url.rstrip("/") + ":" + args.remote_file,
            ))
        finally:
            db.close()


if __name__ == "__main__":
    main()
