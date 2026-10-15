# CommBox Android

CommBox Android is a native Kotlin app for phones and tablets. It currently includes TCP/UDP sockets, USB host serial, classic Bluetooth SPP, BLE UART profiles, raw HEX/text capture and send, Modbus request templates (03/04/06/10), bounded local history, CSV export, and checksum-checked APK updates.

## Build

Requirements: JDK 17, Android SDK platform 37, Android build tools 37.0.0, and Gradle 9.6.0.

```sh
gradle -p apps/android :app:testDebugUnitTest :app:assembleDebug
```

The debug APK is written to `apps/android/app/build/outputs/apk/debug/`. The repository CI installs the pinned Gradle and Android SDK versions and runs the unit tests and debug build. There is no checked-in Gradle wrapper yet.

## Connect

- **TCP/UDP:** enter a host and port. TCP client/server and UDP send/reply are supported. TCP clients can opt into automatic reconnect; UDP listeners reply to the most recent sender.
- **USB serial:** connect a USB OTG serial adapter, refresh the device list, select the device and port, set baud rate and 5–8 data bits, 1–2 stop bits, and parity, then grant the Android USB prompt. Unplugging the adapter marks the session disconnected.
- **Bluetooth SPP:** enter the paired device MAC address. Android asks for Bluetooth Connect permission on first use. The remote device must provide the standard SPP service UUID.
- **BLE:** enter the device MAC address and UART service/write/notify UUIDs. Nordic UART Service UUIDs are prefilled. BLE writes split according to the negotiated MTU.

The capture list retains the latest 500 items in memory for the current screen and can display bytes as HEX or UTF-8 text. The local database keeps at most 10,000 packets and 32 MiB of raw payload, and CSV export preserves raw bytes as HEX. TCP reads are recorded as received chunks, not inferred protocol frames.

## Modbus templates

Use function codes 03, 04, 06, and 10. Enter zero-based register address, quantity, and comma-separated values for writes. The generated RTU or TCP bytes appear in the send field so they can be reviewed before sending. RTU templates are available for USB/SPP/BLE; Modbus TCP templates are available for TCP. UDP remains raw datagram send.

## APK updates and signing

The app checks the public Gitee and GitHub latest releases. It downloads only an Android APK from the project release hosts, requires a SHA256 listed in the release notes, verifies the package ID and signing certificate, then hands the APK to Android's installer. Android may ask the user to allow CommBox to install apps. Installation always requires the system confirmation screen.

To publish a signed APK, create a persistent release keystore and configure GitHub Actions secrets:

- `ANDROID_KEYSTORE_BASE64`: base64-encoded JKS/PKCS12 keystore
- `ANDROID_KEYSTORE_PASSWORD`
- `ANDROID_KEY_ALIAS`
- `ANDROID_KEY_PASSWORD`

Back up the keystore and passwords outside the repository. Every update must use the same application ID (`com.xiaoleng.commbox`) and signing key. The release workflow uses the desktop version name and an increasing Git commit-count version code, adds the APK to the existing release assets, and includes its SHA256 in the release notes.

## Validation status

Test sources exercise session event isolation, loopback network sockets, Modbus vectors, BLE chunk boundaries, APK update validation, and local storage. This workstation has no Java runtime, Gradle, Android SDK, or `adb`, so the Android tests and APK build have not run here. The attempted JDK download timed out. CI is configured to build and run JVM tests; connected UI tests, USB adapters, SPP devices, BLE UART implementations, and same-key upgrade installation still need emulator or device validation. The release APK also needs the persistent signing key configured in GitHub Actions. Enter a device's actual UUIDs for private BLE UART profiles.
