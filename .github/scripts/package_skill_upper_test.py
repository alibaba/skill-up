from __future__ import annotations

import hashlib
import importlib.util
from pathlib import Path
import shutil
import tarfile
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("package_skill_upper.py")
SPEC = importlib.util.spec_from_file_location("package_skill_upper", SCRIPT)
package_skill_upper = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(package_skill_upper)


class PackageSkillUpperTest(unittest.TestCase):
    def setUp(self) -> None:
        self.repository_root = SCRIPT.parents[2]
        self.source = self.repository_root / "skills" / "skill-upper"

    def test_packages_canonical_skill_without_mutating_source(self) -> None:
        original = (self.source / "SKILL.md").read_bytes()
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary)
            archive = package_skill_upper.package_skill(self.source, output, "v1.2.3")
            with tarfile.open(archive, "r:gz") as package:
                names = package.getnames()
                self.assertIn("skill-upper/SKILL.md", names)
                self.assertIn("skill-upper/references/distribution.md", names)
                skill_md = package.extractfile("skill-upper/SKILL.md")
                assert skill_md is not None
                contents = skill_md.read().decode()
                self.assertNotIn("\nversion:", contents)
            self.assertEqual((self.source / "SKILL.md").read_bytes(), original)

            digest = hashlib.sha256(archive.read_bytes()).hexdigest()
            checksum = output / "skill-up_1.2.3_checksums.txt"
            self.assertIn(
                f"{digest}  skill-upper_1.2.3.tar.gz\n",
                checksum.read_text(encoding="utf-8"),
            )

    def test_archive_is_reproducible(self) -> None:
        with tempfile.TemporaryDirectory() as first, tempfile.TemporaryDirectory() as second:
            first_archive = package_skill_upper.package_skill(
                self.source, Path(first), "1.2.3"
            )
            second_archive = package_skill_upper.package_skill(
                self.source, Path(second), "1.2.3"
            )
            self.assertEqual(first_archive.read_bytes(), second_archive.read_bytes())

    def test_archive_is_reproducible_across_checkout_modes(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            first_source = root / "first"
            second_source = root / "second"
            shutil.copytree(self.source, first_source)
            shutil.copytree(self.source, second_source)
            (second_source / "SKILL.md").chmod(0o600)
            (second_source / "references").chmod(0o700)
            first_archive = package_skill_upper.package_skill(
                first_source, root / "first-output", "1.2.3"
            )
            second_archive = package_skill_upper.package_skill(
                second_source, root / "second-output", "1.2.3"
            )
            self.assertEqual(first_archive.read_bytes(), second_archive.read_bytes())

    def test_rejects_invalid_version(self) -> None:
        with self.assertRaisesRegex(ValueError, "valid SemVer"):
            package_skill_upper.normalize_version("latest")

    def test_rejects_source_symlinks_without_following_them(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / "skill-upper"
            source.mkdir()
            (source / "SKILL.md").write_text("skill\n", encoding="utf-8")
            outside = root / "outside.txt"
            outside.write_text("must not be packaged\n", encoding="utf-8")
            (source / "leak.txt").symlink_to(outside)
            with self.assertRaisesRegex(ValueError, "must not contain symlinks"):
                package_skill_upper.package_skill(source, root / "dist", "1.2.3")


if __name__ == "__main__":
    unittest.main()
