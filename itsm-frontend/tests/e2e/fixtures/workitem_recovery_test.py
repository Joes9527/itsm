"""Recovery verdict counterexamples; also exercised against real restored manifests."""
import copy
import unittest
from workitem_recovery import verify_recovery
class RecoveryTests(unittest.TestCase):
    def test_full_recovery_requires_every_surface(self):
        baseline={name:{'sha256':'same'} for name in ['tables','sequences','roles_acl','attachments','application','consumers','configuration','control','config_yaml']}
        verify_recovery(baseline,copy.deepcopy(baseline))
        for surface in baseline:
            with self.subTest(surface=surface):
                candidate=copy.deepcopy(baseline);candidate[surface]={'sha256':'old-or-missing'}
                with self.assertRaises(ValueError): verify_recovery(baseline,candidate)
    def test_empty_manifests_cannot_prove_complete_recovery(self):
        with self.assertRaises(ValueError): verify_recovery({}, {})
    def test_uncaptured_postbackup_data_prevents_zero_loss(self):
        with self.assertRaises(ValueError): verify_recovery({}, {}, uncaptured=['post-backup-workitem'])
if __name__=='__main__': unittest.main()
