"""Tail Chrome-usable public CT logs as a source of hostname candidates."""

import base64
import gzip
import io
import json
import struct
from urllib.parse import urlsplit
from urllib.error import HTTPError
from urllib.request import Request, urlopen

from cryptography import x509
from cryptography.x509.oid import ExtensionOID

LOG_LIST = "https://www.gstatic.com/ct/log_list/v3/log_list.json"
MAX_RESPONSE = 16 * 1024 * 1024


def fetch(url):
    parsed = urlsplit(url)
    if parsed.scheme != "https" or not parsed.hostname or parsed.username:
        raise ValueError("expected a public HTTPS CT endpoint")
    with urlopen(Request(url, headers={"User-Agent": "CNC WordPress Generator"}), timeout=8) as response:
        if response.headers.get("Content-Encoding", "").lower() == "gzip":
            with gzip.GzipFile(fileobj=response) as decoded:
                data = decoded.read(MAX_RESPONSE + 1)
        else:
            data = response.read(MAX_RESPONSE + 1)
    if len(data) > MAX_RESPONSE:
        raise ValueError("CT response exceeds size limit")
    return data


def logs():
    for operator in json.loads(fetch(LOG_LIST))["operators"]:
        for field, tiled in (("logs", False), ("tiled_logs", True)):
            for log in operator.get(field, ()):
                if "usable" in log.get("state", {}):
                    yield log["log_id"], log["monitoring_url" if tiled else "url"].rstrip("/") + "/", tiled


def head(url, tiled):
    if tiled:
        return int(fetch(url + "checkpoint").splitlines()[1])
    return int(json.loads(fetch(url + "ct/v1/get-sth"))["tree_size"])


def read(stream, size):
    value = stream.read(size)
    if len(value) != size:
        raise ValueError("truncated CT entry")
    return value


def vector(stream, width):
    return read(stream, int.from_bytes(read(stream, width), "big"))


def names(der):
    try:
        cert = x509.load_der_x509_certificate(der)
        return cert.extensions.get_extension_for_oid(ExtensionOID.SUBJECT_ALTERNATIVE_NAME).value.get_values_for_type(x509.DNSName)
    except (ValueError, x509.ExtensionNotFound, x509.DuplicateExtension):
        return ()


def dynamic_entries(url, start, stop):
    data = json.loads(fetch(f"{url}ct/v1/get-entries?start={start}&end={stop - 1}"))
    entries = data["entries"]
    if not 0 < len(entries) <= stop - start:
        raise ValueError("unexpected CT entry count")
    for entry in entries:
        leaf = io.BytesIO(base64.b64decode(entry["leaf_input"], validate=True))
        if read(leaf, 2) != b"\x00\x00":
            raise ValueError("unsupported CT leaf")
        _, kind = struct.unpack(">QH", read(leaf, 10))
        if kind == 0:
            yield names(vector(leaf, 3))
        elif kind == 1:
            extra = io.BytesIO(base64.b64decode(entry["extra_data"], validate=True))
            yield names(vector(extra, 3))
        else:
            raise ValueError("unsupported CT entry type")


def tile_index(index):
    components = [f"{index % 1000:03d}"]
    index //= 1000
    while index:
        components.append(f"x{index % 1000:03d}")
        index //= 1000
    return "/".join(reversed(components))


def tiled_entries(url, start, size):
    index, offset = divmod(start, 256)
    width = min(256, size - index * 256)
    path = f"{url}tile/data/{tile_index(index)}"
    total = width
    try:
        data = fetch(path + (f".p/{width}" if width < 256 else ""))
    except HTTPError as error:
        if width == 256 or error.code != 404:
            raise
        error.close()
        data = fetch(path)
        total = 256
    stream = io.BytesIO(data)
    entries = []
    for i in range(total):
        _, kind = struct.unpack(">QH", read(stream, 10))
        if kind == 1:
            read(stream, 32)
        elif kind != 0:
            raise ValueError("unsupported CT tile entry type")
        certificate = vector(stream, 3)
        vector(stream, 2)
        if kind == 1:
            certificate = vector(stream, 3)
        vector(stream, 2)
        if offset <= i < width:
            entries.append(names(certificate))
    if stream.read(1):
        raise ValueError("unexpected CT tile size")
    return entries
