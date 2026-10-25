# Building 1loop

These are the builds and checks the CI runs (`.github/workflows/ci.yml`),
from the repository root, on Debian or Ubuntu (x86-64). Go comes from
`go.mod`.

## Linux: checks, desktop and server

```bash
sudo apt-get install libgl-dev libegl-dev xorg-dev libwayland-dev libxkbcommon-dev xvfb
test -z "$(git ls-files -z -- '*.go' | xargs -0 -r gofmt -l)"
go vet ./...
xvfb-run -a go test ./...     # the tests need a display
make server                   # bin/1loop-server
packaging/linux/build.sh binaries tarball deb appimage
```

The downloads land in `dist/`: `1loop-VERSION-linux-amd64.tar.gz`,
`1loop-VERSION-amd64.deb` and `1loop-VERSION-x86_64.AppImage`. They use the
libraries of the machine that builds them: the `.deb` requires the glibc
version its binaries actually need. `make` alone builds `bin/1loop` and
`bin/1loop-wayland` for development.

## Windows (cross-compiled from Linux)

```bash
sudo apt-get install gcc-mingw-w64-x86-64 p7zip-full
packaging/windows/build.sh
```

`bin/windows/` then holds `1loop.exe`, `1loop-server.exe`, and `software/`
with `1loop-software.cmd` (the Mesa CPU renderer, for PCs without a working
OpenGL driver). The tests need Windows itself: run `go test ./...` there.

## Android

JDK 21 and the Android SDK with `platforms;android-37.0`,
`build-tools;37.0.0` and the NDK `30.0.16248370` (`ANDROID_NDK_HOME` pointing
to it), plus `cmake`, `make`, `patch` and `patchelf` for the native helpers.

```bash
./android/gradlew -p android \
  :app:assembleDebug :app:assembleRelease :app:testDebugUnitTest :app:lintDebug
python3 android/runtime/build.py --output android/app/build/local-runtime \
  --source-bundle android/app/build/local-runtime/native-sources.zip
```

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
can point to it from elsewhere. Without it, the debug APK uses the local
Android debug key (installable for testing, but it cannot update an app
signed with the publication key) and the release APK is unsigned.

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
