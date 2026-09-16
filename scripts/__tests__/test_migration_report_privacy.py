import pytest

from migration.report import Evidence, PIIError, assert_pii_free, tokenise


def test_token_is_stable_and_not_the_value():
    token = tokenise('Someone@Example.com')
    assert token.startswith('sha256:') and len(token) == 15
    assert token == tokenise('someone@example.com')
    assert 'someone' not in token


def test_email_in_evidence_is_refused():
    with pytest.raises(PIIError, match='email'):
        assert_pii_free({'sample': 'a real person@keas.kln.comm'})


def test_phone_and_employee_code_are_refused():
    with pytest.raises(PIIError, match='phone'):
        assert_pii_free({'sample': '+86 138 1010 1665'})
    with pytest.raises(PIIError, match='code'):
        assert_pii_free({'sample': 'D44967'})


def test_tokens_and_counts_are_allowed():
    assert assert_pii_free({'by_key': tokenise('D44967'), 'matched': 7834}) is None


def test_evidence_refuses_to_write_pii(tmp_path):
    evidence = Evidence(mode='verify', profile=None)
    evidence.add('samples', ['person@keas.kln.comm'])
    with pytest.raises(PIIError):
        evidence.write(tmp_path / 'e.json')
    assert not (tmp_path / 'e.json').exists()


def test_evidence_writes_when_clean(tmp_path):
    evidence = Evidence(mode='verify', profile=None)
    evidence.add('matched', 7834)
    payload = evidence.write(tmp_path / 'e.json')
    assert payload['mode'] == 'verify'
    assert (tmp_path / 'e.json').exists()
    assert 'generated_at' in payload


def test_record_drift_is_carried_into_the_evidence():
    profile = type('P', (), {'path': 'p.yaml', 'name': 'demo', 'record_drift': {'users': {'declared': 2, 'actual': 3}}})()
    evidence = Evidence(mode='verify', profile=profile)
    assert evidence.payload()['record_drift'] == {'users': {'declared': 2, 'actual': 3}}
