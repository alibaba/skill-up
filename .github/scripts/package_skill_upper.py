#!/usr/bin/env python3
"""Build the canonical skill-upper release archive reproducibly."""

from __future__ import annotations

import argparse
import gzip
import hashlib
from pathlib import Path
import re
import shutil
import tarfile
import tempfile


SEMVER = re.compile(
    r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)"
    r"(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?"
    r"(?:\+[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$"
)


def normalize_version(raw: str) -> str:
    version = raw.removeprefix("v")
    if not SEMVER.fullmatch(version):
        raise ValueError(f"version must be valid SemVer: {raw}")
    return version


def normalized_tar_info(info: tarfile.TarInfo) -> tarfile.TarInfo:
    info.uid = 0
    info.gid = 0
    info.uname = "root"
    info.gname = "root"
    info.mtime = 0
    if info.isdir():
        info.mode = 0o755
    elif info.isfile():
        info.mode = 0o755 if info.mode & 0o111 else 0o644
    return info


def add_tree(archive: tarfile.TarFile, source: Path) -> None:
    paths = [source, *sorted(source.rglob("*"), key=lambda path: path.as_posix())]
    for path in paths:
        if path.is_symlink():
            raise ValueError(f"release Skill must not contain symlinks: {path}")
        relative = path.relative_to(source)
        archive_name = Path("skill-upper") / relative
        info = normalized_tar_info(
            archive.gettarinfo(str(path), arcname=archive_name.as_posix())
        )
        if path.is_file():
            with path.open("rb") as stream:
                archive.addfile(info, stream)
        else:
            archive.addfile(info)


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def update_checksum_file(checksum_file: Path, archive: Path, digest: str) -> None:
    entries: dict[str, str] = {}
    if checksum_file.exists():
        for line in checksum_file.read_text(encoding="utf-8").splitlines():
            fields = line.split()
            if len(fields) == 2:
                entries[fields[1].lstrip("*")] = fields[0]
    entries[archive.name] = digest
    contents = "".join(
        f"{entries[name]}  {name}\n" for name in sorted(entries)
    )
    checksum_file.write_text(contents, encoding="utf-8")


def package_skill(source: Path, output_dir: Path, raw_version: str) -> Path:
    version = normalize_version(raw_version)
    if not (source / "SKILL.md").is_file():
        raise ValueError(f"canonical Skill not found: {source}")

    output_dir.mkdir(parents=True, exist_ok=True)
    archive_path = output_dir / f"skill-upper_{version}.tar.gz"
    checksum_file = output_dir / f"skill-up_{version}_checksums.txt"

    with tempfile.TemporaryDirectory(prefix="skill-upper-release-") as temporary:
        staged_skill = Path(temporary) / "skill-upper"
        shutil.copytree(source, staged_skill, symlinks=True)
        with archive_path.open("wb") as raw_stream:
            with gzip.GzipFile(fileobj=raw_stream, mode="wb", filename="", mtime=0) as zipped:
                with tarfile.open(fileobj=zipped, mode="w") as archive:
                    add_tree(archive, staged_skill)

    digest = sha256(archive_path)
    update_checksum_file(checksum_file, archive_path, digest)
    print(f"{archive_path} sha256:{digest}")
    return archive_path


def main() -> None:
    repository_root = Path(__file__).resolve().parents[2]
    parser = argparse.ArgumentParser()
    parser.add_argument("--version", required=True)
    parser.add_argument(
        "--source",
        type=Path,
        default=repository_root / "skills" / "skill-upper",
    )
    parser.add_argument("--output-dir", type=Path, default=repository_root / "dist")
    args = parser.parse_args()
    package_skill(args.source.resolve(), args.output_dir.resolve(), args.version)


if __name__ == "__main__":
    main()
