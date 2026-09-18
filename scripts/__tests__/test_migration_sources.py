import json
from pathlib import Path

import pytest

from migration.profile import SourceFile
from migration.sources import SourceError, load_source


def test_indexes_rows_by_id_field(tmp_path):
    (tmp_path / 'u.json').write_text(json.dumps([{'userName': 'A1'}, {'userName': 'A2'}]))
    index = load_source(tmp_path, SourceFile(name='users', file='u.json', id_field='userName'))
    assert index.records == 2
    assert sorted(index.by_key) == ['A1', 'A2']
    assert index.sha256 == __import__('hashlib').sha256((tmp_path / 'u.json').read_bytes()).hexdigest()


def test_duplicate_keys_keep_the_first_row(tmp_path):
    (tmp_path / 'u.json').write_text(json.dumps([{'userName': 'A', 'email': '1'},
                                                 {'userName': 'A', 'email': '2'}]))
    index = load_source(tmp_path, SourceFile(name='users', file='u.json', id_field='userName'))
    assert index.duplicates == ['A']
    assert index.by_key['A']['email'] == '1'


def test_missing_id_field_is_an_error(tmp_path):
    (tmp_path / 'u.json').write_text(json.dumps([{'other': 1}]))
    with pytest.raises(SourceError, match='userName'):
        load_source(tmp_path, SourceFile(name='users', file='u.json', id_field='userName'))


def test_non_list_payload_is_an_error(tmp_path):
    (tmp_path / 'u.json').write_text(json.dumps({'userName': 'A'}))
    with pytest.raises(SourceError, match='list'):
        load_source(tmp_path, SourceFile(name='users', file='u.json', id_field='userName'))
