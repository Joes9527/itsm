"""Live anchor: needs the export files and the running environment, so it is skipped otherwise."""
import json
import os
import subprocess
from pathlib import Path

import pytest

# A live run must be explicitly bound to a reviewed profile and its own evidence.
PROFILE = os.environ.get('ITSM_MIGRATION_LIVE_PROFILE')
ANCHOR = os.environ.get('ITSM_MIGRATION_LIVE_ANCHOR')


def live_environment_available() -> bool:
    return bool(PROFILE and ANCHOR and Path(PROFILE).is_file() and Path(ANCHOR).is_file())


@pytest.mark.skipif(not live_environment_available(),
                    reason='needs the export files and the target environment')
def test_live_run_matches_the_committed_anchor(tmp_path):
    out = tmp_path / 'evidence.json'
    completed = subprocess.run(['python3', '-m', 'scripts.migration', 'verify', '--profile', str(PROFILE),
                                '--no-lineage', '--evidence-out', str(out)],
                               capture_output=True, text=True)
    assert completed.returncode in (0, 3), completed.stderr[-400:]
    anchor = json.loads(Path(ANCHOR).read_text(encoding='utf-8'))
    fresh = json.loads(out.read_text(encoding='utf-8'))
    for entity, expected in anchor['entities'].items():
        for field in ('matched', 'only_source', 'only_target'):
            assert fresh['entities'][entity][field] == expected[field], \
                '%s.%s drifted: %s -> %s' % (entity, field, expected[field],
                                             fresh['entities'][entity][field])
    for entity in anchor['entities']:
        assert fresh['entities'][entity]['structure'] == anchor['entities'][entity]['structure'], \
            '%s structure drifted' % entity
