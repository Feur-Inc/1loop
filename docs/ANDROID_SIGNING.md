# Android release signing

Release APKs use a private publication key. Debug builds keep Android's separate
development key. A release build without the publication credentials fails rather
than silently using the debug key.

## Local configuration

Keep the keystore and its credentials in `android/signing/` (ignored by Git).
`android/signing/release.properties` contains:

```properties
storeFile=1loop-release.p12
keyAlias=1loop-release
storePassword=YOUR_PRIVATE_STORE_PASSWORD
keyPassword=YOUR_PRIVATE_KEY_PASSWORD
```

`storeFile` is relative to the properties file. For another location or a Git
worktree, set `ONELOOP_ANDROID_SIGNING_PROPERTIES` to the absolute path of that
file before running Gradle, `ci.sh`, or `release.sh`:

```bash
export ONELOOP_ANDROID_SIGNING_PROPERTIES=/private/path/release.properties
cd android
./gradlew :app:assembleRelease
```

The signed output is `android/app/build/outputs/apk/release/app-release.apk`.
Verify it with the Android SDK's `apksigner verify --verbose --print-certs`.
Keep an encrypted backup of the keystore **and** its passwords, with restricted
access. Never commit them, attach them to a PR, or upload them with release files.
The public certificate and its SHA-256 fingerprint are safe to share.

## Existing installations

Changing from the previous debug certificate to this publication certificate is
not an ordinary compatible update. Do not publish it through the update feed or
uninstall an existing app without an agreed migration and data-backup procedure.
Supported Android versions can use an explicitly prepared signing-key rotation;
this configuration does not yet include a rotation lineage. Android 8 does not
support v3 key rotation. No phone data is migrated or erased by configuring a build.

The APK keystore is separate from `release-key.pem`, which signs the GitHub Pages
update manifest. Keep that existing manifest key unchanged.

No online registration, certificate authority or paid certificate is needed to
create an Android APK signing key. Google Play publication separately requires
its developer account and Play App Signing setup. A publication signature does
not establish that an APK is harmless or guarantee that Play Protect accepts it.
