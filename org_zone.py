#!/usr/bin/env python3
"""Import delegation names from a plain-text or gzip .org zone snapshot."""
import argparse
import gzip
from pathlib import Path
import tempfile

from main import DEFAULT_DATABASE, connect, ingest


def strip_comments(text):
    in_quote = False
    for i, char in enumerate(text):
        if char == '"':
            in_quote = not in_quote
        elif char == ';' and not in_quote:
            return text[:i]
    return text


def delegations(stream):
    origin = "org."
    owner = None
    depth = 0
    last_emitted = None
    for number, raw in enumerate(stream, 1):
        # Delegation records in CZDS snapshots use unquoted owner/type fields.
        line = strip_comments(raw).rstrip()
        if not line.strip():
            continue
        fields = line.split()
        if fields[0].startswith("$"):
            directive = fields[0].upper()
            if directive == "$ORIGIN" and len(fields) == 2:
                origin = fields[1].lower()
                if origin != "org.":
                    raise ValueError(f"Unexpected origin at line {number}")
            elif directive == "$TTL" and len(fields) == 2:
                pass
            else:
                raise ValueError(f"Unsupported zone directive at line {number}")
            continue
        if depth:
            depth += line.count("(") - line.count(")")
            continue
        depth = line.count("(") - line.count(")")
        if depth < 0:
            raise ValueError(f"Unbalanced record at line {number}")
        if not raw[0].isspace():
            owner = fields.pop(0)
        if owner is None:
            raise ValueError(f"Missing owner at line {number}")
        # Skip TTL and class before the record type.
        while fields and (fields[0].upper() == "IN" or
                          fields[0][0].isdigit()):
            fields.pop(0)
        if not fields or fields[0].upper() != "NS":
            continue
        if len(fields) < 2:
            raise ValueError(f"Missing nameserver at line {number}")
        name = owner.lower()
        if name == "@":
            name = origin
        elif not name.endswith("."):
            name += "." + origin
        name = name.rstrip(".")
        labels = name.split(".")
        if len(labels) != 2 or labels[1] != "org":
            continue
        label = labels[0]
        if not 1 <= len(label) <= 63 or label[0] == "-" or label[-1] == "-":
            raise ValueError(f"Invalid delegation at line {number}")
        if not all(c in "abcdefghijklmnopqrstuvwxyz0123456789-" for c in label):
            raise ValueError(f"Invalid delegation at line {number}")
        if name != last_emitted:
            last_emitted = name
            yield name
    if depth:
        raise ValueError("Incomplete multiline zone record")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("input", type=Path)
    parser.add_argument("--source", required=True)
    parser.add_argument("--database", type=Path, default=DEFAULT_DATABASE)
    args = parser.parse_args()
    opening = gzip.open if args.input.suffix == ".gz" else open
    with tempfile.TemporaryDirectory(prefix="internet-map-zone-") as folder:
        extracted = Path(folder) / "delegations.txt"
        with opening(args.input, "rt", encoding="ascii", errors="replace") as stream, extracted.open("w") as output:
            for domain in delegations(stream):
                output.write(domain + "\n")
        db = connect(args.database)
        try:
            ingest(db, argparse.Namespace(input=extracted, format="txt", source=args.source))
        finally:
            db.close()


if __name__ == "__main__":
    main()
