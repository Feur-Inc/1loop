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
| `packaging/android/build.sh` | `1loop-VERSION.apk`, `1loop-VERSION-android-native-sources.zip` |

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

JDK 21 and the Android SDK (`ANDROID_HOME`) with `platforms;android-37.0`,
`build-tools;37.0.0` and the NDK `30.0.16248370` (`ANDROID_NDK_HOME` pointing
to it), plus `cmake`, `make`, `patch` and `patchelf` for the native helpers.

### Signing

Debug and release are signed with the **same** publication certificate,
read from `android/signing/release.properties` (never committed: `.pem`,
`.key`, `.p12`, `.jks`, `.keystore` and `android/signing/` are ignored):

```properties
storeFile=1loop-release.p12
storePassword=…
keyAlias=1loop-release
keyPassword=…
```

`storeFile` is relative to that file; `ONELOOP_ANDROID_SIGNING_PROPERTIES`
can point to it from elsewhere. The script copies the release APK to `dist/`
only when debug and release carry that same certificate.

Without the key (a contributor, a pull request), the debug APK uses the local
Android debug key, so it installs for testing but cannot update an app signed
with the publication key; the release APK stays unsigned and the script
copies no APK.

## CI secrets

Pull requests never receive them. A push on `dev` uses them to publish the
alpha prerelease:

- `ANDROID_SIGNING_BUNDLE`: the publication keystore, as base64 of a gzip tar
  holding exactly `1loop-release.p12` and `release.properties`:

  ```bash
  tar -C android/signing -czf - 1loop-release.p12 release.properties | base64 -w0 |
    gh secret set ANDROID_SIGNING_BUNDLE --repo Feur-Inc/1loop
  ```

- `RELEASE_KEY`: `release-key.pem`, the key whose public half is
  `.github/release-key.pub` (and the update key of `internal/update`). It
  signs the alpha's `SHA256SUMS`:

  ```bash
  gh secret set RELEASE_KEY --repo Feur-Inc/1loop < release-key.pem
  ```

Keep backups of both keys: installed apps only accept updates signed with
them.
