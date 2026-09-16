"""Assemble evidence and guarantee it carries no personal data."""
from __future__ import annotations

import hashlib
import json
import re
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

EMAIL = re.compile(r'[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}')
PHONE = re.compile(r'(?<!\d)(?:\+?86)?1[3-9]\d{9}(?!\d)')
SEPARATORS = re.compile(r'[\s\-().]')
CODE = re.compile(r'\b[DH]\d{5}\b')
TOKEN = re.compile(r'^sha256:[0-9a-f]{8}$')


class PIIError(Exception):
    """Raised when evidence would carry personal data; nothing is written."""


def tokenise(value: str) -> str:
    return 'sha256:' + hashlib.sha256(value.strip().lower().encode()).hexdigest()[:8]


def _walk(node: Any, path: str, hits: list[str]) -> None:
    if isinstance(node, dict):
        for key, value in node.items():
            _walk(value, '%s.%s' % (path, key), hits)
    elif isinstance(node, (list, tuple)):
        for position, value in enumerate(node):
            _walk(value, '%s[%d]' % (path, position), hits)
    elif isinstance(node, str):
        if TOKEN.match(node):
            return
        # a phone number is often written with spaces or hyphens; normalise before matching
        normalised = SEPARATORS.sub('', node)
        for label, pattern, subject in (('email', EMAIL, node), ('phone', PHONE, normalised),
                                        ('code', CODE, node)):
            if pattern.search(subject):
                hits.append('%s (%s)' % (path, label))
                return


def assert_pii_free(payload: Any) -> None:
    hits: list[str] = []
    _walk(payload, '$', hits)
    if hits:
        raise PIIError('evidence contains personal data: ' + '; '.join(hits[:5]))


class Evidence:
    def __init__(self, mode: str, profile=None):
        self.mode = mode
        self.profile = profile
        self._data: dict[str, Any] = {
            'mode': mode,
            'generated_at': datetime.now(timezone.utc).isoformat(),
            'profile': str(getattr(profile, 'path', '')) or None,
            'profile_name': getattr(profile, 'name', None),
        }
        drift = getattr(profile, 'record_drift', None)
        if drift:
            self._data['record_drift'] = drift

    def add(self, key: str, value: Any) -> None:
        self._data[key] = value

    def payload(self) -> dict:
        assert_pii_free(self._data)
        return self._data

    def write(self, path: Path | None) -> dict:
        payload = self.payload()
        if path:
            Path(path).write_text(json.dumps(payload, indent=2, ensure_ascii=False) + '\n',
                                  encoding='utf-8')
        return payload

    def summary(self) -> str:
        return '\n'.join('%s: %s' % (key, json.dumps(value, ensure_ascii=False)[:120])
                         for key, value in self._data.items()
                         if key not in {'generated_at', 'profile', 'profile_name'})
