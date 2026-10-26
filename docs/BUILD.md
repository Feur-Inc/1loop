# Building 1loop

Each download has its build script, run from the repository root on Debian
or Ubuntu (x86-64). They write their files in `dist/` (`DIST=folder` to
change it). The CI (`.github/workflows/ci.yml`) runs the same scripts after
the checks below; `release.sh` uses them too. Go comes from `go.mod`.

| Script | Downloads in `dist/` |
|---|---|
| `packaging/linux/build.sh binaries tarball deb appimage` | `1loop-VERSION-linux-amd64.tar.gz`, `1loop-VERSION-amd64.deb`, `1loop-VERSION-x86_64.AppImage` |
| `packaging/server/build.sh` | `1loop-server-VERSION-linux-amd64`, `1loop-server-VERSION-windows-amd64.exe` |
| `packaging/windows/build.sh` | `1loop-VERSION-windows-amd64.zip` |
| `packaging/android/build.sh debug` | `1loop-VERSION-debug.apk`, `1loop-VERSION-android-native-sources.zip` |
| `packaging/android/build.sh release` | `1loop-VERSION.apk` (only with the release key), same native sources |

For development, `make` builds `bin/1loop` and `bin/1loop-wayland`, and
`make server` builds `bin/1loop-server`.

## Checks

The CI runs them before building:

```bash
sudo apt-get install libgl-dev libegl-dev xorg-dev libwayland-dev libxkbcommon-dev xvfb
test -z "$(git ls-files -z -- '*.go' | xargs -0 -r gofmt -l)"
go vet ./...
xvfb-run -a go test ./...     # the tests need a display
./android/gradlew -p android :app:testDebugUnitTest :app:lintDebug --warning-mode fail
```

## Linux desktop

Needs the libraries above. The binaries use the libraries of the machine that
builds them: the `.deb` requires the glibc version they actually need. The
AppImage step downloads a pinned `appimagetool`. `make tarball deb appimage`
runs the same script.

### Flatpak

Install `flatpak`, `flatpak-builder`, `appstream-compose` and `desktop-file-utils`, then follow
the runtime setup in [INSTALL.md](INSTALL.md#flatpak). The manifest copies only
the build inputs, as the Nix package does; root tokens, Android signing files
and generated caches are excluded. Keep credentials outside source directories.

```bash
make flatpak
desktop-file-validate bin/flatpak-build/files/share/applications/io.github.Feur_Inc.OneLoop.desktop
flatpak build bin/flatpak-build /app/bin/1loop-flatpak --help
```

CI builds the same manifest, validates the desktop entry and executable,
and includes the bundle in the signed alpha downloads. The manifest needs
network access to fetch Go modules; it is not suitable for Flathub submission
as it stands.

### Nix

With Nix's `nix-command` and `flakes` features enabled:

```bash
nix flake check --no-write-lock-file
nix build .#default --no-write-lock-file
./result/bin/1loop --help
./result/libexec/1loop/1loop-wayland --help
./result/libexec/1loop/computer-python -c 'import gi, dbus, PIL; gi.require_version("Gst", "1.0"); from gi.repository import Gst; Gst.init(None); assert Gst.ElementFactory.find("pipewiresrc")'
```

CI evaluates and builds the locked flake and checks this Python helper.
It does not publish a Nix binary archive. These checks do not replace a
desktop session test: native Wayland/XWayland, GNOME/KDE launcher identity,
and Computer Use portal permissions need a graphical session on the target
system, including NixOS. They are not covered by the headless CI checks.

## Servers

Static, without cgo: nothing to install besides Go.

## Windows (cross-compiled from Linux)

```bash
sudo apt-get install gcc-mingw-w64-x86-64 p7zip-full zip
```

The zip holds `1loop.exe`, `1loop-server.exe`, and `software/` with
`1loop-software.cmd` (the Mesa CPU renderer, downloaded and checked, for PCs
without a working OpenGL driver). `go vet` runs for Windows; the tests need
Windows itself: run `go test ./...` there.

## Android

The CI builds only `debug`. Both need JDK 21 and the Android SDK
(`ANDROID_HOME`) with `platforms;android-37.0`, `build-tools;37.0.0` and the
NDK `30.0.16248370` (`ANDROID_NDK_HOME` pointing to it), plus `cmake`, `make`,
`patch` and `patchelf` for the native helpers.

### Signing

Two keys, never mixed:

- **Debug: one shared key, public on purpose.** `android/debug.keystore`
  (password `android`, alias `androiddebugkey`) is committed, so every debug
  build, on any machine and in the CI, has the same signature and updates any
  other. The debug app is "1loop debug" (`io.oneloop.remote.debug`): it
  installs beside the release app. It is debuggable: anyone with ADB access
  to the phone can read its data. This key never signs a published release.
- **Release: one private key.** It stays out of Git, in
  `android/signing/` on the machine that publishes (`.pem`, `.key`, `.p12`,
  `.jks`, `.keystore` and `android/signing/` are ignored), described by
  `android/signing/release.properties`:

  ```properties
  storeFile=1loop-release.p12
  storePassword=…
  keyAlias=1loop-release
  keyPassword=…
  ```

  `storeFile` is relative to that file; `ONELOOP_ANDROID_SIGNING_PROPERTIES`
  can point to it from elsewhere. Without it, the release APK is unsigned and
  `packaging/android/build.sh release` stops. Installed release apps only
  accept updates signed with this key: keep encrypted backups of it.

## CI secrets

- `ANDROID_SIGNING_BUNDLE`: the release keystore, stored for a future
  release workflow and **not used by the CI**, which never builds a release
  APK. It is base64 of a gzip tar holding exactly `1loop-release.p12` and
  `release.properties`. GitHub never gives a secret back: it is not a backup.

  ```bash
  tar -C android/signing -czf - 1loop-release.p12 release.properties | base64 -w0 |
    gh secret set ANDROID_SIGNING_BUNDLE --repo Feur-Inc/1loop
  ```

- `RELEASE_KEY`: `release-key.pem`, the key whose public half is
  `.github/release-key.pub` (and the update key of `internal/update`). A
  push on `dev` uses it to sign the alpha's `SHA256SUMS`; pull requests
  never receive it:

  ```bash
  gh secret set RELEASE_KEY --repo Feur-Inc/1loop < release-key.pem
  ```

Keep backups of `android/signing/` and `release-key.pem`: installed apps
only accept updates signed with them.
