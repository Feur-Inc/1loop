#!/usr/bin/env python3
"""Build the small APK-native launchers; Debian is installed separately on demand."""

import argparse
import hashlib
import os
from pathlib import Path, PurePosixPath
import shutil
import subprocess
import tarfile
import urllib.request
import zipfile


SCRIPT = Path(__file__).resolve()
PATCHES = SCRIPT.parent / "patches"
ABWRAP_COMMIT = "047fd1038aa363cd2da2887c104f7aa3110cde92"
ABWRAP_SHA256 = "de69984904f1c21cc23e0279d60f85764160eaa0166df7f459b4a0459686aede"
PROOT_VERSION = "5.1.107.96"
PROOT_SHA256 = "75f654fe60dea92dabff2bf083ae8bfe4f91baa6a1a374786a6bf391015eebaa"
TALLOC_SHA256 = "556591f43bb773ad8777e1a29522640866a55f95dab71914418b94a8c58ad5a7"
TALLOC_URL = "https://packages.termux.dev/apt/termux-main/pool/main/libt/libtalloc/libtalloc_2.5.0_aarch64.deb"
TALLOC_SOURCE_SHA256 = "912afa237510ae542a7733998eb18a12bcda35ab6729c8e2ddb43e8d0ebab007"


def run(*args, cwd=None):
    subprocess.run([str(value) for value in args], cwd=cwd, check=True)


def download(cache, name, url, digest):
    path = cache / name
    if not path.exists():
        with urllib.request.urlopen(url, timeout=60) as response:
            data = response.read()
        if hashlib.sha256(data).hexdigest() != digest:
            raise RuntimeError(f"Checksum mismatch: {name}")
        path.write_bytes(data)
    if hashlib.sha256(path.read_bytes()).hexdigest() != digest:
        raise RuntimeError(f"Cached checksum mismatch: {name}")
    return path


def archive_path(name):
    path = PurePosixPath(name)
    if path.is_absolute() or ".." in path.parts:
        raise RuntimeError(f"Unsafe archive path: {name}")
    return Path(*path.parts)


def sources(cache, working):
    abwrap_archive = download(
        cache, "abwrap.tar.gz",
        f"https://codeload.github.com/dukmu/abwrap-android/tar.gz/{ABWRAP_COMMIT}", ABWRAP_SHA256,
    )
    proot_archive = download(
        cache, "proot.zip",
        f"https://codeload.github.com/termux/proot/zip/refs/tags/v{PROOT_VERSION}", PROOT_SHA256,
    )
    abwrap = working / "abwrap"
    proot = working / "proot"
    if not (working / "patched").exists():
        with tarfile.open(abwrap_archive) as archive:
            for member in archive.getmembers():
                parts = archive_path(member.name).parts[1:]
                if not parts or not member.isfile():
                    continue
                if parts[0] not in {"src", "include", "CMakeLists.txt", "LICENSE"}:
                    continue
                target = abwrap / Path(*parts)
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(archive.extractfile(member).read())
        with zipfile.ZipFile(proot_archive) as archive:
            for member in archive.infolist():
                parts = archive_path(member.filename).parts[1:]
                if not parts or member.is_dir():
                    continue
                if parts[0] not in {"src", "COPYING", "AUTHORS"}:
                    continue
                target = proot / Path(*parts)
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(archive.read(member))
        run("patch", "--batch", "-p1", "-i", PATCHES / "abwrap-empty-path.patch", cwd=abwrap)
        run("patch", "--batch", "-p1", "-i", PATCHES / "proot-exitkill.patch", cwd=proot)
        (working / "patched").touch()
    return abwrap, proot


