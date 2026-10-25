# Ledger for Android

Native Kotlin / Jetpack Compose owner console for Android 9 and newer. Connect it to an existing Ledger HTTPS server with the **owner password** used by the web console, or with a **passkey**. The OAuth approval password is a separate credential.

## Passkeys

Settings › Access › Passkeys adds, renames, and removes the owner's passkeys through Android's Credential Manager; adding or removing one asks for the owner password. Passkeys need Android 14 or newer with Bitwarden or Google Password Manager turned on as a passkey provider (Google Password Manager also works on older versions through Google Play services). Once a passkey exists, the password alone no longer signs in: **Sign in** continues to the passkey, and **Sign in with a passkey** needs no password. If every passkey is lost, `ledger-admin reset-passkeys` on the server returns to password-only sign-in.

Android only lets an app use a server's passkeys when the server vouches for it in `https://<host>/.well-known/assetlinks.json`. Ledger serves that file for each app in `LEDGER_ANDROID_APPS` (`package:SHA256HEX` entries). The default lists the release app (`com.cesarpetrescu.ledger`, signed with the release key). A debug build (`com.cesarpetrescu.ledger.debug`, debug key) can use passkeys only on a server that adds its package and signing certificate there.

Settings › Access › Signed-in devices lists every browser and phone with an owner session, marks this one, and signs out one or all of the others. The app always asks to be remembered: it stays signed in for 30 days after its last use, 90 days at most. It names itself `LedgerAndroid/<version> (Android <release>; <model>)` so the list can show "Ledger app on Android".

Install `ledger-android-vX.Y.Z.apk` from [GitHub Releases](https://github.com/CesarPetrescu/ledger/releases/latest). Android may ask you to allow installations from the browser or file manager. Future release APKs update the same app and retain its session. The Settings screen links to the latest release; installation remains under Android's control.

The app opens on **Today**: what your agents are waiting on you for, each with Reply, Handled and Snooze on its card, then the most urgent todos (tap the ring to finish one), blocked projects, and each project's week. The other tabs are Projects, Handoffs and Reading; Search, at the top of each, also lists every project's entries by project, kind, agent or tag. The round account button opens Calendar, Agents, Recent actions, Trash, Settings and Help.

Its look lives in two files: `Theme.kt` holds the web console's colours for light and dark, the state colours, type (Sora, bundled under the SIL Open Font License in `assets/fonts`) and shapes, and `Components.kt` the building blocks screens are made of. Screens use those instead of styling Material components themselves.

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
