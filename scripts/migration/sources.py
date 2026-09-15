"""Read an export file, prove it is the expected one, and index it by its business key."""
from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass, field
from pathlib import Path

from migration.profile import SourceFile


class SourceError(Exception):
    """Raised when a declared source file cannot be read or does not look like the export."""


@dataclass
class SourceIndex:
    name: str
    path: Path | None
    id_field: str
    sha256: str
    records: int
    by_key: dict[str, dict]
    duplicates: list[str] = field(default_factory=list)


def load_source(root: Path, spec: SourceFile) -> SourceIndex:
    path = Path(root) / spec.file
    if not path.exists():
        raise SourceError('source file %s does not exist at %s' % (spec.file, path))
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    try:
        rows = json.loads(path.read_text(encoding='utf-8'))
    except json.JSONDecodeError as exc:
        raise SourceError('source file %s is not valid JSON: %s' % (spec.file, exc)) from exc
    if not isinstance(rows, list):
        raise SourceError('source file %s must contain a list of records' % spec.file)
    by_key: dict[str, dict] = {}
    duplicates: list[str] = []
    for row in rows:
        if not isinstance(row, dict) or spec.id_field not in row:
            raise SourceError('source file %s has a record without the key field %r'
                              % (spec.file, spec.id_field))
        key = str(row[spec.id_field])
        if key in by_key:
            duplicates.append(key)
            continue
        by_key[key] = row
    return SourceIndex(name=spec.name, path=path, id_field=spec.id_field, sha256=digest,
                       records=len(rows), by_key=by_key, duplicates=duplicates)
