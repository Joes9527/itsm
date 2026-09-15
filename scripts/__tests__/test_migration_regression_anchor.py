"""Live anchor: needs the export files and the running environment, so it is skipped otherwise."""
import json
import os
import subprocess
from pathlib import Path

import pytest

ANCHOR = Path('docs/migrations/2026-09-15-legacy-migration-validation-evidence.json')
PROFILE = Path('scripts/migration/profiles/legacy-itsm-2026-08.yaml')


def live_environment_available() -> bool:
    return bool(os.environ.get('ITSM_TARGET_DB_PASSWORD')) and Path(
        '/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main/data/itsm_users.json').exists()


@pytest.mark.skipif(not live_environment_available(),
                    reason='needs the export files and the target environment')
def test_live_run_matches_the_committed_anchor(tmp_path):
    out = tmp_path / 'evidence.json'
    completed = subprocess.run(['python3', '-m', 'scripts.migration', 'verify', '--profile', str(PROFILE),
                                '--no-lineage', '--evidence-out', str(out)],
                               capture_output=True, text=True)
    assert completed.returncode in (0, 3), completed.stderr[-400:]
    anchor = json.loads(ANCHOR.read_text(encoding='utf-8'))
    fresh = json.loads(out.read_text(encoding='utf-8'))
    for entity, expected in anchor['entities'].items():
        for field in ('matched', 'only_source', 'only_target'):
            assert fresh['entities'][entity][field] == expected[field], \
                '%s.%s drifted: %s -> %s' % (entity, field, expected[field],
                                             fresh['entities'][entity][field])
    for entity in anchor['entities']:
        assert fresh['entities'][entity]['structure'] == anchor['entities'][entity]['structure'], \
            '%s structure drifted' % entity
