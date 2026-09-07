# Ledger for Android

Native Kotlin / Jetpack Compose owner console for Android 9 and newer. Connect it to an existing Ledger HTTPS server with the **owner password** used by the web console. The OAuth approval password is a separate credential.

Install `ledger-android-vX.Y.Z.apk` from [GitHub Releases](https://github.com/CesarPetrescu/ledger/releases/latest). Android may ask you to allow installations from the browser or file manager. Future release APKs update the same app and retain its session. The Settings screen links to the latest release; installation remains under Android's control.

The app supports projects and append-only entries; handoffs, message state transitions and attachments; cross-project search; Nextcloud calendars and event editing; connected-client revocation and CLI device approval. Recurring calendar series are managed in the calendar provider. It requires a network connection and does not queue writes offline. Draft attachments must be added before publishing a message.

## Build and verify

Use JDK 21 and Android SDK platform 36 / build-tools 36.0.0. Set `ANDROID_HOME` to your SDK location or configure the ignored `local.properties` file.

```sh
cd android
./gradlew testDebugUnitTest lintDebug assembleDebug assembleDebugAndroidTest
# With an emulator or Android device attached:
./gradlew connectedDebugAndroidTest
```

The debug APK is `app/build/outputs/apk/debug/app-debug.apk`. It uses a separate `.debug` package so it can coexist with the release app. CI runs unit tests, lint, builds both the app and instrumentation test APK, and runs the smoke test on Android 9 (API 28) and Android 16 (API 36). The emulator checks cover native sign-in, encrypted session storage, and the owner flows against a disposable HTTPS fixture (see `smoke.sh`). No production credentials are needed.

## Releases and signing

Push a `vMAJOR.MINOR.PATCH` tag on a commit merged into `main`. The existing release workflow waits for the full CI suite, builds the Linux and Windows CLIs and a signed, optimized universal APK, verifies its signature, and publishes them with `SHA256SUMS`. It fails when signing secrets are missing. Each push / PR also produces a development APK artifact in CI.

Configure these GitHub Actions repository secrets once:

- `ANDROID_KEYSTORE_BASE64`: base64 encoding of the release JKS keystore.
- `ANDROID_KEYSTORE_PASSWORD`: keystore password.
- `ANDROID_KEY_ALIAS`: signing key alias.
- `ANDROID_KEY_PASSWORD`: signing key password.

Keep an independent secure backup of the keystore and passwords. Every update must use the same signing key. Never commit the keystore, passwords, server address, or a signed APK. Local release builds use these same environment variables, except `ANDROID_KEYSTORE_PATH` points to the keystore file instead of supplying base64.

```sh
./gradlew -PreleaseVersion=0.1.0 testDebugUnitTest lintRelease assembleRelease
```

Version codes are `major * 1000000 + minor * 1000 + patch`; minor and patch must be below 1000 and major at most 2000. Increase the version for each release.

## Session security

Only HTTPS is accepted. The app uses the platform certificate trust store, disables redirects and cleartext traffic, and sends cookies only to the configured server's admin API. Mutation requests include the server's Origin and CSRF token. The password is held only while signing in. Session credentials are encrypted with an Android Keystore AES-GCM key; backup and device transfer are disabled. Sign out revokes the server session. Forget this phone only removes the local copy, for use when the server is unreachable.

The app requests only Internet permission. Attachments use Android's document picker. There is no analytics, advertising, embedded browser, background sync, or hardcoded deployment address.