def build(ndk, output):
    fingerprint = hashlib.sha256(SCRIPT.read_bytes())
    fingerprint.update((SCRIPT.parent / "guard.c").read_bytes())
    for patch in sorted(PATCHES.glob("*.patch")):
        fingerprint.update(patch.read_bytes())
    fingerprint = fingerprint.hexdigest()
    stamp = output / "fingerprint"
    libraries = output / "jniLibs" / "arm64-v8a"
    expected = ("libproot.so", "libloader.so", "libabwrap.so", "libtalloc.so", "libguard.so")
    if stamp.exists() and stamp.read_text() == fingerprint and all((libraries / name).is_file() for name in expected):
        return
    compiler = ndk / "toolchains/llvm/prebuilt/linux-x86_64/bin"
    if not (compiler / "aarch64-linux-android31-clang").is_file():
        raise RuntimeError("Install Android NDK 30.0.16248370 and set ANDROID_NDK_HOME (Linux build host).")
    cache = output / "downloads"
    working = output / "sources" / fingerprint
    cache.mkdir(parents=True, exist_ok=True)
    working.mkdir(parents=True, exist_ok=True)
    libraries.mkdir(parents=True, exist_ok=True)
    abwrap, proot = sources(cache, working)
    talloc = download(cache, "libtalloc.deb", TALLOC_URL, TALLOC_SHA256)
    talloc_root = working / "talloc"
    run("dpkg-deb", "--extract", talloc, talloc_root)
    prefix = talloc_root / "data/data/com.termux/files/usr"
    shutil.copyfile(prefix / "lib/libtalloc.so.2.5.0", libraries / "libtalloc.so")
    run("patchelf", "--set-soname", "libtalloc.so", libraries / "libtalloc.so")
    run(
        "make", "-C", proot / "src", "-j2",
        "CC=" + str(compiler / "aarch64-linux-android31-clang"),
        "OBJCOPY=" + str(compiler / "llvm-objcopy"),
        "OBJDUMP=" + str(compiler / "llvm-objdump"),
        'CPPFLAGS=-D_FILE_OFFSET_BITS=64 -D_GNU_SOURCE -I. -I' + str(prefix / "include")
        + ' -DARG_MAX=131072 -DVERSION=\\"' + PROOT_VERSION + '-oneloop\\"',
        "LDFLAGS=-L" + str(libraries) + " -ltalloc -Wl,-z,noexecstack",
        "PROOT_UNBUNDLE_LOADER=/oneloop", "HAS_LOADER_32BIT=",
    )
    abwrap_build = working / "abwrap-build"
    run(
        "cmake", "-S", abwrap, "-B", abwrap_build,
        "-DBUILD_TESTING=OFF", "-DCMAKE_BUILD_TYPE=Release",
        "-DCMAKE_TOOLCHAIN_FILE=" + str(ndk / "build/cmake/android.toolchain.cmake"),
        "-DANDROID_ABI=arm64-v8a", "-DANDROID_PLATFORM=android-31",
        "-DCMAKE_EXE_LINKER_FLAGS=-static",
    )
    run("cmake", "--build", abwrap_build, "-j2")
    run(compiler / "aarch64-linux-android31-clang", "-Wall", "-Wextra", "-Werror", "-O2", "-static",
        SCRIPT.parent / "guard.c", "-o", libraries / "libguard.so")
    for source, name in (
        (proot / "src/proot", "libproot.so"),
        (proot / "src/loader/loader", "libloader.so"),
        (abwrap_build / "abwrap", "libabwrap.so"),
    ):
        shutil.copyfile(source, libraries / name)
    for name in expected:
        run(compiler / "llvm-strip", libraries / name)
    stamp.write_text(fingerprint)


def source_bundle(output, destination):
    cache = output / "downloads"
    cache.mkdir(parents=True, exist_ok=True)
    archives = [
        download(cache, "abwrap.tar.gz", f"https://codeload.github.com/dukmu/abwrap-android/tar.gz/{ABWRAP_COMMIT}", ABWRAP_SHA256),
        download(cache, "proot.zip", f"https://codeload.github.com/termux/proot/zip/refs/tags/v{PROOT_VERSION}", PROOT_SHA256),
        download(cache, "talloc-2.5.0.tar.gz", "https://www.samba.org/ftp/talloc/talloc-2.5.0.tar.gz", TALLOC_SOURCE_SHA256),
        download(cache, "termux-libtalloc-build.sh", "https://raw.githubusercontent.com/termux/termux-packages/bddd9721910e56bc36412a764b4b4f7bbdbb1eb1/packages/libtalloc/build.sh", "b722c5adb89e4d4ad4595dbba40be562d9894103a190777392c363b57b1a7201"),
    ]
    destination.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(destination, "w", compression=zipfile.ZIP_DEFLATED) as archive:
        for path in archives:
            archive.write(path, "upstream/" + path.name)
        for path in [SCRIPT, SCRIPT.parent / "guard.c", *sorted(PATCHES.glob("*.patch"))]:
            archive.write(path, str(path.relative_to(SCRIPT.parent)))


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--ndk", type=Path, default=Path(os.environ.get("ANDROID_NDK_HOME", "/missing-ndk")))
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--source-bundle", type=Path)
    arguments = parser.parse_args()
    if arguments.source_bundle:
        source_bundle(arguments.output.resolve(), arguments.source_bundle.resolve())
    else:
        build(arguments.ndk.resolve(), arguments.output.resolve())
