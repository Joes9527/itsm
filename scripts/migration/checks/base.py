"""The protocol every entity plug-in implements, plus the shared result shapes."""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Protocol, runtime_checkable

from migration.profile import EntitySpec
from migration.sources import SourceIndex


@dataclass
class ReconcileResult:
    matched: int = 0
    only_source: list[str] = field(default_factory=list)
    only_target: list[str] = field(default_factory=list)
    only_source_by_rule: dict[str, int] = field(default_factory=dict)
    only_target_buckets: dict[str, int] = field(default_factory=dict)
    unattributed: list[str] = field(default_factory=list)


@dataclass
class WriteIntent:
    key: str
    payload: dict[str, Any]
    preflight: list[str] = field(default_factory=list)
    rollback: dict[str, str] = field(default_factory=dict)


@runtime_checkable
class EntityCheck(Protocol):
    """`runtime_checkable` so the registry can assert that a plug-in implements the shape."""

    name: str

    def fetch_target(self, target, spec: EntitySpec) -> list[dict]: ...

    def reconcile(self, src: SourceIndex, tgt: list[dict], spec: EntitySpec) -> ReconcileResult: ...

    def check_fields(self, src: SourceIndex, tgt: list[dict], spec: EntitySpec) -> dict: ...

    def check_structure(self, tgt: list[dict], spec: EntitySpec) -> dict: ...

    def render_write_plan(self, src, tgt, spec: EntitySpec) -> list[WriteIntent]: ...
